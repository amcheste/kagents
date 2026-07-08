package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

func TestRenderConsolidatedBranch_Default(t *testing.T) {
	team := minimalTeam("ship")
	branch, err := renderConsolidatedBranch(team)
	require.NoError(t, err)
	assert.Equal(t, "teams/ship", branch)
}

func TestRenderConsolidatedBranch_CustomTemplate(t *testing.T) {
	team := minimalTeam("alpha")
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{
		ConsolidatedBranchTemplate: "claude/{{.Namespace}}/{{.TeamName}}",
	}
	branch, err := renderConsolidatedBranch(team)
	require.NoError(t, err)
	assert.Equal(t, "claude/default/alpha", branch)
}

func TestEnsurePushBranchJob_Creates(t *testing.T) {
	team := withRepo(minimalTeam("push-me"))
	r := newReconciler(team)
	team = fetch(t, r, "push-me")
	ctx := context.Background()

	require.NoError(t, r.ensurePushBranchJob(ctx, team, "teams/push-me"))

	var job batchv1.Job
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "push-me-push-branch", Namespace: "default"}, &job))
	require.Len(t, job.Spec.Template.Spec.Containers, 1)

	// Script should reference the consolidated branch and each teammate.
	// The merge itself uses a runtime variable (teammate-$TM), so check for
	// the teammate name appearing in the for-loop list rather than the
	// already-expanded branch name.
	script := job.Spec.Template.Spec.Containers[0].Command[2]
	assert.Contains(t, script, "teams/push-me", "script must target the consolidated branch")
	assert.Contains(t, script, "for TM in worker", "script must iterate over the team's teammates")
	assert.Contains(t, script, `"teammate-$TM"`, "script must merge the per-teammate branch by name")

	// Repo PVC mounted at /workspace.
	require.Len(t, job.Spec.Template.Spec.Volumes, 1)
	assert.Equal(t, "repo", job.Spec.Template.Spec.Volumes[0].Name)
}

func TestEnsurePushBranchJob_Idempotent(t *testing.T) {
	team := withRepo(minimalTeam("idem"))
	r := newReconciler(team)
	team = fetch(t, r, "idem")
	ctx := context.Background()

	require.NoError(t, r.ensurePushBranchJob(ctx, team, "teams/idem"))
	require.NoError(t, r.ensurePushBranchJob(ctx, team, "teams/idem"),
		"second call with the same branch must not error")
}

func TestEnsurePushBranchJob_UsesLifecycleCredentialsSecret(t *testing.T) {
	// Lifecycle.GitCredentialsSecret takes precedence over
	// Repository.CredentialsSecret — teams that want a write-scoped push token
	// separate from their read-scoped clone token rely on this.
	team := withRepo(minimalTeam("creds"))
	team.Spec.Repository.CredentialsSecret = "clone-cred"
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{GitCredentialsSecret: "push-cred"}

	r := newReconciler(team)
	team = fetch(t, r, "creds")
	ctx := context.Background()

	require.NoError(t, r.ensurePushBranchJob(ctx, team, "teams/creds"))

	var job batchv1.Job
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "creds-push-branch", Namespace: "default"}, &job))

	env := job.Spec.Template.Spec.Containers[0].Env
	require.Len(t, env, 1)
	assert.Equal(t, "GIT_TOKEN", env[0].Name)
	require.NotNil(t, env[0].ValueFrom)
	require.NotNil(t, env[0].ValueFrom.SecretKeyRef)
	assert.Equal(t, "push-cred", env[0].ValueFrom.SecretKeyRef.Name,
		"lifecycle.gitCredentialsSecret must take precedence over repo.credentialsSecret")
}

func TestEnsurePushBranchJob_FallsBackToRepoCredentials(t *testing.T) {
	team := withRepo(minimalTeam("fallback"))
	team.Spec.Repository.CredentialsSecret = "clone-cred"
	// No lifecycle override.
	r := newReconciler(team)
	team = fetch(t, r, "fallback")
	ctx := context.Background()

	require.NoError(t, r.ensurePushBranchJob(ctx, team, "teams/fallback"))

	var job batchv1.Job
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "fallback-push-branch", Namespace: "default"}, &job))

	env := job.Spec.Template.Spec.Containers[0].Env
	require.Len(t, env, 1)
	assert.Equal(t, "clone-cred", env[0].ValueFrom.SecretKeyRef.Name)
}

func TestEnsurePushBranchJob_NoCredsIsAllowed(t *testing.T) {
	// A team with a public HTTPS repo and no credential secrets configured
	// should still produce a runnable Job — the script's GIT_TOKEN check
	// degrades gracefully.
	team := withRepo(minimalTeam("public"))
	r := newReconciler(team)
	team = fetch(t, r, "public")
	ctx := context.Background()

	require.NoError(t, r.ensurePushBranchJob(ctx, team, "teams/public"))

	var job batchv1.Job
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "public-push-branch", Namespace: "default"}, &job))
	assert.Empty(t, job.Spec.Template.Spec.Containers[0].Env)
}

// --- runFinalization ---

func TestRunFinalization_NoRepoIsReadyImmediately(t *testing.T) {
	// Teams without a repository (e.g. Cowork teams) have nothing to
	// consolidate — finalization returns ready=true on the first pass.
	team := minimalTeam("no-repo")
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{OnComplete: "push-branch"}
	r := newReconciler(team)
	team = fetch(t, r, "no-repo")

	ready, err := r.runFinalization(context.Background(), team)
	require.NoError(t, err)
	assert.True(t, ready)
	assert.Empty(t, team.Status.ConsolidatedBranch)
}

func TestRunFinalization_OnCompleteNotifyIsReadyImmediately(t *testing.T) {
	team := withRepo(minimalTeam("notify"))
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{OnComplete: "notify"}
	r := newReconciler(team)
	team = fetch(t, r, "notify")

	ready, err := r.runFinalization(context.Background(), team)
	require.NoError(t, err)
	assert.True(t, ready, "notify mode does not need push-branch finalization")
}

func TestRunFinalization_PushBranchSubmitsJobAndWaits(t *testing.T) {
	team := withRepo(minimalTeam("wait-job"))
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{OnComplete: "push-branch"}
	r := newReconciler(team)
	team = fetch(t, r, "wait-job")
	ctx := context.Background()

	// First call: submits Job, returns ready=false because Job hasn't started.
	ready, err := r.runFinalization(ctx, team)
	require.NoError(t, err)
	assert.False(t, ready, "first pass must wait for the submitted Job")
	assert.Empty(t, team.Status.ConsolidatedBranch)

	var job batchv1.Job
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "wait-job-push-branch", Namespace: "default"}, &job))
}

func TestRunFinalization_PushBranchJobSucceededSetsConsolidatedBranch(t *testing.T) {
	// Simulate the Job completing by pre-creating it with Succeeded status
	// in the fake client — runFinalization should write
	// Status.ConsolidatedBranch and return ready=true.
	team := withRepo(minimalTeam("done-job"))
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{OnComplete: "push-branch"}

	succeededJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "done-job-push-branch", Namespace: "default"},
		Status:     batchv1.JobStatus{Succeeded: 1},
	}
	r := newReconciler(team, succeededJob)
	team = fetch(t, r, "done-job")

	ready, err := r.runFinalization(context.Background(), team)
	require.NoError(t, err)
	assert.True(t, ready)
	assert.Equal(t, "teams/done-job", team.Status.ConsolidatedBranch)
}

func TestRunFinalization_PushBranchJobFailedReturnsError(t *testing.T) {
	team := withRepo(minimalTeam("failed-job"))
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{OnComplete: "push-branch"}

	// Failed >= BackoffLimit (both default 3) → checkJobStatus reports failed.
	backoff := int32(3)
	failedJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "failed-job-push-branch", Namespace: "default"},
		Spec:       batchv1.JobSpec{BackoffLimit: &backoff},
		Status:     batchv1.JobStatus{Failed: 3},
	}
	r := newReconciler(team, failedJob)
	team = fetch(t, r, "failed-job")

	ready, err := r.runFinalization(context.Background(), team)
	require.Error(t, err)
	assert.False(t, ready)
	assert.Contains(t, err.Error(), "backoff")
}

func TestRunFinalization_ConsolidatedBranchAlreadySetSkipsJob(t *testing.T) {
	// If a prior reconcile already wrote Status.ConsolidatedBranch, a later
	// pass must not re-submit the Job. Prevents phantom Jobs if the
	// reconciler is re-entered.
	team := withRepo(minimalTeam("already-done"))
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{OnComplete: "push-branch"}

	r := newReconciler(team)
	team = fetch(t, r, "already-done")
	team.Status.ConsolidatedBranch = "teams/already-done"

	ready, err := r.runFinalization(context.Background(), team)
	require.NoError(t, err)
	assert.True(t, ready)

	// No Job was submitted.
	var job batchv1.Job
	err = r.Get(context.Background(), types.NamespacedName{Name: "already-done-push-branch", Namespace: "default"}, &job)
	assert.True(t, strings.Contains(err.Error(), "not found"),
		"push-branch Job must not be re-submitted once ConsolidatedBranch is set")
}

// --- create-pr integration: head branch selection ---

func TestPRBranches_PrefersConsolidatedBranchOverRepoBranch(t *testing.T) {
	team := minimalTeam("head")
	team.Spec.Repository = &claudev1alpha1.RepositorySpec{URL: "https://x/y", Branch: "feature"}
	team.Status.ConsolidatedBranch = "teams/head"

	head, _ := prBranches(team)
	assert.Equal(t, "teams/head", head,
		"when push-branch ran, create-pr must PR the consolidated branch, not the base work branch")
}

// --- reconcileRunning integration ---

func TestReconcileRunning_AllDone_WithPushBranch_StaysRunningUntilJobSucceeds(t *testing.T) {
	team := withRepo(minimalTeam("flow"))
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{OnComplete: "push-branch"}
	leadPod := succeededPod("flow-lead", "default", "flow")
	workerPod := succeededPod("flow-worker", "default", "flow")
	start := metav1.NewTime(time.Now().Add(-1 * time.Minute))

	r := newReconciler(team, leadPod, workerPod)
	team = fetch(t, r, "flow")
	team.Status.Phase = "Running"
	team.Status.StartedAt = &start
	ctx := context.Background()

	result, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)
	// Team must remain Running until the push-branch Job completes.
	assert.Equal(t, "Running", team.Status.Phase)
	// Requeue should be set so the controller polls the Job.
	assert.Greater(t, result.RequeueAfter, time.Duration(0))
}

func TestReconcileRunning_AllDone_WithPushBranch_JobSucceededTransitionsToCompleted(t *testing.T) {
	team := withRepo(minimalTeam("flow2"))
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{OnComplete: "push-branch"}
	leadPod := succeededPod("flow2-lead", "default", "flow2")
	workerPod := succeededPod("flow2-worker", "default", "flow2")
	succeededJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "flow2-push-branch", Namespace: "default"},
		Status:     batchv1.JobStatus{Succeeded: 1},
	}
	start := metav1.NewTime(time.Now().Add(-1 * time.Minute))

	r := newReconciler(team, leadPod, workerPod, succeededJob)
	team = fetch(t, r, "flow2")
	team.Status.Phase = "Running"
	team.Status.StartedAt = &start
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)
	assert.Equal(t, "Completed", team.Status.Phase)
	assert.Equal(t, "teams/flow2", team.Status.ConsolidatedBranch)
}

func TestReconcileRunning_AllDone_WithPushBranch_JobFailedMarksTeamFailed(t *testing.T) {
	team := withRepo(minimalTeam("flow3"))
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{OnComplete: "push-branch"}
	leadPod := succeededPod("flow3-lead", "default", "flow3")
	workerPod := succeededPod("flow3-worker", "default", "flow3")
	backoff := int32(3)
	failedJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "flow3-push-branch", Namespace: "default"},
		Spec:       batchv1.JobSpec{BackoffLimit: &backoff},
		Status:     batchv1.JobStatus{Failed: 3},
	}
	start := metav1.NewTime(time.Now().Add(-1 * time.Minute))

	r := newReconciler(team, leadPod, workerPod, failedJob)
	team = fetch(t, r, "flow3")
	team.Status.Phase = "Running"
	team.Status.StartedAt = &start
	ctx := context.Background()

	// The worker pods in Succeeded phase mean allPodsComplete → allDone → runFinalization.
	// Finalization fails due to the Job backoff, so the team goes Failed.
	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err, "reconcileRunning swallows finalization failures into the team's Phase")
	assert.Equal(t, "Failed", team.Status.Phase)
}
