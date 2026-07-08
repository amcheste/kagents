package controller

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// templateReconciler builds an AgentTeamTemplateReconciler against a fake
// client seeded with the given objects, using the same test scheme as the
// AgentTeam tests.
func templateReconciler(objs ...client.Object) *AgentTeamTemplateReconciler {
	s := testScheme()
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&claudev1alpha1.AgentTeamTemplate{}).
		Build()
	return &AgentTeamTemplateReconciler{Client: c, Scheme: s}
}

func minimalTemplate(name string, teammates ...claudev1alpha1.TeammateSpec) *claudev1alpha1.AgentTeamTemplate {
	return &claudev1alpha1.AgentTeamTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: claudev1alpha1.AgentTeamTemplateSpec{
			Description: "test template",
			Teammates:   teammates,
		},
	}
}

func fetchTemplate(t *testing.T, r *AgentTeamTemplateReconciler, name string) *claudev1alpha1.AgentTeamTemplate {
	t.Helper()
	var tmpl claudev1alpha1.AgentTeamTemplate
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, &tmpl))
	return &tmpl
}

// --- validateTemplate (pure function) ---

func TestValidateTemplate_Empty(t *testing.T) {
	// CRD-level validation enforces MinItems=1 already, but the function
	// should still handle a zero-teammate spec without panicking.
	err := validateTemplate(&claudev1alpha1.AgentTeamTemplateSpec{})
	assert.NoError(t, err)
}

func TestValidateTemplate_DuplicateName(t *testing.T) {
	err := validateTemplate(&claudev1alpha1.AgentTeamTemplateSpec{
		Teammates: []claudev1alpha1.TeammateSpec{
			{Name: "alpha", Model: "sonnet", Prompt: "x"},
			{Name: "alpha", Model: "sonnet", Prompt: "y"},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate teammate name")
}

func TestValidateTemplate_InvalidModel(t *testing.T) {
	err := validateTemplate(&claudev1alpha1.AgentTeamTemplateSpec{
		Teammates: []claudev1alpha1.TeammateSpec{
			{Name: "alpha", Model: "gpt-4", Prompt: "x"},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid model")
}

func TestValidateTemplate_EmptyModelIsAllowed(t *testing.T) {
	// Empty model is valid — the AgentTeam type defaults it to "sonnet" via
	// kubebuilder. The template controller should not punish unset values.
	err := validateTemplate(&claudev1alpha1.AgentTeamTemplateSpec{
		Teammates: []claudev1alpha1.TeammateSpec{{Name: "alpha", Prompt: "x"}},
	})
	assert.NoError(t, err)
}

func TestValidateTemplate_DependsOnSelf(t *testing.T) {
	err := validateTemplate(&claudev1alpha1.AgentTeamTemplateSpec{
		Teammates: []claudev1alpha1.TeammateSpec{
			{Name: "alpha", Model: "sonnet", Prompt: "x", DependsOn: []string{"alpha"}},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot depend on itself")
}

func TestValidateTemplate_DependsOnUnknown(t *testing.T) {
	err := validateTemplate(&claudev1alpha1.AgentTeamTemplateSpec{
		Teammates: []claudev1alpha1.TeammateSpec{
			{Name: "alpha", Model: "sonnet", Prompt: "x", DependsOn: []string{"ghost"}},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown teammate")
}

func TestValidateTemplate_ChainedDependencies(t *testing.T) {
	// Linear A → B → C dependency chain is fine. Cycles are not detected at
	// this layer (kubebuilder ordering naturally handles them via spawn-time
	// dependsOn checks); validation here only catches references that
	// definitionally cannot resolve.
	err := validateTemplate(&claudev1alpha1.AgentTeamTemplateSpec{
		Teammates: []claudev1alpha1.TeammateSpec{
			{Name: "a", Model: "opus", Prompt: "x"},
			{Name: "b", Model: "sonnet", Prompt: "x", DependsOn: []string{"a"}},
			{Name: "c", Model: "sonnet", Prompt: "x", DependsOn: []string{"a", "b"}},
		},
	})
	assert.NoError(t, err)
}

func TestValidateTemplate_AllValidModels(t *testing.T) {
	for _, m := range []string{"opus", "sonnet", "haiku"} {
		err := validateTemplate(&claudev1alpha1.AgentTeamTemplateSpec{
			Teammates: []claudev1alpha1.TeammateSpec{{Name: "a", Model: m, Prompt: "x"}},
		})
		assert.NoError(t, err, "model=%s should be accepted", m)
	}
}

// --- setTemplateReady ---

func TestSetTemplateReady_AppendsThenUpdatesInPlace(t *testing.T) {
	tmpl := &claudev1alpha1.AgentTeamTemplate{}

	setTemplateReady(tmpl, true, "Valid", "ok")
	require.Len(t, tmpl.Status.Conditions, 1)
	assert.True(t, tmpl.Status.Ready)
	assert.Equal(t, metav1.ConditionTrue, tmpl.Status.Conditions[0].Status)

	// A second call updates the existing entry rather than appending.
	setTemplateReady(tmpl, false, "Bad", "broken")
	require.Len(t, tmpl.Status.Conditions, 1)
	assert.False(t, tmpl.Status.Ready)
	assert.Equal(t, metav1.ConditionFalse, tmpl.Status.Conditions[0].Status)
	assert.Equal(t, "Bad", tmpl.Status.Conditions[0].Reason)
	assert.Equal(t, "broken", tmpl.Status.Conditions[0].Message)
}

func TestSetTemplateReady_UpdatesTransitionTimeOnStatusChange(t *testing.T) {
	tmpl := &claudev1alpha1.AgentTeamTemplate{}
	setTemplateReady(tmpl, true, "Valid", "ok")
	first := tmpl.Status.Conditions[0].LastTransitionTime

	// Same status — LastTransitionTime stays put.
	setTemplateReady(tmpl, true, "Valid", "ok again")
	assert.Equal(t, first, tmpl.Status.Conditions[0].LastTransitionTime,
		"LastTransitionTime must NOT change when status stays the same")

	// Different status — LastTransitionTime updates.
	setTemplateReady(tmpl, false, "Bad", "broken")
	assert.NotEqual(t, first, tmpl.Status.Conditions[0].LastTransitionTime,
		"LastTransitionTime must change when status flips")
}

func TestSetTemplateReady_TrimsLongMessage(t *testing.T) {
	tmpl := &claudev1alpha1.AgentTeamTemplate{}
	long := strings.Repeat("x", 500)
	setTemplateReady(tmpl, false, "Bad", long)
	// "…" is 3 bytes in UTF-8, so the upper bound is 256 + len("…") = 259.
	// We just need to prove it's significantly shorter than the input.
	assert.LessOrEqual(t, len(tmpl.Status.Conditions[0].Message), 259,
		"message must be trimmed (256 chars + ellipsis) to keep kubectl describe readable")
	assert.Less(t, len(tmpl.Status.Conditions[0].Message), 500,
		"trimmed message must be smaller than the original")
}

// --- Reconcile ---

func TestReconcile_ValidTemplateSetsReady(t *testing.T) {
	tmpl := minimalTemplate("good",
		claudev1alpha1.TeammateSpec{Name: "a", Model: "sonnet", Prompt: "p"},
	)
	r := templateReconciler(tmpl)

	_, err := r.Reconcile(context.Background(), reqFor("good"))
	require.NoError(t, err)

	got := fetchTemplate(t, r, "good")
	assert.True(t, got.Status.Ready)
	require.Len(t, got.Status.Conditions, 1)
	assert.Equal(t, "Valid", got.Status.Conditions[0].Reason)
	assert.Equal(t, metav1.ConditionTrue, got.Status.Conditions[0].Status)
}

func TestReconcile_InvalidTemplateSetsReadyFalseWithReason(t *testing.T) {
	tmpl := minimalTemplate("bad",
		claudev1alpha1.TeammateSpec{Name: "a", Model: "gpt-4", Prompt: "p"},
	)
	r := templateReconciler(tmpl)

	_, err := r.Reconcile(context.Background(), reqFor("bad"))
	require.NoError(t, err, "validation failure must not return a reconcile error — that would cause requeue storms")

	got := fetchTemplate(t, r, "bad")
	assert.False(t, got.Status.Ready)
	require.Len(t, got.Status.Conditions, 1)
	assert.Equal(t, "ValidationFailed", got.Status.Conditions[0].Reason)
	assert.Contains(t, got.Status.Conditions[0].Message, "invalid model")
}

func TestReconcile_TemplateNotFoundReturnsCleanly(t *testing.T) {
	// Deleted template mid-reconcile must not error — the framework expects
	// Reconcile to no-op and return when the object is gone.
	r := templateReconciler()
	_, err := r.Reconcile(context.Background(), reqFor("missing"))
	require.NoError(t, err)
}

func TestReconcile_FlipsFromInvalidToValidOnUpdate(t *testing.T) {
	// First reconcile sees bad model, second sees valid one — Ready flips
	// from false to true. (LastTransitionTime advancement is verified in
	// TestSetTemplateReady_UpdatesTransitionTimeOnStatusChange which uses
	// the helper directly; here metav1.Now's second-level resolution makes
	// timestamp comparison flaky on fast machines.)
	tmpl := minimalTemplate("flip",
		claudev1alpha1.TeammateSpec{Name: "a", Model: "gpt-4", Prompt: "p"},
	)
	r := templateReconciler(tmpl)

	_, err := r.Reconcile(context.Background(), reqFor("flip"))
	require.NoError(t, err)
	got := fetchTemplate(t, r, "flip")
	require.False(t, got.Status.Ready)
	require.Equal(t, metav1.ConditionFalse, got.Status.Conditions[0].Status)

	// Update spec to a valid model and reconcile again.
	got.Spec.Teammates[0].Model = "sonnet"
	require.NoError(t, r.Update(context.Background(), got))

	_, err = r.Reconcile(context.Background(), reqFor("flip"))
	require.NoError(t, err)
	got = fetchTemplate(t, r, "flip")
	assert.True(t, got.Status.Ready)
	assert.Equal(t, metav1.ConditionTrue, got.Status.Conditions[0].Status)
	assert.Equal(t, "Valid", got.Status.Conditions[0].Reason)
}

// reqFor builds a controller-runtime reconcile request for a template in the
// default test namespace.
func reqFor(name string) ctrl.Request {
	return ctrl.Request{
		NamespacedName: types.NamespacedName{Name: name, Namespace: "default"},
	}
}
