package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
	"github.com/amcheste/kagents/internal/budget"
	"github.com/amcheste/kagents/internal/delivery"
	"github.com/amcheste/kagents/internal/github"
	"github.com/amcheste/kagents/internal/harness"
	"github.com/amcheste/kagents/internal/metrics"
	"github.com/amcheste/kagents/internal/webhook"
)

const (
	defaultInitImage = "alpine/git:latest"

	// defaultSkillPullerImage is the OCI artifact puller used to
	// materialize skills declared with SkillSource.OCI. ORAS speaks
	// the OCI distribution spec directly and supports artifacts that
	// aren't container images, which is what skills are.
	defaultSkillPullerImage = "ghcr.io/oras-project/oras:v1.2.0"
)

// AgentTeamReconciler reconciles an AgentTeam object.
type AgentTeamReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// AgentImage overrides the default claude-code-runner image used for agent pods.
	// Used in acceptance tests to substitute a lightweight no-op container.
	AgentImage string

	// InitImage overrides the default alpine/git image used for the repo init Job.
	InitImage string

	// SkillPullerImage overrides the default ORAS image used to pull
	// OCI-distributed skills into agent pods. Tests can swap in a
	// no-op image; air-gapped clusters can pin to an internally
	// mirrored ORAS build.
	SkillPullerImage string

	// SkipInitScript replaces the init Job's git-clone script with a no-op (exit 0).
	// Used in acceptance tests where no real git repository is available.
	SkipInitScript bool

	// AgentCommand overrides the container command for agent pods.
	// When empty the image's default entrypoint is used.
	// In acceptance tests set to e.g. ["sh","-c","sleep 30 && exit 0"] so pods
	// live long enough to be observed before the operator deletes them.
	AgentCommand []string

	// PVCAccessMode overrides the access mode used for all operator-managed PVCs.
	// Defaults to ReadWriteMany (requires NFS/EFS). Set to ReadWriteOnce for
	// single-node clusters such as Kind where all pods share the same node.
	PVCAccessMode corev1.PersistentVolumeAccessMode

	// GitHubBaseURL overrides the GitHub REST API base URL used for
	// OnComplete=create-pr. Empty means production (https://api.github.com).
	// Primarily a test seam; a production deployment that needs GitHub
	// Enterprise can set this to the Enterprise API URL.
	GitHubBaseURL string

	// Recorder emits Kubernetes Events against AgentTeam objects. Populated by
	// SetupWithManager. Tests may inject a fake recorder directly. The
	// recordEvent helper tolerates a nil recorder so unit tests that construct
	// a reconciler directly are not forced to wire one up.
	Recorder record.EventRecorder

	// Harnesses is the registry of supported agent runtimes, keyed by
	// spec.harness value. Populated by main.go from harness.DefaultRegistry().
	// Unit tests that construct a reconciler directly may leave this nil;
	// harnessFor falls back to a built-in claude-code adapter in that case.
	Harnesses map[string]harness.Harness

	// Delivery dispatches DeliveryTarget instances when OnComplete=deliver.
	// Tests inject a Dispatcher with stub senders; production wires
	// delivery.NewDispatcher() in cmd/manager. May be nil — a default
	// production dispatcher is constructed on demand.
	Delivery *delivery.Dispatcher
}

// deliveryDispatcher returns the configured Delivery dispatcher or a
// freshly-built default. Centralizing the fallback keeps unit tests that
// don't populate Delivery working and gives misconfigured reconcilers a
// safe default rather than a nil-deref.
func (r *AgentTeamReconciler) deliveryDispatcher() *delivery.Dispatcher {
	if r.Delivery != nil {
		return r.Delivery
	}
	return delivery.NewDispatcher()
}

// harnessFor returns the [harness.Harness] adapter that should drive the
// given team. Selection: team.Spec.Harness (defaulting to claude-code if
// unset) is looked up in r.Harnesses; if the registry is nil or the named
// harness isn't registered, the built-in claude-code adapter is returned.
// Centralizing the fallback here keeps unit tests that don't populate
// Harnesses working and gives misconfigured CRs a safe default rather than
// a panic.
func (r *AgentTeamReconciler) harnessFor(team *claudev1alpha1.AgentTeam) harness.Harness {
	name := team.Spec.Harness
	if name == "" {
		name = harness.DefaultHarness
	}
	if r.Harnesses != nil {
		if h, ok := r.Harnesses[name]; ok {
			return h
		}
	}
	return harness.ClaudeCode{}
}

// recordEvent emits an Event against the AgentTeam if a Recorder is configured.
// eventType is corev1.EventTypeNormal or corev1.EventTypeWarning.
func (r *AgentTeamReconciler) recordEvent(team *claudev1alpha1.AgentTeam, eventType, reason, messageFmt string, args ...interface{}) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Eventf(team, eventType, reason, messageFmt, args...)
}

func (r *AgentTeamReconciler) pvcAccessMode() corev1.PersistentVolumeAccessMode {
	if r.PVCAccessMode != "" {
		return r.PVCAccessMode
	}
	return corev1.ReadWriteMany
}

func (r *AgentTeamReconciler) agentImage() string {
	if r.AgentImage != "" {
		return r.AgentImage
	}
	// The default runner image comes from the harness adapter rather than a
	// hardcoded operator constant. With claude-code as the only registered
	// harness today, this evaluates to the same image as the old constant.
	return harness.ClaudeCode{}.DefaultImage()
}

func (r *AgentTeamReconciler) initImage() string {
	if r.InitImage != "" {
		return r.InitImage
	}
	return defaultInitImage
}

func (r *AgentTeamReconciler) skillPullerImage() string {
	if r.SkillPullerImage != "" {
		return r.SkillPullerImage
	}
	return defaultSkillPullerImage
}

// +kubebuilder:rbac:groups=kagents.dev,resources=agentteams,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kagents.dev,resources=agentteams/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kagents.dev,resources=agentteams/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete

func (r *AgentTeamReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var team claudev1alpha1.AgentTeam
	if err := r.Get(ctx, req.NamespacedName, &team); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	log.V(1).Info("Reconciling AgentTeam", "phase", team.Status.Phase)

	switch team.Status.Phase {
	case "", "Pending":
		return r.reconcilePending(ctx, &team)
	case "Initializing":
		return r.reconcileInitializing(ctx, &team)
	case "Running":
		return r.reconcileRunning(ctx, &team)
	case "Completed", "Failed", "TimedOut", "BudgetExceeded":
		return r.reconcileTerminal(ctx, &team)
	default:
		log.Info("Unknown phase, resetting to Pending", "phase", team.Status.Phase)
		team.Status.Phase = "Pending"
		return ctrl.Result{Requeue: true}, r.Status().Update(ctx, &team)
	}
}

// --- Phase: Pending ---

// reconcilePending creates shared PVCs and, for coding teams, the init Job.
func (r *AgentTeamReconciler) reconcilePending(ctx context.Context, team *claudev1alpha1.AgentTeam) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("Phase: Pending")

	// Always create the team-state PVC (holds .claude/teams/ and .claude/tasks/ for agent coordination).
	if err := r.ensurePVC(ctx, team, teamStatePVCName(team), "nfs", r.pvcAccessMode(), "1Gi"); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring team-state PVC: %w", err)
	}

	// Coding mode: repo PVC + init Job to clone and create worktrees.
	if team.Spec.Repository != nil && team.Spec.Repository.URL != "" {
		if err := r.ensurePVC(ctx, team, repoPVCName(team), "nfs", r.pvcAccessMode(), "10Gi"); err != nil {
			return ctrl.Result{}, fmt.Errorf("ensuring repo PVC: %w", err)
		}
		if err := r.ensureInitJob(ctx, team); err != nil {
			return ctrl.Result{}, fmt.Errorf("ensuring init job: %w", err)
		}
	}

	// Cowork mode: create the output PVC if configured.
	if team.Spec.Workspace != nil && team.Spec.Workspace.Output != nil {
		out := team.Spec.Workspace.Output
		pvcName := out.PVC
		if pvcName == "" {
			pvcName = outputPVCName(team)
		}
		size := out.Size
		if size == "" {
			size = "5Gi"
		}
		sc := out.StorageClass
		if sc == "" {
			sc = "nfs"
		}
		if err := r.ensurePVC(ctx, team, pvcName, sc, r.pvcAccessMode(), size); err != nil {
			return ctrl.Result{}, fmt.Errorf("ensuring output PVC: %w", err)
		}
	}

	team.Status.Phase = "Initializing"
	now := metav1.Now()
	team.Status.StartedAt = &now
	setCondition(team, metav1.ConditionTrue, "Initializing", "PVCs provisioned, init job started")
	r.recordEvent(team, corev1.EventTypeNormal, "Initializing", "PVCs provisioned; init job started")
	metrics.RecordTeamStart(team.Name, team.Namespace)
	return ctrl.Result{RequeueAfter: 5 * time.Second}, r.Status().Update(ctx, team)
}

// --- Phase: Initializing ---

// reconcileInitializing waits for the init Job (coding mode), then deploys agent pods.
func (r *AgentTeamReconciler) reconcileInitializing(ctx context.Context, team *claudev1alpha1.AgentTeam) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("Phase: Initializing")

	// Enforce timeout even during initialization — prevents a team being permanently
	// stuck on a slow or hung init Job.
	if r.isTimedOut(team) {
		log.Info("Team timed out during initialization")
		team.Status.Phase = "TimedOut"
		setCondition(team, metav1.ConditionFalse, "TimedOut", "Team exceeded configured timeout during initialization")
		r.recordEvent(team, corev1.EventTypeWarning, "TimedOut", "Team exceeded configured timeout during initialization")
		return ctrl.Result{}, r.Status().Update(ctx, team)
	}

	// In coding mode, wait for the init Job before spawning pods.
	if team.Spec.Repository != nil && team.Spec.Repository.URL != "" {
		done, failed, err := r.checkJobStatus(ctx, team, initJobName(team))
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("checking init job: %w", err)
		}
		if failed {
			team.Status.Phase = "Failed"
			setCondition(team, metav1.ConditionFalse, "InitJobFailed", "Init job exceeded backoff limit")
			r.recordEvent(team, corev1.EventTypeWarning, "InitJobFailed", "Init job exceeded backoff limit")
			return ctrl.Result{}, r.Status().Update(ctx, team)
		}
		if !done {
			log.Info("Init job still running")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
	}

	// Deploy the lead pod.
	if err := r.ensureAgentPod(ctx, team, "lead", team.Spec.Lead.Model, team.Spec.Lead.Prompt,
		team.Spec.Lead.PermissionMode, true, team.Spec.Lead.Resources, nil,
		team.Spec.Lead.Skills, team.Spec.Lead.MCPServers, nil); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring lead pod: %w", err)
	}

	// Deploy teammates whose dependencies are already met (or have none).
	// Inputs[].From implicitly extends DependsOn so consumers wait for producers.
	// Pipeline mode additionally gates the entire stage on
	// approved.kagents.dev/stage-{name} when ApprovalRequired is set.
	for _, tm := range team.Spec.Teammates {
		if !r.dependenciesMet(ctx, team, effectiveDependencies(team, tm)) {
			continue
		}
		if stage, ok := stageForTeammate(team, tm.Name); ok && !stageApprovalGranted(team, stage) {
			r.setTeammatePendingApproval(team, tm.Name, "stage-"+stage.Name)
			continue
		}
		if !r.checkApprovalGate(ctx, team, "spawn-"+tm.Name) {
			r.setTeammatePendingApproval(team, tm.Name, "spawn-"+tm.Name)
			continue
		}
		if err := r.ensureAgentPod(ctx, team, tm.Name, tm.Model, tm.Prompt,
			"auto-accept", false, tm.Resources, tm.Scope,
			tm.Skills, tm.MCPServers, tm.Inputs); err != nil {
			return ctrl.Result{}, fmt.Errorf("ensuring teammate pod %s: %w", tm.Name, err)
		}
	}

	team.Status.Phase = "Running"
	setCondition(team, metav1.ConditionTrue, "Running", "Agent pods deployed")
	r.recordEvent(team, corev1.EventTypeNormal, "Running", "Agent pods deployed")
	_ = teamNotifier(team).SendEvent(ctx, "team.started", teamEventPayload(team, map[string]interface{}{
		"leadModel": team.Spec.Lead.Model,
		"teammates": len(team.Spec.Teammates),
	}))
	return ctrl.Result{RequeueAfter: 30 * time.Second}, r.Status().Update(ctx, team)
}

// --- Phase: Running ---

// reconcileRunning monitors agents, enforces timeout/budget, handles dynamic pod spawning and completion.
func (r *AgentTeamReconciler) reconcileRunning(ctx context.Context, team *claudev1alpha1.AgentTeam) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("Phase: Running")

	// Check timeout.
	if r.isTimedOut(team) {
		log.Info("Team timed out")
		if err := r.terminateAllPods(ctx, team); err != nil {
			return ctrl.Result{}, err
		}
		team.Status.Phase = "TimedOut"
		setCondition(team, metav1.ConditionFalse, "TimedOut", "Team exceeded configured timeout")
		r.recordEvent(team, corev1.EventTypeWarning, "TimedOut", "Team exceeded configured timeout; all pods terminated")
		return ctrl.Result{}, r.Status().Update(ctx, team)
	}

	// Update cost estimate and check budget.
	team.Status.EstimatedCost = estimateCost(team)
	r.exportBudgetMetrics(team)
	r.maybeFireBudgetWarning(ctx, team)
	if r.isBudgetExceeded(team) {
		log.Info("Budget exceeded", "cost", team.Status.EstimatedCost)
		if err := r.terminateAllPods(ctx, team); err != nil {
			return ctrl.Result{}, err
		}
		team.Status.Phase = "BudgetExceeded"
		setCondition(team, metav1.ConditionFalse, "BudgetExceeded", "Estimated cost exceeded budget limit")
		r.recordEvent(team, corev1.EventTypeWarning, "BudgetExceeded", "Estimated cost %s exceeded budget limit; all pods terminated", team.Status.EstimatedCost)
		return ctrl.Result{}, r.Status().Update(ctx, team)
	}

	// Sync pod statuses into team.Status.
	r.syncPodStatuses(ctx, team)
	// Recompute pipeline status once teammate phases are fresh.
	r.updatePipelineStatus(team, time.Now())

	// Re-spawn crashed teammates whose RestartCount is still below the limit;
	// fail the team if any teammate has exhausted its restarts.
	if fatal, err := r.handleTeammateFailures(ctx, team); err != nil {
		return ctrl.Result{}, err
	} else if fatal != "" {
		team.Status.Phase = "Failed"
		setCondition(team, metav1.ConditionFalse, "RestartLimitExceeded",
			fmt.Sprintf("Teammate %s exceeded maxRestarts=%d", fatal, maxRestarts(team)))
		r.recordEvent(team, corev1.EventTypeWarning, "RestartLimitExceeded",
			"Teammate %s exceeded maxRestarts=%d; all pods terminated", fatal, maxRestarts(team))
		r.fireTeammateErrorEvents(ctx, team)
		return ctrl.Result{}, r.Status().Update(ctx, team)
	}

	// Spawn any newly unblocked or newly approved teammates.
	for _, tm := range team.Spec.Teammates {
		podName := agentPodName(team, tm.Name)
		pod := &corev1.Pod{}
		if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: team.Namespace}, pod); err == nil {
			continue // Already spawned.
		} else if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		if !r.dependenciesMet(ctx, team, effectiveDependencies(team, tm)) {
			continue
		}
		if stage, ok := stageForTeammate(team, tm.Name); ok && !stageApprovalGranted(team, stage) {
			r.setTeammatePendingApproval(team, tm.Name, "stage-"+stage.Name)
			continue
		}
		if !r.checkApprovalGate(ctx, team, "spawn-"+tm.Name) {
			r.setTeammatePendingApproval(team, tm.Name, "spawn-"+tm.Name)
			continue
		}
		r.clearTeammatePendingApproval(team, tm.Name)
		if err := r.ensureAgentPod(ctx, team, tm.Name, tm.Model, tm.Prompt,
			"auto-accept", false, tm.Resources, tm.Scope,
			tm.Skills, tm.MCPServers, tm.Inputs); err != nil {
			return ctrl.Result{}, fmt.Errorf("spawning teammate %s: %w", tm.Name, err)
		}
		log.Info("Spawned teammate", "name", tm.Name)
	}

	// Check for overall completion.
	allDone, anyFailed, err := r.allPodsComplete(ctx, team)
	if err != nil {
		return ctrl.Result{}, err
	}
	if anyFailed {
		team.Status.Phase = "Failed"
		setCondition(team, metav1.ConditionFalse, "AgentFailed", "One or more agent pods failed")
		r.recordEvent(team, corev1.EventTypeWarning, "AgentFailed", "One or more agent pods failed")
		r.fireTeammateErrorEvents(ctx, team)
		return ctrl.Result{}, r.Status().Update(ctx, team)
	}
	if allDone {
		log.Info("All agents complete, running finalization")
		ready, err := r.runFinalization(ctx, team)
		if err != nil {
			// Hard failure during finalization (e.g. push-branch Job exceeded
			// its backoff limit). Mark the team Failed so the terminal phase
			// cleans up.
			team.Status.Phase = "Failed"
			setCondition(team, metav1.ConditionFalse, "FinalizationFailed", err.Error())
			r.recordEvent(team, corev1.EventTypeWarning, "FinalizationFailed", "%v", err)
			return ctrl.Result{}, r.Status().Update(ctx, team)
		}
		if !ready {
			// Async finalization work still in flight — keep the team in
			// Running and requeue so the next pass checks the Job status.
			setCondition(team, metav1.ConditionTrue, "Finalizing", "Waiting on finalization Job")
			if err := r.Status().Update(ctx, team); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		if err := r.executeOnComplete(ctx, team); err != nil {
			log.Error(err, "OnComplete action failed")
			// Don't fail the team for post-completion actions beyond
			// finalization (e.g. create-pr API errors are logged, not fatal).
		}
		team.Status.Phase = "Completed"
		setCondition(team, metav1.ConditionFalse, "Completed", "All agents finished successfully")
		r.recordEvent(team, corev1.EventTypeNormal, "Completed", "All agents finished successfully")
		return ctrl.Result{}, r.Status().Update(ctx, team)
	}

	// Save any status changes made during this reconcile.
	if err := r.Status().Update(ctx, team); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// --- Phase: Terminal ---

// reconcileTerminal cleans up pods for completed/failed teams.
func (r *AgentTeamReconciler) reconcileTerminal(ctx context.Context, team *claudev1alpha1.AgentTeam) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("Phase: Terminal", "phase", team.Status.Phase)

	if err := r.terminateAllPods(ctx, team); err != nil {
		return ctrl.Result{}, err
	}

	r.recordTerminalMetrics(team)

	if team.Status.CompletedAt == nil {
		now := metav1.Now()
		team.Status.CompletedAt = &now
		return ctrl.Result{}, r.Status().Update(ctx, team)
	}
	return ctrl.Result{}, nil
}

const defaultMaxRestarts int32 = 3

// maxRestarts returns the team's configured maxRestarts limit, falling back to
// the default when unset.
func maxRestarts(team *claudev1alpha1.AgentTeam) int32 {
	if team.Spec.Lifecycle == nil || team.Spec.Lifecycle.MaxRestarts == nil {
		return defaultMaxRestarts
	}
	return *team.Spec.Lifecycle.MaxRestarts
}

// teammateRestartCount returns the RestartCount recorded for a teammate, or 0
// if the teammate has no status entry yet.
func teammateRestartCount(team *claudev1alpha1.AgentTeam, name string) int32 {
	for _, st := range team.Status.Teammates {
		if st.Name == name {
			return st.RestartCount
		}
	}
	return 0
}

// setTeammateRestartCount updates the RestartCount on a teammate's status
// entry in-place. The caller is responsible for persisting the team via
// Status().Update.
func setTeammateRestartCount(team *claudev1alpha1.AgentTeam, name string, count int32) {
	for i, st := range team.Status.Teammates {
		if st.Name == name {
			team.Status.Teammates[i].RestartCount = count
			return
		}
	}
}

// handleTeammateFailures inspects teammate pods for the Failed phase and
// either re-spawns them (when RestartCount < maxRestarts) or reports the
// first teammate that has exhausted its restarts. Returns the exhausted
// teammate's name (or "" when everything is fine) so the caller can transition
// the team to Failed with a meaningful condition message.
//
// Re-spawning a teammate bumps its RestartCount, fires a teammate.error
// webhook event with restart metadata, and increments the
// claude_teammate_restarts_total metric. The newly-spawned pod starts in
// Pending; the next reconcile will resume normal flow once it's Running.
func (r *AgentTeamReconciler) handleTeammateFailures(ctx context.Context, team *claudev1alpha1.AgentTeam) (fatalTeammate string, err error) {
	log := log.FromContext(ctx)
	limit := maxRestarts(team)
	notifier := teamNotifier(team)

	for _, tm := range team.Spec.Teammates {
		podName := agentPodName(team, tm.Name)
		pod := &corev1.Pod{}
		if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: team.Namespace}, pod); err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			return "", err
		}
		if pod.Status.Phase != corev1.PodFailed {
			continue
		}

		currentCount := teammateRestartCount(team, tm.Name)
		if currentCount >= limit {
			return tm.Name, nil
		}

		log.Info("Re-spawning crashed teammate", "name", tm.Name, "restart", currentCount+1, "limit", limit)
		if err := r.Delete(ctx, pod); err != nil && !errors.IsNotFound(err) {
			return "", fmt.Errorf("deleting failed pod %s: %w", podName, err)
		}

		newCount := currentCount + 1
		setTeammateRestartCount(team, tm.Name, newCount)
		metrics.RecordTeammateRestart(team.Name, tm.Name)
		_ = notifier.SendEvent(ctx, "teammate.error", teamEventPayload(team, map[string]interface{}{
			"teammate":     tm.Name,
			"pod":          pod.Name,
			"reason":       pod.Status.Reason,
			"message":      pod.Status.Message,
			"restartCount": int64(newCount),
			"maxRestarts":  int64(limit),
			"action":       "respawn",
		}))
		r.recordEvent(team, corev1.EventTypeWarning, "TeammateRestarted",
			"Teammate %s re-spawned after pod failure (restart %d/%d)", tm.Name, newCount, limit)

		if err := r.ensureAgentPod(ctx, team, tm.Name, tm.Model, tm.Prompt,
			"auto-accept", false, tm.Resources, tm.Scope,
			tm.Skills, tm.MCPServers, tm.Inputs); err != nil {
			return "", fmt.Errorf("re-spawning teammate %s: %w", tm.Name, err)
		}
	}
	return "", nil
}

// fireTeammateErrorEvents emits a `teammate.error` webhook event for every
// failed pod belonging to this team. Called once at the transition into the
// Failed phase so events do not repeat across reconciles.
func (r *AgentTeamReconciler) fireTeammateErrorEvents(ctx context.Context, team *claudev1alpha1.AgentTeam) {
	n := teamNotifier(team)
	if n == nil {
		return
	}
	check := func(teammateName, podName string) {
		pod := &corev1.Pod{}
		if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: team.Namespace}, pod); err != nil {
			return
		}
		if pod.Status.Phase != corev1.PodFailed {
			return
		}
		_ = n.SendEvent(ctx, "teammate.error", teamEventPayload(team, map[string]interface{}{
			"teammate": teammateName,
			"pod":      podName,
			"reason":   pod.Status.Reason,
			"message":  pod.Status.Message,
		}))
	}
	check("lead", agentPodName(team, "lead"))
	for _, tm := range team.Spec.Teammates {
		check(tm.Name, agentPodName(team, tm.Name))
	}
}

// maybeFireBudgetWarning emits the `budget.warning` webhook event the first
// time the team's estimated cost crosses 80% of its configured limit. Uses a
// status Condition to dedupe across reconcile passes and operator restarts.
// No-op when no budget is set or the threshold has not been reached.
func (r *AgentTeamReconciler) maybeFireBudgetWarning(ctx context.Context, team *claudev1alpha1.AgentTeam) {
	if team.Spec.Lifecycle == nil || team.Spec.Lifecycle.BudgetLimit == nil {
		return
	}
	if budgetWarningSent(team) {
		return
	}
	var limit float64
	if _, err := fmt.Sscanf(*team.Spec.Lifecycle.BudgetLimit, "%f", &limit); err != nil || limit <= 0 {
		return
	}
	current := newTeamTracker(team).GetTotalCost()
	if current < 0.8*limit {
		return
	}
	_ = teamNotifier(team).SendEvent(ctx, "budget.warning", teamEventPayload(team, map[string]interface{}{
		"estimatedCost": fmt.Sprintf("%.2f", current),
		"budgetLimit":   fmt.Sprintf("%.2f", limit),
		"threshold":     "80%",
	}))
	markBudgetWarningSent(team, current, limit)
}

// exportBudgetMetrics publishes the team's current estimated cost and remaining
// budget to Prometheus. Called on each reconcileRunning pass after the cost
// estimate has been updated.
func (r *AgentTeamReconciler) exportBudgetMetrics(team *claudev1alpha1.AgentTeam) {
	tracker := newTeamTracker(team)
	current := tracker.GetTotalCost()
	metrics.RecordCost(team.Name, team.Namespace, current)

	if team.Spec.Lifecycle == nil || team.Spec.Lifecycle.BudgetLimit == nil {
		return
	}
	var limit float64
	if _, err := fmt.Sscanf(*team.Spec.Lifecycle.BudgetLimit, "%f", &limit); err != nil {
		return
	}
	metrics.SetBudgetRemaining(team.Name, team.Namespace, limit-current)
}

// recordTerminalMetrics records the team's outcome in Prometheus. Idempotent:
// safe to call on every terminal reconcile pass.
func (r *AgentTeamReconciler) recordTerminalMetrics(team *claudev1alpha1.AgentTeam) {
	if team.Status.Phase == "Completed" {
		var duration float64
		if team.Status.StartedAt != nil {
			duration = time.Since(team.Status.StartedAt.Time).Seconds()
		}
		metrics.RecordTeamComplete(team.Name, team.Namespace, duration)
		return
	}
	metrics.RecordTeamFailed(team.Name, team.Namespace)
}

// --- PVC Management ---

func (r *AgentTeamReconciler) ensurePVC(ctx context.Context, team *claudev1alpha1.AgentTeam, name, storageClass string, accessMode corev1.PersistentVolumeAccessMode, size string) error {
	pvc := &corev1.PersistentVolumeClaim{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: team.Namespace}, pvc); err == nil {
		return nil
	} else if !errors.IsNotFound(err) {
		return err
	}

	qty, err := resource.ParseQuantity(size)
	if err != nil {
		return fmt.Errorf("parsing storage size %q: %w", size, err)
	}

	pvc = &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: team.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{accessMode},
			StorageClassName: &storageClass,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: qty,
				},
			},
		},
	}
	if err := ctrl.SetControllerReference(team, pvc, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, pvc)
}

// --- Init Job (Coding Mode) ---

// ensureInitJob creates the Job that clones the repo and sets up per-teammate worktrees.
func (r *AgentTeamReconciler) ensureInitJob(ctx context.Context, team *claudev1alpha1.AgentTeam) error {
	jobName := initJobName(team)
	job := &batchv1.Job{}
	if err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: team.Namespace}, job); err == nil {
		return nil
	} else if !errors.IsNotFound(err) {
		return err
	}

	repo := team.Spec.Repository
	branch := repo.Branch
	if branch == "" {
		branch = "main"
	}

	teammateNames := make([]string, len(team.Spec.Teammates))
	for i, tm := range team.Spec.Teammates {
		teammateNames[i] = tm.Name
	}

	// The init script clones the repo and sets up one worktree per teammate.
	initScript := fmt.Sprintf(`
set -eu
echo "[init] Cloning %s @ %s"
git clone --branch %s %s /workspace/repo
cd /workspace/repo

echo "[init] Creating per-teammate worktrees"
for tm in %s; do
  git worktree add /workspace/worktrees/$tm -b teammate-$tm
  echo "  worktree: /workspace/worktrees/$tm"
done

echo "[init] Initialising team-state directories"
mkdir -p /state/teams/%s/inboxes
mkdir -p /state/tasks/%s
printf '{"tasks":[],"version":1}\n' > /state/tasks/%s/tasks.json

echo "[init] Done"
`,
		repo.URL, branch, branch, repo.URL,
		strings.Join(teammateNames, " "),
		team.Name, team.Name, team.Name,
	)

	envVars := []corev1.EnvVar{}
	if repo.CredentialsSecret != "" {
		envVars = append(envVars, corev1.EnvVar{
			Name: "GIT_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: repo.CredentialsSecret},
					Key:                  "token",
					Optional:             boolPtr(true),
				},
			},
		})
	}

	backoffLimit := int32(3)
	job = &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: team.Namespace,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyOnFailure,
					Containers: []corev1.Container{
						{
							Name:  "init",
							Image: r.initImage(),
							Command: func() []string {
								if r.SkipInitScript {
									return []string{"sh", "-c", "exit 0"}
								}
								return []string{"sh", "-c", initScript}
							}(),
							Env: envVars,
							VolumeMounts: []corev1.VolumeMount{
								{Name: "repo", MountPath: "/workspace"},
								{Name: "team-state", MountPath: "/state"},
							},
						},
					},
					Volumes: []corev1.Volume{
						pvcVolume("repo", repoPVCName(team)),
						pvcVolume("team-state", teamStatePVCName(team)),
					},
				},
			},
		},
	}
	if err := ctrl.SetControllerReference(team, job, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, job)
}

// ensurePushBranchJob creates the Job that consolidates teammate worktree
// branches into a single branch and pushes it to the remote. Idempotent; a
// pre-existing Job is left alone so reconciler passes after submission just
// poll checkJobStatus.
//
// The script merges each `teammate-<name>` branch (init Job's naming
// convention) into a fresh branch rooted at spec.repository.branch, then
// pushes over HTTPS with the token from the credentials Secret embedded in
// the origin URL.
func (r *AgentTeamReconciler) ensurePushBranchJob(ctx context.Context, team *claudev1alpha1.AgentTeam, consolidatedBranch string) error {
	jobName := pushBranchJobName(team)
	job := &batchv1.Job{}
	if err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: team.Namespace}, job); err == nil {
		return nil
	} else if !errors.IsNotFound(err) {
		return err
	}

	repo := team.Spec.Repository
	baseBranch := repo.Branch
	if baseBranch == "" {
		baseBranch = "main"
	}

	teammateNames := make([]string, len(team.Spec.Teammates))
	for i, tm := range team.Spec.Teammates {
		teammateNames[i] = tm.Name
	}

	script := fmt.Sprintf(`
set -eu
cd /workspace/repo

# Identity for merge commits. These are operator-generated merges; using a
# distinct identity makes git log | grep trivially filter them.
git config user.email "operator@claude-teams.local"
git config user.name "kagents"

# Fresh consolidated branch rooted at the current base tip.
git fetch origin %s
git checkout -B %s origin/%s

# Merge each teammate worktree branch. A teammate that didn't commit anything
# produces "Already up to date." which is fine. A merge conflict fails the Job
# — human resolution required before push.
for TM in %s; do
  git merge --no-ff -m "merge teammate $TM" "teammate-$TM" || {
    echo "[push-branch] merge conflict on teammate $TM"
    exit 1
  }
done

# HTTPS push with token embedded when available. SSH support is a follow-up.
if [ -n "${GIT_TOKEN:-}" ]; then
  ORIGIN_URL="$(git remote get-url origin)"
  case "$ORIGIN_URL" in
    https://*)
      PUSH_URL="$(echo "$ORIGIN_URL" | sed "s|^https://|https://${GIT_TOKEN}@|")"
      git remote set-url origin "$PUSH_URL"
      ;;
  esac
fi
git push origin %s
echo "[push-branch] Pushed %s"
`,
		baseBranch,
		consolidatedBranch, baseBranch,
		strings.Join(teammateNames, " "),
		consolidatedBranch,
		consolidatedBranch,
	)

	// Prefer lifecycle.gitCredentialsSecret, fall back to repository.credentialsSecret.
	credSecret := ""
	if team.Spec.Lifecycle != nil && team.Spec.Lifecycle.GitCredentialsSecret != "" {
		credSecret = team.Spec.Lifecycle.GitCredentialsSecret
	} else if repo.CredentialsSecret != "" {
		credSecret = repo.CredentialsSecret
	}

	envVars := []corev1.EnvVar{}
	if credSecret != "" {
		envVars = append(envVars, corev1.EnvVar{
			Name: "GIT_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: credSecret},
					Key:                  "token",
					Optional:             boolPtr(true),
				},
			},
		})
	}

	backoffLimit := int32(3)
	job = &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: team.Namespace,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyOnFailure,
					Containers: []corev1.Container{
						{
							Name:    "push-branch",
							Image:   r.initImage(),
							Command: []string{"sh", "-c", script},
							Env:     envVars,
							VolumeMounts: []corev1.VolumeMount{
								{Name: "repo", MountPath: "/workspace"},
							},
						},
					},
					Volumes: []corev1.Volume{
						pvcVolume("repo", repoPVCName(team)),
					},
				},
			},
		},
	}
	if err := ctrl.SetControllerReference(team, job, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, job)
}

// renderConsolidatedBranch resolves the branch name template. Defaults to
// "teams/{{.TeamName}}" when unset.
func renderConsolidatedBranch(team *claudev1alpha1.AgentTeam) (string, error) {
	tmpl := "teams/{{.TeamName}}"
	if team.Spec.Lifecycle != nil && team.Spec.Lifecycle.ConsolidatedBranchTemplate != "" {
		tmpl = team.Spec.Lifecycle.ConsolidatedBranchTemplate
	}
	t, err := template.New("consolidated-branch").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, map[string]string{
		"TeamName":  team.Name,
		"Namespace": team.Namespace,
	}); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

// pushBranchJobName returns the Job name for the push-branch consolidation.
// Separate from initJobName so the two can coexist on the same team.
func pushBranchJobName(team *claudev1alpha1.AgentTeam) string {
	return team.Name + "-push-branch"
}

func (r *AgentTeamReconciler) checkJobStatus(ctx context.Context, team *claudev1alpha1.AgentTeam, jobName string) (completed, failed bool, err error) {
	job := &batchv1.Job{}
	if err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: team.Namespace}, job); err != nil {
		if errors.IsNotFound(err) {
			return false, false, nil
		}
		return false, false, err
	}
	if job.Status.Succeeded > 0 {
		return true, false, nil
	}
	limit := int32(3)
	if job.Spec.BackoffLimit != nil {
		limit = *job.Spec.BackoffLimit
	}
	if job.Status.Failed >= limit {
		return false, true, nil
	}
	return false, false, nil
}

// --- Agent Pod Management ---

// ensureAgentPod creates an agent pod if it doesn't already exist.
func (r *AgentTeamReconciler) ensureAgentPod(
	ctx context.Context,
	team *claudev1alpha1.AgentTeam,
	agentName, model, prompt, permissionMode string,
	isLead bool,
	resources corev1.ResourceRequirements,
	scope *claudev1alpha1.ScopeSpec,
	skills []claudev1alpha1.SkillSpec,
	mcpServers []claudev1alpha1.MCPServerSpec,
	inputs []claudev1alpha1.InputSpec,
) error {
	podName := agentPodName(team, agentName)
	pod := &corev1.Pod{}
	if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: team.Namespace}, pod); err == nil {
		return nil
	} else if !errors.IsNotFound(err) {
		return err
	}

	// Provision the agent's RBAC (SA + Role + RoleBinding) before the pod so
	// the kubelet can bind the SA at creation time.
	if err := r.ensureAgentServiceAccount(ctx, team, agentName); err != nil {
		return fmt.Errorf("ensuring ServiceAccount for %s: %w", agentName, err)
	}

	// Create a ConfigMap with the MCP config if this agent has MCP servers.
	if len(mcpServers) > 0 {
		if err := r.ensureMCPConfigMap(ctx, team, agentName, mcpServers); err != nil {
			return fmt.Errorf("ensuring MCP configmap for %s: %w", agentName, err)
		}
	}

	pod = r.buildAgentPod(team, agentName, model, prompt, permissionMode, isLead, resources, scope, skills, mcpServers, inputs)
	if err := ctrl.SetControllerReference(team, pod, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, pod)
}

// ensureAgentServiceAccount provisions the ServiceAccount, Role, and
// RoleBinding for a single agent pod. The Role is narrowly scoped — get on
// the team's API key Secret (restricted to that exact secret name) and
// get/list/watch on the team's PVCs. A compromised agent pod therefore
// cannot enumerate cluster resources or reach other teams' secrets.
//
// The lead and each teammate get their own SA so per-agent compromise stays
// contained. This is the core KubeCon RBAC story; it's worth the churn of
// N resources per team.
//
// Safe to call repeatedly — missing resources are created and existing Roles
// are updated in place if the team's auth secret or PVC set has changed.
func (r *AgentTeamReconciler) ensureAgentServiceAccount(ctx context.Context, team *claudev1alpha1.AgentTeam, agentName string) error {
	name := agentServiceAccountName(team, agentName)

	// ServiceAccount.
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: team.Namespace},
	}
	if err := ctrl.SetControllerReference(team, sa, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, sa); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("creating ServiceAccount %s: %w", name, err)
	}

	// Role. Rules are computed from the team's current auth + PVC set so the
	// Role tracks spec changes.
	rules := agentPolicyRules(team)
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: team.Namespace},
		Rules:      rules,
	}
	if err := ctrl.SetControllerReference(team, role, r.Scheme); err != nil {
		return err
	}
	existing := &rbacv1.Role{}
	switch err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: team.Namespace}, existing); {
	case errors.IsNotFound(err):
		if err := r.Create(ctx, role); err != nil {
			return fmt.Errorf("creating Role %s: %w", name, err)
		}
	case err != nil:
		return err
	default:
		existing.Rules = rules
		if err := r.Update(ctx, existing); err != nil {
			return fmt.Errorf("updating Role %s: %w", name, err)
		}
	}

	// RoleBinding.
	binding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: team.Namespace},
		Subjects: []rbacv1.Subject{
			{Kind: "ServiceAccount", Name: name, Namespace: team.Namespace},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     name,
		},
	}
	if err := ctrl.SetControllerReference(team, binding, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, binding); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("creating RoleBinding %s: %w", name, err)
	}
	return nil
}

// agentPolicyRules returns the per-agent Role rule set: read the team's API
// key Secret (by exact name), and read the team's PVCs. Rules are only
// emitted for resources that actually exist on the team — e.g. an OAuth-only
// team has no Secret rule.
func agentPolicyRules(team *claudev1alpha1.AgentTeam) []rbacv1.PolicyRule {
	var rules []rbacv1.PolicyRule

	secretNames := []string{}
	if team.Spec.Auth.APIKeySecret != "" {
		secretNames = append(secretNames, team.Spec.Auth.APIKeySecret)
	}
	if team.Spec.Auth.OAuthSecret != "" {
		secretNames = append(secretNames, team.Spec.Auth.OAuthSecret)
	}
	if len(secretNames) > 0 {
		rules = append(rules, rbacv1.PolicyRule{
			APIGroups:     []string{""},
			Resources:     []string{"secrets"},
			ResourceNames: secretNames,
			Verbs:         []string{"get"},
		})
	}

	if pvcs := teamPVCNames(team); len(pvcs) > 0 {
		rules = append(rules, rbacv1.PolicyRule{
			APIGroups:     []string{""},
			Resources:     []string{"persistentvolumeclaims"},
			ResourceNames: pvcs,
			Verbs:         []string{"get", "list", "watch"},
		})
	}
	return rules
}

// ensureMCPConfigMap creates a ConfigMap with the agent's .mcp.json content.
func (r *AgentTeamReconciler) ensureMCPConfigMap(ctx context.Context, team *claudev1alpha1.AgentTeam, agentName string, servers []claudev1alpha1.MCPServerSpec) error {
	cmName := agentMCPConfigMapName(team, agentName)
	cm := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{Name: cmName, Namespace: team.Namespace}, cm); err == nil {
		return nil
	} else if !errors.IsNotFound(err) {
		return err
	}

	type mcpEntry struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	}
	mcpMap := map[string]mcpEntry{}
	for _, s := range servers {
		mcpMap[s.Name] = mcpEntry{Type: "sse", URL: s.URL}
	}
	wrapped := map[string]interface{}{"mcpServers": mcpMap}
	data, err := json.Marshal(wrapped)
	if err != nil {
		return fmt.Errorf("marshalling MCP config: %w", err)
	}

	cm = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: team.Namespace,
		},
		Data: map[string]string{
			"mcp.json": string(data),
		},
	}
	if err := ctrl.SetControllerReference(team, cm, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, cm)
}

// buildAgentPod constructs a Pod spec for a Claude Code agent.
func (r *AgentTeamReconciler) buildAgentPod(
	team *claudev1alpha1.AgentTeam,
	agentName, model, prompt, permissionMode string,
	isLead bool,
	resources corev1.ResourceRequirements,
	scope *claudev1alpha1.ScopeSpec,
	skills []claudev1alpha1.SkillSpec,
	mcpServers []claudev1alpha1.MCPServerSpec,
	inputs []claudev1alpha1.InputSpec,
) *corev1.Pod {
	role := "teammate"
	if isLead {
		role = "lead"
	}

	labels := map[string]string{
		"app.kubernetes.io/name":      "kagents",
		"app.kubernetes.io/instance":  team.Name,
		"app.kubernetes.io/component": agentName,
		"kagents.dev/team":            team.Name,
		"kagents.dev/role":            role,
	}

	// Protocol-activation env vars come from the harness adapter; everything
	// else (model selection, permission mode, prompt, auth, scope) is
	// harness-neutral and contributed by the operator itself.
	envVars := r.harnessFor(team).ProtocolEnv(harness.AgentRole(role), agentName, team)
	envVars = append(envVars,
		corev1.EnvVar{Name: "CLAUDE_MODEL", Value: model},
		corev1.EnvVar{Name: "CLAUDE_PERMISSION_MODE", Value: permissionMode},
		corev1.EnvVar{Name: "AGENT_PROMPT", Value: prompt},
	)

	// Auth: prefer API key, fall back to OAuth.
	if team.Spec.Auth.APIKeySecret != "" {
		envVars = append(envVars, corev1.EnvVar{
			Name: "ANTHROPIC_API_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: team.Spec.Auth.APIKeySecret},
					Key:                  "ANTHROPIC_API_KEY",
				},
			},
		})
	} else if team.Spec.Auth.OAuthSecret != "" {
		envVars = append(envVars, corev1.EnvVar{
			Name: "CLAUDE_OAUTH_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: team.Spec.Auth.OAuthSecret},
					Key:                  "CLAUDE_OAUTH_TOKEN",
				},
			},
		})
	}

	// Scope: pass include/exclude paths to the entrypoint.
	if scope != nil {
		if len(scope.IncludePaths) > 0 {
			envVars = append(envVars, corev1.EnvVar{
				Name:  "SCOPE_INCLUDE_PATHS",
				Value: strings.Join(scope.IncludePaths, ":"),
			})
		}
		if len(scope.ExcludePaths) > 0 {
			envVars = append(envVars, corev1.EnvVar{
				Name:  "SCOPE_EXCLUDE_PATHS",
				Value: strings.Join(scope.ExcludePaths, ":"),
			})
		}
	}

	// In coding mode, route each teammate to their own worktree.
	if team.Spec.Repository != nil && !isLead {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "WORKTREE_PATH",
			Value: fmt.Sprintf("worktrees/%s", agentName),
		})
	}

	volumes := []corev1.Volume{
		// team-state is mounted at /var/claude-state; entrypoint symlinks teams/ and tasks/ into ~/.claude/.
		pvcVolume("team-state", teamStatePVCName(team)),
	}
	volumeMounts := []corev1.VolumeMount{
		{Name: "team-state", MountPath: "/var/claude-state"},
	}

	// Coding mode: mount the repo PVC.
	if team.Spec.Repository != nil {
		volumes = append(volumes, pvcVolume("repo", repoPVCName(team)))
		volumeMounts = append(volumeMounts, corev1.VolumeMount{Name: "repo", MountPath: "/workspace"})
	}

	// Cowork mode: mount output PVC and all input volumes.
	if team.Spec.Workspace != nil {
		if team.Spec.Workspace.Output != nil {
			outPVCName := team.Spec.Workspace.Output.PVC
			if outPVCName == "" {
				outPVCName = outputPVCName(team)
			}
			mountPath := team.Spec.Workspace.Output.MountPath
			if mountPath == "" {
				mountPath = "/workspace/output"
			}
			volumes = append(volumes, pvcVolume("workspace-output", outPVCName))
			volumeMounts = append(volumeMounts, corev1.VolumeMount{Name: "workspace-output", MountPath: mountPath})
		}
		for i, input := range team.Spec.Workspace.Inputs {
			volName := fmt.Sprintf("workspace-input-%d", i)
			if input.ConfigMap != "" {
				volumes = append(volumes, corev1.Volume{
					Name: volName,
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: input.ConfigMap},
						},
					},
				})
			} else if input.PVC != "" {
				volumes = append(volumes, pvcVolumeReadOnly(volName, input.PVC))
			} else {
				continue
			}
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      volName,
				MountPath: input.MountPath,
				ReadOnly:  true,
			})
		}
	}

	// Skills: each skill is materialized at /var/claude-skills/{name}/;
	// the runner entrypoint copies that directory into ~/.claude/skills/{name}/
	// before launching Claude Code.
	//
	// ConfigMap source: mount directly. Cheapest path — no network, no
	// extra container, just a projected ConfigMap volume.
	//
	// OCI source: an init container pulls the artifact via `oras` into
	// a per-skill emptyDir that the main container also mounts. Pull
	// credentials come from spec.imagePullSecrets — the first listed
	// kubernetes.io/dockerconfigjson Secret is projected at
	// /auth/.docker/config.json and DOCKER_CONFIG points at it, which
	// ORAS picks up natively. Multi-registry deployments combine creds
	// into a single dockerconfigjson; this keeps the init container's
	// auth surface to one mount.
	var skillInitContainers []corev1.Container
	needSkillAuth := false
	for _, skill := range skills {
		volName := "skill-" + skill.Name
		switch {
		case skill.Source.ConfigMap != "":
			volumes = append(volumes, corev1.Volume{
				Name: volName,
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: skill.Source.ConfigMap},
					},
				},
			})
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      volName,
				MountPath: fmt.Sprintf("/var/claude-skills/%s", skill.Name),
				ReadOnly:  true,
			})
		case skill.Source.OCI != "":
			volumes = append(volumes, corev1.Volume{
				Name:         volName,
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			})
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      volName,
				MountPath: fmt.Sprintf("/var/claude-skills/%s", skill.Name),
				ReadOnly:  true,
			})
			initMounts := []corev1.VolumeMount{
				{Name: volName, MountPath: "/skill-out"},
			}
			var initEnv []corev1.EnvVar
			if len(team.Spec.ImagePullSecrets) > 0 {
				needSkillAuth = true
				initMounts = append(initMounts, corev1.VolumeMount{
					Name: "skill-auth", MountPath: "/auth/.docker", ReadOnly: true,
				})
				initEnv = append(initEnv, corev1.EnvVar{Name: "DOCKER_CONFIG", Value: "/auth/.docker"})
			}
			skillInitContainers = append(skillInitContainers, corev1.Container{
				Name:         "pull-skill-" + skill.Name,
				Image:        r.skillPullerImage(),
				Command:      []string{"oras", "pull", "--output", "/skill-out", skill.Source.OCI},
				Env:          initEnv,
				VolumeMounts: initMounts,
			})
		}
	}
	if needSkillAuth {
		// Project the first imagePullSecret's .dockerconfigjson into a
		// fixed path the init containers read. Items[] is explicit so
		// the mounted filename is always "config.json", regardless of
		// the Secret's key name preference.
		volumes = append(volumes, corev1.Volume{
			Name: "skill-auth",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: team.Spec.ImagePullSecrets[0].Name,
					Items:      []corev1.KeyToPath{{Key: ".dockerconfigjson", Path: "config.json"}},
				},
			},
		})
	}

	// MCP config: mount the per-agent ConfigMap at /var/claude-mcp/mcp.json.
	// The entrypoint copies it to ~/.mcp.json.
	if len(mcpServers) > 0 {
		cmName := agentMCPConfigMapName(team, agentName)
		volumes = append(volumes, corev1.Volume{
			Name: "mcp-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "mcp-config",
			MountPath: "/var/claude-mcp",
			ReadOnly:  true,
		})
	}

	// Output routing: stage each upstream-produced artifact at this teammate's
	// requested mountPath via a per-input init container. The same shared
	// workspace-output PVC that the producer wrote to is mounted into the
	// init container at the team's standard output path; the artifact is then
	// copied to a per-input emptyDir mounted at MountPath, which the main
	// container also mounts so the agent sees the artifact at
	// {MountPath}/{Artifact}.
	//
	// Misconfigured inputs (no matching producer / no matching artifact) are
	// skipped silently; the teammate still launches, just without that input
	// staged. The implicit-dependency wiring in effectiveDependencies ensures
	// the producer has already reached Succeeded before this pod is created.
	var initContainers []corev1.Container
	// Skill pulls run first so they're available before any input
	// staging that might reference skill-produced configs.
	initContainers = append(initContainers, skillInitContainers...)
	if team.Spec.Workspace != nil && team.Spec.Workspace.Output != nil && len(inputs) > 0 {
		outMountPath := team.Spec.Workspace.Output.MountPath
		if outMountPath == "" {
			outMountPath = "/workspace/output"
		}
		for i, in := range inputs {
			producerPath := findProducerOutputPath(team, in.From, in.Artifact)
			if producerPath == "" {
				continue
			}
			volName := fmt.Sprintf("input-%d", i)
			volumes = append(volumes, corev1.Volume{
				Name:         volName,
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			})
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      volName,
				MountPath: in.MountPath,
				ReadOnly:  true,
			})
			initContainers = append(initContainers, corev1.Container{
				Name:  fmt.Sprintf("stage-input-%d", i),
				Image: r.agentImage(),
				Command: []string{"sh", "-c",
					fmt.Sprintf("mkdir -p %q && cp %q %q",
						in.MountPath, producerPath, filepath.Join(in.MountPath, in.Artifact))},
				VolumeMounts: []corev1.VolumeMount{
					{Name: "workspace-output", MountPath: outMountPath, ReadOnly: true},
					{Name: volName, MountPath: in.MountPath},
				},
			})
		}
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agentPodName(team, agentName),
			Namespace: team.Namespace,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			RestartPolicy:      corev1.RestartPolicyNever,
			ServiceAccountName: agentServiceAccountName(team, agentName),
			ImagePullSecrets:   team.Spec.ImagePullSecrets,
			InitContainers:     initContainers,
			Containers: []corev1.Container{
				{
					Name:         "claude-code",
					Image:        r.agentImage(),
					Command:      r.AgentCommand,
					Env:          envVars,
					VolumeMounts: volumeMounts,
					Resources:    resources,
				},
			},
			Volumes: volumes,
		},
	}

	return pod
}

// --- Status Sync ---

// syncPodStatuses refreshes team.Status.Lead and team.Status.Teammates from
// current pod phases and computes team.Status.Ready ("running+completed/total").
// Every teammate declared in the spec is ensured a status entry — even before
// its pod is scheduled (dependsOn, approval gate, or first reconcile) — so
// `kubectl describe` surfaces the full roster with a "Waiting" phase for
// anything not yet deployed.
//
// TasksCompleted, TasksClaimed, and PendingApproval are preserved across
// reconciles; they are populated by the approval-gate helpers and will be
// filled from the shared task list in a later milestone. Transient API
// errors when fetching a teammate pod leave the existing phase untouched
// rather than clobbering it with "Waiting".
func (r *AgentTeamReconciler) syncPodStatuses(ctx context.Context, team *claudev1alpha1.AgentTeam) {
	// Lead pod.
	leadPod := &corev1.Pod{}
	if err := r.Get(ctx, types.NamespacedName{Name: agentPodName(team, "lead"), Namespace: team.Namespace}, leadPod); err == nil {
		if team.Status.Lead == nil {
			team.Status.Lead = &claudev1alpha1.AgentStatus{}
		}
		team.Status.Lead.PodName = leadPod.Name
		team.Status.Lead.Phase = podPhaseToAgentPhase(leadPod)
	}

	// Preserve existing non-pod fields (TasksCompleted/Claimed, PendingApproval)
	// across the rebuild below.
	prev := map[string]claudev1alpha1.TeammateStatus{}
	for _, st := range team.Status.Teammates {
		prev[st.Name] = st
	}

	rebuilt := make([]claudev1alpha1.TeammateStatus, 0, len(team.Spec.Teammates))
	ready := 0
	for _, tm := range team.Spec.Teammates {
		st := prev[tm.Name]
		st.Name = tm.Name

		pod := &corev1.Pod{}
		err := r.Get(ctx, types.NamespacedName{Name: agentPodName(team, tm.Name), Namespace: team.Namespace}, pod)
		switch {
		case err == nil:
			st.PodName = pod.Name
			st.Phase = podPhaseToAgentPhase(pod)
			// Record any declared outputs as ArtifactStatus entries the first
			// time we observe the producer pod in Succeeded. Idempotent; safe
			// to call on every reconcile thereafter.
			if pod.Status.Phase == corev1.PodSucceeded {
				recordTeammateArtifacts(team, tm, time.Now())
			}
		case errors.IsNotFound(err):
			st.PodName = ""
			st.Phase = "Waiting"
		}
		// On other (transient) errors, leave st.Phase and st.PodName as-is.

		if st.Phase == "Running" || st.Phase == "Completed" {
			ready++
		}
		rebuilt = append(rebuilt, st)
	}
	team.Status.Teammates = rebuilt
	team.Status.Ready = fmt.Sprintf("%d/%d", ready, len(team.Spec.Teammates))
}

func podPhaseToAgentPhase(pod *corev1.Pod) string {
	switch pod.Status.Phase {
	case corev1.PodPending:
		return "Pending"
	case corev1.PodRunning:
		return "Running"
	case corev1.PodSucceeded:
		return "Completed"
	case corev1.PodFailed:
		return "Failed"
	default:
		return "Pending"
	}
}

// --- Completion ---

func (r *AgentTeamReconciler) allPodsComplete(ctx context.Context, team *claudev1alpha1.AgentTeam) (allDone, anyFailed bool, err error) {
	checkPod := func(name string) (done, failed bool, err error) {
		pod := &corev1.Pod{}
		if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: team.Namespace}, pod); err != nil {
			if errors.IsNotFound(err) {
				return false, false, nil // Not yet spawned — keep waiting.
			}
			return false, false, err
		}
		switch pod.Status.Phase {
		case corev1.PodSucceeded:
			return true, false, nil
		case corev1.PodFailed:
			return false, true, nil
		default:
			return false, false, nil
		}
	}

	// Scan every pod (lead + all teammates) in a single pass so that a failed
	// teammate is detected even while the lead is still running.
	allSucceeded := true

	done, failed, err := checkPod(agentPodName(team, "lead"))
	if err != nil {
		return false, false, err
	}
	if failed {
		return false, true, nil
	}
	if !done {
		allSucceeded = false
	}

	for _, tm := range team.Spec.Teammates {
		done, failed, err := checkPod(agentPodName(team, tm.Name))
		if err != nil {
			return false, false, err
		}
		if failed {
			return false, true, nil
		}
		if !done {
			allSucceeded = false
		}
	}
	return allSucceeded, false, nil
}

// runFinalization handles async post-completion work — currently just the
// push-branch Job, which both OnComplete=push-branch and OnComplete=create-pr
// need. Returns ready=true when there's nothing async outstanding and the
// caller can transition to Completed; ready=false means "come back next
// reconcile, job still running". An error means the finalization failed
// terminally (e.g. Job backoff exhausted).
//
// Safe to call on every reconcile pass while allDone is true — it's a pure
// check-and-act state machine gated on team.Status.ConsolidatedBranch and
// the Job's status.
func (r *AgentTeamReconciler) runFinalization(ctx context.Context, team *claudev1alpha1.AgentTeam) (ready bool, err error) {
	log := log.FromContext(ctx)

	if team.Spec.Lifecycle == nil {
		return true, nil
	}
	mode := team.Spec.Lifecycle.OnComplete
	if mode != "push-branch" && mode != "create-pr" {
		return true, nil
	}
	// Both modes need a repo; without one there's nothing to consolidate.
	if team.Spec.Repository == nil || team.Spec.Repository.URL == "" {
		return true, nil
	}
	// Already finalized in a prior reconcile.
	if team.Status.ConsolidatedBranch != "" {
		return true, nil
	}

	branch, err := renderConsolidatedBranch(team)
	if err != nil {
		return false, fmt.Errorf("rendering consolidated branch: %w", err)
	}

	if err := r.ensurePushBranchJob(ctx, team, branch); err != nil {
		return false, fmt.Errorf("ensuring push-branch Job: %w", err)
	}

	done, failed, err := r.checkJobStatus(ctx, team, pushBranchJobName(team))
	if err != nil {
		return false, err
	}
	if failed {
		return false, fmt.Errorf("push-branch Job exceeded backoff limit")
	}
	if !done {
		log.Info("push-branch Job still running", "branch", branch)
		return false, nil
	}

	team.Status.ConsolidatedBranch = branch
	r.recordEvent(team, corev1.EventTypeNormal, "BranchPushed", "Pushed consolidated branch %s", branch)
	return true, nil
}

func (r *AgentTeamReconciler) executeOnComplete(ctx context.Context, team *claudev1alpha1.AgentTeam) error {
	if team.Spec.Lifecycle == nil {
		return nil
	}
	log := log.FromContext(ctx)
	switch team.Spec.Lifecycle.OnComplete {
	case "notify":
		if team.Spec.Observability != nil && team.Spec.Observability.Webhook != nil {
			return r.sendWebhookEvent(ctx, team.Spec.Observability.Webhook.URL, "completed", team)
		}
	case "create-pr":
		if err := r.executeCreatePR(ctx, team); err != nil {
			log.Error(err, "create-pr failed")
			r.recordEvent(team, corev1.EventTypeWarning, "PRCreationFailed", "%v", err)
			return err
		}
	case "push-branch":
		// The actual consolidation runs in runFinalization before this
		// function is invoked, so by the time we land here the branch is
		// already pushed and team.Status.ConsolidatedBranch is populated.
		// This case exists just so the enum value is terminal-ish and the
		// log message reflects what happened.
		if team.Status.ConsolidatedBranch == "" {
			log.Info("push-branch: no consolidation ran (missing repo URL?)")
		} else {
			log.Info("push-branch: consolidated branch pushed", "branch", team.Status.ConsolidatedBranch)
		}
	case "deliver":
		r.executeDelivery(ctx, team)
	}
	return nil
}

// executeDelivery dispatches each DeliveryTarget in spec.lifecycle.delivery
// to its registered sender, recording a DeliveryStatus entry per target
// on team.Status.Delivery regardless of outcome. Failures are surfaced
// as events + status but never return as errors — the design's stance
// is that delivery is best-effort and a partial delivery (e.g. Slack
// posted, email broke) shouldn't fail the team retroactively.
//
// Idempotent at the executeOnComplete level: the caller only invokes
// this once per team's lifecycle (when transitioning to a terminal
// phase). The function itself doesn't guard against re-invocation —
// the reconciler's transition logic is the gate.
func (r *AgentTeamReconciler) executeDelivery(ctx context.Context, team *claudev1alpha1.AgentTeam) {
	if team.Spec.Lifecycle == nil || len(team.Spec.Lifecycle.Delivery) == 0 {
		return
	}
	dispatcher := r.deliveryDispatcher()
	now := time.Now()
	for _, target := range team.Spec.Lifecycle.Delivery {
		status := claudev1alpha1.DeliveryStatus{
			Type:        target.Type,
			Target:      delivery.TargetLabel(target),
			DeliveredAt: metav1.NewTime(now),
			Success:     true,
		}
		if err := dispatcher.Send(ctx, r.Client, target, team); err != nil {
			status.Success = false
			status.Error = err.Error()
			metrics.RecordDeliveryFailure(team.Name, team.Namespace, target.Type)
			r.recordEvent(team, corev1.EventTypeWarning, "DeliveryFailed",
				"Delivery %s to %s failed: %v", target.Type, status.Target, err)
		} else {
			metrics.RecordDeliverySuccess(team.Name, team.Namespace, target.Type)
			r.recordEvent(team, corev1.EventTypeNormal, "DeliveryComplete",
				"Delivery %s to %s succeeded", target.Type, status.Target)
		}
		team.Status.Delivery = append(team.Status.Delivery, status)
	}
}

// --- Pull Request Creation ---

const (
	defaultPRTitleTemplate = "claude-teams: {{.TeamName}}"
	defaultBaseBranch      = "main"
)

// executeCreatePR opens a GitHub pull request for a completed coding team.
// Requires spec.repository.url (to parse owner/repo) and
// spec.lifecycle.githubTokenSecret (to authenticate). Reviewers and labels
// from spec.lifecycle.pullRequest are applied best-effort; failures there
// are logged but do not fail the overall operation — the PR already exists.
//
// Writes the created PR's URL and state into status.pullRequest. Safe to
// call repeatedly; a PR that already exists does not re-trigger the
// HTTP request because executeOnComplete only runs once per team.
func (r *AgentTeamReconciler) executeCreatePR(ctx context.Context, team *claudev1alpha1.AgentTeam) error {
	log := log.FromContext(ctx)

	if team.Spec.Repository == nil || team.Spec.Repository.URL == "" {
		return fmt.Errorf("create-pr requires spec.repository.url")
	}
	if team.Spec.Lifecycle == nil || team.Spec.Lifecycle.GitHubTokenSecret == "" {
		return fmt.Errorf("create-pr requires spec.lifecycle.githubTokenSecret")
	}

	owner, repo, err := github.ParseRepo(team.Spec.Repository.URL)
	if err != nil {
		return fmt.Errorf("parsing repo URL: %w", err)
	}

	token, err := r.readGitHubToken(ctx, team)
	if err != nil {
		return err
	}

	title, err := renderPRTitle(team)
	if err != nil {
		return fmt.Errorf("rendering PR title: %w", err)
	}
	body := buildPRBody(team)

	head, base := prBranches(team)
	var clientOpts []github.Option
	if r.GitHubBaseURL != "" {
		clientOpts = append(clientOpts, github.WithBaseURL(r.GitHubBaseURL))
	}
	client := github.NewClient(token, clientOpts...)
	pr, err := client.CreatePullRequest(ctx, owner, repo, &github.PullRequestRequest{
		Title: title,
		Body:  body,
		Head:  head,
		Base:  base,
	})
	if err != nil {
		return fmt.Errorf("creating pull request: %w", err)
	}

	team.Status.PullRequest = &claudev1alpha1.PullRequestStatus{
		URL:   pr.HTMLURL,
		State: pr.State,
	}
	r.recordEvent(team, corev1.EventTypeNormal, "PullRequestCreated", "Opened PR %s", pr.HTMLURL)
	log.Info("Pull request created", "url", pr.HTMLURL, "number", pr.Number)

	// Reviewers + labels are nice-to-haves: if the token cannot modify them
	// (e.g. missing scopes on a read-write-to-pulls-only PAT), the PR is
	// still useful. Log the error and keep going.
	if prSpec := team.Spec.Lifecycle.PullRequest; prSpec != nil {
		if err := client.RequestReviewers(ctx, owner, repo, pr.Number, prSpec.Reviewers); err != nil {
			log.Error(err, "requesting reviewers", "reviewers", prSpec.Reviewers)
		}
		if err := client.AddLabels(ctx, owner, repo, pr.Number, prSpec.Labels); err != nil {
			log.Error(err, "adding labels", "labels", prSpec.Labels)
		}
	}
	return nil
}

// readGitHubToken loads the GitHub token from the configured Secret. The
// Secret must have a key named GITHUB_TOKEN.
func (r *AgentTeamReconciler) readGitHubToken(ctx context.Context, team *claudev1alpha1.AgentTeam) (string, error) {
	name := team.Spec.Lifecycle.GitHubTokenSecret
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: team.Namespace}, secret); err != nil {
		return "", fmt.Errorf("reading GitHub token secret %s: %w", name, err)
	}
	token, ok := secret.Data["GITHUB_TOKEN"]
	if !ok || len(token) == 0 {
		return "", fmt.Errorf("secret %s is missing key GITHUB_TOKEN", name)
	}
	return strings.TrimSpace(string(token)), nil
}

// renderPRTitle resolves the PR title template. Precedence:
// Lifecycle.PRTitleTemplate > Lifecycle.PullRequest.TitleTemplate > default.
func renderPRTitle(team *claudev1alpha1.AgentTeam) (string, error) {
	tmpl := defaultPRTitleTemplate
	if team.Spec.Lifecycle != nil {
		if team.Spec.Lifecycle.PRTitleTemplate != "" {
			tmpl = team.Spec.Lifecycle.PRTitleTemplate
		} else if team.Spec.Lifecycle.PullRequest != nil && team.Spec.Lifecycle.PullRequest.TitleTemplate != "" {
			tmpl = team.Spec.Lifecycle.PullRequest.TitleTemplate
		}
	}
	t, err := template.New("pr-title").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, map[string]string{
		"TeamName":  team.Name,
		"Namespace": team.Namespace,
	}); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

// buildPRBody renders the PR body from the team's status. Lists completed
// task counts and the list of teammates that contributed.
func buildPRBody(team *claudev1alpha1.AgentTeam) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Automated pull request from Claude Agent Teams.\n\n")
	fmt.Fprintf(&b, "**Team:** `%s/%s`\n\n", team.Namespace, team.Name)

	if team.Status.Tasks != nil {
		fmt.Fprintf(&b, "## Tasks\n\n- Completed: %d\n- Total: %d\n\n",
			team.Status.Tasks.Completed, team.Status.Tasks.Total)
	}

	if len(team.Status.Teammates) > 0 {
		fmt.Fprintf(&b, "## Teammates\n\n")
		for _, tm := range team.Status.Teammates {
			fmt.Fprintf(&b, "- `%s` — phase=%s, tasksCompleted=%d, restarts=%d\n",
				tm.Name, tm.Phase, tm.TasksCompleted, tm.RestartCount)
		}
		fmt.Fprintln(&b)
	}

	if team.Status.EstimatedCost != "" {
		fmt.Fprintf(&b, "**Estimated cost:** $%s\n\n", team.Status.EstimatedCost)
	}

	fmt.Fprintln(&b, "---")
	fmt.Fprintln(&b, "Generated by [kagents](https://github.com/amcheste/kagents).")
	return b.String()
}

// prBranches returns the head and base branches for the PR.
//
// Head selection (in order): the consolidated branch from push-branch if
// runFinalization wrote one, otherwise spec.repository.branch (useful for
// single-teammate teams that committed directly to their work branch),
// otherwise the default.
//
// Base: spec.lifecycle.pullRequest.targetBranch, default "main".
func prBranches(team *claudev1alpha1.AgentTeam) (head, base string) {
	head = ""
	if team.Status.ConsolidatedBranch != "" {
		head = team.Status.ConsolidatedBranch
	} else if team.Spec.Repository != nil {
		head = team.Spec.Repository.Branch
	}
	if head == "" {
		head = defaultBaseBranch
	}
	base = defaultBaseBranch
	if team.Spec.Lifecycle != nil && team.Spec.Lifecycle.PullRequest != nil && team.Spec.Lifecycle.PullRequest.TargetBranch != "" {
		base = team.Spec.Lifecycle.PullRequest.TargetBranch
	}
	return head, base
}

// --- Approval Gates ---

func (r *AgentTeamReconciler) checkApprovalGate(ctx context.Context, team *claudev1alpha1.AgentTeam, event string) bool {
	if team.Spec.Lifecycle == nil || len(team.Spec.Lifecycle.ApprovalGates) == 0 {
		return true
	}
	var gate *claudev1alpha1.ApprovalGateSpec
	for i := range team.Spec.Lifecycle.ApprovalGates {
		if team.Spec.Lifecycle.ApprovalGates[i].Event == event {
			gate = &team.Spec.Lifecycle.ApprovalGates[i]
			break
		}
	}
	if gate == nil {
		return true // No gate for this event.
	}

	// Check for the approval annotation.
	annotationKey := "approved.kagents.dev/" + event
	if team.Annotations[annotationKey] == "true" {
		return true
	}

	// Send webhook notification so an external system can approve.
	if gate.Channel == "webhook" && gate.WebhookURL != "" {
		if err := r.sendWebhookEvent(ctx, gate.WebhookURL, event, team); err != nil {
			log.FromContext(ctx).Error(err, "Failed to send approval webhook", "event", event)
		}
	}
	return false
}

func (r *AgentTeamReconciler) sendWebhookEvent(ctx context.Context, url, event string, team *claudev1alpha1.AgentTeam) error {
	payload := map[string]string{
		"event":     event,
		"team":      team.Name,
		"namespace": team.Namespace,
		"phase":     team.Status.Phase,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body)) //nolint:gosec // URL from trusted CR
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook %s returned HTTP %d", url, resp.StatusCode)
	}
	return nil
}

func (r *AgentTeamReconciler) setTeammatePendingApproval(team *claudev1alpha1.AgentTeam, tmName, event string) {
	for i := range team.Status.Teammates {
		if team.Status.Teammates[i].Name == tmName {
			team.Status.Teammates[i].PendingApproval = event
			return
		}
	}
	team.Status.Teammates = append(team.Status.Teammates, claudev1alpha1.TeammateStatus{
		Name:            tmName,
		PendingApproval: event,
	})
}

func (r *AgentTeamReconciler) clearTeammatePendingApproval(team *claudev1alpha1.AgentTeam, tmName string) {
	for i := range team.Status.Teammates {
		if team.Status.Teammates[i].Name == tmName {
			team.Status.Teammates[i].PendingApproval = ""
			return
		}
	}
}

// --- Dependency Ordering ---

// dependenciesMet returns true if all pods named in deps have Succeeded.
func (r *AgentTeamReconciler) dependenciesMet(ctx context.Context, team *claudev1alpha1.AgentTeam, deps []string) bool {
	for _, dep := range deps {
		pod := &corev1.Pod{}
		if err := r.Get(ctx, types.NamespacedName{Name: agentPodName(team, dep), Namespace: team.Namespace}, pod); err != nil {
			return false
		}
		if pod.Status.Phase != corev1.PodSucceeded {
			return false
		}
	}
	return true
}

// effectiveDependencies returns the set of teammate names this teammate must
// wait for before spawning. Sources, in order:
//
//   - When team.Spec.Pipeline is set, every teammate in every upstream
//     stage of this teammate's stage (the pipeline graph drives ordering;
//     per-teammate DependsOn is mutually exclusive and CEL-rejected).
//   - Otherwise, the explicit DependsOn list on TeammateSpec.
//   - In both cases, every Inputs[].From — data-flow dependencies always
//     contribute regardless of the ordering mode.
//
// Duplicates and empty names are dropped so the result is safe to pass to
// dependenciesMet. The team argument may be nil for unit tests that only
// exercise the DependsOn + Inputs paths.
func effectiveDependencies(team *claudev1alpha1.AgentTeam, tm claudev1alpha1.TeammateSpec) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	add := func(name string) {
		if name == "" {
			return
		}
		if _, dup := seen[name]; dup {
			return
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}

	if team != nil && team.Spec.Pipeline != nil {
		for _, dep := range pipelineDependencies(team, tm.Name) {
			add(dep)
		}
	} else {
		for _, d := range tm.DependsOn {
			add(d)
		}
	}
	for _, in := range tm.Inputs {
		add(in.From)
	}
	return out
}

// stageForTeammate locates the StageSpec that contains the named teammate
// within team.Spec.Pipeline. Returns (zero, false) when no pipeline is set
// or the teammate is not assigned to any stage — a misconfiguration in the
// latter case that the reconciler treats as "no pipeline-derived deps" so
// the teammate still has a chance to spawn via Inputs/DependsOn.
func stageForTeammate(team *claudev1alpha1.AgentTeam, tmName string) (claudev1alpha1.StageSpec, bool) {
	if team == nil || team.Spec.Pipeline == nil {
		return claudev1alpha1.StageSpec{}, false
	}
	for _, st := range team.Spec.Pipeline.Stages {
		for _, t := range st.Teammates {
			if t == tmName {
				return st, true
			}
		}
	}
	return claudev1alpha1.StageSpec{}, false
}

// pipelineDependencies returns the names of teammates that must complete
// before the named teammate can spawn, derived from the pipeline graph:
// every teammate in every stage listed in this teammate's stage's DependsOn.
// Returns nil when no pipeline is set or the teammate isn't in any stage.
func pipelineDependencies(team *claudev1alpha1.AgentTeam, tmName string) []string {
	stage, ok := stageForTeammate(team, tmName)
	if !ok {
		return nil
	}
	if len(stage.DependsOn) == 0 {
		return nil
	}
	// Index stages by name for fast lookup.
	byName := make(map[string]claudev1alpha1.StageSpec, len(team.Spec.Pipeline.Stages))
	for _, st := range team.Spec.Pipeline.Stages {
		byName[st.Name] = st
	}
	var deps []string
	for _, depStage := range stage.DependsOn {
		st, ok := byName[depStage]
		if !ok {
			continue
		}
		deps = append(deps, st.Teammates...)
	}
	return deps
}

// stageApprovalGranted reports whether the stage's approval gate is
// satisfied. A stage without ApprovalRequired is always granted; one
// with ApprovalRequired needs the annotation
// `approved.kagents.dev/stage-{name}=true` on the team.
func stageApprovalGranted(team *claudev1alpha1.AgentTeam, stage claudev1alpha1.StageSpec) bool {
	if !stage.ApprovalRequired {
		return true
	}
	return team.Annotations["approved.kagents.dev/stage-"+stage.Name] == "true"
}

// updatePipelineStatus refreshes team.Status.Pipeline from current teammate
// pod phases when spec.pipeline is set, or clears the field when not.
// Must be called after syncPodStatuses so team.Status.Teammates is current.
//
// Per-stage Phase is computed from teammate phases plus the upstream-stage
// completion graph:
//
//   - any teammate Failed                                       → "Failed"
//   - every teammate Completed                                  → "Completed"
//   - any upstream stage not yet Completed                      → "Waiting"
//   - this stage gated on ApprovalRequired + no annotation yet  → "PendingApproval"
//   - any teammate already spawned (Running/Pending/Completed)  → "Running"
//   - otherwise (gates clear, no teammate yet spawned)          → "Waiting"
//
// StartedAt/CompletedAt are preserved across reconciles once set so a
// stage's transition timeline survives status rebuilds.
func (r *AgentTeamReconciler) updatePipelineStatus(team *claudev1alpha1.AgentTeam, now time.Time) {
	if team.Spec.Pipeline == nil || len(team.Spec.Pipeline.Stages) == 0 {
		team.Status.Pipeline = nil
		return
	}

	// Index teammate phases by name.
	phase := make(map[string]string, len(team.Status.Teammates))
	for _, st := range team.Status.Teammates {
		phase[st.Name] = st.Phase
	}

	// Preserve existing timestamps across reconciles.
	prev := map[string]claudev1alpha1.StageStatus{}
	if team.Status.Pipeline != nil {
		for _, ss := range team.Status.Pipeline.Stages {
			prev[ss.Name] = ss
		}
	}

	stages := make([]claudev1alpha1.StageStatus, 0, len(team.Spec.Pipeline.Stages))
	stageCompleted := make(map[string]bool, len(team.Spec.Pipeline.Stages))
	completed := 0
	currentStage := ""

	for _, s := range team.Spec.Pipeline.Stages {
		ss := prev[s.Name]
		ss.Name = s.Name

		ready := 0
		anyFailed := false
		anySpawned := false
		for _, tmName := range s.Teammates {
			switch phase[tmName] {
			case "Completed":
				ready++
				anySpawned = true
			case "Failed":
				anyFailed = true
				anySpawned = true
			case "Running", "Pending":
				anySpawned = true
			}
		}
		ss.TeammatesReady = fmt.Sprintf("%d/%d", ready, len(s.Teammates))

		upstreamReady := true
		for _, dep := range s.DependsOn {
			if !stageCompleted[dep] {
				upstreamReady = false
				break
			}
		}

		prevPhase := ss.Phase
		switch {
		case anyFailed:
			ss.Phase = "Failed"
		case ready == len(s.Teammates) && len(s.Teammates) > 0:
			ss.Phase = "Completed"
			stageCompleted[s.Name] = true
			if ss.CompletedAt == nil {
				t := metav1.NewTime(now)
				ss.CompletedAt = &t
			}
		case !upstreamReady:
			ss.Phase = "Waiting"
		case s.ApprovalRequired && !stageApprovalGranted(team, s):
			ss.Phase = "PendingApproval"
		case anySpawned:
			ss.Phase = "Running"
			if ss.StartedAt == nil {
				t := metav1.NewTime(now)
				ss.StartedAt = &t
			}
		default:
			ss.Phase = "Waiting"
		}

		// Emit observability signals on phase transitions. Running and
		// Completed are the two interesting edges:
		//
		//   * Running:   flip the stage_active gauge to 1 so dashboards
		//                show where each team currently is.
		//   * Completed: flip the gauge back to 0 and observe the stage's
		//                wall-clock duration (StartedAt → now). The
		//                histogram observation is gated on a fresh
		//                CompletedAt (== now) so re-reconciles of an
		//                already-completed stage don't double-count.
		metrics.SetPipelineStageActive(team.Name, team.Namespace, ss.Name, ss.Phase == "Running")
		if prevPhase != "Completed" && ss.Phase == "Completed" && ss.StartedAt != nil && ss.CompletedAt != nil {
			metrics.ObservePipelineStageDuration(team.Name, team.Namespace, ss.Name,
				ss.CompletedAt.Sub(ss.StartedAt.Time).Seconds())
		}

		if ss.Phase == "Completed" {
			completed++
		} else if currentStage == "" {
			currentStage = s.Name
		}
		stages = append(stages, ss)
	}

	team.Status.Pipeline = &claudev1alpha1.PipelineStatus{
		CurrentStage:    currentStage,
		StagesCompleted: completed,
		StagesTotal:     len(team.Spec.Pipeline.Stages),
		Stages:          stages,
	}
}

// findProducerOutputPath looks up the absolute path the producer teammate
// writes the named artifact to, by scanning the producer's Outputs for an
// entry whose path basename matches. Returns "" if no such producer or
// artifact is declared — call sites treat that as a misconfigured Inputs[]
// entry and skip the input.
func findProducerOutputPath(team *claudev1alpha1.AgentTeam, from, artifact string) string {
	for _, tm := range team.Spec.Teammates {
		if tm.Name != from {
			continue
		}
		for _, out := range tm.Outputs {
			if filepath.Base(out.Path) == artifact {
				return out.Path
			}
		}
	}
	return ""
}

// recordTeammateArtifacts appends ArtifactStatus entries to team.Status.Artifacts
// for each Outputs[] entry the named teammate declares. Idempotent: an artifact
// already present (same Name + ProducedBy) is not re-added, so calling this
// every reconcile after the producer reaches Succeeded is safe. Should be
// invoked once per teammate transition to Completed.
//
// Each newly-appended artifact also bumps the
// kagents_team_artifacts_produced_total counter — the existence check
// above is what makes the metric idempotent across reconciles.
func recordTeammateArtifacts(team *claudev1alpha1.AgentTeam, tm claudev1alpha1.TeammateSpec, at time.Time) {
	if len(tm.Outputs) == 0 {
		return
	}
	existing := make(map[string]struct{}, len(team.Status.Artifacts))
	for _, a := range team.Status.Artifacts {
		existing[a.ProducedBy+"/"+a.Name] = struct{}{}
	}
	for _, out := range tm.Outputs {
		name := filepath.Base(out.Path)
		key := tm.Name + "/" + name
		if _, ok := existing[key]; ok {
			continue
		}
		team.Status.Artifacts = append(team.Status.Artifacts, claudev1alpha1.ArtifactStatus{
			Name:       name,
			Path:       out.Path,
			ProducedBy: tm.Name,
			ProducedAt: metav1.NewTime(at),
		})
		metrics.RecordArtifactProduced(team.Name, team.Namespace, tm.Name)
	}
}

// --- Timeout / Budget ---

func (r *AgentTeamReconciler) isTimedOut(team *claudev1alpha1.AgentTeam) bool {
	if team.Status.StartedAt == nil {
		return false
	}
	timeout := "4h"
	if team.Spec.Lifecycle != nil && team.Spec.Lifecycle.Timeout != "" {
		timeout = team.Spec.Lifecycle.Timeout
	}
	d, err := time.ParseDuration(timeout)
	if err != nil {
		return false
	}
	return time.Since(team.Status.StartedAt.Time) > d
}

func (r *AgentTeamReconciler) isBudgetExceeded(team *claudev1alpha1.AgentTeam) bool {
	if team.Spec.Lifecycle == nil || team.Spec.Lifecycle.BudgetLimit == nil {
		return false
	}
	return newTeamTracker(team).IsOverBudget()
}

// estimateCost returns the team's current USD cost estimate, formatted for
// Status.EstimatedCost display. Delegates to the budget package so rates and
// heuristics live in one place.
func estimateCost(team *claudev1alpha1.AgentTeam) string {
	return fmt.Sprintf("%.2f", newTeamTracker(team).GetTotalCost())
}

// newTeamTracker builds a budget Tracker seeded with one session per agent
// (lead + teammates) covering the wall-clock time elapsed since the team
// started. The tracker's limit comes from Spec.Lifecycle.BudgetLimit so a
// single tracker can answer both "how much has been spent" and "are we
// over-budget" without a second parse of the limit string.
func newTeamTracker(team *claudev1alpha1.AgentTeam) *budget.Tracker {
	var limit float64
	if team.Spec.Lifecycle != nil && team.Spec.Lifecycle.BudgetLimit != nil {
		fmt.Sscanf(*team.Spec.Lifecycle.BudgetLimit, "%f", &limit) //nolint:errcheck
	}
	tracker := budget.NewTracker(limit)
	if team.Status.StartedAt == nil {
		return tracker
	}
	elapsedSec := int64(time.Since(team.Status.StartedAt.Time).Seconds())
	tracker.RecordSession(team.Spec.Lead.Model, elapsedSec)
	for _, tm := range team.Spec.Teammates {
		tracker.RecordSession(tm.Model, elapsedSec)
	}
	return tracker
}

// --- Pod Cleanup ---

func (r *AgentTeamReconciler) terminateAllPods(ctx context.Context, team *claudev1alpha1.AgentTeam) error {
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList,
		client.InNamespace(team.Namespace),
		client.MatchingLabels{"kagents.dev/team": team.Name},
	); err != nil {
		return err
	}
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.DeletionTimestamp == nil {
			if err := r.Delete(ctx, pod); err != nil && !errors.IsNotFound(err) {
				return err
			}
		}
	}
	return nil
}

// --- Webhook Helpers ---

// teamNotifier returns a webhook.Notifier built from the team's Observability
// config, or nil if no webhook is configured. The returned *Notifier is
// nil-safe — callers may invoke SendEvent on it unconditionally.
func teamNotifier(team *claudev1alpha1.AgentTeam) *webhook.Notifier {
	if team.Spec.Observability == nil || team.Spec.Observability.Webhook == nil {
		return nil
	}
	w := team.Spec.Observability.Webhook
	return webhook.NewNotifier(w.URL, w.Events)
}

// teamEventPayload builds the standard webhook envelope fields (team, namespace)
// merged with an event-specific `data` subobject.
func teamEventPayload(team *claudev1alpha1.AgentTeam, data map[string]interface{}) map[string]interface{} {
	payload := map[string]interface{}{
		"team":      team.Name,
		"namespace": team.Namespace,
	}
	if data != nil {
		payload["data"] = data
	}
	return payload
}

// budgetWarningSent reports whether the 80% budget warning webhook has already
// fired for this team. Persisted in the team's Conditions so it survives
// operator restarts.
func budgetWarningSent(team *claudev1alpha1.AgentTeam) bool {
	for _, c := range team.Status.Conditions {
		if c.Type == "BudgetWarningSent" && c.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

// markBudgetWarningSent records that the budget warning webhook has fired so
// subsequent reconciles do not re-fire it.
func markBudgetWarningSent(team *claudev1alpha1.AgentTeam, cost, limit float64) {
	now := metav1.Now()
	team.Status.Conditions = append(team.Status.Conditions, metav1.Condition{
		Type:               "BudgetWarningSent",
		Status:             metav1.ConditionTrue,
		Reason:             "ThresholdReached",
		Message:            fmt.Sprintf("Estimated cost %.2f reached 80%% of budget limit %.2f", cost, limit),
		LastTransitionTime: now,
	})
}

// --- Condition Helpers ---

func setCondition(team *claudev1alpha1.AgentTeam, status metav1.ConditionStatus, reason, message string) {
	const condType = "Progressing"
	now := metav1.Now()
	for i, c := range team.Status.Conditions {
		if c.Type == condType {
			if c.Status != status {
				team.Status.Conditions[i].LastTransitionTime = now
			}
			team.Status.Conditions[i].Status = status
			team.Status.Conditions[i].Reason = reason
			team.Status.Conditions[i].Message = message
			return
		}
	}
	team.Status.Conditions = append(team.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
	})
}

// --- Volume Helpers ---

func pvcVolume(name, claimName string) corev1.Volume {
	return corev1.Volume{
		Name: name,
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claimName},
		},
	}
}

func pvcVolumeReadOnly(name, claimName string) corev1.Volume {
	return corev1.Volume{
		Name: name,
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claimName, ReadOnly: true},
		},
	}
}

// --- Name Helpers ---

func teamStatePVCName(team *claudev1alpha1.AgentTeam) string {
	return team.Name + "-team-state"
}

func repoPVCName(team *claudev1alpha1.AgentTeam) string {
	return team.Name + "-repo"
}

func outputPVCName(team *claudev1alpha1.AgentTeam) string {
	return team.Name + "-output"
}

func initJobName(team *claudev1alpha1.AgentTeam) string {
	return team.Name + "-init"
}

func agentPodName(team *claudev1alpha1.AgentTeam, agentName string) string {
	return team.Name + "-" + agentName
}

func agentMCPConfigMapName(team *claudev1alpha1.AgentTeam, agentName string) string {
	return team.Name + "-" + agentName + "-mcp"
}

// agentServiceAccountName returns the ServiceAccount name used by an agent pod.
// Shares the pod's naming convention so `kubectl get sa,pod -l ...` pairs them
// visually.
func agentServiceAccountName(team *claudev1alpha1.AgentTeam, agentName string) string {
	return agentPodName(team, agentName)
}

// teamPVCNames returns every PVC name an agent pod may mount for the team:
// the team-state PVC (always), the repo PVC in coding mode, and the output
// PVC in cowork mode. Order is stable so the Role's resourceNames list
// does not churn across reconciles.
func teamPVCNames(team *claudev1alpha1.AgentTeam) []string {
	names := []string{teamStatePVCName(team)}
	if team.Spec.Repository != nil && team.Spec.Repository.URL != "" {
		names = append(names, repoPVCName(team))
	}
	if team.Spec.Workspace != nil && team.Spec.Workspace.Output != nil {
		n := team.Spec.Workspace.Output.PVC
		if n == "" {
			n = outputPVCName(team)
		}
		names = append(names, n)
	}
	return names
}

func boolPtr(b bool) *bool { return &b }

// --- Controller Setup ---

// SetupWithManager sets up the controller with the Manager.
func (r *AgentTeamReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorderFor("agentteam-controller")
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&claudev1alpha1.AgentTeam{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}
