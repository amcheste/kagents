package controller

import (
	"context"
	"sort"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// AgentTeamTriggerReconciler maintains AgentTeamTrigger status from the
// AgentTeamRun resources the kagents-trigger HTTP listener creates. The
// listener — not this reconciler — is what actually fires runs on
// incoming webhooks. Splitting responsibilities this way keeps the
// internet-reachable surface out of the manager's leader-elected pod
// while still giving each trigger a familiar K8s-native status view
// (LastTriggeredAt, ActiveRun, TotalRuns, recent Runs).
type AgentTeamTriggerReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// HistoryLimit caps how many Runs appear in Status.Runs[]. Older
	// entries are dropped from the status view (the AgentTeamRun objects
	// themselves remain — this reconciler does not GC them; that's a
	// separate concern from history-display).
	HistoryLimit int
}

const (
	// triggerLabel is set on every AgentTeamRun the listener creates so
	// this reconciler can find them with a label selector.
	triggerLabel = "kagents.dev/trigger"

	// defaultTriggerHistoryLimit is used when HistoryLimit is unset.
	defaultTriggerHistoryLimit = 20
)

// +kubebuilder:rbac:groups=kagents.dev,resources=agentteamtriggers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kagents.dev,resources=agentteamtriggers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kagents.dev,resources=agentteamtriggers/finalizers,verbs=update

func (r *AgentTeamTriggerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var trig claudev1alpha1.AgentTeamTrigger
	if err := r.Get(ctx, req.NamespacedName, &trig); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// List runs labeled with this trigger.
	var runs claudev1alpha1.AgentTeamRunList
	if err := r.List(ctx, &runs,
		client.InNamespace(trig.Namespace),
		client.MatchingLabels{triggerLabel: trig.Name},
	); err != nil {
		return ctrl.Result{}, err
	}

	// Sort by creation time, newest first.
	sort.Slice(runs.Items, func(i, j int) bool {
		return runs.Items[i].CreationTimestamp.After(runs.Items[j].CreationTimestamp.Time)
	})

	limit := r.HistoryLimit
	if limit <= 0 {
		limit = defaultTriggerHistoryLimit
	}

	history := make([]claudev1alpha1.TriggerRunStatus, 0, len(runs.Items))
	var activeRun string
	for i, run := range runs.Items {
		if i < limit {
			history = append(history, claudev1alpha1.TriggerRunStatus{
				Name:        run.Name,
				TriggeredAt: run.CreationTimestamp,
				Phase:       run.Status.Phase,
			})
		}
		if activeRun == "" && !isRunTerminal(run.Status.Phase) {
			activeRun = run.Name
		}
	}

	trig.Status.Runs = history
	trig.Status.ActiveRun = activeRun
	trig.Status.TotalRuns = int64(len(runs.Items))
	if len(runs.Items) > 0 {
		// Most recent Run's creation time = LastTriggeredAt.
		t := runs.Items[0].CreationTimestamp
		trig.Status.LastTriggeredAt = &t
	}

	if err := r.Status().Update(ctx, &trig); err != nil {
		return ctrl.Result{}, err
	}

	// Requeue periodically so terminal-phase transitions on tracked Runs
	// don't sit invisible until the next webhook fires.
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// SetupWithManager registers the reconciler with the controller manager.
func (r *AgentTeamTriggerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&claudev1alpha1.AgentTeamTrigger{}).
		Complete(r)
}
