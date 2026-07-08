package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// AgentTeamScheduleReconciler turns an AgentTeamSchedule into recurring
// AgentTeamRun instances. The controller is deliberately thin:
//
//   - Parse the schedule's cron expression.
//   - On reconcile, if the next-fire time has passed, create one
//     AgentTeamRun (deterministic name per fire window so repeated
//     reconciles never double-fire) and advance the fire pointer.
//   - Mirror each tracked Run's phase back into Schedule.Status.Runs.
//   - Garbage-collect completed Runs beyond Spec.HistoryLimit.
//
// The existing AgentTeamRun controller does the heavy lifting of turning
// the Run into an AgentTeam — schedules don't create teams directly,
// they produce the same Run resources a human would apply by hand.
type AgentTeamScheduleReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// scheduleCronParser uses the standard 5-field cron spec ("* * * * *").
// Captured at package scope so tests can substitute a different schedule
// if needed in the future without re-parsing per reconcile.
var scheduleCronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
)

// scheduleRequeueAfterFloor caps how far in the future we'll requeue,
// ensuring the controller wakes up at least once a minute to update Run
// phases and re-evaluate the schedule even when the next fire is hours
// away. Cron windows shorter than this just requeue at the natural delta.
const scheduleRequeueAfterFloor = time.Minute

// +kubebuilder:rbac:groups=kagents.dev,resources=agentteamschedules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kagents.dev,resources=agentteamschedules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kagents.dev,resources=agentteamschedules/finalizers,verbs=update
// +kubebuilder:rbac:groups=kagents.dev,resources=agentteamruns,verbs=get;list;watch;create;update;patch;delete

func (r *AgentTeamScheduleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var sched claudev1alpha1.AgentTeamSchedule
	if err := r.Get(ctx, req.NamespacedName, &sched); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	parsed, err := scheduleCronParser.Parse(sched.Spec.Schedule)
	if err != nil {
		// A malformed cron is a terminal config error from the controller's
		// perspective; we don't keep retrying it. The user must fix the spec.
		logger.Error(err, "Invalid cron schedule", "schedule", sched.Spec.Schedule)
		return ctrl.Result{}, nil
	}

	now := time.Now()

	// Bootstrap on first reconcile: just record when the next fire would be,
	// don't synthesize a run for "the past since creation."
	if sched.Status.NextScheduledAt == nil {
		next := metav1.NewTime(parsed.Next(now))
		sched.Status.NextScheduledAt = &next
		return ctrl.Result{RequeueAfter: requeueDelay(next.Time, now)}, r.Status().Update(ctx, &sched)
	}

	// Fire any pending windows. A reconcile that wakes up well after a fire
	// (e.g. operator down for an hour) still produces one Run for the most
	// recent missed window — it doesn't retroactively replay every window
	// since the controller was last up. That matches the CronJob controller
	// in upstream Kubernetes and is the safer default for AI-spend bounded
	// work.
	if !now.Before(sched.Status.NextScheduledAt.Time) {
		windowAt := sched.Status.NextScheduledAt.Time
		if err := r.createRunForWindow(ctx, &sched, windowAt); err != nil {
			return ctrl.Result{}, fmt.Errorf("creating run for window %s: %w", windowAt.Format(time.RFC3339), err)
		}
		last := metav1.NewTime(windowAt)
		sched.Status.LastScheduledAt = &last
		next := metav1.NewTime(parsed.Next(now))
		sched.Status.NextScheduledAt = &next
	}

	// Refresh tracked Run phases.
	r.refreshRunPhases(ctx, &sched)

	// GC completed Runs beyond HistoryLimit.
	if err := r.gcOldRuns(ctx, &sched); err != nil {
		logger.Error(err, "Failed to garbage-collect old runs")
		// Non-fatal — we still want the schedule to keep firing.
	}

	if err := r.Status().Update(ctx, &sched); err != nil {
		return ctrl.Result{}, err
	}

	requeue := scheduleRequeueAfterFloor
	if sched.Status.NextScheduledAt != nil {
		requeue = requeueDelay(sched.Status.NextScheduledAt.Time, now)
	}
	return ctrl.Result{RequeueAfter: requeue}, nil
}

// createRunForWindow creates an AgentTeamRun named deterministically for
// the given fire window so repeated reconciles can never double-fire:
// AlreadyExists from the API server is treated as success.
func (r *AgentTeamScheduleReconciler) createRunForWindow(ctx context.Context, sched *claudev1alpha1.AgentTeamSchedule, windowAt time.Time) error {
	runName := scheduledRunName(sched, windowAt)
	run := &claudev1alpha1.AgentTeamRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      runName,
			Namespace: sched.Namespace,
			Labels: map[string]string{
				"kagents.dev/schedule": sched.Name,
			},
		},
		Spec: claudev1alpha1.AgentTeamRunSpec{
			TemplateRef: sched.Spec.TemplateRef,
			Repository:  sched.Spec.Repository,
			Workspace:   sched.Spec.Workspace,
			Auth:        sched.Spec.Auth,
			Lead:        sched.Spec.Lead,
			Lifecycle:   sched.Spec.Lifecycle,
		},
	}
	if err := ctrl.SetControllerReference(sched, run, r.Scheme); err != nil {
		return fmt.Errorf("setting controller reference: %w", err)
	}
	if err := r.Create(ctx, run); err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}
		// Already exists from a previous reconcile that crashed before the
		// status update — that's the idempotency guarantee, not an error.
	}

	// Record in history. Idempotent: don't append if the run is already there.
	for _, rs := range sched.Status.Runs {
		if rs.Name == runName {
			sched.Status.ActiveRun = runName
			return nil
		}
	}
	sched.Status.Runs = append(sched.Status.Runs, claudev1alpha1.ScheduledRunStatus{
		Name:        runName,
		ScheduledAt: metav1.NewTime(windowAt),
		Phase:       "Pending",
	})
	sched.Status.ActiveRun = runName
	return nil
}

// refreshRunPhases polls each tracked Run's status.phase and mirrors it
// into the schedule's history. Also clears ActiveRun once it's no longer
// running.
func (r *AgentTeamScheduleReconciler) refreshRunPhases(ctx context.Context, sched *claudev1alpha1.AgentTeamSchedule) {
	for i := range sched.Status.Runs {
		ref := &sched.Status.Runs[i]
		var run claudev1alpha1.AgentTeamRun
		err := r.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: sched.Namespace}, &run)
		switch {
		case err == nil:
			ref.Phase = run.Status.Phase
		case errors.IsNotFound(err):
			// Run was deleted out from under us (e.g. a previous GC cycle).
			// Leave the last observed phase as-is so history is preserved.
		default:
			// Transient error; skip this entry, try again next reconcile.
		}
	}
	if sched.Status.ActiveRun != "" {
		for _, rs := range sched.Status.Runs {
			if rs.Name != sched.Status.ActiveRun {
				continue
			}
			if isRunTerminal(rs.Phase) {
				sched.Status.ActiveRun = ""
			}
			break
		}
	}
}

// gcOldRuns deletes AgentTeamRun objects beyond HistoryLimit, oldest first,
// considering only terminal Runs. Active or in-flight Runs are never GC'd
// regardless of HistoryLimit so a long-running team isn't ripped out from
// under itself by the schedule firing many fast follow-ups.
func (r *AgentTeamScheduleReconciler) gcOldRuns(ctx context.Context, sched *claudev1alpha1.AgentTeamSchedule) error {
	limit := int(sched.Spec.HistoryLimit)
	if limit <= 0 {
		return nil
	}

	// Partition: terminal first (ordered oldest→newest from the existing
	// append-order), then everything else preserved.
	terminal := make([]int, 0, len(sched.Status.Runs))
	for i, rs := range sched.Status.Runs {
		if isRunTerminal(rs.Phase) {
			terminal = append(terminal, i)
		}
	}
	excess := len(terminal) - limit
	if excess <= 0 {
		return nil
	}

	// Delete the oldest excess terminal Runs and drop them from status.
	toDelete := terminal[:excess]
	deletedIdx := make(map[int]struct{}, len(toDelete))
	for _, idx := range toDelete {
		deletedIdx[idx] = struct{}{}
		name := sched.Status.Runs[idx].Name
		var run claudev1alpha1.AgentTeamRun
		err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: sched.Namespace}, &run)
		if err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("fetching run %s for GC: %w", name, err)
		}
		if err == nil {
			if delErr := r.Delete(ctx, &run); delErr != nil && !errors.IsNotFound(delErr) {
				return fmt.Errorf("deleting run %s for GC: %w", name, delErr)
			}
		}
	}

	// Compact status.Runs.
	kept := make([]claudev1alpha1.ScheduledRunStatus, 0, len(sched.Status.Runs)-len(deletedIdx))
	for i, rs := range sched.Status.Runs {
		if _, drop := deletedIdx[i]; drop {
			continue
		}
		kept = append(kept, rs)
	}
	sched.Status.Runs = kept
	return nil
}

// scheduledRunName produces a deterministic per-window run name.
// Stable across reconciles so a retry after a crash mid-fire hits the
// AlreadyExists path and stays idempotent.
func scheduledRunName(sched *claudev1alpha1.AgentTeamSchedule, windowAt time.Time) string {
	return fmt.Sprintf("%s-%d", sched.Name, windowAt.UTC().Unix())
}

// isRunTerminal reports whether the named phase is one the operator
// shouldn't expect to progress further.
func isRunTerminal(phase string) bool {
	switch phase {
	case "Completed", "Failed", "TimedOut", "BudgetExceeded":
		return true
	}
	return false
}

// requeueDelay returns a positive duration until the next-fire time,
// floored at scheduleRequeueAfterFloor so the controller never goes silent
// for longer than the floor. The function takes `now` as a parameter
// (rather than calling time.Now() internally) so tests can pin the clock.
func requeueDelay(nextAt, now time.Time) time.Duration {
	d := nextAt.Sub(now)
	if d < scheduleRequeueAfterFloor {
		return scheduleRequeueAfterFloor
	}
	return d
}

// SetupWithManager registers the reconciler with the controller manager.
func (r *AgentTeamScheduleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&claudev1alpha1.AgentTeamSchedule{}).
		Owns(&claudev1alpha1.AgentTeamRun{}).
		Complete(r)
}
