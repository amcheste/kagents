package controller

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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
	"github.com/amcheste/kagents/internal/github"
)

// githubCaptureServer stands up a fake GitHub API capturing each request's
// path, method, body (parsed as PullRequestRequest), and any reviewer/label
// calls. Returns a stable PR response so tests can assert the status update.
type capturedRequest struct {
	method string
	path   string
	body   []byte
}

func githubCaptureServer(t *testing.T) (url string, reqs *[]capturedRequest) {
	t.Helper()
	list := []capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		list = append(list, capturedRequest{method: r.Method, path: r.URL.Path, body: body})
		if strings.HasSuffix(r.URL.Path, "/pulls") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":99,"html_url":"https://github.com/acme/repo/pull/99","state":"open"}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &list
}

// withCreatePR is a test helper that configures a team for OnComplete=create-pr
// and seeds the referenced GitHub token Secret in the fake client.
func withCreatePR(t *testing.T, team *claudev1alpha1.AgentTeam, repoURL, secretName string, reviewers, labels []string) {
	t.Helper()
	team.Spec.Repository = &claudev1alpha1.RepositorySpec{
		URL:    repoURL,
		Branch: "feature-branch",
	}
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{
		OnComplete:        "create-pr",
		GitHubTokenSecret: secretName,
		PullRequest: &claudev1alpha1.PullRequestSpec{
			TargetBranch: "main",
			Reviewers:    reviewers,
			Labels:       labels,
		},
	}
}

// TestExecuteCreatePR_HappyPath — seeded secret + reachable API → PR is
// created, reviewers requested, labels added, status.PullRequest populated.
//
// The controller calls the real GitHub REST API via a *github.Client built
// inside executeCreatePR. To redirect to the httptest server we monkey-patch
// the default base URL via the github package's DefaultBaseURL constant —
// test-only since the constant is package-scoped.
func TestExecuteCreatePR_HappyPath(t *testing.T) {
	apiURL, reqs := githubCaptureServer(t)

	// Build the team + token secret.
	team := minimalTeam("ship-it")
	withCreatePR(t, team, "https://github.com/acme/repo.git", "gh-token", []string{"alice"}, []string{"ai"})

	token := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "gh-token", Namespace: "default"},
		Data:       map[string][]byte{"GITHUB_TOKEN": []byte("ghp_test_secret")},
	}

	r := newReconciler(team, token)
	r.GitHubBaseURL = apiURL
	team = fetch(t, r, "ship-it")

	err := r.executeCreatePR(context.Background(), team)
	require.NoError(t, err)

	// Status populated.
	require.NotNil(t, team.Status.PullRequest)
	assert.Equal(t, "https://github.com/acme/repo/pull/99", team.Status.PullRequest.URL)
	assert.Equal(t, "open", team.Status.PullRequest.State)

	// Three API calls: create PR, request reviewers, add labels.
	paths := []string{}
	for _, r := range *reqs {
		paths = append(paths, r.path)
	}
	assert.Contains(t, paths, "/repos/acme/repo/pulls")
	assert.Contains(t, paths, "/repos/acme/repo/pulls/99/requested_reviewers")
	assert.Contains(t, paths, "/repos/acme/repo/issues/99/labels")

	// PR request body carries the templated title, head, and base.
	var prBody github.PullRequestRequest
	for _, req := range *reqs {
		if req.path == "/repos/acme/repo/pulls" {
			require.NoError(t, json.Unmarshal(req.body, &prBody))
		}
	}
	assert.Contains(t, prBody.Title, "ship-it", "title must reference the team name")
	assert.Equal(t, "feature-branch", prBody.Head)
	assert.Equal(t, "main", prBody.Base)
	assert.Contains(t, prBody.Body, "ship-it", "body must carry team context")
}

func TestExecuteCreatePR_MissingTokenSecretErrors(t *testing.T) {
	team := minimalTeam("no-token")
	withCreatePR(t, team, "https://github.com/acme/repo", "missing-secret", nil, nil)

	r := newReconciler(team)
	team = fetch(t, r, "no-token")

	err := r.executeCreatePR(context.Background(), team)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing-secret")
}

func TestExecuteCreatePR_SecretWithoutTokenKeyErrors(t *testing.T) {
	// Secret exists but lacks the GITHUB_TOKEN key — treat as misconfigured,
	// not as "no token at all".
	team := minimalTeam("empty-key")
	withCreatePR(t, team, "https://github.com/acme/repo", "empty", nil, nil)

	badSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "empty", Namespace: "default"},
		Data:       map[string][]byte{"WRONG_KEY": []byte("x")},
	}
	r := newReconciler(team, badSecret)
	team = fetch(t, r, "empty-key")

	err := r.executeCreatePR(context.Background(), team)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GITHUB_TOKEN")
}

func TestExecuteCreatePR_APIErrorSurfacesInErrorMessage(t *testing.T) {
	// 422 Validation Failed — the operator must bubble GitHub's message up
	// so kubectl describe shows why the PR didn't open.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Validation Failed","errors":[{"field":"head","code":"invalid"}]}`))
	}))
	defer srv.Close()

	team := minimalTeam("bad-head")
	withCreatePR(t, team, "https://github.com/acme/repo", "gh-token", nil, nil)
	token := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "gh-token", Namespace: "default"},
		Data:       map[string][]byte{"GITHUB_TOKEN": []byte("ghp_test_secret")},
	}
	r := newReconciler(team, token)
	r.GitHubBaseURL = srv.URL
	team = fetch(t, r, "bad-head")

	err := r.executeCreatePR(context.Background(), team)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "422")
	assert.Contains(t, err.Error(), "Validation Failed")
}

func TestExecuteCreatePR_SkipsReviewerAndLabelFailures(t *testing.T) {
	// Reviewer/label calls failing must not fail the overall operation — the
	// PR already exists and is the primary deliverable. Simulate this by
	// returning 200 for the PR and 403 for the follow-up calls.
	var prCreated bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/pulls") {
			prCreated = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":7,"html_url":"https://github.com/a/b/pull/7","state":"open"}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible"}`))
	}))
	defer srv.Close()

	team := minimalTeam("partial-perm")
	withCreatePR(t, team, "https://github.com/a/b", "gh-token", []string{"alice"}, []string{"ai"})
	token := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "gh-token", Namespace: "default"},
		Data:       map[string][]byte{"GITHUB_TOKEN": []byte("ghp_test_secret")},
	}
	r := newReconciler(team, token)
	r.GitHubBaseURL = srv.URL
	team = fetch(t, r, "partial-perm")

	require.NoError(t, r.executeCreatePR(context.Background(), team),
		"PR creation is the primary deliverable — reviewer/label failures must not fail the whole operation")
	assert.True(t, prCreated)
	require.NotNil(t, team.Status.PullRequest)
	assert.Equal(t, "https://github.com/a/b/pull/7", team.Status.PullRequest.URL)
}

// --- Title templating ---

func TestRenderPRTitle_Default(t *testing.T) {
	team := minimalTeam("defaulty")
	title, err := renderPRTitle(team)
	require.NoError(t, err)
	assert.Equal(t, "claude-teams: defaulty", title)
}

func TestRenderPRTitle_LifecycleLevelOverride(t *testing.T) {
	team := minimalTeam("over")
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{
		PRTitleTemplate: "[bot] {{.TeamName}} in {{.Namespace}}",
	}
	title, err := renderPRTitle(team)
	require.NoError(t, err)
	assert.Equal(t, "[bot] over in default", title)
}

func TestRenderPRTitle_FallsBackToPullRequestSpecTemplate(t *testing.T) {
	team := minimalTeam("fb")
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{
		PullRequest: &claudev1alpha1.PullRequestSpec{TitleTemplate: "pr-spec: {{.TeamName}}"},
	}
	title, err := renderPRTitle(team)
	require.NoError(t, err)
	assert.Equal(t, "pr-spec: fb", title)
}

func TestRenderPRTitle_LifecycleTakesPrecedenceOverPullRequestSpec(t *testing.T) {
	team := minimalTeam("pri")
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{
		PRTitleTemplate: "lifecycle wins",
		PullRequest:     &claudev1alpha1.PullRequestSpec{TitleTemplate: "pr-spec"},
	}
	title, err := renderPRTitle(team)
	require.NoError(t, err)
	assert.Equal(t, "lifecycle wins", title)
}

// --- PR body ---

func TestBuildPRBody_IncludesTeamAndTasks(t *testing.T) {
	team := minimalTeam("body-test")
	team.Status.Tasks = &claudev1alpha1.TaskSummary{Total: 10, Completed: 7}
	team.Status.Teammates = []claudev1alpha1.TeammateStatus{
		{Name: "reviewer", AgentStatus: claudev1alpha1.AgentStatus{Phase: "Completed"}, TasksCompleted: 3, RestartCount: 1},
	}
	team.Status.EstimatedCost = "2.50"

	body := buildPRBody(team)
	assert.Contains(t, body, "body-test")
	assert.Contains(t, body, "Completed: 7")
	assert.Contains(t, body, "Total: 10")
	assert.Contains(t, body, "reviewer")
	assert.Contains(t, body, "restarts=1")
	assert.Contains(t, body, "$2.50")
}

// --- Branch selection ---

func TestPRBranches_DefaultsToMain(t *testing.T) {
	team := minimalTeam("branches")
	head, base := prBranches(team)
	assert.Equal(t, "main", head, "head falls back to 'main' when no repo branch is set")
	assert.Equal(t, "main", base)
}

func TestPRBranches_UsesRepoBranchAndTargetBranch(t *testing.T) {
	team := minimalTeam("b")
	team.Spec.Repository = &claudev1alpha1.RepositorySpec{URL: "https://github.com/x/y", Branch: "work"}
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{
		PullRequest: &claudev1alpha1.PullRequestSpec{TargetBranch: "develop"},
	}
	head, base := prBranches(team)
	assert.Equal(t, "work", head)
	assert.Equal(t, "develop", base)
}
