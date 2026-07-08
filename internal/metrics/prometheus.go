package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	teamActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "claude_team_active_total",
		Help: "Number of AgentTeams currently in a non-terminal phase (Pending, Initializing, or Running).",
	})

	teamDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "claude_team_duration_seconds",
		Help:    "Wall-clock duration from team start to terminal phase, in seconds.",
		Buckets: prometheus.ExponentialBuckets(30, 2, 10),
	}, []string{"team", "namespace"})

	teammateTokens = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "claude_teammate_tokens_total",
		Help: "Total tokens consumed per teammate and model.",
	}, []string{"team", "teammate", "model"})

	teamCost = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "claude_team_cost_usd",
		Help: "Current estimated API cost in USD for the team.",
	}, []string{"team", "namespace"})

	teamTasksCompleted = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "claude_team_tasks_completed_total",
		Help: "Total tasks completed by each teammate.",
	}, []string{"team", "teammate"})

	teammateRestarts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "claude_teammate_restarts_total",
		Help: "Total teammate pod restarts.",
	}, []string{"team", "teammate"})

	teamBudgetRemaining = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "claude_team_budget_remaining_usd",
		Help: "Remaining budget in USD (budgetLimit minus estimated cost).",
	}, []string{"team", "namespace"})

	teammateIdle = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "claude_teammate_idle_seconds",
		Help:    "Duration a teammate spends idle between tasks, in seconds.",
		Buckets: prometheus.ExponentialBuckets(1, 2, 10),
	}, []string{"team", "teammate"})

	// --- Knowledge-work observability (v0.8.0+) ---
	//
	// These metrics use the `kagents_` prefix to match the rebranded
	// project name. Existing `claude_*` metrics above stay put for
	// backwards compatibility — they'll get a synchronized rename in a
	// future major; for now operators dashboard against either set.

	pipelineStageActive = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kagents_team_pipeline_stage_active",
		Help: "1 when this pipeline stage is in the Running phase, 0 otherwise. Useful for stacking by stage to visualize where a team is in flight.",
	}, []string{"team", "namespace", "stage"})

	pipelineStageDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kagents_team_stage_duration_seconds",
		Help:    "Wall-clock duration from a pipeline stage's first teammate spawning to its last completing, in seconds. Observed once per (team, stage) at the Completed transition.",
		Buckets: prometheus.ExponentialBuckets(30, 2, 10),
	}, []string{"team", "namespace", "stage"})

	teamArtifactsProduced = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kagents_team_artifacts_produced_total",
		Help: "Artifacts recorded on a team's status by a teammate completing its declared outputs.",
	}, []string{"team", "namespace", "teammate"})

	teamDeliverySuccess = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kagents_team_delivery_success_total",
		Help: "Successful deliveries dispatched when OnComplete=deliver, by target type.",
	}, []string{"team", "namespace", "type"})

	teamDeliveryFailure = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kagents_team_delivery_failure_total",
		Help: "Failed deliveries dispatched when OnComplete=deliver, by target type.",
	}, []string{"team", "namespace", "type"})

	collectors = []prometheus.Collector{
		teamActive,
		teamDuration,
		teammateTokens,
		teamCost,
		teamTasksCompleted,
		teammateRestarts,
		teamBudgetRemaining,
		teammateIdle,
		pipelineStageActive,
		pipelineStageDuration,
		teamArtifactsProduced,
		teamDeliverySuccess,
		teamDeliveryFailure,
	}

	registerOnce sync.Once

	// Active-team tracking is in-process only. On operator restart the gauge resets
	// to zero; callers that need post-restart accuracy should use SetActiveTeams
	// from a list-based reconciliation loop.
	startedTeams  sync.Map
	finishedTeams sync.Map
)

// RegisterMetrics registers all Claude team metrics with the controller-runtime
// registry. Safe to call multiple times; only the first call registers.
func RegisterMetrics() {
	registerOnce.Do(func() {
		ctrlmetrics.Registry.MustRegister(collectors...)
	})
}

func teamKey(name, namespace string) string {
	return namespace + "/" + name
}

// RecordTeamStart increments the active-teams gauge. Idempotent per team key.
func RecordTeamStart(name, namespace string) {
	if _, loaded := startedTeams.LoadOrStore(teamKey(name, namespace), struct{}{}); loaded {
		return
	}
	teamActive.Inc()
}

// RecordTeamComplete decrements the active gauge and observes the team's duration.
// Idempotent per team key.
func RecordTeamComplete(name, namespace string, durationSec float64) {
	if _, loaded := finishedTeams.LoadOrStore(teamKey(name, namespace), struct{}{}); loaded {
		return
	}
	teamActive.Dec()
	teamDuration.WithLabelValues(name, namespace).Observe(durationSec)
}

// RecordTeamFailed decrements the active gauge for a team that entered a terminal
// failure phase (Failed, TimedOut, BudgetExceeded). Idempotent per team key.
func RecordTeamFailed(name, namespace string) {
	if _, loaded := finishedTeams.LoadOrStore(teamKey(name, namespace), struct{}{}); loaded {
		return
	}
	teamActive.Dec()
}

// RecordTokenUsage adds to the per-teammate token counter.
func RecordTokenUsage(team, teammate, model string, tokens int64) {
	teammateTokens.WithLabelValues(team, teammate, model).Add(float64(tokens))
}

// RecordCost sets the current estimated cost for a team.
func RecordCost(team, namespace string, costUSD float64) {
	teamCost.WithLabelValues(team, namespace).Set(costUSD)
}

// RecordTaskCompleted increments the tasks-completed counter for a teammate.
func RecordTaskCompleted(team, teammate string) {
	teamTasksCompleted.WithLabelValues(team, teammate).Inc()
}

// RecordTeammateRestart increments the teammate-restart counter.
func RecordTeammateRestart(team, teammate string) {
	teammateRestarts.WithLabelValues(team, teammate).Inc()
}

// SetBudgetRemaining sets the remaining-budget gauge for a team.
func SetBudgetRemaining(team, namespace string, remaining float64) {
	teamBudgetRemaining.WithLabelValues(team, namespace).Set(remaining)
}

// SetActiveTeams sets the active-teams gauge directly. Prefer this over the
// Inc/Dec path when computing the count from a full list of non-terminal teams,
// e.g. at operator startup.
func SetActiveTeams(count int) {
	teamActive.Set(float64(count))
}

// SetPipelineStageActive marks a stage as Running (1) or not (0). The
// gauge is set on every reconcile so a stage that transitions
// Running → Completed flips to 0 without needing a separate "stage
// done" event.
func SetPipelineStageActive(team, namespace, stage string, active bool) {
	v := 0.0
	if active {
		v = 1.0
	}
	pipelineStageActive.WithLabelValues(team, namespace, stage).Set(v)
}

// ObservePipelineStageDuration records the wall-clock seconds a stage
// spent in Running before transitioning to Completed. Call this once
// per (team, stage) — the reconciler guards against re-observing.
func ObservePipelineStageDuration(team, namespace, stage string, durationSec float64) {
	pipelineStageDuration.WithLabelValues(team, namespace, stage).Observe(durationSec)
}

// RecordArtifactProduced increments the per-teammate artifact counter.
// One increment per artifact appended to status.artifacts.
func RecordArtifactProduced(team, namespace, teammate string) {
	teamArtifactsProduced.WithLabelValues(team, namespace, teammate).Inc()
}

// RecordDeliverySuccess increments the per-type delivery success counter.
func RecordDeliverySuccess(team, namespace, deliveryType string) {
	teamDeliverySuccess.WithLabelValues(team, namespace, deliveryType).Inc()
}

// RecordDeliveryFailure increments the per-type delivery failure counter.
func RecordDeliveryFailure(team, namespace, deliveryType string) {
	teamDeliveryFailure.WithLabelValues(team, namespace, deliveryType).Inc()
}
