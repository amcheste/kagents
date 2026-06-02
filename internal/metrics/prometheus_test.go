package metrics

import (
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetState clears in-memory dedup maps and zeroes every collector so each
// test starts from a clean slate.
func resetState(t *testing.T) {
	t.Helper()
	startedTeams = sync.Map{}
	finishedTeams = sync.Map{}
	teamActive.Set(0)
	teamDuration.Reset()
	teammateTokens.Reset()
	teamCost.Reset()
	teamTasksCompleted.Reset()
	teammateRestarts.Reset()
	teamBudgetRemaining.Reset()
	teammateIdle.Reset()
	pipelineStageActive.Reset()
	pipelineStageDuration.Reset()
	teamArtifactsProduced.Reset()
	teamDeliverySuccess.Reset()
	teamDeliveryFailure.Reset()
}

func TestRegisterMetricsIsIdempotent(t *testing.T) {
	// Calling RegisterMetrics multiple times must not panic with duplicate
	// collector registration errors.
	require.NotPanics(t, RegisterMetrics)
	require.NotPanics(t, RegisterMetrics)
}

func TestRecordTeamStart_IncrementsActiveOnce(t *testing.T) {
	resetState(t)

	RecordTeamStart("team-a", "ns1")
	assert.Equal(t, float64(1), testutil.ToFloat64(teamActive))

	// Second call for the same team is a no-op — the controller may retry
	// reconcilePending after a transient PVC error.
	RecordTeamStart("team-a", "ns1")
	assert.Equal(t, float64(1), testutil.ToFloat64(teamActive), "duplicate start must not re-increment")

	// Different team increments independently.
	RecordTeamStart("team-b", "ns1")
	assert.Equal(t, float64(2), testutil.ToFloat64(teamActive))

	// Same name, different namespace is a different team.
	RecordTeamStart("team-a", "ns2")
	assert.Equal(t, float64(3), testutil.ToFloat64(teamActive))
}

func TestRecordTeamComplete_DecrementsActiveAndObservesDuration(t *testing.T) {
	resetState(t)

	RecordTeamStart("team-a", "ns1")
	RecordTeamComplete("team-a", "ns1", 42.0)

	assert.Equal(t, float64(0), testutil.ToFloat64(teamActive))

	// Histogram sample count increments to 1 for this team's labels.
	assert.Equal(t, 1, testutil.CollectAndCount(teamDuration), "histogram should have one series recorded")

	// Duplicate complete is a no-op — reconcileTerminal runs repeatedly.
	RecordTeamComplete("team-a", "ns1", 99.0)
	assert.Equal(t, float64(0), testutil.ToFloat64(teamActive), "duplicate complete must not re-decrement")
	assert.Equal(t, 1, testutil.CollectAndCount(teamDuration), "duplicate complete must not add a second observation")
}

func TestRecordTeamFailed_DecrementsActive(t *testing.T) {
	resetState(t)

	RecordTeamStart("team-a", "ns1")
	RecordTeamFailed("team-a", "ns1")
	assert.Equal(t, float64(0), testutil.ToFloat64(teamActive))

	// Duplicate failure is a no-op.
	RecordTeamFailed("team-a", "ns1")
	assert.Equal(t, float64(0), testutil.ToFloat64(teamActive))
}

func TestRecordTeamFailed_AfterCompleteIsNoOp(t *testing.T) {
	// A team that went through Complete should not be double-counted if a
	// later pass somehow reports it as Failed.
	resetState(t)

	RecordTeamStart("team-a", "ns1")
	RecordTeamComplete("team-a", "ns1", 10.0)
	RecordTeamFailed("team-a", "ns1")
	assert.Equal(t, float64(0), testutil.ToFloat64(teamActive), "complete then failed must not go negative")
}

func TestRecordTokenUsage_AddsToCounter(t *testing.T) {
	resetState(t)

	RecordTokenUsage("team-a", "reviewer", "opus", 1000)
	RecordTokenUsage("team-a", "reviewer", "opus", 500)

	v := testutil.ToFloat64(teammateTokens.WithLabelValues("team-a", "reviewer", "opus"))
	assert.Equal(t, float64(1500), v)

	// Different label set is a separate series.
	RecordTokenUsage("team-a", "reviewer", "sonnet", 100)
	assert.Equal(t, float64(100), testutil.ToFloat64(teammateTokens.WithLabelValues("team-a", "reviewer", "sonnet")))
}

func TestRecordCost_SetsGauge(t *testing.T) {
	resetState(t)

	RecordCost("team-a", "ns1", 4.50)
	assert.Equal(t, 4.50, testutil.ToFloat64(teamCost.WithLabelValues("team-a", "ns1")))

	// Gauge overwrites, not adds.
	RecordCost("team-a", "ns1", 7.25)
	assert.Equal(t, 7.25, testutil.ToFloat64(teamCost.WithLabelValues("team-a", "ns1")))
}

func TestRecordTaskCompleted_IncrementsCounter(t *testing.T) {
	resetState(t)

	RecordTaskCompleted("team-a", "reviewer")
	RecordTaskCompleted("team-a", "reviewer")
	RecordTaskCompleted("team-a", "reviewer")

	assert.Equal(t, float64(3), testutil.ToFloat64(teamTasksCompleted.WithLabelValues("team-a", "reviewer")))
}

func TestRecordTeammateRestart_IncrementsCounter(t *testing.T) {
	resetState(t)

	RecordTeammateRestart("team-a", "reviewer")
	RecordTeammateRestart("team-a", "reviewer")

	assert.Equal(t, float64(2), testutil.ToFloat64(teammateRestarts.WithLabelValues("team-a", "reviewer")))
}

func TestSetBudgetRemaining_SetsGauge(t *testing.T) {
	resetState(t)

	SetBudgetRemaining("team-a", "ns1", 5.50)
	assert.Equal(t, 5.50, testutil.ToFloat64(teamBudgetRemaining.WithLabelValues("team-a", "ns1")))

	// Gauges can be set to zero or negative (over-budget scenarios).
	SetBudgetRemaining("team-a", "ns1", -1.25)
	assert.Equal(t, -1.25, testutil.ToFloat64(teamBudgetRemaining.WithLabelValues("team-a", "ns1")))
}

func TestSetActiveTeams_OverwritesCount(t *testing.T) {
	resetState(t)

	SetActiveTeams(7)
	assert.Equal(t, float64(7), testutil.ToFloat64(teamActive))

	SetActiveTeams(0)
	assert.Equal(t, float64(0), testutil.ToFloat64(teamActive))
}

func TestMetricsExposeExpectedNames(t *testing.T) {
	// Guard against accidentally renaming a metric — downstream dashboards and
	// alerting rules depend on these exact names.
	expected := []string{
		"claude_team_active_total",
		"claude_team_duration_seconds",
		"claude_teammate_tokens_total",
		"claude_team_cost_usd",
		"claude_team_tasks_completed_total",
		"claude_teammate_restarts_total",
		"claude_team_budget_remaining_usd",
		"claude_teammate_idle_seconds",
		// kagents_* — knowledge-work observability (v0.8.0+).
		"kagents_team_pipeline_stage_active",
		"kagents_team_stage_duration_seconds",
		"kagents_team_artifacts_produced_total",
		"kagents_team_delivery_success_total",
		"kagents_team_delivery_failure_total",
	}

	// Collect descriptors rather than observations — Vec collectors with no
	// observations don't appear in registry.Gather().
	ch := make(chan *prometheus.Desc, 32)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, c := range collectors {
			c.Describe(ch)
		}
		close(ch)
	}()

	got := make(map[string]bool)
	for desc := range ch {
		// Desc.String() format: `Desc{fqName: "claude_team_active_total", ...}`
		s := desc.String()
		for _, name := range expected {
			if strings.Contains(s, `"`+name+`"`) {
				got[name] = true
			}
		}
	}
	<-done

	for _, name := range expected {
		assert.True(t, got[name], "metric %s not described by any collector", name)
	}
}

func TestMetricsHelpTextHasClaudePrefix(t *testing.T) {
	// A loose sanity check to catch copy/paste mistakes: every collector's help
	// should mention the domain ("team", "teammate", "budget", etc.) rather than
	// an unrelated subsystem.
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors...)
	mfs, err := reg.Gather()
	require.NoError(t, err)

	for _, mf := range mfs {
		help := strings.ToLower(mf.GetHelp())
		assert.NotEmpty(t, help, "metric %s missing help text", mf.GetName())
	}
}

// --- Knowledge-work observability (v0.8.0+) ---

func TestSetPipelineStageActive_FlipsGauge(t *testing.T) {
	resetState(t)
	SetPipelineStageActive("team-a", "ns1", "research", true)
	assert.Equal(t, 1.0, testutil.ToFloat64(pipelineStageActive.WithLabelValues("team-a", "ns1", "research")))
	SetPipelineStageActive("team-a", "ns1", "research", false)
	assert.Equal(t, 0.0, testutil.ToFloat64(pipelineStageActive.WithLabelValues("team-a", "ns1", "research")))
}

func TestObservePipelineStageDuration_RecordsObservation(t *testing.T) {
	resetState(t)
	ObservePipelineStageDuration("team-a", "ns1", "analysis", 42)
	// CollectAndCount on a histogram returns 1 once any observation has landed.
	assert.Equal(t, 1, testutil.CollectAndCount(pipelineStageDuration))
}

func TestRecordArtifactProduced_Increments(t *testing.T) {
	resetState(t)
	RecordArtifactProduced("team-a", "ns1", "writer")
	RecordArtifactProduced("team-a", "ns1", "writer")
	assert.Equal(t, 2.0, testutil.ToFloat64(teamArtifactsProduced.WithLabelValues("team-a", "ns1", "writer")))
}

func TestRecordDelivery_SuccessAndFailureAreSeparate(t *testing.T) {
	resetState(t)
	RecordDeliverySuccess("team-a", "ns1", "webhook")
	RecordDeliveryFailure("team-a", "ns1", "slack")
	assert.Equal(t, 1.0, testutil.ToFloat64(teamDeliverySuccess.WithLabelValues("team-a", "ns1", "webhook")))
	assert.Equal(t, 1.0, testutil.ToFloat64(teamDeliveryFailure.WithLabelValues("team-a", "ns1", "slack")))
	// Counters are independent — labelling success vs failure on the
	// SAME (team, type) tuple must not collide.
	assert.Equal(t, 0.0, testutil.ToFloat64(teamDeliveryFailure.WithLabelValues("team-a", "ns1", "webhook")))
}
