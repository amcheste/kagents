//go:build integration

package controller_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// The full chain: a Template lands → its reconciler marks it Ready → an
// AgentTeamRun pointing at that template causes the run reconciler to
// create a child AgentTeam owned by the run, and the team's status flows
// back into the run.
var _ = Describe("AgentTeamRun → AgentTeam chain", func() {
	It("creates a child AgentTeam owned by the Run after the Template is Ready", func() {
		ns := testNS()

		tmpl := &claudev1alpha1.AgentTeamTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "ci-template", Namespace: ns},
			Spec: claudev1alpha1.AgentTeamTemplateSpec{
				Description: "ci",
				Teammates: []claudev1alpha1.TeammateSpec{
					{Name: "alpha", Model: "sonnet", Prompt: "do alpha"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, tmpl)).To(Succeed())

		// Wait for the template controller to mark it Ready before the Run
		// reconciler will instantiate it.
		Eventually(func() bool {
			var got claudev1alpha1.AgentTeamTemplate
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "ci-template", Namespace: ns}, &got); err != nil {
				return false
			}
			return got.Status.Ready
		}).Should(BeTrue(), "template should reach Ready=true")

		run := &claudev1alpha1.AgentTeamRun{
			ObjectMeta: metav1.ObjectMeta{Name: "first-run", Namespace: ns},
			Spec: claudev1alpha1.AgentTeamRunSpec{
				TemplateRef: claudev1alpha1.TemplateReference{Name: "ci-template"},
				Auth:        claudev1alpha1.AuthSpec{APIKeySecret: "my-secret"},
				Lead:        claudev1alpha1.LeadSpec{Model: "opus", Prompt: "lead"},
			},
		}
		Expect(k8sClient.Create(ctx, run)).To(Succeed())

		// The run controller should produce a child AgentTeam with the
		// merged spec and an owner reference back to the Run.
		Eventually(func() bool {
			var team claudev1alpha1.AgentTeam
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "first-run", Namespace: ns}, &team); err != nil {
				return false
			}
			if len(team.OwnerReferences) != 1 {
				return false
			}
			if team.OwnerReferences[0].Name != "first-run" {
				return false
			}
			if len(team.Spec.Teammates) != 1 || team.Spec.Teammates[0].Name != "alpha" {
				return false
			}
			return team.Spec.Auth.APIKeySecret == "my-secret"
		}).Should(BeTrue(), "child AgentTeam should be created with merged spec and owner ref")

		// And the Run's TeamCreated condition should flip True.
		Eventually(func() bool {
			var got claudev1alpha1.AgentTeamRun
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "first-run", Namespace: ns}, &got); err != nil {
				return false
			}
			for _, c := range got.Status.Conditions {
				if c.Type == "TeamCreated" && c.Status == metav1.ConditionTrue {
					return true
				}
			}
			return false
		}).Should(BeTrue(), "Run.Status.Conditions[TeamCreated] should be True")
	})

	It("refuses to instantiate a Run when the Template is not Ready", func() {
		ns := testNS()

		// Build a template the validator will reject. We use a dependsOn
		// reference to an unknown teammate — that passes CRD-level
		// validation (kubebuilder doesn't cross-reference fields) but
		// fails our reconciler's validateTemplate, marking Ready=false.
		// (An invalid model would be rejected at admission by the
		// kubebuilder enum, never reaching the reconciler.)
		tmpl := &claudev1alpha1.AgentTeamTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "broken", Namespace: ns},
			Spec: claudev1alpha1.AgentTeamTemplateSpec{
				Description: "broken",
				Teammates: []claudev1alpha1.TeammateSpec{
					{Name: "a", Model: "sonnet", Prompt: "x", DependsOn: []string{"ghost"}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, tmpl)).To(Succeed())

		// Confirm the template is Ready=false before continuing — otherwise
		// a flaky observation could let the Run see Ready=true momentarily.
		Eventually(func() string {
			var got claudev1alpha1.AgentTeamTemplate
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "broken", Namespace: ns}, &got); err != nil {
				return ""
			}
			for _, c := range got.Status.Conditions {
				if c.Type == "Ready" {
					return c.Reason
				}
			}
			return ""
		}).Should(Equal("ValidationFailed"))

		run := &claudev1alpha1.AgentTeamRun{
			ObjectMeta: metav1.ObjectMeta{Name: "should-not-instantiate", Namespace: ns},
			Spec: claudev1alpha1.AgentTeamRunSpec{
				TemplateRef: claudev1alpha1.TemplateReference{Name: "broken"},
				Auth:        claudev1alpha1.AuthSpec{APIKeySecret: "my-secret"},
				Lead:        claudev1alpha1.LeadSpec{Model: "opus", Prompt: "lead"},
			},
		}
		Expect(k8sClient.Create(ctx, run)).To(Succeed())

		// Run should reach a TemplateNotReady condition without spawning a child.
		Eventually(func() string {
			var got claudev1alpha1.AgentTeamRun
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "should-not-instantiate", Namespace: ns}, &got); err != nil {
				return ""
			}
			for _, c := range got.Status.Conditions {
				if c.Type == "TeamCreated" {
					return c.Reason
				}
			}
			return ""
		}).Should(Equal("TemplateNotReady"))

		// And a polling check that a team was NOT created — keep this
		// short; the template will never become Ready so the Run will
		// never instantiate.
		Consistently(func() bool {
			var team claudev1alpha1.AgentTeam
			err := k8sClient.Get(ctx, types.NamespacedName{Name: "should-not-instantiate", Namespace: ns}, &team)
			return err != nil // Not found
		}, "1s").Should(BeTrue(), "no child team should ever be created when template is not Ready")
	})
})
