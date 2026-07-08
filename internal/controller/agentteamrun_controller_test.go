package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// runReconciler builds an AgentTeamRunReconciler with the AgentTeam,
// AgentTeamTemplate, and AgentTeamRun status subresources registered so
// status updates work in the fake client.
func runReconciler(objs ...client.Object) *AgentTeamRunReconciler {
	s := testScheme()
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(
			&claudev1alpha1.AgentTeam{},
			&claudev1alpha1.AgentTeamTemplate{},
			&claudev1alpha1.AgentTeamRun{},
		).
		Build()
	return &AgentTeamRunReconciler{Client: c, Scheme: s}
}

func readyTemplate(name string, teammates ...claudev1alpha1.TeammateSpec) *claudev1alpha1.AgentTeamTemplate {
	tmpl := &claudev1alpha1.AgentTeamTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: claudev1alpha1.AgentTeamTemplateSpec{
			Description: "ready",
			Teammates:   teammates,
		},
		Status: claudev1alpha1.AgentTeamTemplateStatus{
			Ready: true,
			Conditions: []metav1.Condition{
				{Type: templateConditionReady, Status: metav1.ConditionTrue, Reason: "Valid"},
			},
		},
	}
	return tmpl
}

func minimalRun(name, templateName string) *claudev1alpha1.AgentTeamRun {
	return &claudev1alpha1.AgentTeamRun{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: claudev1alpha1.AgentTeamRunSpec{
			TemplateRef: claudev1alpha1.TemplateReference{Name: templateName},
			Auth:        claudev1alpha1.AuthSpec{APIKeySecret: "my-secret"},
			Lead:        claudev1alpha1.LeadSpec{Model: "opus", Prompt: "Lead the team"},
		},
	}
}

func fetchRun(t *testing.T, r *AgentTeamRunReconciler, name string) *claudev1alpha1.AgentTeamRun {
	t.Helper()
	var run claudev1alpha1.AgentTeamRun
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, &run))
	return &run
}

func reqRunFor(name string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: "default"}}
}

// --- mergeRunIntoTemplate ---

func TestMergeRunIntoTemplate_TeammatesComeFromTemplate(t *testing.T) {
	tmpl := readyTemplate("t",
		claudev1alpha1.TeammateSpec{Name: "alpha", Model: "sonnet", Prompt: "x"},
		claudev1alpha1.TeammateSpec{Name: "beta", Model: "opus", Prompt: "y"},
	)
	run := minimalRun("r", "t")

	spec := mergeRunIntoTemplate(run, tmpl)
	require.Len(t, spec.Teammates, 2)
	assert.Equal(t, "alpha", spec.Teammates[0].Name)
	assert.Equal(t, "beta", spec.Teammates[1].Name)
}

func TestMergeRunIntoTemplate_AuthAndLeadComeFromRun(t *testing.T) {
	tmpl := readyTemplate("t", claudev1alpha1.TeammateSpec{Name: "a", Prompt: "x"})
	run := minimalRun("r", "t")
	run.Spec.Auth = claudev1alpha1.AuthSpec{APIKeySecret: "different-secret"}
	run.Spec.Lead = claudev1alpha1.LeadSpec{Model: "haiku", Prompt: "Override prompt"}

	spec := mergeRunIntoTemplate(run, tmpl)
	assert.Equal(t, "different-secret", spec.Auth.APIKeySecret)
	assert.Equal(t, "haiku", spec.Lead.Model)
	assert.Equal(t, "Override prompt", spec.Lead.Prompt)
}

func TestMergeRunIntoTemplate_RepositoryAndWorkspaceFromRun(t *testing.T) {
	tmpl := readyTemplate("t", claudev1alpha1.TeammateSpec{Name: "a", Prompt: "x"})
	run := minimalRun("r", "t")
	run.Spec.Repository = &claudev1alpha1.RepositorySpec{
		URL: "https://github.com/example/repo", Branch: "main",
	}

	spec := mergeRunIntoTemplate(run, tmpl)
	require.NotNil(t, spec.Repository)
	assert.Equal(t, "https://github.com/example/repo", spec.Repository.URL)
}

func TestMergeRunIntoTemplate_LifecycleRunWinsAllOrNothing(t *testing.T) {
	// Run sets Lifecycle entirely → that wins; the template's Lifecycle is
	// dropped to avoid surprising "half from each side" merges.
	timeout := "1h"
	tmpl := readyTemplate("t", claudev1alpha1.TeammateSpec{Name: "a", Prompt: "x"})
	tmpl.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{
		Timeout:    "8h",
		OnComplete: "notify",
	}
	run := minimalRun("r", "t")
	run.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{Timeout: timeout}

	spec := mergeRunIntoTemplate(run, tmpl)
	require.NotNil(t, spec.Lifecycle)
	assert.Equal(t, "1h", spec.Lifecycle.Timeout, "Run.Lifecycle wins")
	assert.Equal(t, "", spec.Lifecycle.OnComplete, "Template.Lifecycle.OnComplete must NOT bleed through")
}

func TestMergeRunIntoTemplate_LifecycleFallsBackToTemplate(t *testing.T) {
	tmpl := readyTemplate("t", claudev1alpha1.TeammateSpec{Name: "a", Prompt: "x"})
	tmpl.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{Timeout: "8h", OnComplete: "create-pr"}
	run := minimalRun("r", "t")
	// Run.Lifecycle is nil → template's lifecycle is used as-is.

	spec := mergeRunIntoTemplate(run, tmpl)
	require.NotNil(t, spec.Lifecycle)
	assert.Equal(t, "8h", spec.Lifecycle.Timeout)
	assert.Equal(t, "create-pr", spec.Lifecycle.OnComplete)
}

func TestMergeRunIntoTemplate_CoordinationAndQualityGatesFromTemplate(t *testing.T) {
	tmpl := readyTemplate("t", claudev1alpha1.TeammateSpec{Name: "a", Prompt: "x"})
	tmpl.Spec.Coordination = &claudev1alpha1.CoordinationSpec{MailboxBackend: "shared-volume"}
	tmpl.Spec.QualityGates = &claudev1alpha1.QualityGateSpec{RequireTests: true}
	run := minimalRun("r", "t")

	spec := mergeRunIntoTemplate(run, tmpl)
	require.NotNil(t, spec.Coordination)
	assert.Equal(t, "shared-volume", spec.Coordination.MailboxBackend)
	require.NotNil(t, spec.QualityGates)
	assert.True(t, spec.QualityGates.RequireTests)
}

// --- mirrorTeamStatus ---

func TestMirrorTeamStatus_CopiesPhaseAndPreservesRunConditions(t *testing.T) {
	run := &claudev1alpha1.AgentTeamRun{
		Status: claudev1alpha1.AgentTeamStatus{
			Conditions: []metav1.Condition{{Type: runConditionTeamCreated, Status: metav1.ConditionTrue}},
		},
	}
	team := &claudev1alpha1.AgentTeam{
		Status: claudev1alpha1.AgentTeamStatus{
			Phase:         "Running",
			Ready:         "1/2",
			EstimatedCost: "0.42",
			Conditions: []metav1.Condition{
				// Note: child team conditions must NOT bleed into the Run.
				{Type: "Progressing", Status: metav1.ConditionTrue},
			},
		},
	}

	mirrorTeamStatus(run, team)

	assert.Equal(t, "Running", run.Status.Phase)
	assert.Equal(t, "1/2", run.Status.Ready)
	assert.Equal(t, "0.42", run.Status.EstimatedCost)

	// The Run's own TeamCreated condition is preserved; the child's
	// Progressing condition is NOT pulled in.
	require.Len(t, run.Status.Conditions, 1)
	assert.Equal(t, runConditionTeamCreated, run.Status.Conditions[0].Type)
}

// --- Reconcile ---

func TestReconcileRun_TemplateNotFoundFailsRunWithReason(t *testing.T) {
	run := minimalRun("orphan", "missing-template")
	r := runReconciler(run)

	_, err := r.Reconcile(context.Background(), reqRunFor("orphan"))
	require.NoError(t, err, "template-not-found is a config error, not a controller error")

	got := fetchRun(t, r, "orphan")
	assert.Equal(t, "Failed", got.Status.Phase)
	require.Len(t, got.Status.Conditions, 1)
	assert.Equal(t, "TemplateNotFound", got.Status.Conditions[0].Reason)
}

func TestReconcileRun_TemplateNotReadyRequeuesAndDoesNotCreateTeam(t *testing.T) {
	tmpl := readyTemplate("t", claudev1alpha1.TeammateSpec{Name: "a", Prompt: "x"})
	tmpl.Status.Ready = false // ← gate
	tmpl.Status.Conditions[0].Status = metav1.ConditionFalse
	run := minimalRun("waiting", "t")

	r := runReconciler(run, tmpl)

	res, err := r.Reconcile(context.Background(), reqRunFor("waiting"))
	require.NoError(t, err)
	assert.Equal(t, runRequeueWaitingForTemplate, res.RequeueAfter,
		"must requeue while waiting for the template to become Ready")

	// No child team created yet.
	var team claudev1alpha1.AgentTeam
	err = r.Get(context.Background(), types.NamespacedName{Name: "waiting", Namespace: "default"}, &team)
	assert.True(t, errors.IsNotFound(err), "must not create the team while template is not Ready")

	// TeamCreated condition is False with Reason=TemplateNotReady.
	got := fetchRun(t, r, "waiting")
	require.Len(t, got.Status.Conditions, 1)
	assert.Equal(t, "TemplateNotReady", got.Status.Conditions[0].Reason)
}

func TestReconcileRun_CreatesChildTeamWithMergedSpec(t *testing.T) {
	tmpl := readyTemplate("t",
		claudev1alpha1.TeammateSpec{Name: "alpha", Model: "sonnet", Prompt: "do alpha"},
	)
	tmpl.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{Timeout: "2h", OnComplete: "notify"}

	run := minimalRun("inst", "t")
	run.Spec.Repository = &claudev1alpha1.RepositorySpec{URL: "https://x/y", Branch: "main"}

	r := runReconciler(run, tmpl)

	_, err := r.Reconcile(context.Background(), reqRunFor("inst"))
	require.NoError(t, err)

	var team claudev1alpha1.AgentTeam
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "inst", Namespace: "default"}, &team))

	// Spec merged correctly.
	require.Len(t, team.Spec.Teammates, 1)
	assert.Equal(t, "alpha", team.Spec.Teammates[0].Name)
	assert.Equal(t, "my-secret", team.Spec.Auth.APIKeySecret)
	require.NotNil(t, team.Spec.Repository)
	assert.Equal(t, "https://x/y", team.Spec.Repository.URL)
	require.NotNil(t, team.Spec.Lifecycle)
	assert.Equal(t, "2h", team.Spec.Lifecycle.Timeout, "template Lifecycle used when Run doesn't override")

	// Owner reference back to the Run.
	require.Len(t, team.OwnerReferences, 1)
	assert.Equal(t, "inst", team.OwnerReferences[0].Name)
	assert.Equal(t, "AgentTeamRun", team.OwnerReferences[0].Kind)

	// Run.Status reflects TeamCreated.
	got := fetchRun(t, r, "inst")
	require.GreaterOrEqual(t, len(got.Status.Conditions), 1)
	var found bool
	for _, c := range got.Status.Conditions {
		if c.Type == runConditionTeamCreated {
			found = true
			assert.Equal(t, metav1.ConditionTrue, c.Status)
			assert.Equal(t, "TeamCreated", c.Reason)
		}
	}
	assert.True(t, found)
}

func TestReconcileRun_UpdatesChildTeamSpecOnRunChange(t *testing.T) {
	// First reconcile creates the team. A subsequent reconcile after the Run
	// changes its Lead must update the team's spec — proving spec is owner-
	// managed (we stomp it every reconcile rather than letting drift live).
	tmpl := readyTemplate("t", claudev1alpha1.TeammateSpec{Name: "a", Model: "sonnet", Prompt: "x"})
	run := minimalRun("upd", "t")

	r := runReconciler(run, tmpl)

	_, err := r.Reconcile(context.Background(), reqRunFor("upd"))
	require.NoError(t, err)

	// Mutate the Run's Lead and reconcile again.
	got := fetchRun(t, r, "upd")
	got.Spec.Lead.Model = "haiku"
	require.NoError(t, r.Update(context.Background(), got))

	_, err = r.Reconcile(context.Background(), reqRunFor("upd"))
	require.NoError(t, err)

	var team claudev1alpha1.AgentTeam
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "upd", Namespace: "default"}, &team))
	assert.Equal(t, "haiku", team.Spec.Lead.Model,
		"child team spec must follow the Run's spec on update")
}

func TestReconcileRun_MirrorsChildTeamPhase(t *testing.T) {
	// Pre-create a child team in Running phase so the mirror branch is
	// exercised. The Run picks up the team's status on the next reconcile.
	tmpl := readyTemplate("t", claudev1alpha1.TeammateSpec{Name: "a", Prompt: "x"})
	run := minimalRun("mirror", "t")
	team := &claudev1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{Name: "mirror", Namespace: "default"},
		Spec: claudev1alpha1.AgentTeamSpec{
			Auth: claudev1alpha1.AuthSpec{APIKeySecret: "my-secret"},
			Lead: claudev1alpha1.LeadSpec{Model: "opus", Prompt: "Lead the team"},
			Teammates: []claudev1alpha1.TeammateSpec{
				{Name: "a", Prompt: "x"},
			},
		},
		Status: claudev1alpha1.AgentTeamStatus{
			Phase:         "Running",
			Ready:         "2/2",
			EstimatedCost: "1.23",
		},
	}
	r := runReconciler(run, tmpl, team)

	_, err := r.Reconcile(context.Background(), reqRunFor("mirror"))
	require.NoError(t, err)

	got := fetchRun(t, r, "mirror")
	assert.Equal(t, "Running", got.Status.Phase)
	assert.Equal(t, "2/2", got.Status.Ready)
	assert.Equal(t, "1.23", got.Status.EstimatedCost)
}

func TestReconcileRun_NotFoundReturnsCleanly(t *testing.T) {
	r := runReconciler()
	_, err := r.Reconcile(context.Background(), reqRunFor("ghost"))
	require.NoError(t, err)
}

// --- setRunCondition ---

func TestSetRunCondition_AppendsAndUpdatesInPlace(t *testing.T) {
	run := &claudev1alpha1.AgentTeamRun{}
	setRunCondition(run, runConditionTeamCreated, metav1.ConditionFalse, "Pending", "x")
	require.Len(t, run.Status.Conditions, 1)

	setRunCondition(run, runConditionTeamCreated, metav1.ConditionTrue, "Created", "y")
	require.Len(t, run.Status.Conditions, 1, "second call must update in place")
	assert.Equal(t, metav1.ConditionTrue, run.Status.Conditions[0].Status)
	assert.Equal(t, "Created", run.Status.Conditions[0].Reason)
}
