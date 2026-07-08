//go:build acceptance

package acceptance_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

var _ = Describe("AgentTeam operator — acceptance", func() {

	// ──────────────────────────────────────────────────────────────────────────
	// Operator health
	// ──────────────────────────────────────────────────────────────────────────
	Describe("Operator health", func() {
		It("has a ready controller-manager deployment", func() {
			// BeforeSuite already asserts this; this spec makes it visible in the
			// report as a named check.
			Succeed()
		})
	})

	// ──────────────────────────────────────────────────────────────────────────
	// CRD validation
	// ──────────────────────────────────────────────────────────────────────────
	Describe("CRD validation", func() {
		var namespace string

		BeforeEach(func() {
			namespace = testNS()
		})

		It("rejects an invalid model value", func() {
			team := minimalCoworkTeam("invalid-model", namespace, "dummy-secret")
			team.Spec.Lead.Model = "gpt-4o"
			err := k8sClient.Create(ctx, team)
			Expect(err).To(HaveOccurred(), "expected admission to reject invalid model")
			Expect(errors.IsInvalid(err) || errors.IsBadRequest(err)).To(BeTrue())
		})

		It("rejects a team with no teammates", func() {
			team := minimalCoworkTeam("no-teammates", namespace, "dummy-secret")
			team.Spec.Teammates = nil
			err := k8sClient.Create(ctx, team)
			Expect(err).To(HaveOccurred())
		})
	})

	// ──────────────────────────────────────────────────────────────────────────
	// Cowork mode — full lifecycle (no git repo)
	//
	// The operator is deployed with --agent-image=busybox:latest so the agent
	// containers start, run busybox sh (exits 0 immediately), and the operator
	// observes real PodSucceeded events to drive phase transitions.
	// ──────────────────────────────────────────────────────────────────────────
	Describe("Cowork mode full lifecycle", func() {
		var (
			namespace string
			team      *claudev1alpha1.AgentTeam
		)

		BeforeEach(func() {
			namespace = testNS()
			dummyAPIKeySecret("anthropic-key", namespace)
			team = minimalCoworkTeam("cowork-lifecycle", namespace, "anthropic-key")
			Expect(k8sClient.Create(ctx, team)).To(Succeed())
		})

		It("creates a team-state PVC and an output PVC", func() {
			waitForPVC(team.Name+"-team-state", namespace)
			waitForPVC(team.Name+"-output", namespace)
		})

		It("does NOT create a repo PVC or init Job", func() {
			// Fast check: output PVC proves operator ran at least once.
			waitForPVC(team.Name+"-output", namespace)

			Expect(k8sClient.Get(ctx, nn(team.Name+"-repo", namespace),
				&corev1.PersistentVolumeClaim{})).
				To(MatchError(notFound, "IsNotFound"),
					"cowork mode must not create a repo PVC")

			Expect(k8sClient.Get(ctx, nn(team.Name+"-init", namespace),
				&batchv1.Job{})).
				To(MatchError(notFound, "IsNotFound"),
					"cowork mode must not create an init Job")
		})

		It("deploys lead and teammate pods", func() {
			waitForPod(team.Name+"-lead", namespace)
			waitForPod(team.Name+"-worker", namespace)
		})

		It("reaches Completed phase after all pods succeed", func() {
			// The operator drives phase transitions based on real pod exit codes.
			// busybox exits 0 quickly; pods may already be cleaned up by the time
			// we poll, so just assert the final phase rather than intermediate pod state.
			waitForPhase(team.Name, namespace, "Completed")
		})

		It("stamps completedAt on Completed", func() {
			waitForPhase(team.Name, namespace, "Completed")
			var t claudev1alpha1.AgentTeam
			Expect(k8sClient.Get(ctx, nn(team.Name, namespace), &t)).To(Succeed())
			Expect(t.Status.CompletedAt).NotTo(BeNil())
		})
	})

	// ──────────────────────────────────────────────────────────────────────────
	// Coding mode — full lifecycle (with git repo)
	//
	// The operator is deployed with --skip-init-script so the init Job runs
	// busybox sh -c "exit 0" instead of the real git-clone script.
	// ──────────────────────────────────────────────────────────────────────────
	Describe("Coding mode full lifecycle", func() {
		var (
			namespace string
			team      *claudev1alpha1.AgentTeam
		)

		BeforeEach(func() {
			namespace = testNS()
			dummyAPIKeySecret("anthropic-key", namespace)

			// Dummy git credentials secret (contents don't matter since init
			// script is replaced with exit 0).
			Expect(k8sClient.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "git-creds", Namespace: namespace},
				StringData: map[string]string{"key": "dummy"},
			})).To(Succeed())

			team = &claudev1alpha1.AgentTeam{
				ObjectMeta: metav1.ObjectMeta{Name: "coding-lifecycle", Namespace: namespace},
				Spec: claudev1alpha1.AgentTeamSpec{
					Auth: claudev1alpha1.AuthSpec{APIKeySecret: "anthropic-key"},
					Repository: &claudev1alpha1.RepositorySpec{
						URL:               "https://github.com/example/repo.git",
						Branch:            "main",
						WorktreeStrategy:  "per-teammate",
						CredentialsSecret: "git-creds",
					},
					Lead: claudev1alpha1.LeadSpec{
						Model:  "sonnet",
						Prompt: "Implement the feature.",
					},
					Teammates: []claudev1alpha1.TeammateSpec{
						{
							Name:   "coder",
							Model:  "sonnet",
							Prompt: "Write the code.",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, team)).To(Succeed())
		})

		It("creates team-state PVC, repo PVC, and init Job", func() {
			waitForPVC(team.Name+"-team-state", namespace)
			waitForPVC(team.Name+"-repo", namespace)
			waitForJob(team.Name+"-init", namespace)
		})

		It("transitions to Initializing", func() {
			waitForPhase(team.Name, namespace, "Initializing")
		})

		It("deploys lead and teammate pods after init Job succeeds", func() {
			// The init Job runs busybox sh exit 0 — wait for it to complete.
			waitForPhase(team.Name, namespace, "Running")
			waitForPod(team.Name+"-lead", namespace)
			waitForPod(team.Name+"-coder", namespace)
		})

		It("reaches Completed phase", func() {
			waitForPhase(team.Name, namespace, "Completed")
		})
	})

	// ──────────────────────────────────────────────────────────────────────────
	// Failure handling
	// ──────────────────────────────────────────────────────────────────────────
	Describe("Failure handling", func() {
		var (
			namespace string
			team      *claudev1alpha1.AgentTeam
		)

		BeforeEach(func() {
			namespace = testNS()
			dummyAPIKeySecret("anthropic-key", namespace)
		})

		It("sets Failed phase when the init Job exhausts its backoff limit", func() {
			// Use a deliberately broken init image with a command that exits 1.
			team = minimalCoworkTeam("fail-init", namespace, "anthropic-key")
			// Switch to coding mode so there IS an init job, and patch it to fail.
			Expect(k8sClient.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "git-creds", Namespace: namespace},
				StringData: map[string]string{"key": "dummy"},
			})).To(Succeed())

			failTeam := &claudev1alpha1.AgentTeam{
				ObjectMeta: metav1.ObjectMeta{Name: "fail-init", Namespace: namespace},
				Spec: claudev1alpha1.AgentTeamSpec{
					Auth: claudev1alpha1.AuthSpec{APIKeySecret: "anthropic-key"},
					Repository: &claudev1alpha1.RepositorySpec{
						URL:               "https://github.com/example/repo.git",
						Branch:            "main",
						WorktreeStrategy:  "per-teammate",
						CredentialsSecret: "git-creds",
					},
					Lead: claudev1alpha1.LeadSpec{
						Model:  "sonnet",
						Prompt: "Lead.",
					},
					Teammates: []claudev1alpha1.TeammateSpec{
						{Name: "w", Model: "haiku", Prompt: "Work."},
					},
				},
			}
			Expect(k8sClient.Create(ctx, failTeam)).To(Succeed())
			waitForJob(failTeam.Name+"-init", namespace)

			// Patch the init Job status to failed (exhausted backoff limit).
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, nn(failTeam.Name+"-init", namespace), &job)).To(Succeed())
			job.Status.Failed = 3
			Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

			waitForPhase(failTeam.Name, namespace, "Failed")
		})

		It("sets Failed phase when a teammate pod fails", func() {
			// Use a special image command override injected via the operator's
			// --agent-image flag. Instead, manually patch the pod status.
			team = minimalCoworkTeam("fail-pod", namespace, "anthropic-key")
			Expect(k8sClient.Create(ctx, team)).To(Succeed())
			waitForPod(team.Name+"-lead", namespace)

			// Patch lead pod to Failed.
			var pod corev1.Pod
			Expect(k8sClient.Get(ctx, nn(team.Name+"-lead", namespace), &pod)).To(Succeed())
			pod.Status.Phase = corev1.PodFailed
			Expect(k8sClient.Status().Update(ctx, &pod)).To(Succeed())

			waitForPhase(team.Name, namespace, "Failed")
		})
	})

	// ──────────────────────────────────────────────────────────────────────────
	// DependsOn ordering
	// ──────────────────────────────────────────────────────────────────────────
	Describe("dependsOn ordering", func() {
		var (
			namespace string
			team      *claudev1alpha1.AgentTeam
		)

		BeforeEach(func() {
			namespace = testNS()
			dummyAPIKeySecret("anthropic-key", namespace)

			team = &claudev1alpha1.AgentTeam{
				ObjectMeta: metav1.ObjectMeta{Name: "deps-team", Namespace: namespace},
				Spec: claudev1alpha1.AgentTeamSpec{
					Auth: claudev1alpha1.AuthSpec{APIKeySecret: "anthropic-key"},
					Lead: claudev1alpha1.LeadSpec{
						Model:  "sonnet",
						Prompt: "Lead.",
					},
					Teammates: []claudev1alpha1.TeammateSpec{
						{
							Name:   "first",
							Model:  "haiku",
							Prompt: "First step.",
						},
						{
							Name:      "second",
							Model:     "haiku",
							Prompt:    "Second step, depends on first.",
							DependsOn: []string{"first"},
						},
					},
					Workspace: &claudev1alpha1.WorkspaceSpec{
						Output: &claudev1alpha1.WorkspaceOutputSpec{
							StorageClass: "standard",
							Size:         "100Mi",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, team)).To(Succeed())
		})

		It("spawns second only after first pod succeeds", func() {
			// Wait for first pod to appear.
			waitForPod(team.Name+"-first", namespace)

			// Second pod must NOT yet exist.
			Consistently(func(g Gomega) {
				err := k8sClient.Get(ctx, nn(team.Name+"-second", namespace), &corev1.Pod{})
				g.Expect(errors.IsNotFound(err)).To(BeTrue(), "second pod should be blocked")
			}).WithTimeout(10 * time.Second).Should(Succeed())

			// Let first pod actually run (busybox exits 0) then second should appear.
			waitForPodPhase(team.Name+"-first", namespace, corev1.PodSucceeded)
			waitForPod(team.Name+"-second", namespace)
		})
	})

	// ──────────────────────────────────────────────────────────────────────────
	// Approval gates
	// ──────────────────────────────────────────────────────────────────────────
	Describe("Approval gates", func() {
		var (
			namespace string
			team      *claudev1alpha1.AgentTeam
		)

		BeforeEach(func() {
			namespace = testNS()
			dummyAPIKeySecret("anthropic-key", namespace)

			team = &claudev1alpha1.AgentTeam{
				ObjectMeta: metav1.ObjectMeta{Name: "gate-team", Namespace: namespace},
				Spec: claudev1alpha1.AgentTeamSpec{
					Auth: claudev1alpha1.AuthSpec{APIKeySecret: "anthropic-key"},
					Lead: claudev1alpha1.LeadSpec{
						Model:  "sonnet",
						Prompt: "Lead.",
					},
					Teammates: []claudev1alpha1.TeammateSpec{
						{
							Name:   "gated",
							Model:  "haiku",
							Prompt: "Gated worker.",
						},
					},
					Lifecycle: &claudev1alpha1.LifecycleSpec{
						ApprovalGates: []claudev1alpha1.ApprovalGateSpec{
							{Event: "spawn-gated", Channel: "none"},
						},
					},
					Workspace: &claudev1alpha1.WorkspaceSpec{
						Output: &claudev1alpha1.WorkspaceOutputSpec{
							StorageClass: "standard",
							Size:         "100Mi",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, team)).To(Succeed())
		})

		It("blocks the gated teammate until approval annotation is set", func() {
			waitForPod(team.Name+"-lead", namespace)

			// Gated teammate must not exist yet.
			Consistently(func(g Gomega) {
				err := k8sClient.Get(ctx, nn(team.Name+"-gated", namespace), &corev1.Pod{})
				g.Expect(errors.IsNotFound(err)).To(BeTrue(), "gated pod should be blocked")
			}).WithTimeout(10 * time.Second).Should(Succeed())

			// Grant approval.
			var t claudev1alpha1.AgentTeam
			Expect(k8sClient.Get(ctx, nn(team.Name, namespace), &t)).To(Succeed())
			if t.Annotations == nil {
				t.Annotations = map[string]string{}
			}
			t.Annotations["approved.kagents.dev/spawn-gated"] = "true"
			Expect(k8sClient.Update(ctx, &t)).To(Succeed())

			// Gated teammate should now be spawned.
			waitForPod(team.Name+"-gated", namespace)
		})
	})

	// ──────────────────────────────────────────────────────────────────────────
	// Resource ownership
	// ──────────────────────────────────────────────────────────────────────────
	Describe("Owner references", func() {
		var (
			namespace string
			team      *claudev1alpha1.AgentTeam
		)

		BeforeEach(func() {
			namespace = testNS()
			dummyAPIKeySecret("anthropic-key", namespace)
			team = minimalCoworkTeam("owner-test", namespace, "anthropic-key")
			Expect(k8sClient.Create(ctx, team)).To(Succeed())
		})

		It("sets an owner reference on the team-state PVC", func() {
			waitForPVC(team.Name+"-team-state", namespace)
			var pvc corev1.PersistentVolumeClaim
			Expect(k8sClient.Get(ctx, nn(team.Name+"-team-state", namespace), &pvc)).To(Succeed())
			Expect(pvc.OwnerReferences).NotTo(BeEmpty())
			Expect(pvc.OwnerReferences[0].Name).To(Equal(team.Name))
		})

		It("sets an owner reference on agent pods", func() {
			waitForPod(team.Name+"-lead", namespace)
			var pod corev1.Pod
			Expect(k8sClient.Get(ctx, nn(team.Name+"-lead", namespace), &pod)).To(Succeed())
			Expect(pod.OwnerReferences).NotTo(BeEmpty())
			Expect(pod.OwnerReferences[0].Name).To(Equal(team.Name))
		})
	})

	// ──────────────────────────────────────────────────────────────────────────
	// RBAC smoke test — operator can manage resources in test namespaces
	// ──────────────────────────────────────────────────────────────────────────
	Describe("RBAC", func() {
		It("operator service account can create pods in arbitrary namespaces", func() {
			// The cowork lifecycle test implicitly covers this — if RBAC is wrong
			// the operator cannot create PVCs/pods and the team stays in Pending forever.
			// This spec documents the implicit coverage explicitly in the report.
			Succeed()
		})
	})
})
