package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
	"github.com/amcheste/kagents/internal/delivery"
)

// deliverySchemeForTests is a local scheme so this file's tests stay
// independent of helpers in the larger test suite.
func deliverySchemeForTests(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, claudev1alpha1.AddToScheme(s))
	return s
}

// TestExecuteDelivery_RecordsStatusPerTarget exercises the full
// reconciler-side delivery path with a stub dispatcher. The point is
// to prove the operator records exactly one DeliveryStatus per target,
// captures the per-type label, and surfaces success/error correctly —
// without coupling the test to the real HTTP senders (those are
// covered in the delivery package's own tests).
func TestExecuteDelivery_RecordsStatusPerTarget(t *testing.T) {
	t.Parallel()
	scheme := deliverySchemeForTests(t)
	team := &claudev1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "ns"},
		Spec: claudev1alpha1.AgentTeamSpec{
			Lifecycle: &claudev1alpha1.LifecycleSpec{
				OnComplete: "deliver",
				Delivery: []claudev1alpha1.DeliveryTarget{
					{Type: "webhook", URL: "https://ops.example.com/hook"},
					{Type: "slack", Channel: "#reports", CredentialsSecret: "creds"},
				},
			},
		},
		Status: claudev1alpha1.AgentTeamStatus{Phase: "Completed"},
	}

	d := delivery.NewDispatcher()
	d.SetSender("webhook", delivery.SenderFunc(func(_ context.Context, _ client.Client, _ claudev1alpha1.DeliveryTarget, _ *claudev1alpha1.AgentTeam) error {
		return nil
	}))
	d.SetSender("slack", delivery.SenderFunc(func(_ context.Context, _ client.Client, _ claudev1alpha1.DeliveryTarget, _ *claudev1alpha1.AgentTeam) error {
		return errors.New("boom")
	}))

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(team).Build()
	r := &AgentTeamReconciler{Client: c, Scheme: scheme, Delivery: d}

	r.executeDelivery(context.Background(), team)

	require.Len(t, team.Status.Delivery, 2)
	assert.Equal(t, "webhook", team.Status.Delivery[0].Type)
	assert.Equal(t, "https://ops.example.com/hook", team.Status.Delivery[0].Target)
	assert.True(t, team.Status.Delivery[0].Success)
	assert.Empty(t, team.Status.Delivery[0].Error)
	assert.False(t, team.Status.Delivery[0].DeliveredAt.IsZero())

	assert.Equal(t, "slack", team.Status.Delivery[1].Type)
	assert.Equal(t, "#reports", team.Status.Delivery[1].Target)
	assert.False(t, team.Status.Delivery[1].Success)
	assert.Contains(t, team.Status.Delivery[1].Error, "boom")
}

// TestExecuteDelivery_NoOpOnEmpty verifies the function returns
// cleanly when no targets are configured — the OnComplete=deliver enum
// is allowed without targets (e.g. user-typed CR with empty array)
// and we don't want a stray panic or status mutation in that case.
func TestExecuteDelivery_NoOpOnEmpty(t *testing.T) {
	t.Parallel()
	scheme := deliverySchemeForTests(t)
	team := &claudev1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "ns"},
		Spec: claudev1alpha1.AgentTeamSpec{
			Lifecycle: &claudev1alpha1.LifecycleSpec{OnComplete: "deliver"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(team).Build()
	r := &AgentTeamReconciler{Client: c, Scheme: scheme}
	r.executeDelivery(context.Background(), team)
	assert.Empty(t, team.Status.Delivery)
}

// TestExecuteDelivery_NilLifecycle defends against a programmer error
// — the OnComplete branch in executeOnComplete is unreachable if
// Lifecycle is nil, but we still want belt-and-suspenders here so a
// future refactor that exposes executeDelivery from elsewhere doesn't
// silently crash.
func TestExecuteDelivery_NilLifecycle(t *testing.T) {
	t.Parallel()
	scheme := deliverySchemeForTests(t)
	team := &claudev1alpha1.AgentTeam{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "ns"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(team).Build()
	r := &AgentTeamReconciler{Client: c, Scheme: scheme}
	r.executeDelivery(context.Background(), team)
	assert.Empty(t, team.Status.Delivery)
}

// TestExecuteOnComplete_DispatchesDeliver wires the higher-level entry
// point — executeOnComplete with OnComplete="deliver" — to confirm the
// switch case routes to executeDelivery. This is the only test that
// crosses the case boundary; everything else exercises executeDelivery
// directly so failure messages stay focused.
func TestExecuteOnComplete_DispatchesDeliver(t *testing.T) {
	t.Parallel()
	scheme := deliverySchemeForTests(t)
	team := &claudev1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "ns"},
		Spec: claudev1alpha1.AgentTeamSpec{
			Lifecycle: &claudev1alpha1.LifecycleSpec{
				OnComplete: "deliver",
				Delivery: []claudev1alpha1.DeliveryTarget{
					{Type: "webhook", URL: "https://x"},
				},
			},
		},
	}
	d := delivery.NewDispatcher()
	called := false
	d.SetSender("webhook", delivery.SenderFunc(func(_ context.Context, _ client.Client, _ claudev1alpha1.DeliveryTarget, _ *claudev1alpha1.AgentTeam) error {
		called = true
		return nil
	}))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(team).Build()
	r := &AgentTeamReconciler{Client: c, Scheme: scheme, Delivery: d}

	require.NoError(t, r.executeOnComplete(context.Background(), team))
	assert.True(t, called, "executeOnComplete should route OnComplete=deliver to executeDelivery")
	require.Len(t, team.Status.Delivery, 1)
}
