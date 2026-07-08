package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

func richTeam(name, namespace string) *claudev1alpha1.AgentTeam {
	budget := "10.00"
	return &claudev1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         namespace,
			CreationTimestamp: metav1.Time{},
		},
		Spec: claudev1alpha1.AgentTeamSpec{
			Auth: claudev1alpha1.AuthSpec{APIKeySecret: "k"},
			Lead: claudev1alpha1.LeadSpec{Model: "opus", Prompt: "lead"},
			Teammates: []claudev1alpha1.TeammateSpec{
				{Name: "reviewer", Model: "sonnet", Prompt: "review"},
				{Name: "builder", Model: "haiku", Prompt: "build"},
			},
			Lifecycle: &claudev1alpha1.LifecycleSpec{
				BudgetLimit: &budget,
				Timeout:     "4h",
			},
		},
		Status: claudev1alpha1.AgentTeamStatus{
			Phase:         "Running",
			Ready:         "2/2",
			EstimatedCost: "3.50",
			Tasks: &claudev1alpha1.TaskSummary{
				Total:     10,
				Completed: 5,
			},
			Teammates: []claudev1alpha1.TeammateStatus{
				{
					Name:           "reviewer",
					AgentStatus:    claudev1alpha1.AgentStatus{Phase: "Running"},
					TasksCompleted: 3,
					RestartCount:   0,
				},
			},
			Conditions: []metav1.Condition{
				{Type: "Progressing", Status: metav1.ConditionTrue, Reason: "Running", Message: "OK"},
			},
		},
	}
}

// --- / (list view) ---

func TestRouteRoot_RendersListPage(t *testing.T) {
	srv := newServer(richTeam("alpha", "ns1"))
	rec := do(t, srv, http.MethodGet, "/")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))

	body := rec.Body.String()
	// Layout chrome
	assert.Contains(t, body, "kagents")
	assert.Contains(t, body, "Agent Teams")
	// SSE wiring on the tbody (replaced 5s polling in #139)
	assert.Contains(t, body, `hx-ext="sse"`)
	assert.Contains(t, body, `sse-connect="/api/htmx/teams/sse"`)
	assert.Contains(t, body, `sse-swap="update"`)
	// Team data rendered
	assert.Contains(t, body, "alpha")
	assert.Contains(t, body, "ns1")
	assert.Contains(t, body, "Running")
	assert.Contains(t, body, "2/2")
	assert.Contains(t, body, "5 / 10")
	assert.Contains(t, body, "$3.50")
}

func TestRouteRoot_NonRootPathReturns404(t *testing.T) {
	srv := newServer()
	rec := do(t, srv, http.MethodGet, "/bogus-path")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "Page not found")
	assert.Contains(t, rec.Body.String(), "/bogus-path")
}

func TestRouteRoot_RejectsNonGet(t *testing.T) {
	srv := newServer()
	rec := do(t, srv, http.MethodPost, "/")
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestRouteRoot_EmptyClusterStillRenders(t *testing.T) {
	// Empty list shouldn't 500 — the empty-state message takes over.
	srv := newServer()
	rec := do(t, srv, http.MethodGet, "/")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "No AgentTeams")
}

// --- /teams/{ns}/{name} (detail) ---

func TestRouteDetail_RendersDetailPage(t *testing.T) {
	srv := newServer(richTeam("alpha", "ns1"))
	rec := do(t, srv, http.MethodGet, "/teams/ns1/alpha")
	assert.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()

	// Page chrome
	assert.Contains(t, body, "alpha")
	assert.Contains(t, body, "ns1/alpha")

	// SSE wiring on the detail body wrapper (replaced 5s polling in #139)
	assert.Contains(t, body, `hx-ext="sse"`)
	assert.Contains(t, body, `sse-connect="/api/teams/ns1/alpha/events"`)

	// Teammates table populated
	assert.Contains(t, body, "reviewer")
	assert.Contains(t, body, "builder")
	assert.Contains(t, body, "sonnet")
	assert.Contains(t, body, "haiku")

	// Budget bar — 3.50 / 10.00 = 35% → green
	assert.Contains(t, body, "35%")
	assert.Contains(t, body, "bg-green-500")
}

func TestRouteDetail_OverBudgetShowsRedBar(t *testing.T) {
	team := richTeam("hot", "ns1")
	team.Status.EstimatedCost = "9.50" // 95% of $10 → red
	srv := newServer(team)
	rec := do(t, srv, http.MethodGet, "/teams/ns1/hot")
	assert.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "95%")
	assert.Contains(t, body, "bg-red-500")
}

func TestRouteDetail_BudgetCappedAt100(t *testing.T) {
	team := richTeam("over", "ns1")
	team.Status.EstimatedCost = "50.00" // 500% of $10 → capped at 100
	srv := newServer(team)
	rec := do(t, srv, http.MethodGet, "/teams/ns1/over")
	body := rec.Body.String()
	assert.Contains(t, body, "100%")
	assert.NotContains(t, body, "500%", "percent must be capped, not literal")
}

func TestRouteDetail_NoBudgetShowsNoBar(t *testing.T) {
	// A team without Lifecycle.BudgetLimit should render fine — the budget
	// section just doesn't show up.
	team := richTeam("noco", "ns1")
	team.Spec.Lifecycle = nil
	srv := newServer(team)
	rec := do(t, srv, http.MethodGet, "/teams/ns1/noco")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRouteDetail_NotFoundReturnsHTMLErrorPage(t *testing.T) {
	srv := newServer()
	rec := do(t, srv, http.MethodGet, "/teams/ns1/missing")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "Team not found")
	assert.Contains(t, body, "404")
}

func TestRouteDetail_BadDepthReturns404(t *testing.T) {
	// Note: paths with double-slashes (e.g. "/teams//empty") are normalized
	// by net/http's ServeMux into a 307 redirect before our handler sees
	// them — that's a framework concern, not a handler one.
	srv := newServer()
	for _, path := range []string{
		"/teams/justone",
		"/teams/ns/name/extra",
	} {
		rec := do(t, srv, http.MethodGet, path)
		assert.Equal(t, http.StatusNotFound, rec.Code, "path=%s", path)
	}
}

// --- /api/htmx/teams (list fragment) ---

func TestHTMXListRows_ReturnsTbodyFragmentWithoutLayout(t *testing.T) {
	srv := newServer(richTeam("alpha", "ns1"))
	rec := do(t, srv, http.MethodGet, "/api/htmx/teams")
	assert.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()

	// Fragment has rows but NOT the layout chrome.
	assert.Contains(t, body, "alpha")
	assert.NotContains(t, body, "<!doctype html>")
	assert.NotContains(t, body, "<html")
	assert.NotContains(t, body, "tailwindcss")
}

func TestHTMXListRows_EmptyClusterReturnsEmptyStateRow(t *testing.T) {
	srv := newServer()
	rec := do(t, srv, http.MethodGet, "/api/htmx/teams")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "No AgentTeams")
}

// --- /api/htmx/teams/{ns}/{name} (detail fragment) ---

func TestHTMXDetailBody_ReturnsFragmentWithoutLayout(t *testing.T) {
	srv := newServer(richTeam("alpha", "ns1"))
	rec := do(t, srv, http.MethodGet, "/api/htmx/teams/ns1/alpha")
	assert.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()

	assert.Contains(t, body, "alpha")
	assert.Contains(t, body, "reviewer")
	assert.NotContains(t, body, "<!doctype html>")
	assert.NotContains(t, body, "<html")
}

func TestHTMXDetailBody_NotFoundReturns404Plain(t *testing.T) {
	srv := newServer()
	rec := do(t, srv, http.MethodGet, "/api/htmx/teams/ns1/ghost")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	// Fragment 404s don't render the full HTML error page — the parent
	// page is already rendered, the swap just gets a plain message.
	assert.NotContains(t, rec.Body.String(), "<!doctype html>")
}

// --- error pages ---

func TestHTMLError_RendersFullErrorPage(t *testing.T) {
	srv := newServer()
	rec := httptest.NewRecorder()
	srv.htmlError(rec, http.StatusForbidden, "Nope", "you shall not pass")
	require.Equal(t, http.StatusForbidden, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "<!doctype html>", "error page should still have layout")
	assert.Contains(t, body, "403")
	assert.Contains(t, body, "Nope")
	assert.Contains(t, body, "you shall not pass")
}

// --- template func sanity ---

func TestBudgetPercent_NoBudgetReturnsZero(t *testing.T) {
	team := claudev1alpha1.AgentTeam{}
	got := templateFuncs["budgetPercent"].(func(claudev1alpha1.AgentTeam) int)(team)
	assert.Equal(t, 0, got)
}

func TestBudgetPercent_NormalRange(t *testing.T) {
	limit := "10.00"
	team := claudev1alpha1.AgentTeam{
		Spec:   claudev1alpha1.AgentTeamSpec{Lifecycle: &claudev1alpha1.LifecycleSpec{BudgetLimit: &limit}},
		Status: claudev1alpha1.AgentTeamStatus{EstimatedCost: "5.00"},
	}
	got := templateFuncs["budgetPercent"].(func(claudev1alpha1.AgentTeam) int)(team)
	assert.Equal(t, 50, got)
}

func TestTeammateStatus_LookupReturnsZeroValueWhenMissing(t *testing.T) {
	statuses := []claudev1alpha1.TeammateStatus{
		{Name: "alice", AgentStatus: claudev1alpha1.AgentStatus{Phase: "Running"}},
	}
	fn := templateFuncs["teammateStatus"].(func([]claudev1alpha1.TeammateStatus, string) claudev1alpha1.TeammateStatus)
	got := fn(statuses, "ghost")
	assert.Equal(t, "ghost", got.Name)
	assert.Equal(t, "", got.Phase, "missing status returns zero-value with name set")
}

func TestDeref_HandlesNil(t *testing.T) {
	fn := templateFuncs["deref"].(func(*string) string)
	assert.Equal(t, "", fn(nil))
	s := "hello"
	assert.Equal(t, "hello", fn(&s))
}

// --- routing precedence regression ---

// /api/teams must not be caught by the / (root) route. ServeMux's longest-
// match rule should handle this, but it's worth a regression test because
// reordering registrations could silently break it.
func TestRoutingPrecedence_APITeamsTakesPriorityOverRoot(t *testing.T) {
	srv := newServer(richTeam("a", "ns1"))
	rec := do(t, srv, http.MethodGet, "/api/teams")
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"),
		"/api/teams must hit the JSON handler, not the HTML root")
	assert.True(t, strings.HasPrefix(rec.Body.String(), "["),
		"response must be a JSON array")
}
