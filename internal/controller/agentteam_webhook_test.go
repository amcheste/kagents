package controller

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// webhookCaptureServer returns an httptest server that pushes every received
// JSON body onto a channel. Used to assert that controller phase transitions
// fire the expected webhook events.
func webhookCaptureServer(t *testing.T) (url string, events <-chan map[string]interface{}) {
	t.Helper()
	ch := make(chan map[string]interface{}, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var msg map[string]interface{}
		_ = json.Unmarshal(body, &msg)
		ch <- msg
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, ch
}

func withWebhook(team *claudev1alpha1.AgentTeam, url string, events []string) *claudev1alpha1.AgentTeam {
	team.Spec.Observability = &claudev1alpha1.ObservabilitySpec{
		Webhook: &claudev1alpha1.WebhookSpec{URL: url, Events: events},
	}
	return team
}

// waitForEvent blocks up to 2s for a webhook POST to arrive. The notifier
// POSTs asynchronously, so tests must await delivery rather than inspect
// synchronously after the reconcile returns.
func waitForEvent(t *testing.T, ch <-chan map[string]interface{}) map[string]interface{} {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook delivery")
		return nil
	}
}

func TestReconcileInitializing_FiresTeamStartedWebhook(t *testing.T) {
	url, events := webhookCaptureServer(t)

	team := withWebhook(withRepo(minimalTeam("wh-start")), url, []string{"team.started"})
	job := completedJob("wh-start-init", "default")
	r := newReconciler(team, job)
	team = fetch(t, r, "wh-start")
	team.Spec.Observability = &claudev1alpha1.ObservabilitySpec{
		Webhook: &claudev1alpha1.WebhookSpec{URL: url, Events: []string{"team.started"}},
	}
	require.NoError(t, r.Update(context.Background(), team))
	team = fetch(t, r, "wh-start")
	ctx := context.Background()

	_, err := r.reconcileInitializing(ctx, team)
	require.NoError(t, err)
	assert.Equal(t, "Running", team.Status.Phase)

	msg := waitForEvent(t, events)
	assert.Equal(t, "team.started", msg["event"])
	assert.Equal(t, "wh-start", msg["team"])
	assert.Equal(t, "default", msg["namespace"])
	assert.NotEmpty(t, msg["timestamp"])
	data, ok := msg["data"].(map[string]interface{})
	require.True(t, ok, "data subobject expected")
	assert.Equal(t, "opus", data["leadModel"])
}

func TestReconcileRunning_FiresBudgetWarningAt80Percent(t *testing.T) {
	url, events := webhookCaptureServer(t)

	// Under the budget package heuristic, minimalTeam (opus lead + sonnet
	// worker) runs at $0.60/min. At 15m elapsed that's $9.00 — 90% of a $10
	// budget, safely in the [80%, 100%) window so the warning fires without
	// the exceeded branch pre-empting the reconcile.
	team := withWebhook(withLifecycle(minimalTeam("wh-budget"), "24h", "10.00"), url, []string{"budget.warning"})
	team.Status.Phase = "Running"
	start := metav1.NewTime(time.Now().Add(-15 * time.Minute))
	team.Status.StartedAt = &start

	r := newReconciler(team)
	team = fetch(t, r, "wh-budget")
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)

	msg := waitForEvent(t, events)
	assert.Equal(t, "budget.warning", msg["event"])
	assert.Equal(t, "wh-budget", msg["team"])
	data, ok := msg["data"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "80%", data["threshold"])
	assert.Equal(t, "10.00", data["budgetLimit"])
}

func TestReconcileRunning_BudgetWarningFiresOnlyOnce(t *testing.T) {
	// Subsequent reconciles must not re-fire the warning; the condition
	// dedup must hold across calls.
	url, events := webhookCaptureServer(t)

	team := withWebhook(withLifecycle(minimalTeam("wh-budget-dedup"), "24h", "10.00"), url, []string{"budget.warning"})
	team.Status.Phase = "Running"
	start := metav1.NewTime(time.Now().Add(-15 * time.Minute))
	team.Status.StartedAt = &start

	r := newReconciler(team)
	team = fetch(t, r, "wh-budget-dedup")
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)
	_ = waitForEvent(t, events) // first fire

	// Re-fetch and run again — condition should block the second fire.
	team = fetch(t, r, "wh-budget-dedup")
	_, err = r.reconcileRunning(ctx, team)
	require.NoError(t, err)

	select {
	case msg := <-events:
		t.Fatalf("budget.warning fired twice: %v", msg)
	case <-time.After(300 * time.Millisecond):
		// Expected — no second delivery.
	}
}

func TestReconcileRunning_FiresTeammateErrorOnRespawn(t *testing.T) {
	url, events := webhookCaptureServer(t)

	team := withWebhook(minimalTeam("wh-respawn"), url, []string{"teammate.error"})
	team.Status.Phase = "Running"

	// Teammate pod in Failed phase, under the default restart limit — the
	// reconciler should delete, re-spawn, and fire teammate.error with
	// restart metadata.
	leadPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "wh-respawn-lead",
			Namespace: "default",
			Labels:    map[string]string{"kagents.dev/team": "wh-respawn"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	workerPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "wh-respawn-worker",
			Namespace: "default",
			Labels:    map[string]string{"kagents.dev/team": "wh-respawn"},
		},
		Status: corev1.PodStatus{
			Phase:   corev1.PodFailed,
			Reason:  "Error",
			Message: "agent crashed",
		},
	}

	r := newReconciler(team, leadPod, workerPod)
	team = fetch(t, r, "wh-respawn")
	team.Status.Phase = "Running"
	startTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))
	team.Status.StartedAt = &startTime
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)
	assert.Equal(t, "Running", team.Status.Phase, "team must stay Running while under restart limit")

	msg := waitForEvent(t, events)
	assert.Equal(t, "teammate.error", msg["event"])
	data, ok := msg["data"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "worker", data["teammate"])
	assert.Equal(t, "wh-respawn-worker", data["pod"])
	assert.Equal(t, "Error", data["reason"])
	assert.Equal(t, "agent crashed", data["message"])
	assert.Equal(t, "respawn", data["action"])
	// JSON unmarshals numbers as float64.
	assert.Equal(t, float64(1), data["restartCount"])
	assert.Equal(t, float64(3), data["maxRestarts"])
}

func TestReconcileRunning_FiresTeammateErrorOnRestartLimitExceeded(t *testing.T) {
	url, events := webhookCaptureServer(t)

	team := withWebhook(minimalTeam("wh-exceed"), url, []string{"teammate.error"})
	team.Status.Phase = "Running"

	leadPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "wh-exceed-lead",
			Namespace: "default",
			Labels:    map[string]string{"kagents.dev/team": "wh-exceed"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
	}
	workerPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "wh-exceed-worker",
			Namespace: "default",
			Labels:    map[string]string{"kagents.dev/team": "wh-exceed"},
		},
		Status: corev1.PodStatus{
			Phase:   corev1.PodFailed,
			Reason:  "Error",
			Message: "agent crashed",
		},
	}

	r := newReconciler(team, leadPod, workerPod)
	team = fetch(t, r, "wh-exceed")
	team.Status.Phase = "Running"
	startTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))
	team.Status.StartedAt = &startTime
	// Exhaust restarts so the next failure fails the team.
	team.Status.Teammates = []claudev1alpha1.TeammateStatus{
		{Name: "worker", RestartCount: 3},
	}
	ctx := context.Background()

	_, err := r.reconcileRunning(ctx, team)
	require.NoError(t, err)
	assert.Equal(t, "Failed", team.Status.Phase)

	msg := waitForEvent(t, events)
	assert.Equal(t, "teammate.error", msg["event"])
	data, ok := msg["data"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "worker", data["teammate"])
	// Final failure event from fireTeammateErrorEvents does not include the
	// "respawn" action marker.
	_, hasAction := data["action"]
	assert.False(t, hasAction, "final failure event must not carry respawn metadata")
}

func TestWebhookEvents_OnlySubscribedEventsFire(t *testing.T) {
	// Team subscribed to ONLY team.completed — starting should not deliver
	// anything.
	url, events := webhookCaptureServer(t)

	team := withWebhook(withRepo(minimalTeam("wh-filter")), url, []string{"team.completed"})
	job := completedJob("wh-filter-init", "default")
	r := newReconciler(team, job)
	team = fetch(t, r, "wh-filter")
	team.Spec.Observability = &claudev1alpha1.ObservabilitySpec{
		Webhook: &claudev1alpha1.WebhookSpec{URL: url, Events: []string{"team.completed"}},
	}
	require.NoError(t, r.Update(context.Background(), team))
	team = fetch(t, r, "wh-filter")
	ctx := context.Background()

	_, err := r.reconcileInitializing(ctx, team)
	require.NoError(t, err)

	select {
	case msg := <-events:
		t.Fatalf("unsubscribed team.started fired: %v", msg)
	case <-time.After(300 * time.Millisecond):
		// Expected.
	}
}
