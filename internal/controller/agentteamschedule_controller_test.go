package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// --- pure helpers ---

func TestScheduledRunName(t *testing.T) {
	t.Parallel()
	sched := &claudev1alpha1.AgentTeamSchedule{}
	sched.Name = "weekly"
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	got := scheduledRunName(sched, at)
	// Deterministic — same window always produces the same name. The unix
	// timestamp for 2026-06-01 12:00:00 UTC is 1780315200.
	assert.Equal(t, "weekly-1780315200", got)
	// Identical inputs produce identical outputs (idempotency contract).
	assert.Equal(t, got, scheduledRunName(sched, at))
}

func TestIsRunTerminal(t *testing.T) {
	t.Parallel()
	terminal := []string{"Completed", "Failed", "TimedOut", "BudgetExceeded"}
	for _, p := range terminal {
		assert.True(t, isRunTerminal(p), "phase %q must be terminal", p)
	}
	notTerminal := []string{"", "Pending", "Initializing", "Running"}
	for _, p := range notTerminal {
		assert.False(t, isRunTerminal(p), "phase %q must not be terminal", p)
	}
}

func TestRequeueDelay(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	// In the past or now → floored.
	assert.Equal(t, scheduleRequeueAfterFloor, requeueDelay(now, now))
	assert.Equal(t, scheduleRequeueAfterFloor, requeueDelay(now.Add(-time.Hour), now))

	// Less than the floor → floored.
	assert.Equal(t, scheduleRequeueAfterFloor, requeueDelay(now.Add(10*time.Second), now))

	// Far in the future → ~the actual delta.
	d := requeueDelay(now.Add(2*time.Hour), now)
	assert.Greater(t, d, 90*time.Minute)
	assert.LessOrEqual(t, d, 2*time.Hour)
}

// --- controller-level (fake client) ---

func buildScheduleReconciler() *AgentTeamScheduleReconciler {
	s := testScheme()
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&claudev1alpha1.AgentTeamSchedule{}, &claudev1alpha1.AgentTeamRun{}).
		Build()
	return &AgentTeamScheduleReconciler{Client: c, Scheme: s}
}

func sampleSchedule(name string) *claudev1alpha1.AgentTeamSchedule {
	return &claudev1alpha1.AgentTeamSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: claudev1alpha1.AgentTeamScheduleSpec{
			Schedule:    "0 6 * * MON",
			TemplateRef: claudev1alpha1.TemplateReference{Name: "standup"},
			Auth:        claudev1alpha1.AuthSpec{APIKeySecret: "anthropic"},
			Lead:        claudev1alpha1.LeadSpec{Model: "opus", Prompt: "coordinate"},
		},
	}
}

func TestReconcileSchedule_BootstrapSetsNext(t *testing.T) {
	r := buildScheduleReconciler()
	sched := sampleSchedule("bootstrap")
	require.NoError(t, r.Create(context.Background(), sched))

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: sched.Name, Namespace: sched.Namespace}})
	require.NoError(t, err)

	var got claudev1alpha1.AgentTeamSchedule
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: sched.Name, Namespace: sched.Namespace}, &got))
	require.NotNil(t, got.Status.NextScheduledAt, "first reconcile must populate NextScheduledAt")
	assert.Nil(t, got.Status.LastScheduledAt, "first reconcile must NOT fire")
	assert.Empty(t, got.Status.Runs)
}

func TestReconcileSchedule_InvalidCronIsTerminal(t *testing.T) {
	r := buildScheduleReconciler()
	sched := sampleSchedule("bad-cron")
	sched.Spec.Schedule = "not a cron"
	require.NoError(t, r.Create(context.Background(), sched))

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: sched.Name, Namespace: sched.Namespace}})
	require.NoError(t, err, "invalid cron should not bubble an error (terminal-style fail)")
	assert.Zero(t, result.RequeueAfter, "invalid cron should not requeue")

	var got claudev1alpha1.AgentTeamSchedule
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: sched.Name, Namespace: sched.Namespace}, &got))
	assert.Nil(t, got.Status.NextScheduledAt, "invalid cron leaves status untouched")
}

func TestCreateRunForWindow_CreatesAndRecordsIdempotently(t *testing.T) {
	r := buildScheduleReconciler()
	sched := sampleSchedule("idempotent")
	require.NoError(t, r.Create(context.Background(), sched))

	windowAt := time.Date(2026, 6, 1, 6, 0, 0, 0, time.UTC)
	require.NoError(t, r.createRunForWindow(context.Background(), sched, windowAt))

	// Run exists in API.
	want := scheduledRunName(sched, windowAt)
	var run claudev1alpha1.AgentTeamRun
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: want, Namespace: sched.Namespace}, &run))
	assert.Equal(t, sched.Spec.TemplateRef.Name, run.Spec.TemplateRef.Name)
	assert.Equal(t, sched.Spec.Auth.APIKeySecret, run.Spec.Auth.APIKeySecret)
	assert.Equal(t, sched.Spec.Lead.Model, run.Spec.Lead.Model)
	assert.Equal(t, sched.Name, run.Labels["kagents.dev/schedule"], "Run carries schedule label")

	// Status recorded once.
	require.Len(t, sched.Status.Runs, 1)
	assert.Equal(t, want, sched.Status.Runs[0].Name)
	assert.Equal(t, want, sched.Status.ActiveRun)

	// Re-call with the same window: no duplicate Run created, no duplicate
	// status entry appended.
	require.NoError(t, r.createRunForWindow(context.Background(), sched, windowAt))
	assert.Len(t, sched.Status.Runs, 1, "re-fire of same window must be idempotent")
}

func TestRefreshRunPhases_MirrorsAndClearsActive(t *testing.T) {
	r := buildScheduleReconciler()
	sched := sampleSchedule("refresh")
	sched.Status.Runs = []claudev1alpha1.ScheduledRunStatus{
		{Name: "refresh-1", ScheduledAt: metav1.Now(), Phase: "Pending"},
		{Name: "refresh-2", ScheduledAt: metav1.Now(), Phase: "Running"},
	}
	sched.Status.ActiveRun = "refresh-2"
	require.NoError(t, r.Create(context.Background(), sched))

	// Create the corresponding AgentTeamRuns with completed status.
	for _, name := range []string{"refresh-1", "refresh-2"} {
		run := &claudev1alpha1.AgentTeamRun{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: sched.Namespace},
		}
		require.NoError(t, r.Create(context.Background(), run))
		run.Status.Phase = "Completed"
		require.NoError(t, r.Status().Update(context.Background(), run))
	}

	r.refreshRunPhases(context.Background(), sched)
	assert.Equal(t, "Completed", sched.Status.Runs[0].Phase)
	assert.Equal(t, "Completed", sched.Status.Runs[1].Phase)
	assert.Empty(t, sched.Status.ActiveRun, "ActiveRun cleared once the active run reaches terminal")
}

func TestGCOldRuns_TrimsBeyondHistoryLimit(t *testing.T) {
	r := buildScheduleReconciler()
	sched := sampleSchedule("gc")
	sched.Spec.HistoryLimit = 2
	// 4 terminal runs (oldest first) + 1 still running.
	now := metav1.Now()
	sched.Status.Runs = []claudev1alpha1.ScheduledRunStatus{
		{Name: "gc-1", ScheduledAt: now, Phase: "Completed"},
		{Name: "gc-2", ScheduledAt: now, Phase: "Completed"},
		{Name: "gc-3", ScheduledAt: now, Phase: "Completed"},
		{Name: "gc-4", ScheduledAt: now, Phase: "Completed"},
		{Name: "gc-5", ScheduledAt: now, Phase: "Running"},
	}
	require.NoError(t, r.Create(context.Background(), sched))
	// Create the AgentTeamRun objects so the GC has something to delete.
	for _, name := range []string{"gc-1", "gc-2", "gc-3", "gc-4", "gc-5"} {
		require.NoError(t, r.Create(context.Background(), &claudev1alpha1.AgentTeamRun{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: sched.Namespace},
		}))
	}

	require.NoError(t, r.gcOldRuns(context.Background(), sched))

	// HistoryLimit=2 → keep the 2 newest terminals (gc-3, gc-4) + the
	// non-terminal one (gc-5). gc-1 and gc-2 are deleted.
	names := make([]string, 0, len(sched.Status.Runs))
	for _, rs := range sched.Status.Runs {
		names = append(names, rs.Name)
	}
	assert.ElementsMatch(t, []string{"gc-3", "gc-4", "gc-5"}, names)

	// Deleted Runs no longer in API.
	for _, gone := range []string{"gc-1", "gc-2"} {
		var run claudev1alpha1.AgentTeamRun
		err := r.Get(context.Background(), types.NamespacedName{Name: gone, Namespace: sched.Namespace}, &run)
		assert.Error(t, err, "%s should have been deleted by GC", gone)
	}
}

func TestGCOldRuns_HistoryLimitZeroDoesNothing(t *testing.T) {
	r := buildScheduleReconciler()
	sched := sampleSchedule("gc-off")
	now := metav1.Now()
	sched.Status.Runs = []claudev1alpha1.ScheduledRunStatus{
		{Name: "x-1", ScheduledAt: now, Phase: "Completed"},
		{Name: "x-2", ScheduledAt: now, Phase: "Completed"},
		{Name: "x-3", ScheduledAt: now, Phase: "Completed"},
	}
	require.NoError(t, r.Create(context.Background(), sched))

	require.NoError(t, r.gcOldRuns(context.Background(), sched))
	assert.Len(t, sched.Status.Runs, 3, "HistoryLimit=0 keeps everything")
}
