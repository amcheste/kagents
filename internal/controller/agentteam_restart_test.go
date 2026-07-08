package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

func TestMaxRestarts_DefaultIs3WhenUnset(t *testing.T) {
	team := minimalTeam("r")
	assert.Equal(t, int32(3), maxRestarts(team))
}

func TestMaxRestarts_HonorsSpec(t *testing.T) {
	custom := int32(7)
	team := minimalTeam("r")
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{MaxRestarts: &custom}
	assert.Equal(t, int32(7), maxRestarts(team))
}

func TestMaxRestarts_ZeroDisablesRespawn(t *testing.T) {
	// A zero limit means: no restarts allowed. The first failure fails the team.
	zero := int32(0)
	team := minimalTeam("no-restart")
	team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{MaxRestarts: &zero}
	leadPod := runningPod("no-restart-lead", "default", "no-restart")
	workerPod := failedPod("no-restart-worker", "default", "no-restart")
	startTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))

	r := newReconciler(team, leadPod, workerPod)
	team = fetch(t, r, "no-restart")
	team.Status.Phase = "Running"
	team.Status.StartedAt = &startTime
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)
	assert.Equal(t, "Failed", team.Status.Phase, "zero-restart policy must fail the team on first pod failure")
}

func TestHandleTeammateFailures_LeadFailureStillFailsTeam(t *testing.T) {
	// The lead pod is not subject to the restart limit — a lead crash still
	// drops the team into Failed via the existing allPodsComplete path.
	team := minimalTeam("lead-dies")
	leadPod := failedPod("lead-dies-lead", "default", "lead-dies")
	workerPod := runningPod("lead-dies-worker", "default", "lead-dies")
	startTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))

	r := newReconciler(team, leadPod, workerPod)
	team = fetch(t, r, "lead-dies")
	team.Status.Phase = "Running"
	team.Status.StartedAt = &startTime
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)
	assert.Equal(t, "Failed", team.Status.Phase)
}

func TestTeammateRestartCount_ZeroWhenStatusMissing(t *testing.T) {
	team := minimalTeam("r")
	assert.Equal(t, int32(0), teammateRestartCount(team, "worker"))
}

func TestTeammateRestartCount_ReturnsPersistedValue(t *testing.T) {
	team := minimalTeam("r")
	team.Status.Teammates = []claudev1alpha1.TeammateStatus{
		{Name: "worker", RestartCount: 5},
	}
	assert.Equal(t, int32(5), teammateRestartCount(team, "worker"))
}

func TestSetTeammateRestartCount_UpdatesInPlace(t *testing.T) {
	team := minimalTeam("r")
	team.Status.Teammates = []claudev1alpha1.TeammateStatus{
		{Name: "worker", RestartCount: 0},
	}
	setTeammateRestartCount(team, "worker", 2)
	assert.Equal(t, int32(2), team.Status.Teammates[0].RestartCount)
}

func TestSetTeammateRestartCount_NoopWhenNameMissing(t *testing.T) {
	// Caller must not panic or allocate a new entry if the teammate has no
	// status slot yet — syncPodStatuses is responsible for creating entries.
	team := minimalTeam("r")
	team.Status.Teammates = []claudev1alpha1.TeammateStatus{
		{Name: "other", RestartCount: 0},
	}
	setTeammateRestartCount(team, "missing", 9)
	assert.Equal(t, int32(0), team.Status.Teammates[0].RestartCount)
	assert.Len(t, team.Status.Teammates, 1, "missing teammate must not be appended")
}

func TestReconcileRunning_FailedPodDeletedDuringRespawn(t *testing.T) {
	// Prove the old Failed pod is actually deleted (not just orphaned)
	// before the re-spawn creates a new one with the same name.
	team := minimalTeam("delete-check")
	leadPod := runningPod("delete-check-lead", "default", "delete-check")
	workerPod := failedPod("delete-check-worker", "default", "delete-check")
	workerPod.ResourceVersion = "99" // track original object identity
	startTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))

	r := newReconciler(team, leadPod, workerPod)
	team = fetch(t, r, "delete-check")
	team.Status.Phase = "Running"
	team.Status.StartedAt = &startTime
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)

	var respawnedPod corev1.Pod
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "delete-check-worker", Namespace: "default"}, &respawnedPod))
	// A fresh object would have a different resourceVersion than the original "99".
	assert.NotEqual(t, "99", respawnedPod.ResourceVersion, "failed pod must be deleted and replaced with a fresh object")
	assert.NotEqual(t, corev1.PodFailed, respawnedPod.Status.Phase)
}
