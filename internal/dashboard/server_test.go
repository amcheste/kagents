package dashboard

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// dashboardScheme registers the types the dashboard handlers need to
// serialize. AgentTeam types live under claudev1alpha1; Pod types come from
// clientgoscheme for the log-streaming code path.
func dashboardScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = claudev1alpha1.AddToScheme(s)
	return s
}

func newServer(objs ...client.Object) *Server {
	c := fake.NewClientBuilder().
		WithScheme(dashboardScheme()).
		WithObjects(objs...).
		Build()
	return &Server{CRClient: c}
}

func sampleTeam(name, namespace string) *claudev1alpha1.AgentTeam {
	return &claudev1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: claudev1alpha1.AgentTeamSpec{
			Auth: claudev1alpha1.AuthSpec{APIKeySecret: "k"},
			Lead: claudev1alpha1.LeadSpec{Model: "opus", Prompt: "lead"},
			Teammates: []claudev1alpha1.TeammateSpec{
				{Name: "worker", Model: "sonnet", Prompt: "work"},
			},
		},
		Status: claudev1alpha1.AgentTeamStatus{Phase: "Running"},
	}
}

func do(t *testing.T, srv *Server, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	return rec
}

// --- /healthz ---

func TestHealthz_ReturnsOK(t *testing.T) {
	srv := newServer()
	rec := do(t, srv, http.MethodGet, "/healthz")
	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, "ok", body["status"])
}

// --- /api/teams (list) ---

func TestListTeams_EmptyClusterReturnsEmptyArray(t *testing.T) {
	srv := newServer()
	rec := do(t, srv, http.MethodGet, "/api/teams")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "[]", "empty list must serialize as [] not null")
}

func TestListTeams_ReturnsAllTeams(t *testing.T) {
	srv := newServer(
		sampleTeam("alpha", "ns1"),
		sampleTeam("beta", "ns2"),
		sampleTeam("gamma", "ns2"),
	)
	rec := do(t, srv, http.MethodGet, "/api/teams")
	assert.Equal(t, http.StatusOK, rec.Code)

	var teams []claudev1alpha1.AgentTeam
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&teams))
	assert.Len(t, teams, 3)
}

func TestListTeams_NamespaceQueryFilters(t *testing.T) {
	srv := newServer(
		sampleTeam("alpha", "ns1"),
		sampleTeam("beta", "ns2"),
	)
	rec := do(t, srv, http.MethodGet, "/api/teams?namespace=ns1")
	assert.Equal(t, http.StatusOK, rec.Code)

	var teams []claudev1alpha1.AgentTeam
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&teams))
	require.Len(t, teams, 1)
	assert.Equal(t, "alpha", teams[0].Name)
}

func TestListTeams_ServerNamespaceScopesByDefault(t *testing.T) {
	srv := newServer(
		sampleTeam("alpha", "ns1"),
		sampleTeam("beta", "ns2"),
	)
	srv.Namespace = "ns2"
	rec := do(t, srv, http.MethodGet, "/api/teams")
	assert.Equal(t, http.StatusOK, rec.Code)

	var teams []claudev1alpha1.AgentTeam
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&teams))
	require.Len(t, teams, 1)
	assert.Equal(t, "beta", teams[0].Name)
}

func TestListTeams_QueryParamOverridesServerNamespace(t *testing.T) {
	srv := newServer(
		sampleTeam("alpha", "ns1"),
		sampleTeam("beta", "ns2"),
	)
	srv.Namespace = "ns2"
	rec := do(t, srv, http.MethodGet, "/api/teams?namespace=ns1")
	assert.Equal(t, http.StatusOK, rec.Code)

	var teams []claudev1alpha1.AgentTeam
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&teams))
	require.Len(t, teams, 1)
	assert.Equal(t, "alpha", teams[0].Name)
}

func TestListTeams_RejectsNonGet(t *testing.T) {
	srv := newServer()
	rec := do(t, srv, http.MethodPost, "/api/teams")
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// --- /api/teams/{ns}/{name} (detail) ---

func TestTeamDetail_ReturnsFullObject(t *testing.T) {
	srv := newServer(sampleTeam("alpha", "ns1"))
	rec := do(t, srv, http.MethodGet, "/api/teams/ns1/alpha")
	assert.Equal(t, http.StatusOK, rec.Code)

	var team claudev1alpha1.AgentTeam
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&team))
	assert.Equal(t, "alpha", team.Name)
	assert.Equal(t, "ns1", team.Namespace)
	assert.Equal(t, "Running", team.Status.Phase)
	require.Len(t, team.Spec.Teammates, 1)
	assert.Equal(t, "worker", team.Spec.Teammates[0].Name)
}

func TestTeamDetail_NotFoundReturns404(t *testing.T) {
	srv := newServer()
	rec := do(t, srv, http.MethodGet, "/api/teams/ns1/missing")
	assert.Equal(t, http.StatusNotFound, rec.Code)

	var body map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Contains(t, body["error"], "missing")
}

func TestTeamDetail_RejectsNonGet(t *testing.T) {
	srv := newServer(sampleTeam("alpha", "ns1"))
	rec := do(t, srv, http.MethodPost, "/api/teams/ns1/alpha")
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// --- routing edge cases ---

func TestRouting_BadDepthReturns404(t *testing.T) {
	srv := newServer()
	cases := []string{
		"/api/teams/ns1",                        // 1 segment after prefix — too short
		"/api/teams/ns1/alpha/extra",            // 3 segments — not the logs shape
		"/api/teams/ns1/alpha/notlogs/agent",    // wrong subresource keyword
		"/api/teams/ns1/alpha/logs/agent/extra", // 5 segments — too deep
	}
	for _, c := range cases {
		rec := do(t, srv, http.MethodGet, c)
		assert.Equal(t, http.StatusNotFound, rec.Code, "path=%s", c)
	}
}

// --- /api/teams/{ns}/{name}/logs/{agent} (log streaming) ---

func TestStreamLogs_NoKubeClientReturns503(t *testing.T) {
	// Most unit tests don't fake the typed clientset because it requires
	// the full kubernetes.Interface. The handler degrades gracefully when
	// KubeClient is nil so those tests can run without that boilerplate.
	srv := newServer(sampleTeam("alpha", "ns1"))
	rec := do(t, srv, http.MethodGet, "/api/teams/ns1/alpha/logs/worker")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// --- writeJSON / writeError helpers ---

func TestWriteJSON_SetsContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]string{"k": "v"})
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	body, _ := io.ReadAll(rec.Body)
	assert.Contains(t, string(body), `"k":"v"`)
}

func TestWriteError_SerializesAsJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusBadRequest, "nope")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var body map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, "nope", body["error"])
}
