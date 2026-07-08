package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRepo_HTTPS(t *testing.T) {
	cases := []struct {
		url, owner, repo string
	}{
		{"https://github.com/amcheste/kagents", "amcheste", "kagents"},
		{"https://github.com/amcheste/kagents.git", "amcheste", "kagents"},
		{"https://github.com/amcheste/kagents/", "amcheste", "kagents"},
		{"http://ghe.internal/team/project.git", "team", "project"},
	}
	for _, c := range cases {
		owner, repo, err := ParseRepo(c.url)
		require.NoError(t, err, "url=%s", c.url)
		assert.Equal(t, c.owner, owner, "url=%s", c.url)
		assert.Equal(t, c.repo, repo, "url=%s", c.url)
	}
}

func TestParseRepo_SSH(t *testing.T) {
	cases := []struct {
		url, owner, repo string
	}{
		{"git@github.com:amcheste/kagents.git", "amcheste", "kagents"},
		{"git@github.com:amcheste/kagents", "amcheste", "kagents"},
	}
	for _, c := range cases {
		owner, repo, err := ParseRepo(c.url)
		require.NoError(t, err, "url=%s", c.url)
		assert.Equal(t, c.owner, owner, "url=%s", c.url)
		assert.Equal(t, c.repo, repo, "url=%s", c.url)
	}
}

func TestParseRepo_Invalid(t *testing.T) {
	for _, bad := range []string{"", "not a url", "github.com/only-owner"} {
		_, _, err := ParseRepo(bad)
		assert.Error(t, err, "url=%s", bad)
	}
}

func TestCreatePullRequest_HappyPath(t *testing.T) {
	var received struct {
		auth    string
		method  string
		path    string
		apiVers string
		accept  string
		body    PullRequestRequest
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.auth = r.Header.Get("Authorization")
		received.method = r.Method
		received.path = r.URL.Path
		received.apiVers = r.Header.Get("X-GitHub-Api-Version")
		received.accept = r.Header.Get("Accept")
		_ = json.NewDecoder(r.Body).Decode(&received.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number": 42, "html_url": "https://github.com/acme/repo/pull/42", "state": "open"}`))
	}))
	defer srv.Close()

	c := NewClient("ghp_test_token", WithBaseURL(srv.URL))
	pr, err := c.CreatePullRequest(context.Background(), "acme", "repo", &PullRequestRequest{
		Title: "Team finished",
		Body:  "Task list complete",
		Head:  "claude-teams/auth-refactor",
		Base:  "main",
	})
	require.NoError(t, err)

	assert.Equal(t, 42, pr.Number)
	assert.Equal(t, "https://github.com/acme/repo/pull/42", pr.HTMLURL)
	assert.Equal(t, "open", pr.State)

	assert.Equal(t, "POST", received.method)
	assert.Equal(t, "/repos/acme/repo/pulls", received.path)
	assert.Equal(t, "Bearer ghp_test_token", received.auth)
	assert.Equal(t, "2022-11-28", received.apiVers)
	assert.Contains(t, received.accept, "vnd.github+json")
	assert.Equal(t, "Team finished", received.body.Title)
	assert.Equal(t, "claude-teams/auth-refactor", received.body.Head)
	assert.Equal(t, "main", received.body.Base)
}

func TestCreatePullRequest_APIErrorIncludesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Validation Failed","errors":[{"field":"head","code":"invalid"}]}`))
	}))
	defer srv.Close()

	c := NewClient("t", WithBaseURL(srv.URL))
	_, err := c.CreatePullRequest(context.Background(), "a", "b", &PullRequestRequest{
		Title: "x", Head: "h", Base: "main",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "422")
	assert.Contains(t, err.Error(), "Validation Failed", "error must surface GitHub's message for kubectl describe")
}

func TestRequestReviewers_NoOpOnEmptyList(t *testing.T) {
	// An empty reviewer list must not issue a request — otherwise teams
	// without reviewers would trip permission errors on repos that don't
	// have any collaborators.
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	c := NewClient("t", WithBaseURL(srv.URL))
	require.NoError(t, c.RequestReviewers(context.Background(), "a", "b", 1, nil))
	require.NoError(t, c.RequestReviewers(context.Background(), "a", "b", 1, []string{}))
	assert.False(t, called)
}

func TestRequestReviewers_PostsNames(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := NewClient("t", WithBaseURL(srv.URL))
	require.NoError(t, c.RequestReviewers(context.Background(), "a", "b", 7, []string{"alice", "bob"}))
	assert.Contains(t, body, "alice")
	assert.Contains(t, body, "bob")
}

func TestAddLabels_NoOpOnEmptyList(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	c := NewClient("t", WithBaseURL(srv.URL))
	require.NoError(t, c.AddLabels(context.Background(), "a", "b", 1, nil))
	assert.False(t, called)
}

func TestAddLabels_PostsToIssuesEndpoint(t *testing.T) {
	// GitHub treats PR labels as issue labels — the request must hit
	// /issues/N/labels, not /pulls/N/labels (which doesn't exist).
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient("t", WithBaseURL(srv.URL))
	require.NoError(t, c.AddLabels(context.Background(), "a", "b", 7, []string{"claude"}))
	assert.True(t, strings.Contains(path, "/issues/7/labels"),
		"labels must go to /issues/N/labels — got %q", path)
}

func TestClient_EmptyTokenOmitsAuthHeader(t *testing.T) {
	// Empty token should not produce an `Authorization: Bearer ` header — the
	// upstream caller is expected to validate token presence first, but the
	// client should not send an obviously malformed request.
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number": 1}`))
	}))
	defer srv.Close()

	c := NewClient("", WithBaseURL(srv.URL))
	_, err := c.CreatePullRequest(context.Background(), "a", "b", &PullRequestRequest{Title: "x", Head: "h", Base: "main"})
	require.NoError(t, err)
	assert.Empty(t, auth)
}
