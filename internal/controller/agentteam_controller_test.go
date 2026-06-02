package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
	"github.com/amcheste/kagents/internal/harness"
)

// --- Test Helpers ---

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = claudev1alpha1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	return s
}

func newReconciler(objs ...client.Object) *AgentTeamReconciler {
	s := testScheme()
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&claudev1alpha1.AgentTeam{}).
		Build()
	return &AgentTeamReconciler{Client: c, Scheme: s}
}

// fetch retrieves a fresh copy of the AgentTeam from the fake client.
func fetch(t *testing.T, r *AgentTeamReconciler, name string) *claudev1alpha1.AgentTeam {
	t.Helper()
	var team claudev1alpha1.AgentTeam
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, &team))
	return &team
}

func minimalTeam(name string) *claudev1alpha1.AgentTeam {
	return &claudev1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: claudev1alpha1.AgentTeamSpec{
			Auth: claudev1alpha1.AuthSpec{APIKeySecret: "my-secret"},
			Lead: claudev1alpha1.LeadSpec{Model: "opus", Prompt: "Lead the team"},
			Teammates: []claudev1alpha1.TeammateSpec{
				{Name: "worker", Model: "sonnet", Prompt: "Do work"},
			},
		},
	}
}

func withRepo(team *claudev1alpha1.AgentTeam) *claudev1alpha1.AgentTeam {
	team.Spec.Repository = &claudev1alpha1.RepositorySpec{
		URL:    "https://github.com/example/repo",
		Branch: "main",
	}
	return team
}

func withWorkspace(team *claudev1alpha1.AgentTeam) *claudev1alpha1.AgentTeam {
	team.Spec.Workspace = &claudev1alpha1.WorkspaceSpec{
		Output: &claudev1alpha1.WorkspaceOutputSpec{Size: "5Gi"},
	}
	return team
}

func withLifecycle(team *claudev1alpha1.AgentTeam, timeout, budget string) *claudev1alpha1.AgentTeam { //nolint:unparam
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{
		Timeout:     timeout,
		BudgetLimit: &budget,
	}
	return team
}

func startedAt(team *claudev1alpha1.AgentTeam, ago time.Duration) *claudev1alpha1.AgentTeam {
	t := metav1.NewTime(time.Now().Add(-ago))
	team.Status.StartedAt = &t
	team.Status.Phase = "Running"
	return team
}

func succeededPod(name, namespace, teamName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"kagents.dev/team": teamName},
		},
		Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
	}
}

func failedPod(name, namespace, teamName string) *corev1.Pod {
	p := succeededPod(name, namespace, teamName)
	p.Status.Phase = corev1.PodFailed
	return p
}

func runningPod(name, namespace, teamName string) *corev1.Pod { //nolint:unparam
	p := succeededPod(name, namespace, teamName)
	p.Status.Phase = corev1.PodRunning
	return p
}

func completedJob(name, namespace string) *batchv1.Job { //nolint:unparam
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Status:     batchv1.JobStatus{Succeeded: 1},
	}
}

func failedJob(name, namespace string) *batchv1.Job {
	limit := int32(3)
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       batchv1.JobSpec{BackoffLimit: &limit},
		Status:     batchv1.JobStatus{Failed: 3},
	}
}

// --- reconcilePending ---

func TestReconcilePending_CodingMode_CreatesPVCsAndInitJob(t *testing.T) {
	team := withRepo(minimalTeam("coding-team"))
	r := newReconciler(team)
	team = fetch(t, r, "coding-team")
	ctx := context.Background()

	result, err := r.reconcilePending(ctx, team)
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, result.RequeueAfter)
	assert.Equal(t, "Initializing", team.Status.Phase)
	assert.NotNil(t, team.Status.StartedAt)

	var pvc corev1.PersistentVolumeClaim

	// team-state PVC is ReadWriteMany.
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "coding-team-team-state", Namespace: "default"}, &pvc))
	assert.Equal(t, corev1.ReadWriteMany, pvc.Spec.AccessModes[0])

	// repo PVC created.
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "coding-team-repo", Namespace: "default"}, &pvc))

	// init Job created with a container that references both PVCs.
	var job batchv1.Job
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "coding-team-init", Namespace: "default"}, &job))
	assert.Len(t, job.Spec.Template.Spec.Volumes, 2)
}

func TestReconcilePending_CoworkMode_CreatesOutputPVC(t *testing.T) {
	team := withWorkspace(minimalTeam("cw-team"))
	r := newReconciler(team)
	team = fetch(t, r, "cw-team")
	ctx := context.Background()

	_, err := r.reconcilePending(ctx, team)
	require.NoError(t, err)

	// output PVC created.
	var pvc corev1.PersistentVolumeClaim
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "cw-team-output", Namespace: "default"}, &pvc))

	// no repo PVC or init Job for Cowork teams.
	err = r.Get(ctx, types.NamespacedName{Name: "cw-team-repo", Namespace: "default"}, &pvc)
	assert.True(t, errors.IsNotFound(err), "repo PVC should not exist in Cowork mode")

	var job batchv1.Job
	err = r.Get(ctx, types.NamespacedName{Name: "cw-team-init", Namespace: "default"}, &job)
	assert.True(t, errors.IsNotFound(err), "init Job should not exist in Cowork mode")
}

func TestReconcilePending_Idempotent(t *testing.T) {
	team := withRepo(minimalTeam("idem-team"))
	r := newReconciler(team)
	ctx := context.Background()

	team = fetch(t, r, "idem-team")
	_, err := r.reconcilePending(ctx, team)
	require.NoError(t, err)

	// Re-fetch (first call updated status) and call again.
	team = fetch(t, r, "idem-team")
	_, err = r.reconcilePending(ctx, team)
	require.NoError(t, err, "second reconcilePending call must not error on pre-existing resources")
}

// --- reconcileInitializing ---

func TestReconcileInitializing_WaitsForInitJob(t *testing.T) {
	// Init Job exists but has not yet succeeded.
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "wait-team-init", Namespace: "default"},
		Status:     batchv1.JobStatus{Active: 1},
	}
	team := withRepo(minimalTeam("wait-team"))
	r := newReconciler(team, job)
	team = fetch(t, r, "wait-team")
	ctx := context.Background()

	result, err := r.reconcileInitializing(ctx, team)
	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, result.RequeueAfter)
	assert.Equal(t, "", team.Status.Phase, "phase should not advance while init job is running")
}

func TestReconcileInitializing_InitJobFailed_SetsFailedPhase(t *testing.T) {
	team := withRepo(minimalTeam("fail-team"))
	job := failedJob("fail-team-init", "default")
	r := newReconciler(team, job)
	team = fetch(t, r, "fail-team")
	ctx := context.Background()

	_, err := r.reconcileInitializing(ctx, team)
	require.NoError(t, err)
	assert.Equal(t, "Failed", team.Status.Phase)
}

func TestReconcileInitializing_DeploysPods(t *testing.T) {
	team := withRepo(minimalTeam("deploy-team"))
	job := completedJob("deploy-team-init", "default")
	r := newReconciler(team, job)
	team = fetch(t, r, "deploy-team")
	ctx := context.Background()

	_, err := r.reconcileInitializing(ctx, team)
	require.NoError(t, err)
	assert.Equal(t, "Running", team.Status.Phase)

	// Lead pod created.
	var pod corev1.Pod
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "deploy-team-lead", Namespace: "default"}, &pod))
	assert.Equal(t, "lead", pod.Labels["kagents.dev/role"])

	// Teammate pod created (no dependencies).
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "deploy-team-worker", Namespace: "default"}, &pod))
	assert.Equal(t, "teammate", pod.Labels["kagents.dev/role"])
}

func TestReconcileInitializing_DependsOnBlocks(t *testing.T) {
	team := withRepo(minimalTeam("dep-team"))
	team.Spec.Teammates = []claudev1alpha1.TeammateSpec{
		{Name: "first", Model: "sonnet", Prompt: "First"},
		{Name: "second", Model: "sonnet", Prompt: "Second", DependsOn: []string{"first"}},
	}
	job := completedJob("dep-team-init", "default")
	r := newReconciler(team, job)
	team = fetch(t, r, "dep-team")
	ctx := context.Background()

	_, err := r.reconcileInitializing(ctx, team)
	require.NoError(t, err)

	// "first" spawned (no deps).
	var pod corev1.Pod
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "dep-team-first", Namespace: "default"}, &pod))

	// "second" NOT spawned (depends on first which hasn't succeeded).
	err = r.Get(ctx, types.NamespacedName{Name: "dep-team-second", Namespace: "default"}, &pod)
	assert.True(t, errors.IsNotFound(err), "second teammate should be blocked by dependsOn")
}

func TestReconcileInitializing_ApprovalGateBlocksTeammate(t *testing.T) {
	team := withRepo(minimalTeam("gate-team"))
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{
		ApprovalGates: []claudev1alpha1.ApprovalGateSpec{
			{Event: "spawn-worker", Channel: "none"},
		},
	}
	job := completedJob("gate-team-init", "default")
	r := newReconciler(team, job)
	team = fetch(t, r, "gate-team")
	ctx := context.Background()

	_, err := r.reconcileInitializing(ctx, team)
	require.NoError(t, err)

	// Lead spawned (no gate on lead).
	var pod corev1.Pod
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "gate-team-lead", Namespace: "default"}, &pod))

	// Worker NOT spawned (gated).
	err = r.Get(ctx, types.NamespacedName{Name: "gate-team-worker", Namespace: "default"}, &pod)
	assert.True(t, errors.IsNotFound(err), "worker should be blocked by approval gate")

	// PendingApproval set on teammate status.
	assert.Equal(t, "spawn-worker", team.Status.Teammates[0].PendingApproval)
}

func TestReconcileInitializing_ApprovalGrantedViaAnnotation(t *testing.T) {
	team := withRepo(minimalTeam("approved-team"))
	team.Annotations = map[string]string{
		"approved.kagents.dev/spawn-worker": "true",
	}
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{
		ApprovalGates: []claudev1alpha1.ApprovalGateSpec{
			{Event: "spawn-worker", Channel: "none"},
		},
	}
	job := completedJob("approved-team-init", "default")
	r := newReconciler(team, job)
	team = fetch(t, r, "approved-team")
	ctx := context.Background()

	_, err := r.reconcileInitializing(ctx, team)
	require.NoError(t, err)

	// Worker spawned because approval annotation is set.
	var pod corev1.Pod
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "approved-team-worker", Namespace: "default"}, &pod))
}

// --- reconcileRunning ---

func TestReconcileRunning_Timeout_SetsTimedOut(t *testing.T) {
	team := withLifecycle(minimalTeam("timeout-team"), "1m", "100.00")
	startedAt(team, 2*time.Minute) // started 2 minutes ago, timeout is 1 minute
	r := newReconciler(team)
	team = fetch(t, r, "timeout-team")
	team.Status.StartedAt = &metav1.Time{Time: time.Now().Add(-2 * time.Minute)}
	team.Status.Phase = "Running"
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)
	assert.Equal(t, "TimedOut", team.Status.Phase)
}

func TestReconcileRunning_BudgetExceeded_SetsBudgetExceeded(t *testing.T) {
	budget := "0.01" // tiny budget, will be exceeded immediately
	team := minimalTeam("budget-team")
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{BudgetLimit: &budget}
	team.Status.Phase = "Running"
	startTime := metav1.NewTime(time.Now().Add(-60 * time.Minute))
	team.Status.StartedAt = &startTime
	r := newReconciler(team)
	team = fetch(t, r, "budget-team")
	team.Status.Phase = "Running"
	team.Status.StartedAt = &startTime
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)
	assert.Equal(t, "BudgetExceeded", team.Status.Phase)
}

func TestReconcileRunning_AllPodsSucceeded_SetsCompleted(t *testing.T) {
	team := minimalTeam("done-team")
	leadPod := succeededPod("done-team-lead", "default", "done-team")
	workerPod := succeededPod("done-team-worker", "default", "done-team")
	startTime := metav1.NewTime(time.Now().Add(-5 * time.Minute))

	r := newReconciler(team, leadPod, workerPod)
	team = fetch(t, r, "done-team")
	team.Status.Phase = "Running"
	team.Status.StartedAt = &startTime
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)
	assert.Equal(t, "Completed", team.Status.Phase)
}

func TestReconcileRunning_PodFailed_SetsFailedPhase(t *testing.T) {
	team := minimalTeam("broken-team")
	leadPod := failedPod("broken-team-lead", "default", "broken-team")
	startTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))

	r := newReconciler(team, leadPod)
	team = fetch(t, r, "broken-team")
	team.Status.Phase = "Running"
	team.Status.StartedAt = &startTime
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)
	assert.Equal(t, "Failed", team.Status.Phase)
}

func TestReconcileRunning_SpawnsNewlyUnblockedTeammate(t *testing.T) {
	team := minimalTeam("unblock-team")
	team.Spec.Teammates = []claudev1alpha1.TeammateSpec{
		{Name: "first", Model: "sonnet", Prompt: "First"},
		{Name: "second", Model: "sonnet", Prompt: "Second", DependsOn: []string{"first"}},
	}
	startTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))
	// lead and first are succeeded; second should now be spawnable.
	leadPod := succeededPod("unblock-team-lead", "default", "unblock-team")
	firstPod := succeededPod("unblock-team-first", "default", "unblock-team")

	r := newReconciler(team, leadPod, firstPod)
	team = fetch(t, r, "unblock-team")
	team.Status.Phase = "Running"
	team.Status.StartedAt = &startTime
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)

	// "second" should now be spawned.
	var pod corev1.Pod
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "unblock-team-second", Namespace: "default"}, &pod))
}

// --- buildAgentPod ---

func TestBuildAgentPod_CodingMode(t *testing.T) {
	team := withRepo(minimalTeam("pod-test"))
	r := newReconciler(team)

	pod := r.buildAgentPod(team, "worker", "sonnet", "do work", "auto-accept", false,
		corev1.ResourceRequirements{}, nil, nil, nil, nil)

	assert.Equal(t, "pod-test-worker", pod.Name)
	assert.Equal(t, corev1.RestartPolicyNever, pod.Spec.RestartPolicy)

	env := envMap(pod)
	assert.Equal(t, "1", env["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"])
	assert.Equal(t, "pod-test", env["CLAUDE_CODE_TEAM_NAME"])
	assert.Equal(t, "worker", env["CLAUDE_CODE_AGENT_NAME"])
	assert.Equal(t, "worktrees/worker", env["WORKTREE_PATH"], "teammate should get per-worktree path")

	volNames := volumeNames(pod)
	assert.Contains(t, volNames, "team-state")
	assert.Contains(t, volNames, "repo")
}

func TestBuildAgentPod_LeadHasNoWorktreePath(t *testing.T) {
	team := withRepo(minimalTeam("lead-test"))
	r := newReconciler(team)

	pod := r.buildAgentPod(team, "lead", "opus", "lead prompt", "auto-accept", true,
		corev1.ResourceRequirements{}, nil, nil, nil, nil)

	env := envMap(pod)
	_, hasWorktree := env["WORKTREE_PATH"]
	assert.False(t, hasWorktree, "lead should not get a worktree path")
	assert.Equal(t, "lead", pod.Labels["kagents.dev/role"])
}

func TestBuildAgentPod_WithSkills(t *testing.T) {
	team := minimalTeam("skill-test")
	r := newReconciler(team)
	skills := []claudev1alpha1.SkillSpec{
		{Name: "web-research", Source: claudev1alpha1.SkillSource{ConfigMap: "web-research-skill"}},
		{Name: "report-writer", Source: claudev1alpha1.SkillSource{ConfigMap: "report-writer-skill"}},
	}

	pod := r.buildAgentPod(team, "worker", "sonnet", "do work", "auto-accept", false,
		corev1.ResourceRequirements{}, nil, skills, nil, nil)

	volNames := volumeNames(pod)
	assert.Contains(t, volNames, "skill-web-research")
	assert.Contains(t, volNames, "skill-report-writer")

	mounts := mountPaths(pod)
	assert.Contains(t, mounts, "/var/claude-skills/web-research")
	assert.Contains(t, mounts, "/var/claude-skills/report-writer")
}

func TestBuildAgentPod_WithMCPServers(t *testing.T) {
	team := minimalTeam("mcp-test")
	r := newReconciler(team)
	mcpServers := []claudev1alpha1.MCPServerSpec{
		{Name: "gmail", URL: "https://gmail.mcp.example.com/mcp"},
	}

	pod := r.buildAgentPod(team, "worker", "sonnet", "do work", "auto-accept", false,
		corev1.ResourceRequirements{}, nil, nil, mcpServers, nil)

	volNames := volumeNames(pod)
	assert.Contains(t, volNames, "mcp-config")

	mounts := mountPaths(pod)
	assert.Contains(t, mounts, "/var/claude-mcp")
}

func TestBuildAgentPod_CoworkMode(t *testing.T) {
	team := withWorkspace(minimalTeam("cowork-test"))
	team.Spec.Workspace.Inputs = []claudev1alpha1.WorkspaceInputSpec{
		{ConfigMap: "quarterly-data", MountPath: "/workspace/data"},
	}
	r := newReconciler(team)

	pod := r.buildAgentPod(team, "worker", "sonnet", "do work", "auto-accept", false,
		corev1.ResourceRequirements{}, nil, nil, nil, nil)

	volNames := volumeNames(pod)
	assert.Contains(t, volNames, "workspace-output")
	assert.Contains(t, volNames, "workspace-input-0")

	// No repo volume in Cowork mode.
	assert.NotContains(t, volNames, "repo")

	env := envMap(pod)
	_, hasWorktree := env["WORKTREE_PATH"]
	assert.False(t, hasWorktree, "Cowork pods should not have a worktree path")
}

func TestBuildAgentPod_ScopeEnvVars(t *testing.T) {
	team := minimalTeam("scope-test")
	r := newReconciler(team)
	scope := &claudev1alpha1.ScopeSpec{
		IncludePaths: []string{"internal/", "api/"},
		ExcludePaths: []string{"vendor/"},
	}

	pod := r.buildAgentPod(team, "worker", "sonnet", "do work", "auto-accept", false,
		corev1.ResourceRequirements{}, scope, nil, nil, nil)

	env := envMap(pod)
	assert.Equal(t, "internal/:api/", env["SCOPE_INCLUDE_PATHS"])
	assert.Equal(t, "vendor/", env["SCOPE_EXCLUDE_PATHS"])
}

// --- estimateCost ---

func TestEstimateCost_ZeroWhenNoStartTime(t *testing.T) {
	team := minimalTeam("cost-test")
	assert.Equal(t, "0.00", estimateCost(team))
}

func TestEstimateCost_OpusMoreExpensiveThanSonnet(t *testing.T) {
	opusTeam := minimalTeam("opus-cost")
	opusTeam.Spec.Lead.Model = "opus"
	opusTeam.Spec.Teammates = []claudev1alpha1.TeammateSpec{{Name: "a", Model: "opus"}}
	t0 := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	opusTeam.Status.StartedAt = &t0

	sonnetTeam := minimalTeam("sonnet-cost")
	sonnetTeam.Spec.Lead.Model = "sonnet"
	sonnetTeam.Spec.Teammates = []claudev1alpha1.TeammateSpec{{Name: "a", Model: "sonnet"}}
	sonnetTeam.Status.StartedAt = &t0

	var opusCost, sonnetCost float64
	_, _ = fmt.Sscanf(estimateCost(opusTeam), "%f", &opusCost)
	_, _ = fmt.Sscanf(estimateCost(sonnetTeam), "%f", &sonnetCost)
	assert.Greater(t, opusCost, sonnetCost)
}

// --- isTimedOut ---

func TestIsTimedOut_NotTimedOut(t *testing.T) {
	team := withLifecycle(minimalTeam("t"), "4h", "100.00")
	t0 := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	team.Status.StartedAt = &t0
	assert.False(t, newReconciler(team).isTimedOut(team))
}

func TestIsTimedOut_TimedOut(t *testing.T) {
	team := withLifecycle(minimalTeam("t"), "30m", "100.00")
	t0 := metav1.NewTime(time.Now().Add(-60 * time.Minute))
	team.Status.StartedAt = &t0
	assert.True(t, newReconciler(team).isTimedOut(team))
}

func TestIsTimedOut_NoStartTime(t *testing.T) {
	team := withLifecycle(minimalTeam("t"), "1h", "100.00")
	assert.False(t, newReconciler(team).isTimedOut(team))
}

// --- isBudgetExceeded ---

func TestIsBudgetExceeded_UnderBudget(t *testing.T) {
	budget := "1000.00"
	team := minimalTeam("b")
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{BudgetLimit: &budget}
	team.Status.EstimatedCost = "0.50"
	assert.False(t, newReconciler(team).isBudgetExceeded(team))
}

func TestIsBudgetExceeded_OverBudget(t *testing.T) {
	budget := "1.00"
	team := minimalTeam("b")
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{BudgetLimit: &budget}
	// Team total under the budget package heuristic is $0.60/min (opus +
	// sonnet). 3 minutes elapsed → $1.80, comfortably over the $1.00 limit.
	start := metav1.NewTime(time.Now().Add(-3 * time.Minute))
	team.Status.StartedAt = &start
	assert.True(t, newReconciler(team).isBudgetExceeded(team))
}

func TestIsBudgetExceeded_NoBudgetLimit(t *testing.T) {
	team := minimalTeam("b")
	team.Status.EstimatedCost = "999.99"
	assert.False(t, newReconciler(team).isBudgetExceeded(team), "no budget limit should never exceed")
}

// --- dependenciesMet ---

func TestDependenciesMet_NoDeps(t *testing.T) {
	team := minimalTeam("d")
	r := newReconciler(team)
	assert.True(t, r.dependenciesMet(context.Background(), team, nil))
	assert.True(t, r.dependenciesMet(context.Background(), team, []string{}))
}

func TestDependenciesMet_DepSucceeded(t *testing.T) {
	team := minimalTeam("d")
	pod := succeededPod("d-first", "default", "d")
	r := newReconciler(team, pod)
	assert.True(t, r.dependenciesMet(context.Background(), team, []string{"first"}))
}

func TestDependenciesMet_DepNotSpawned(t *testing.T) {
	team := minimalTeam("d")
	r := newReconciler(team)
	assert.False(t, r.dependenciesMet(context.Background(), team, []string{"first"}))
}

func TestDependenciesMet_DepStillRunning(t *testing.T) {
	team := minimalTeam("d")
	pod := runningPod("d-first", "default", "d")
	r := newReconciler(team, pod)
	assert.False(t, r.dependenciesMet(context.Background(), team, []string{"first"}))
}

// --- checkApprovalGate ---

func TestCheckApprovalGate_NoGateDefined(t *testing.T) {
	team := minimalTeam("ag")
	r := newReconciler(team)
	assert.True(t, r.checkApprovalGate(context.Background(), team, "spawn-worker"))
}

func TestCheckApprovalGate_GatePresentNotApproved(t *testing.T) {
	team := minimalTeam("ag")
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{
		ApprovalGates: []claudev1alpha1.ApprovalGateSpec{{Event: "spawn-worker", Channel: "none"}},
	}
	r := newReconciler(team)
	assert.False(t, r.checkApprovalGate(context.Background(), team, "spawn-worker"))
}

func TestCheckApprovalGate_ApprovedViaAnnotation(t *testing.T) {
	team := minimalTeam("ag")
	team.Annotations = map[string]string{"approved.kagents.dev/spawn-worker": "true"}
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{
		ApprovalGates: []claudev1alpha1.ApprovalGateSpec{{Event: "spawn-worker", Channel: "none"}},
	}
	r := newReconciler(team)
	assert.True(t, r.checkApprovalGate(context.Background(), team, "spawn-worker"))
}

// --- reconcileTerminal ---

func TestReconcileTerminal_StampsCompletedAt(t *testing.T) {
	team := minimalTeam("term-stamp")
	team.Status.Phase = "Completed"
	r := newReconciler(team)
	team = fetch(t, r, "term-stamp")
	ctx := context.Background()

	_, err := r.reconcileTerminal(ctx, team)
	require.NoError(t, err)
	assert.NotNil(t, team.Status.CompletedAt, "reconcileTerminal should stamp CompletedAt")
}

func TestReconcileTerminal_Idempotent(t *testing.T) {
	team := minimalTeam("term-idem")
	team.Status.Phase = "Completed"
	r := newReconciler(team)
	team = fetch(t, r, "term-idem")
	ctx := context.Background()

	_, err := r.reconcileTerminal(ctx, team)
	require.NoError(t, err)
	require.NotNil(t, team.Status.CompletedAt)
	firstStamp := team.Status.CompletedAt.Time

	// Second call must not error and must not re-stamp CompletedAt.
	team = fetch(t, r, "term-idem")
	require.NotNil(t, team.Status.CompletedAt, "CompletedAt must be persisted after first call")

	_, err = r.reconcileTerminal(ctx, team)
	require.NoError(t, err, "second reconcileTerminal call must not error")
	assert.Equal(t, firstStamp.Unix(), team.Status.CompletedAt.Time.Unix(),
		"second call must not overwrite CompletedAt")
}

func TestReconcileTerminal_DeletesRunningPods(t *testing.T) {
	team := minimalTeam("term-delete")
	team.Status.Phase = "Failed"
	leadPod := runningPod("term-delete-lead", "default", "term-delete")
	workerPod := runningPod("term-delete-worker", "default", "term-delete")
	r := newReconciler(team, leadPod, workerPod)
	team = fetch(t, r, "term-delete")
	ctx := context.Background()

	_, err := r.reconcileTerminal(ctx, team)
	require.NoError(t, err)

	var pod corev1.Pod
	assert.True(t, errors.IsNotFound(r.Get(ctx, types.NamespacedName{Name: "term-delete-lead", Namespace: "default"}, &pod)),
		"lead pod should be deleted in terminal phase")
	assert.True(t, errors.IsNotFound(r.Get(ctx, types.NamespacedName{Name: "term-delete-worker", Namespace: "default"}, &pod)),
		"worker pod should be deleted in terminal phase")
}

// --- reconcileRunning: cleanup on timeout/budget ---

func TestReconcileRunning_Timeout_TerminatesPods(t *testing.T) {
	team := withLifecycle(minimalTeam("timeout-cleanup"), "1m", "100.00")
	leadPod := runningPod("timeout-cleanup-lead", "default", "timeout-cleanup")
	workerPod := runningPod("timeout-cleanup-worker", "default", "timeout-cleanup")
	startTime := metav1.NewTime(time.Now().Add(-2 * time.Minute))

	r := newReconciler(team, leadPod, workerPod)
	team = fetch(t, r, "timeout-cleanup")
	team.Status.Phase = "Running"
	team.Status.StartedAt = &startTime
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)
	assert.Equal(t, "TimedOut", team.Status.Phase)

	var pod corev1.Pod
	assert.True(t, errors.IsNotFound(r.Get(ctx, types.NamespacedName{Name: "timeout-cleanup-lead", Namespace: "default"}, &pod)),
		"lead pod must be deleted when team times out")
	assert.True(t, errors.IsNotFound(r.Get(ctx, types.NamespacedName{Name: "timeout-cleanup-worker", Namespace: "default"}, &pod)),
		"worker pod must be deleted when team times out")
}

func TestReconcileRunning_BudgetExceeded_TerminatesPods(t *testing.T) {
	budget := "0.01" // tiny budget, exceeded after 60 minutes of running
	team := minimalTeam("budget-cleanup")
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{BudgetLimit: &budget}
	leadPod := runningPod("budget-cleanup-lead", "default", "budget-cleanup")
	workerPod := runningPod("budget-cleanup-worker", "default", "budget-cleanup")
	startTime := metav1.NewTime(time.Now().Add(-60 * time.Minute))

	r := newReconciler(team, leadPod, workerPod)
	team = fetch(t, r, "budget-cleanup")
	team.Status.Phase = "Running"
	team.Status.StartedAt = &startTime
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)
	assert.Equal(t, "BudgetExceeded", team.Status.Phase)

	var pod corev1.Pod
	assert.True(t, errors.IsNotFound(r.Get(ctx, types.NamespacedName{Name: "budget-cleanup-lead", Namespace: "default"}, &pod)),
		"lead pod must be deleted when budget is exceeded")
	assert.True(t, errors.IsNotFound(r.Get(ctx, types.NamespacedName{Name: "budget-cleanup-worker", Namespace: "default"}, &pod)),
		"worker pod must be deleted when budget is exceeded")
}

// --- reconcileRunning: teammate failure ---

func TestReconcileRunning_TeammateFailure_SetsFailedPhase(t *testing.T) {
	// Lead is still running; a teammate pod fails AND has already exhausted
	// its restart budget — team must move to Failed. Re-spawn under the
	// limit is covered in TestReconcileRunning_TeammateFailure_UnderLimit_Respawns.
	team := minimalTeam("teammate-fail")
	leadPod := runningPod("teammate-fail-lead", "default", "teammate-fail")
	workerPod := failedPod("teammate-fail-worker", "default", "teammate-fail")
	startTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))

	r := newReconciler(team, leadPod, workerPod)
	team = fetch(t, r, "teammate-fail")
	team.Status.Phase = "Running"
	team.Status.StartedAt = &startTime
	team.Status.Teammates = []claudev1alpha1.TeammateStatus{
		{Name: "worker", RestartCount: 3},
	}
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)
	assert.Equal(t, "Failed", team.Status.Phase)
}

func TestReconcileRunning_TeammateFailure_UnderLimit_Respawns(t *testing.T) {
	// Failed teammate under the restart limit is deleted and re-created
	// rather than failing the whole team — the core #13 story.
	team := minimalTeam("respawn-team")
	leadPod := runningPod("respawn-team-lead", "default", "respawn-team")
	workerPod := failedPod("respawn-team-worker", "default", "respawn-team")
	startTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))

	r := newReconciler(team, leadPod, workerPod)
	team = fetch(t, r, "respawn-team")
	team.Status.Phase = "Running"
	team.Status.StartedAt = &startTime
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)

	assert.Equal(t, "Running", team.Status.Phase)

	var found bool
	for _, st := range team.Status.Teammates {
		if st.Name == "worker" {
			assert.Equal(t, int32(1), st.RestartCount)
			found = true
		}
	}
	assert.True(t, found, "expected worker status with RestartCount=1")

	var newPod corev1.Pod
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "respawn-team-worker", Namespace: "default"}, &newPod))
	assert.NotEqual(t, corev1.PodFailed, newPod.Status.Phase, "re-spawned pod must not carry the old Failed phase")
}

func TestReconcileRunning_TeammateFailure_IncrementalRestarts(t *testing.T) {
	// Each failed reconcile bumps RestartCount by one.
	team := minimalTeam("incr-team")
	leadPod := runningPod("incr-team-lead", "default", "incr-team")
	startTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))

	r := newReconciler(team, leadPod, failedPod("incr-team-worker", "default", "incr-team"))
	team = fetch(t, r, "incr-team")
	team.Status.Phase = "Running"
	team.Status.StartedAt = &startTime
	team.Status.Teammates = []claudev1alpha1.TeammateStatus{
		{Name: "worker", RestartCount: 2},
	}
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)

	assert.Equal(t, "Running", team.Status.Phase)
	for _, st := range team.Status.Teammates {
		if st.Name == "worker" {
			assert.Equal(t, int32(3), st.RestartCount)
		}
	}
}

func TestReconcileRunning_TeammateFailure_CustomMaxRestarts(t *testing.T) {
	// Spec.Lifecycle.MaxRestarts overrides the default — a limit of 1 means
	// a single failure after one prior restart is terminal.
	maxR := int32(1)
	team := minimalTeam("custom-max")
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{MaxRestarts: &maxR}
	leadPod := runningPod("custom-max-lead", "default", "custom-max")
	workerPod := failedPod("custom-max-worker", "default", "custom-max")
	startTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))

	r := newReconciler(team, leadPod, workerPod)
	team = fetch(t, r, "custom-max")
	team.Status.Phase = "Running"
	team.Status.StartedAt = &startTime
	team.Status.Teammates = []claudev1alpha1.TeammateStatus{
		{Name: "worker", RestartCount: 1},
	}
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)
	assert.Equal(t, "Failed", team.Status.Phase)
}

// --- reconcileRunning: pods not yet spawned ---

func TestReconcileRunning_LeadNotSpawned_KeepsRunning(t *testing.T) {
	// Team entered Running but lead pod has not been created yet (e.g. race on first reconcile).
	// Operator should requeue and wait rather than prematurely completing.
	team := minimalTeam("no-lead")
	startTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))
	r := newReconciler(team) // no pods in cluster
	team = fetch(t, r, "no-lead")
	team.Status.Phase = "Running"
	team.Status.StartedAt = &startTime
	ctx := context.Background()

	result, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)
	assert.Equal(t, "Running", team.Status.Phase, "phase must not advance before lead pod is spawned")
	assert.Equal(t, 30*time.Second, result.RequeueAfter)
}

// --- reconcileInitializing: edge cases ---

func TestReconcileInitializing_InitJobMissing_Waits(t *testing.T) {
	// Coding team where the init Job was never created (e.g. operator restarted mid-reconcile).
	// Operator should requeue rather than proceeding to deploy pods.
	team := withRepo(minimalTeam("no-job-team"))
	r := newReconciler(team) // no Job object in cluster
	team = fetch(t, r, "no-job-team")
	ctx := context.Background()

	result, err := r.reconcileInitializing(ctx, team)
	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, result.RequeueAfter)
	assert.Empty(t, team.Status.Phase, "phase must not advance while init job is absent/incomplete")
}

func TestReconcileInitializing_HangingInitJob_TimesOut(t *testing.T) {
	// Init Job is still Active but the team's configured timeout has elapsed.
	// Without a timeout check in reconcileInitializing the team would be stuck forever.
	team := withLifecycle(withRepo(minimalTeam("hang-team")), "1m", "100.00")
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "hang-team-init", Namespace: "default"},
		Status:     batchv1.JobStatus{Active: 1},
	}
	r := newReconciler(team, job)
	team = fetch(t, r, "hang-team")
	// Simulate the team having started 2 minutes ago (past the 1-minute timeout).
	team.Status.StartedAt = &metav1.Time{Time: time.Now().Add(-2 * time.Minute)}
	ctx := context.Background()

	_, err := r.reconcileInitializing(ctx, team)
	require.NoError(t, err)
	assert.Equal(t, "TimedOut", team.Status.Phase,
		"team must time out even when stuck waiting for a hung init job")
}

// --- dependenciesMet: additional edge cases ---

func TestDependenciesMet_DepFailed_NotMet(t *testing.T) {
	// A failed pod must NOT satisfy a dependency — only Succeeded counts.
	team := minimalTeam("dep-fail")
	pod := failedPod("dep-fail-first", "default", "dep-fail")
	r := newReconciler(team, pod)
	assert.False(t, r.dependenciesMet(context.Background(), team, []string{"first"}),
		"a failed dependency pod must not be considered met")
}

func TestDependenciesMet_AllMustSucceed(t *testing.T) {
	// All listed dependencies must have Succeeded; one still Running blocks the gate.
	team := minimalTeam("dep-all")
	first := succeededPod("dep-all-first", "default", "dep-all")
	second := runningPod("dep-all-second", "default", "dep-all")
	r := newReconciler(team, first, second)
	assert.False(t, r.dependenciesMet(context.Background(), team, []string{"first", "second"}),
		"all dependencies must have Succeeded — one Running is not enough")
}

// --- buildAgentPod: auth and command override ---

func TestBuildAgentPod_OAuthAuth(t *testing.T) {
	team := minimalTeam("oauth-test")
	team.Spec.Auth.APIKeySecret = ""
	team.Spec.Auth.OAuthSecret = "my-oauth-secret"
	r := newReconciler(team)

	pod := r.buildAgentPod(team, "worker", "sonnet", "work", "auto-accept", false,
		corev1.ResourceRequirements{}, nil, nil, nil, nil)

	// ANTHROPIC_API_KEY must NOT be injected when OAuth is configured.
	env := envMap(pod)
	_, hasAPIKey := env["ANTHROPIC_API_KEY"]
	assert.False(t, hasAPIKey, "ANTHROPIC_API_KEY must not be set when OAuth is used")

	// CLAUDE_OAUTH_TOKEN must be set via SecretKeyRef (ValueFrom, not plain Value).
	var foundOAuth bool
	for _, e := range pod.Spec.Containers[0].Env {
		if e.Name == "CLAUDE_OAUTH_TOKEN" {
			foundOAuth = true
			require.NotNil(t, e.ValueFrom, "CLAUDE_OAUTH_TOKEN must use ValueFrom SecretKeyRef")
			assert.Equal(t, "my-oauth-secret", e.ValueFrom.SecretKeyRef.Name)
		}
	}
	assert.True(t, foundOAuth, "CLAUDE_OAUTH_TOKEN env var must be present")
}

func TestBuildAgentPod_AgentCommandOverride(t *testing.T) {
	team := minimalTeam("cmd-test")
	r := newReconciler(team)
	r.AgentCommand = []string{"sh", "-c", "exit 0"}

	pod := r.buildAgentPod(team, "worker", "sonnet", "work", "auto-accept", false,
		corev1.ResourceRequirements{}, nil, nil, nil, nil)

	assert.Equal(t, []string{"sh", "-c", "exit 0"}, pod.Spec.Containers[0].Command,
		"AgentCommand override must be applied to the container spec")
}

// --- syncPodStatuses ---

func TestSyncPodStatuses_ReflectsPodPhases(t *testing.T) {
	team := minimalTeam("sync-test")
	leadPod := succeededPod("sync-test-lead", "default", "sync-test")
	workerPod := runningPod("sync-test-worker", "default", "sync-test")
	r := newReconciler(team, leadPod, workerPod)
	team = fetch(t, r, "sync-test")
	ctx := context.Background()

	r.syncPodStatuses(ctx, team)

	require.NotNil(t, team.Status.Lead)
	assert.Equal(t, "sync-test-lead", team.Status.Lead.PodName)
	assert.Equal(t, "Completed", team.Status.Lead.Phase)

	require.Len(t, team.Status.Teammates, 1)
	assert.Equal(t, "sync-test-worker", team.Status.Teammates[0].PodName)
	assert.Equal(t, "Running", team.Status.Teammates[0].Phase)
}

// --- Reconcile dispatch ---
//
// These tests exercise the top-level Reconcile entry point directly, rather
// than calling phase functions (reconcilePending, reconcileRunning, ...)
// in isolation. They verify that the switch on team.Status.Phase routes to
// the correct handler, that a missing object is a no-op, and that an
// unrecognized phase is reset back to "Pending".

// TestReconcile_EmptyPhase_RoutesToPending verifies that a freshly-created
// team (empty Status.Phase) is dispatched to reconcilePending, which is
// observable via (a) phase advancing to "Initializing", (b) StartedAt being
// stamped, and (c) the Cowork output PVC being created.
func TestReconcile_EmptyPhase_RoutesToPending(t *testing.T) {
	team := withWorkspace(minimalTeam("disp-empty"))
	r := newReconciler(team)
	ctx := context.Background()

	result, err := r.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "disp-empty", Namespace: "default"},
	})
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, result.RequeueAfter, "reconcilePending requeues after 5s")

	// Side effects that only reconcilePending produces.
	fetched := fetch(t, r, "disp-empty")
	assert.Equal(t, "Initializing", fetched.Status.Phase)
	assert.NotNil(t, fetched.Status.StartedAt, "reconcilePending must stamp StartedAt")

	var pvc corev1.PersistentVolumeClaim
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "disp-empty-output", Namespace: "default"}, &pvc),
		"output PVC should be created by reconcilePending via the Reconcile dispatcher")
}

// TestReconcile_PendingPhase_RoutesToPending verifies that an explicit
// "Pending" phase is also dispatched to reconcilePending (the case
// statement is `case "", "Pending"`).
func TestReconcile_PendingPhase_RoutesToPending(t *testing.T) {
	team := withWorkspace(minimalTeam("disp-pending"))
	team.Status.Phase = "Pending"
	r := newReconciler(team)
	ctx := context.Background()

	result, err := r.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "disp-pending", Namespace: "default"},
	})
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, result.RequeueAfter)

	fetched := fetch(t, r, "disp-pending")
	assert.Equal(t, "Initializing", fetched.Status.Phase,
		"explicit Pending phase must still transition through reconcilePending")
}

// TestReconcile_RunningPhase_RoutesToRunning verifies that phase "Running"
// is dispatched to reconcileRunning. We use the "lead not spawned"
// scenario so the behavior matches TestReconcileRunning_LeadNotSpawned_KeepsRunning:
// phase stays Running and the result requeues after 30s.
func TestReconcile_RunningPhase_RoutesToRunning(t *testing.T) {
	team := minimalTeam("disp-running")
	startTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))
	team.Status.Phase = "Running"
	team.Status.StartedAt = &startTime
	r := newReconciler(team)
	ctx := context.Background()

	result, err := r.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "disp-running", Namespace: "default"},
	})
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, result.RequeueAfter,
		"reconcileRunning requeues after 30s when waiting on lead pod")

	fetched := fetch(t, r, "disp-running")
	assert.Equal(t, "Running", fetched.Status.Phase,
		"phase must stay Running when dispatcher routes to reconcileRunning with no pods spawned")
}

// TestReconcile_NotFound_ReturnsNilWithoutError verifies the not-found
// branch of the initial Get: Reconcile must return ctrl.Result{} and nil,
// without requeueing or erroring.
func TestReconcile_NotFound_ReturnsNilWithoutError(t *testing.T) {
	r := newReconciler() // empty fake client — no AgentTeam objects
	ctx := context.Background()

	result, err := r.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "does-not-exist", Namespace: "default"},
	})
	require.NoError(t, err, "not-found must be a silent no-op, never an error")
	assert.Equal(t, ctrl.Result{}, result, "not-found must not requeue")
}

// TestReconcile_UnknownPhase_ResetsAndRequeues verifies that a phase value
// not handled by any case statement is reset back to "Pending" and
// requeued so the next reconcile picks it up via the normal flow.
func TestReconcile_UnknownPhase_ResetsAndRequeues(t *testing.T) {
	team := withWorkspace(minimalTeam("disp-unknown"))
	team.Status.Phase = "Bogus"
	r := newReconciler(team)
	ctx := context.Background()

	result, err := r.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "disp-unknown", Namespace: "default"},
	})
	require.NoError(t, err)
	assert.True(t, result.Requeue, "unknown phase must requeue so the reset takes effect")

	fetched := fetch(t, r, "disp-unknown")
	assert.Equal(t, "Pending", fetched.Status.Phase,
		"unknown phase must be reset to Pending")
}

// --- Small helper coverage gaps (issue #28) ---

// TestBuildAgentPod_CoworkMode_PVCInput exercises the pvcVolumeReadOnly branch
// of the workspace-inputs loop by setting workspace.inputs[].pvc instead of
// configMap. The resulting pod must mount the named PVC as a read-only volume.
func TestBuildAgentPod_CoworkMode_PVCInput(t *testing.T) {
	team := withWorkspace(minimalTeam("cowork-pvc-test"))
	team.Spec.Workspace.Inputs = []claudev1alpha1.WorkspaceInputSpec{
		{PVC: "shared-dataset", MountPath: "/workspace/data"},
	}
	r := newReconciler(team)

	pod := r.buildAgentPod(team, "worker", "sonnet", "do work", "auto-accept", false,
		corev1.ResourceRequirements{}, nil, nil, nil, nil)

	// The PVC-backed input must appear as workspace-input-0 and be read-only.
	var inputVol *corev1.Volume
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == "workspace-input-0" {
			inputVol = &pod.Spec.Volumes[i]
			break
		}
	}
	require.NotNil(t, inputVol, "workspace-input-0 volume should be created for PVC input")
	require.NotNil(t, inputVol.PersistentVolumeClaim, "input volume should be PVC-backed, not ConfigMap")
	assert.Equal(t, "shared-dataset", inputVol.PersistentVolumeClaim.ClaimName)
	assert.True(t, inputVol.PersistentVolumeClaim.ReadOnly,
		"PVC-backed workspace inputs must be mounted read-only")

	// The mount path from the spec is propagated to the container.
	mounts := mountPaths(pod)
	assert.Contains(t, mounts, "/workspace/data")
}

// TestPodPhaseToAgentPhase covers every branch of the switch including the
// default/unknown case. The default must fall through to "Pending" so the
// reconciler keeps waiting rather than treating an unrecognized pod as terminal.
func TestPodPhaseToAgentPhase(t *testing.T) {
	cases := []struct {
		name     string
		phase    corev1.PodPhase
		expected string
	}{
		{"Pending", corev1.PodPending, "Pending"},
		{"Running", corev1.PodRunning, "Running"},
		{"Succeeded", corev1.PodSucceeded, "Completed"},
		{"Failed", corev1.PodFailed, "Failed"},
		{"EmptyFallsThroughToPending", "", "Pending"},
		{"UnknownFallsThroughToPending", corev1.PodPhase("Weird"), "Pending"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pod := &corev1.Pod{Status: corev1.PodStatus{Phase: tc.phase}}
			assert.Equal(t, tc.expected, podPhaseToAgentPhase(pod))
		})
	}
}

// TestSetTeammatePendingApproval_AppendsNewEntry covers the append branch:
// when the teammate has no existing TeammateStatus entry, setTeammatePendingApproval
// must append a new one rather than silently dropping the request.
func TestSetTeammatePendingApproval_AppendsNewEntry(t *testing.T) {
	r := &AgentTeamReconciler{}
	team := minimalTeam("append-test")
	// No pre-existing teammate status entries.
	require.Empty(t, team.Status.Teammates)

	r.setTeammatePendingApproval(team, "worker", "spawn-worker")

	require.Len(t, team.Status.Teammates, 1, "new teammate status entry must be appended")
	assert.Equal(t, "worker", team.Status.Teammates[0].Name)
	assert.Equal(t, "spawn-worker", team.Status.Teammates[0].PendingApproval)
}

// TestSetTeammatePendingApproval_UpdatesExistingEntry covers the in-place
// update branch: when a TeammateStatus entry already exists for the named
// teammate, setTeammatePendingApproval must overwrite its PendingApproval
// rather than appending a duplicate row.
func TestSetTeammatePendingApproval_UpdatesExistingEntry(t *testing.T) {
	r := &AgentTeamReconciler{}
	team := minimalTeam("update-test")
	team.Status.Teammates = []claudev1alpha1.TeammateStatus{
		{Name: "worker", PendingApproval: ""},
		{Name: "reviewer", PendingApproval: ""},
	}

	r.setTeammatePendingApproval(team, "reviewer", "spawn-reviewer")

	require.Len(t, team.Status.Teammates, 2, "no new entry should be appended when one already exists")
	assert.Equal(t, "", team.Status.Teammates[0].PendingApproval, "unrelated teammate must not be touched")
	assert.Equal(t, "spawn-reviewer", team.Status.Teammates[1].PendingApproval,
		"existing teammate's PendingApproval must be updated in place")
}

// TestClearTeammatePendingApproval_NotFoundIsNoop covers the fall-through
// branch: calling clear on a teammate name that isn't in the status slice must
// leave the slice untouched rather than panic or append a phantom entry.
func TestClearTeammatePendingApproval_NotFoundIsNoop(t *testing.T) {
	r := &AgentTeamReconciler{}
	team := minimalTeam("clear-missing-test")
	team.Status.Teammates = []claudev1alpha1.TeammateStatus{
		{Name: "worker", PendingApproval: "spawn-worker"},
	}

	r.clearTeammatePendingApproval(team, "ghost")

	require.Len(t, team.Status.Teammates, 1, "clear on missing name must not append or drop entries")
	assert.Equal(t, "worker", team.Status.Teammates[0].Name)
	assert.Equal(t, "spawn-worker", team.Status.Teammates[0].PendingApproval,
		"existing teammate status must be left untouched")
}

// TestClearTeammatePendingApproval_ClearsExistingEntry covers the matched-entry
// branch: when the named teammate has a pending approval, clearTeammatePendingApproval
// must zero out the field so the reconciler stops gating that teammate.
func TestClearTeammatePendingApproval_ClearsExistingEntry(t *testing.T) {
	r := &AgentTeamReconciler{}
	team := minimalTeam("clear-existing-test")
	team.Status.Teammates = []claudev1alpha1.TeammateStatus{
		{Name: "worker", PendingApproval: "spawn-worker"},
		{Name: "reviewer", PendingApproval: "spawn-reviewer"},
	}

	r.clearTeammatePendingApproval(team, "worker")

	require.Len(t, team.Status.Teammates, 2, "clear must not change slice length")
	assert.Equal(t, "", team.Status.Teammates[0].PendingApproval,
		"matched teammate's PendingApproval must be cleared")
	assert.Equal(t, "spawn-reviewer", team.Status.Teammates[1].PendingApproval,
		"unrelated teammate must not be touched")
}

// TestAgentImage_DefaultWhenUnset verifies agentImage() returns the harness
// adapter's default image when the reconciler field is empty, and the
// override when set.
func TestAgentImage_DefaultWhenUnset(t *testing.T) {
	r := &AgentTeamReconciler{}
	assert.Equal(t, harness.ClaudeCode{}.DefaultImage(), r.agentImage(),
		"agentImage must return the claude-code adapter's default when AgentImage is unset")

	r.AgentImage = "custom/image:v1"
	assert.Equal(t, "custom/image:v1", r.agentImage(),
		"agentImage must return the override when AgentImage is set")
}

// TestInitImage_DefaultWhenUnset verifies initImage() returns the baked-in
// default when the reconciler field is empty, and the override when set.
func TestInitImage_DefaultWhenUnset(t *testing.T) {
	r := &AgentTeamReconciler{}
	assert.Equal(t, defaultInitImage, r.initImage(),
		"initImage must return defaultInitImage when InitImage is unset")

	r.InitImage = "custom/init:v1"
	assert.Equal(t, "custom/init:v1", r.initImage(),
		"initImage must return the override when InitImage is set")
}

// TestPVCAccessMode_DefaultWhenUnset verifies pvcAccessMode() returns
// ReadWriteMany when the reconciler field is empty (the production default),
// and the override when set (used by the Kind dev cluster to avoid NFS).
func TestPVCAccessMode_DefaultWhenUnset(t *testing.T) {
	r := &AgentTeamReconciler{}
	assert.Equal(t, corev1.ReadWriteMany, r.pvcAccessMode(),
		"pvcAccessMode must default to ReadWriteMany when PVCAccessMode is unset")

	r.PVCAccessMode = corev1.ReadWriteOnce
	assert.Equal(t, corev1.ReadWriteOnce, r.pvcAccessMode(),
		"pvcAccessMode must return the override when PVCAccessMode is set")
}

// --- MCP ConfigMap (issue #26) ---

// decodeMCPConfig parses the JSON stored in the per-agent MCP ConfigMap and
// returns the mcpServers map. Shared helper for the MCP test suite.
func decodeMCPConfig(t *testing.T, cm *corev1.ConfigMap) map[string]map[string]string {
	t.Helper()
	raw, ok := cm.Data["mcp.json"]
	require.True(t, ok, "ConfigMap must contain an mcp.json key")

	var wrapped struct {
		MCPServers map[string]map[string]string `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &wrapped),
		"mcp.json must be valid JSON wrapped in {\"mcpServers\": {...}}")
	return wrapped.MCPServers
}

// TestEnsureMCPConfigMap_CreatesConfigMapWithCorrectJSON exercises the happy
// path of ensureMCPConfigMap: the ConfigMap does not yet exist, so it must be
// created with the correct name, namespace, and {"mcpServers": {...}} JSON
// structure. Servers are stored with type "sse" and the configured URL.
func TestEnsureMCPConfigMap_CreatesConfigMapWithCorrectJSON(t *testing.T) {
	team := minimalTeam("mcp-create")
	r := newReconciler(team)
	ctx := context.Background()

	servers := []claudev1alpha1.MCPServerSpec{
		{Name: "gmail", URL: "https://gmail.mcp.example.com/mcp"},
	}
	require.NoError(t, r.ensureMCPConfigMap(ctx, team, "worker", servers))

	var cm corev1.ConfigMap
	require.NoError(t, r.Get(ctx, types.NamespacedName{
		Name:      "mcp-create-worker-mcp",
		Namespace: "default",
	}, &cm), "ensureMCPConfigMap must create a ConfigMap named {team}-{agent}-mcp")

	// Owner reference points back at the AgentTeam so garbage collection works.
	require.Len(t, cm.OwnerReferences, 1)
	assert.Equal(t, team.Name, cm.OwnerReferences[0].Name)
	assert.Equal(t, "AgentTeam", cm.OwnerReferences[0].Kind)

	// JSON structure: {"mcpServers": {"gmail": {"type": "sse", "url": "..."}}}
	mcpServers := decodeMCPConfig(t, &cm)
	require.Contains(t, mcpServers, "gmail", "gmail server must be in the mcpServers map")
	assert.Equal(t, "sse", mcpServers["gmail"]["type"],
		"MCP server type must be \"sse\" — Claude Code currently only supports SSE transport here")
	assert.Equal(t, "https://gmail.mcp.example.com/mcp", mcpServers["gmail"]["url"])
}

// TestEnsureMCPConfigMap_Idempotent verifies that calling ensureMCPConfigMap a
// second time is a no-op (does not error, does not re-create). This matters
// because reconcile runs repeatedly and the function must tolerate being
// invoked after the ConfigMap is already present.
func TestEnsureMCPConfigMap_Idempotent(t *testing.T) {
	team := minimalTeam("mcp-idempotent")
	r := newReconciler(team)
	ctx := context.Background()

	servers := []claudev1alpha1.MCPServerSpec{
		{Name: "gmail", URL: "https://gmail.mcp.example.com/mcp"},
	}
	require.NoError(t, r.ensureMCPConfigMap(ctx, team, "worker", servers))

	// Second call must not error even though the ConfigMap already exists.
	require.NoError(t, r.ensureMCPConfigMap(ctx, team, "worker", servers),
		"ensureMCPConfigMap must be idempotent so repeated reconciles don't fail")

	// There must still be exactly one ConfigMap with that name.
	var cmList corev1.ConfigMapList
	require.NoError(t, r.List(ctx, &cmList, client.InNamespace("default")))
	count := 0
	for _, cm := range cmList.Items {
		if cm.Name == "mcp-idempotent-worker-mcp" {
			count++
		}
	}
	assert.Equal(t, 1, count, "ensureMCPConfigMap must not duplicate the ConfigMap on a second call")
}

// TestEnsureMCPConfigMap_MultipleServers verifies that multi-server input
// produces one entry per server in the generated JSON, each with the correct
// URL. This pins the per-server loop behavior.
func TestEnsureMCPConfigMap_MultipleServers(t *testing.T) {
	team := minimalTeam("mcp-multi")
	r := newReconciler(team)
	ctx := context.Background()

	servers := []claudev1alpha1.MCPServerSpec{
		{Name: "gmail", URL: "https://gmail.mcp.example.com/mcp"},
		{Name: "calendar", URL: "https://cal.mcp.example.com/mcp"},
		{Name: "slack", URL: "https://slack.mcp.example.com/mcp"},
	}
	require.NoError(t, r.ensureMCPConfigMap(ctx, team, "worker", servers))

	var cm corev1.ConfigMap
	require.NoError(t, r.Get(ctx, types.NamespacedName{
		Name:      "mcp-multi-worker-mcp",
		Namespace: "default",
	}, &cm))

	mcpServers := decodeMCPConfig(t, &cm)
	assert.Len(t, mcpServers, 3, "all three MCP servers must be present in the JSON")
	assert.Equal(t, "https://gmail.mcp.example.com/mcp", mcpServers["gmail"]["url"])
	assert.Equal(t, "https://cal.mcp.example.com/mcp", mcpServers["calendar"]["url"])
	assert.Equal(t, "https://slack.mcp.example.com/mcp", mcpServers["slack"]["url"])
	for name, entry := range mcpServers {
		assert.Equal(t, "sse", entry["type"], "server %s must have type=sse", name)
	}
}

// TestEnsureAgentPod_WithMCPServers_CreatesConfigMapBeforePod exercises the
// integration between ensureAgentPod and ensureMCPConfigMap: when mcpServers
// are supplied, the ConfigMap must be created alongside the pod so the pod's
// "mcp-config" volume has a backing object at mount time.
func TestEnsureAgentPod_WithMCPServers_CreatesConfigMapBeforePod(t *testing.T) {
	team := minimalTeam("mcp-pod")
	r := newReconciler(team)
	ctx := context.Background()

	servers := []claudev1alpha1.MCPServerSpec{
		{Name: "gmail", URL: "https://gmail.mcp.example.com/mcp"},
	}

	require.NoError(t, r.ensureAgentPod(ctx, team, "worker", "sonnet", "do work",
		"auto-accept", false, corev1.ResourceRequirements{}, nil, nil, servers, nil))

	// The ConfigMap must exist — otherwise the pod's mcp-config volume would
	// fail to mount on a real cluster.
	var cm corev1.ConfigMap
	require.NoError(t, r.Get(ctx, types.NamespacedName{
		Name:      "mcp-pod-worker-mcp",
		Namespace: "default",
	}, &cm), "ensureAgentPod must create the MCP ConfigMap when mcpServers are set")

	// And the pod must reference it via the mcp-config volume.
	var pod corev1.Pod
	require.NoError(t, r.Get(ctx, types.NamespacedName{
		Name:      "mcp-pod-worker",
		Namespace: "default",
	}, &pod))

	var foundMCPVol bool
	for _, v := range pod.Spec.Volumes {
		if v.Name == "mcp-config" {
			require.NotNil(t, v.ConfigMap, "mcp-config volume must be ConfigMap-backed")
			assert.Equal(t, "mcp-pod-worker-mcp", v.ConfigMap.Name,
				"mcp-config volume must reference the per-agent MCP ConfigMap")
			foundMCPVol = true
			break
		}
	}
	assert.True(t, foundMCPVol, "pod must have an mcp-config volume when MCP servers are configured")
}

// TestEnsureAgentPod_Idempotent verifies the early-return branch when the pod
// already exists. ensureAgentPod is called on every reconcile loop, so it must
// short-circuit when the pod is present rather than erroring on AlreadyExists.
func TestEnsureAgentPod_Idempotent(t *testing.T) {
	team := minimalTeam("idem-pod")
	r := newReconciler(team)
	ctx := context.Background()

	require.NoError(t, r.ensureAgentPod(ctx, team, "worker", "sonnet", "do work",
		"auto-accept", false, corev1.ResourceRequirements{}, nil, nil, nil, nil))

	// Second call must be a no-op — no error, pod still present.
	require.NoError(t, r.ensureAgentPod(ctx, team, "worker", "sonnet", "do work",
		"auto-accept", false, corev1.ResourceRequirements{}, nil, nil, nil, nil),
		"ensureAgentPod must be idempotent so repeated reconciles don't fail")

	var pod corev1.Pod
	require.NoError(t, r.Get(ctx, types.NamespacedName{
		Name:      "idem-pod-worker",
		Namespace: "default",
	}, &pod))
}

// TestEnsureAgentPod_NoMCPServers_SkipsConfigMap verifies the negative path:
// without mcpServers the ConfigMap guard in ensureAgentPod must not run, and
// no MCP ConfigMap should be created.
func TestEnsureAgentPod_NoMCPServers_SkipsConfigMap(t *testing.T) {
	team := minimalTeam("mcp-none")
	r := newReconciler(team)
	ctx := context.Background()

	require.NoError(t, r.ensureAgentPod(ctx, team, "worker", "sonnet", "do work",
		"auto-accept", false, corev1.ResourceRequirements{}, nil, nil, nil, nil))

	var cm corev1.ConfigMap
	err := r.Get(ctx, types.NamespacedName{
		Name:      "mcp-none-worker-mcp",
		Namespace: "default",
	}, &cm)
	assert.True(t, errors.IsNotFound(err),
		"no MCP ConfigMap should be created when mcpServers is empty")
}

// --- executeOnComplete and sendWebhookEvent (issue #27) ---

// TestExecuteOnComplete_NotifyWithWebhook_PostsPayload exercises the happy path
// of the "notify" branch: when Lifecycle.OnComplete is "notify" and a webhook
// is configured, executeOnComplete must POST a JSON payload to the webhook URL
// containing the team name, namespace, phase, and event="completed".
func TestExecuteOnComplete_NotifyWithWebhook_PostsPayload(t *testing.T) {
	var capturedBody []byte
	var capturedMethod string
	var capturedContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	team := minimalTeam("notify-team")
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{OnComplete: "notify"}
	team.Spec.Observability = &claudev1alpha1.ObservabilitySpec{
		Webhook: &claudev1alpha1.WebhookSpec{
			URL:    server.URL,
			Events: []string{"completed"},
		},
	}
	team.Status.Phase = "Running" // phase at the time of the webhook fire

	r := newReconciler(team)
	require.NoError(t, r.executeOnComplete(context.Background(), team))

	// Verify the webhook was actually called.
	assert.Equal(t, http.MethodPost, capturedMethod, "executeOnComplete must POST")
	assert.Equal(t, "application/json", capturedContentType,
		"webhook payload must be JSON")

	var payload map[string]string
	require.NoError(t, json.Unmarshal(capturedBody, &payload))
	assert.Equal(t, "completed", payload["event"], "event must be \"completed\" for OnComplete notify")
	assert.Equal(t, "notify-team", payload["team"])
	assert.Equal(t, "default", payload["namespace"])
	assert.Equal(t, "Running", payload["phase"], "phase reflects team state at webhook time")
}

// TestExecuteOnComplete_NotifyWithoutWebhook_NoOp covers the "notify" branch
// when no Webhook is configured: the function must short-circuit to nil
// without erroring rather than dereferencing a nil Webhook.
func TestExecuteOnComplete_NotifyWithoutWebhook_NoOp(t *testing.T) {
	team := minimalTeam("notify-no-webhook")
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{OnComplete: "notify"}
	// Observability set, but Webhook nil.
	team.Spec.Observability = &claudev1alpha1.ObservabilitySpec{LogLevel: "info"}

	r := newReconciler(team)
	require.NoError(t, r.executeOnComplete(context.Background(), team),
		"OnComplete=notify with no webhook must be a silent no-op, not an error")
}

// TestExecuteOnComplete_NotifyWithoutObservability_NoOp covers the case where
// Observability itself is nil — exercises the outer guard on the notify branch.
func TestExecuteOnComplete_NotifyWithoutObservability_NoOp(t *testing.T) {
	team := minimalTeam("notify-no-obs")
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{OnComplete: "notify"}
	// Observability nil entirely.

	r := newReconciler(team)
	require.NoError(t, r.executeOnComplete(context.Background(), team),
		"OnComplete=notify with no Observability must be a silent no-op")
}

// TestExecuteOnComplete_CreatePR_MissingRepoErrors — a team configured with
// OnComplete=create-pr but no repository URL must surface the configuration
// error. The reconcileRunning caller swallows the error so the team still
// finishes, but the operator logs it and emits a PRCreationFailed event.
func TestExecuteOnComplete_CreatePR_MissingRepoErrors(t *testing.T) {
	team := minimalTeam("create-pr-team")
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{OnComplete: "create-pr"}

	r := newReconciler(team)
	err := r.executeOnComplete(context.Background(), team)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repository.url")
}

// TestExecuteOnComplete_PushBranch_StubReturnsNil covers the "push-branch"
// log-only stub for the same reason as create-pr.
func TestExecuteOnComplete_PushBranch_StubReturnsNil(t *testing.T) {
	team := minimalTeam("push-branch-team")
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{OnComplete: "push-branch"}

	r := newReconciler(team)
	assert.NoError(t, r.executeOnComplete(context.Background(), team),
		"push-branch stub must return nil so completion isn't blocked")
}

// TestSendWebhookEvent_HappyPath verifies that sendWebhookEvent POSTs the
// expected JSON shape and returns nil when the server responds 2xx.
func TestSendWebhookEvent_HappyPath(t *testing.T) {
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		w.WriteHeader(http.StatusAccepted) // 202 — also a 2xx
	}))
	defer server.Close()

	team := minimalTeam("webhook-happy")
	team.Status.Phase = "Initializing"
	r := newReconciler(team)

	require.NoError(t, r.sendWebhookEvent(context.Background(), server.URL, "spawn-worker", team))

	var payload map[string]string
	require.NoError(t, json.Unmarshal(capturedBody, &payload))
	assert.Equal(t, "spawn-worker", payload["event"])
	assert.Equal(t, "webhook-happy", payload["team"])
	assert.Equal(t, "default", payload["namespace"])
	assert.Equal(t, "Initializing", payload["phase"])
}

// TestSendWebhookEvent_Non2xxReturnsError verifies that a 4xx/5xx response
// from the webhook is reported as an error so callers can log/surface it.
func TestSendWebhookEvent_Non2xxReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	team := minimalTeam("webhook-500")
	r := newReconciler(team)

	err := r.sendWebhookEvent(context.Background(), server.URL, "completed", team)
	require.Error(t, err, "non-2xx webhook responses must return an error")
	assert.Contains(t, err.Error(), "500", "error message should include the HTTP status code")
}

// TestSendWebhookEvent_BadURLReturnsError covers the http.NewRequestWithContext
// error path: an unparseable URL must surface as an error rather than panicking.
func TestSendWebhookEvent_BadURLReturnsError(t *testing.T) {
	team := minimalTeam("webhook-badurl")
	r := newReconciler(team)

	err := r.sendWebhookEvent(context.Background(), "http://\x7f-bad-host/", "completed", team)
	require.Error(t, err, "an invalid webhook URL must return an error, not panic")
}

// TestReconcileRunning_OnNotifyCompletion_FiresWebhookAndCompletes is the
// integration test for the issue: when reconcileRunning detects all pods done
// and OnComplete is "notify", it must (1) fire the webhook and (2) still set
// Phase=Completed. This pins the contract between the reconciler and the
// post-completion hook.
func TestReconcileRunning_OnNotifyCompletion_FiresWebhookAndCompletes(t *testing.T) {
	var webhookCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		webhookCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	team := minimalTeam("notify-complete")
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{OnComplete: "notify"}
	team.Spec.Observability = &claudev1alpha1.ObservabilitySpec{
		Webhook: &claudev1alpha1.WebhookSpec{
			URL:    server.URL,
			Events: []string{"completed"},
		},
	}
	startTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))
	team.Status.StartedAt = &startTime
	team.Status.Phase = "Running"

	leadPod := succeededPod("notify-complete-lead", "default", "notify-complete")
	workerPod := succeededPod("notify-complete-worker", "default", "notify-complete")

	r := newReconciler(team, leadPod, workerPod)
	team = fetch(t, r, "notify-complete")
	team.Status.Phase = "Running"
	team.Status.StartedAt = &startTime
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)
	assert.True(t, webhookCalled, "reconcileRunning must invoke the notify webhook on completion")

	fetched := fetch(t, r, "notify-complete")
	assert.Equal(t, "Completed", fetched.Status.Phase,
		"team must reach Completed even though the webhook side-effect was performed")
}

// TestReconcileRunning_WebhookFailureDoesNotBlockCompletion verifies the
// "post-completion actions are non-fatal" contract: if the webhook returns 500,
// the reconciler must still mark the team Completed and not bubble the error.
func TestReconcileRunning_WebhookFailureDoesNotBlockCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	team := minimalTeam("notify-fail")
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{OnComplete: "notify"}
	team.Spec.Observability = &claudev1alpha1.ObservabilitySpec{
		Webhook: &claudev1alpha1.WebhookSpec{
			URL:    server.URL,
			Events: []string{"completed"},
		},
	}
	startTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))
	team.Status.StartedAt = &startTime
	team.Status.Phase = "Running"

	leadPod := succeededPod("notify-fail-lead", "default", "notify-fail")
	workerPod := succeededPod("notify-fail-worker", "default", "notify-fail")

	r := newReconciler(team, leadPod, workerPod)
	team = fetch(t, r, "notify-fail")
	team.Status.Phase = "Running"
	team.Status.StartedAt = &startTime
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err, "reconcileRunning must swallow webhook errors")

	fetched := fetch(t, r, "notify-fail")
	assert.Equal(t, "Completed", fetched.Status.Phase,
		"webhook failure must NOT block transition to Completed")
}

// --- Pod/Volume introspection helpers ---

func envMap(pod *corev1.Pod) map[string]string {
	m := map[string]string{}
	for _, e := range pod.Spec.Containers[0].Env {
		m[e.Name] = e.Value
	}
	return m
}

func volumeNames(pod *corev1.Pod) []string {
	names := make([]string, len(pod.Spec.Volumes))
	for i, v := range pod.Spec.Volumes {
		names[i] = v.Name
	}
	return names
}

func mountPaths(pod *corev1.Pod) []string {
	paths := make([]string, len(pod.Spec.Containers[0].VolumeMounts))
	for i, m := range pod.Spec.Containers[0].VolumeMounts {
		paths[i] = m.MountPath
	}
	return paths
}

// drainFakeRecorder reads everything currently sitting in the FakeRecorder's
// channel into a slice. FakeRecorder's channel is buffered — events arrive
// synchronously from Eventf — so a single non-blocking drain after a reconcile
// call gives us everything emitted by that call.
func drainFakeRecorder(r *record.FakeRecorder) []string {
	var events []string
	for {
		select {
		case e := <-r.Events:
			events = append(events, e)
		default:
			return events
		}
	}
}

// --- syncPodStatuses (extended behavior) ---

// TestSyncPodStatuses_EnsuresEntryForEveryTeammate verifies that every spec
// teammate gets a TeammateStatus entry even before its pod is scheduled —
// critical for `kubectl describe` to surface the full roster during startup.
func TestSyncPodStatuses_EnsuresEntryForEveryTeammate(t *testing.T) {
	team := minimalTeam("sync-empty")
	team.Spec.Teammates = append(team.Spec.Teammates, claudev1alpha1.TeammateSpec{
		Name: "reviewer", Model: "sonnet", Prompt: "Review the work",
	})
	r := newReconciler(team) // no pods at all
	team = fetch(t, r, "sync-empty")

	r.syncPodStatuses(context.Background(), team)

	require.Len(t, team.Status.Teammates, 2, "every spec teammate must surface in status even pre-pod")
	assert.Equal(t, "worker", team.Status.Teammates[0].Name)
	assert.Equal(t, "Waiting", team.Status.Teammates[0].Phase)
	assert.Empty(t, team.Status.Teammates[0].PodName, "pre-pod teammate must not carry a PodName")
	assert.Equal(t, "reviewer", team.Status.Teammates[1].Name)
	assert.Equal(t, "Waiting", team.Status.Teammates[1].Phase)
}

// TestSyncPodStatuses_ComputesReadyString verifies Ready is populated as
// "running+completed/total" — the value rendered in the `kubectl get` Ready
// column and the talk-ready demo.
func TestSyncPodStatuses_ComputesReadyString(t *testing.T) {
	team := minimalTeam("sync-ready")
	team.Spec.Teammates = append(team.Spec.Teammates,
		claudev1alpha1.TeammateSpec{Name: "reviewer", Model: "sonnet", Prompt: "Review"},
		claudev1alpha1.TeammateSpec{Name: "idle", Model: "sonnet", Prompt: "Idle"},
	)
	workerPod := runningPod("sync-ready-worker", "default", "sync-ready")
	reviewerPod := succeededPod("sync-ready-reviewer", "default", "sync-ready")
	// 'idle' teammate has no pod → Waiting, not counted.
	r := newReconciler(team, workerPod, reviewerPod)
	team = fetch(t, r, "sync-ready")

	r.syncPodStatuses(context.Background(), team)

	assert.Equal(t, "2/3", team.Status.Ready,
		"Ready must count Running+Completed teammates against the spec total")
}

// TestSyncPodStatuses_PreservesPendingApproval verifies that re-syncing does
// not clobber non-pod fields like PendingApproval that are populated by the
// approval-gate helpers on a separate code path.
func TestSyncPodStatuses_PreservesPendingApproval(t *testing.T) {
	team := minimalTeam("sync-preserve")
	team.Status.Teammates = []claudev1alpha1.TeammateStatus{
		{Name: "worker", PendingApproval: "spawn-worker"},
	}
	r := newReconciler(team) // no pods
	team = fetch(t, r, "sync-preserve")

	r.syncPodStatuses(context.Background(), team)

	require.Len(t, team.Status.Teammates, 1)
	assert.Equal(t, "spawn-worker", team.Status.Teammates[0].PendingApproval,
		"PendingApproval must survive a sync cycle")
	assert.Equal(t, "Waiting", team.Status.Teammates[0].Phase,
		"pod-missing teammate must report Waiting even when other fields are preserved")
}

// --- Event emission on phase transitions ---

// TestReconcilePending_EmitsInitializingEvent verifies the Pending → Initializing
// transition emits a Normal event so `kubectl describe` surfaces it.
func TestReconcilePending_EmitsInitializingEvent(t *testing.T) {
	team := withWorkspace(minimalTeam("evt-init"))
	r := newReconciler(team)
	recorder := record.NewFakeRecorder(8)
	r.Recorder = recorder
	ctx := context.Background()

	_, err := r.reconcilePending(ctx, fetch(t, r, "evt-init"))
	require.NoError(t, err)

	events := drainFakeRecorder(recorder)
	require.NotEmpty(t, events, "expected at least one event on transition")
	found := false
	for _, e := range events {
		if strings.Contains(e, "Normal") && strings.Contains(e, "Initializing") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected a Normal Initializing event, got: %v", events)
}

// TestReconcileRunning_EmitsCompletedEvent verifies the Running → Completed
// transition emits a Normal Completed event.
func TestReconcileRunning_EmitsCompletedEvent(t *testing.T) {
	team := minimalTeam("evt-done")
	startTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))
	team.Status.Phase = "Running"
	team.Status.StartedAt = &startTime
	leadPod := succeededPod("evt-done-lead", "default", "evt-done")
	workerPod := succeededPod("evt-done-worker", "default", "evt-done")
	r := newReconciler(team, leadPod, workerPod)
	recorder := record.NewFakeRecorder(8)
	r.Recorder = recorder
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, fetch(t, r, "evt-done"))
	require.NoError(t, err)

	events := drainFakeRecorder(recorder)
	found := false
	for _, e := range events {
		if strings.Contains(e, "Normal") && strings.Contains(e, "Completed") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected a Normal Completed event, got: %v", events)
}

// TestReconcileRunning_EmitsWarningOnRestart verifies a teammate pod failure
// under the restart limit emits a Warning TeammateRestarted event.
func TestReconcileRunning_EmitsWarningOnRestart(t *testing.T) {
	team := minimalTeam("evt-restart")
	startTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))
	team.Status.Phase = "Running"
	team.Status.StartedAt = &startTime
	leadPod := runningPod("evt-restart-lead", "default", "evt-restart")
	workerPod := failedPod("evt-restart-worker", "default", "evt-restart")
	r := newReconciler(team, leadPod, workerPod)
	recorder := record.NewFakeRecorder(8)
	r.Recorder = recorder
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, fetch(t, r, "evt-restart"))
	require.NoError(t, err)

	events := drainFakeRecorder(recorder)
	found := false
	for _, e := range events {
		if strings.Contains(e, "Warning") && strings.Contains(e, "TeammateRestarted") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected a Warning TeammateRestarted event, got: %v", events)
}

// TestReconcileRunning_EmitsWarningOnRestartLimitExceeded verifies the team
// emits a Warning RestartLimitExceeded event when a teammate exhausts its
// restart budget.
func TestReconcileRunning_EmitsWarningOnRestartLimitExceeded(t *testing.T) {
	team := minimalTeam("evt-exceed")
	startTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))
	team.Status.Phase = "Running"
	team.Status.StartedAt = &startTime
	leadPod := runningPod("evt-exceed-lead", "default", "evt-exceed")
	workerPod := failedPod("evt-exceed-worker", "default", "evt-exceed")
	r := newReconciler(team, leadPod, workerPod)
	fetched := fetch(t, r, "evt-exceed")
	fetched.Status.Phase = "Running"
	fetched.Status.StartedAt = &startTime
	fetched.Status.Teammates = []claudev1alpha1.TeammateStatus{
		{Name: "worker", RestartCount: 3},
	}
	recorder := record.NewFakeRecorder(8)
	r.Recorder = recorder
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, fetched)
	require.NoError(t, err)

	events := drainFakeRecorder(recorder)
	found := false
	for _, e := range events {
		if strings.Contains(e, "Warning") && strings.Contains(e, "RestartLimitExceeded") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected a Warning RestartLimitExceeded event, got: %v", events)
}

// TestRecordEvent_NilRecorderIsNoop verifies recordEvent is safe with a nil
// Recorder — unit tests construct reconcilers directly without
// SetupWithManager, so recordEvent calls from production code paths must not
// panic in that setup.
func TestRecordEvent_NilRecorderIsNoop(t *testing.T) {
	r := &AgentTeamReconciler{}
	team := minimalTeam("nil-rec")
	assert.NotPanics(t, func() {
		r.recordEvent(team, corev1.EventTypeNormal, "Test", "no panic with nil recorder")
	})
}

// --- Output routing (Outputs / Inputs / ArtifactStatus) ---

// TestEffectiveDependencies covers the merge of explicit DependsOn with
// implicit Inputs[].From producers. The result is what spawn-time
// dependency checks should wait for, deduped and order-preserving.
func TestEffectiveDependencies(t *testing.T) {
	cases := []struct {
		name string
		tm   claudev1alpha1.TeammateSpec
		want []string
	}{
		{
			name: "empty",
			tm:   claudev1alpha1.TeammateSpec{Name: "x"},
			want: []string{},
		},
		{
			name: "only DependsOn",
			tm:   claudev1alpha1.TeammateSpec{Name: "writer", DependsOn: []string{"researcher"}},
			want: []string{"researcher"},
		},
		{
			name: "only Inputs",
			tm: claudev1alpha1.TeammateSpec{
				Name:   "writer",
				Inputs: []claudev1alpha1.InputSpec{{From: "researcher", Artifact: "findings.md", MountPath: "/in"}},
			},
			want: []string{"researcher"},
		},
		{
			name: "both with overlap deduped",
			tm: claudev1alpha1.TeammateSpec{
				Name:      "writer",
				DependsOn: []string{"researcher"},
				Inputs: []claudev1alpha1.InputSpec{
					{From: "researcher", Artifact: "findings.md", MountPath: "/in"},
				},
			},
			want: []string{"researcher"},
		},
		{
			name: "merge distinct + preserve DependsOn first",
			tm: claudev1alpha1.TeammateSpec{
				Name:      "writer",
				DependsOn: []string{"a"},
				Inputs: []claudev1alpha1.InputSpec{
					{From: "b", Artifact: "x.md", MountPath: "/x"},
					{From: "a", Artifact: "y.md", MountPath: "/y"}, // dup of DependsOn
					{From: "c", Artifact: "z.md", MountPath: "/z"},
				},
			},
			want: []string{"a", "b", "c"},
		},
		{
			name: "empty From in Inputs ignored",
			tm: claudev1alpha1.TeammateSpec{
				Name:   "writer",
				Inputs: []claudev1alpha1.InputSpec{{From: "", Artifact: "x.md", MountPath: "/x"}},
			},
			want: []string{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := effectiveDependencies(c.tm)
			assert.Equal(t, c.want, got)
		})
	}
}

// TestFindProducerOutputPath resolves a producer's output path by basename.
func TestFindProducerOutputPath(t *testing.T) {
	team := &claudev1alpha1.AgentTeam{
		Spec: claudev1alpha1.AgentTeamSpec{
			Teammates: []claudev1alpha1.TeammateSpec{
				{
					Name: "researcher",
					Outputs: []claudev1alpha1.OutputSpec{
						{Path: "/workspace/output/findings.md"},
						{Path: "/workspace/output/summary.txt"},
					},
				},
				{
					Name: "writer",
					Outputs: []claudev1alpha1.OutputSpec{
						{Path: "/workspace/output/report.pdf"},
					},
				},
			},
		},
	}

	assert.Equal(t, "/workspace/output/findings.md",
		findProducerOutputPath(team, "researcher", "findings.md"))
	assert.Equal(t, "/workspace/output/summary.txt",
		findProducerOutputPath(team, "researcher", "summary.txt"))
	assert.Equal(t, "/workspace/output/report.pdf",
		findProducerOutputPath(team, "writer", "report.pdf"))

	// Unknown producer → empty.
	assert.Empty(t, findProducerOutputPath(team, "ghost", "findings.md"))
	// Known producer, unknown artifact → empty.
	assert.Empty(t, findProducerOutputPath(team, "researcher", "missing.md"))
}

// TestRecordTeammateArtifacts is idempotent and only records declared outputs.
func TestRecordTeammateArtifacts(t *testing.T) {
	team := minimalTeam("artifacts")
	tm := claudev1alpha1.TeammateSpec{
		Name: "researcher",
		Outputs: []claudev1alpha1.OutputSpec{
			{Path: "/workspace/output/findings.md", Description: "research"},
			{Path: "/workspace/output/summary.txt"},
		},
	}

	now := time.Now()
	recordTeammateArtifacts(team, tm, now)
	require.Len(t, team.Status.Artifacts, 2)
	assert.Equal(t, "findings.md", team.Status.Artifacts[0].Name)
	assert.Equal(t, "/workspace/output/findings.md", team.Status.Artifacts[0].Path)
	assert.Equal(t, "researcher", team.Status.Artifacts[0].ProducedBy)
	assert.WithinDuration(t, now, team.Status.Artifacts[0].ProducedAt.Time, time.Second)

	// Second call must not duplicate.
	recordTeammateArtifacts(team, tm, now.Add(time.Hour))
	assert.Len(t, team.Status.Artifacts, 2, "recordTeammateArtifacts must be idempotent")

	// A teammate with no outputs is a no-op.
	recordTeammateArtifacts(team, claudev1alpha1.TeammateSpec{Name: "x"}, now)
	assert.Len(t, team.Status.Artifacts, 2)
}

// TestBuildAgentPod_OutputRouting_AddsInitContainerAndEmptyDir verifies the
// pod-builder integration: a consumer with Inputs gets a per-input emptyDir
// volume, a corresponding main-container mount, and an init container that
// stages the producer's output file at the requested mountPath.
func TestBuildAgentPod_OutputRouting_AddsInitContainerAndEmptyDir(t *testing.T) {
	r := &AgentTeamReconciler{}
	team := minimalTeam("routed")
	team.Spec.Workspace = &claudev1alpha1.WorkspaceSpec{
		Output: &claudev1alpha1.WorkspaceOutputSpec{
			Size:      "1Gi",
			MountPath: "/workspace/output",
		},
	}
	// Producer is declared at the spec level (operator scans team.Spec.Teammates
	// to resolve the producer's output path during build).
	team.Spec.Teammates = []claudev1alpha1.TeammateSpec{
		{
			Name: "researcher",
			Outputs: []claudev1alpha1.OutputSpec{
				{Path: "/workspace/output/findings.md"},
			},
		},
	}

	inputs := []claudev1alpha1.InputSpec{
		{From: "researcher", Artifact: "findings.md", MountPath: "/workspace/stage/research"},
	}

	pod := r.buildAgentPod(team, "writer", "sonnet", "draft the report", "auto-accept",
		false, corev1.ResourceRequirements{}, nil, nil, nil, inputs)

	// One init container, one new emptyDir volume.
	require.Len(t, pod.Spec.InitContainers, 1, "one init container per input")
	require.Equal(t, "stage-input-0", pod.Spec.InitContainers[0].Name)
	require.Contains(t, pod.Spec.InitContainers[0].Command[2], "/workspace/output/findings.md",
		"init container must read the producer's declared output path")
	require.Contains(t, pod.Spec.InitContainers[0].Command[2], "/workspace/stage/research/findings.md",
		"init container must write to {mountPath}/{artifact}")

	// Main container mounts the emptyDir at the consumer-requested mountPath.
	var mainMount *corev1.VolumeMount
	for i := range pod.Spec.Containers[0].VolumeMounts {
		if pod.Spec.Containers[0].VolumeMounts[i].MountPath == "/workspace/stage/research" {
			mainMount = &pod.Spec.Containers[0].VolumeMounts[i]
		}
	}
	require.NotNil(t, mainMount, "main container must mount the input emptyDir at the consumer's mountPath")
	assert.Equal(t, "input-0", mainMount.Name)
	assert.True(t, mainMount.ReadOnly, "main container should not mutate staged inputs")

	// Volume exists.
	var inputVol *corev1.Volume
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == "input-0" {
			inputVol = &pod.Spec.Volumes[i]
		}
	}
	require.NotNil(t, inputVol)
	require.NotNil(t, inputVol.EmptyDir, "input volume must be an emptyDir")
}

// TestBuildAgentPod_OutputRouting_SkipsUnresolvedInput tolerates a misconfigured
// input (no matching producer) by silently dropping the staging — the pod
// still launches.
func TestBuildAgentPod_OutputRouting_SkipsUnresolvedInput(t *testing.T) {
	r := &AgentTeamReconciler{}
	team := minimalTeam("unresolved")
	team.Spec.Workspace = &claudev1alpha1.WorkspaceSpec{
		Output: &claudev1alpha1.WorkspaceOutputSpec{Size: "1Gi"},
	}
	inputs := []claudev1alpha1.InputSpec{
		{From: "ghost", Artifact: "nope.md", MountPath: "/stage"},
	}
	pod := r.buildAgentPod(team, "writer", "sonnet", "p", "auto-accept",
		false, corev1.ResourceRequirements{}, nil, nil, nil, inputs)
	assert.Empty(t, pod.Spec.InitContainers,
		"unresolved input must not produce a stale init container")
}
