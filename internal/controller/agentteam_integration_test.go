//go:build integration

package controller_test

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// --- Test helpers ---

// testNS creates a fresh namespace for a test and registers cleanup.
func testNS() string {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "inttest-"},
	}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	DeferCleanup(func() {
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, ns))).To(Succeed())
	})
	return ns.Name
}

func nn(name, namespace string) types.NamespacedName {
	return types.NamespacedName{Name: name, Namespace: namespace}
}

func codingTeam(name, namespace string) *claudev1alpha1.AgentTeam {
	return &claudev1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: claudev1alpha1.AgentTeamSpec{
			Repository: &claudev1alpha1.RepositorySpec{
				URL:    "https://github.com/example/repo",
				Branch: "main",
			},
			Auth: claudev1alpha1.AuthSpec{APIKeySecret: "api-key"},
			Lead: claudev1alpha1.LeadSpec{Model: "opus", Prompt: "Lead the team"},
			Teammates: []claudev1alpha1.TeammateSpec{
				{Name: "worker", Model: "sonnet", Prompt: "Do work"},
			},
		},
	}
}

func coworkTeam(name, namespace string) *claudev1alpha1.AgentTeam {
	return &claudev1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: claudev1alpha1.AgentTeamSpec{
			Workspace: &claudev1alpha1.WorkspaceSpec{
				Output: &claudev1alpha1.WorkspaceOutputSpec{Size: "1Gi"},
			},
			Auth: claudev1alpha1.AuthSpec{APIKeySecret: "api-key"},
			Lead: claudev1alpha1.LeadSpec{Model: "opus", Prompt: "Lead"},
			Teammates: []claudev1alpha1.TeammateSpec{
				{Name: "writer", Model: "sonnet", Prompt: "Write"},
			},
		},
	}
}

// waitForPhase polls until the team reaches the given phase.
func waitForPhase(name, namespace, phase string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var team claudev1alpha1.AgentTeam
		g.Expect(k8sClient.Get(ctx, nn(name, namespace), &team)).To(Succeed())
		g.Expect(team.Status.Phase).To(Equal(phase), "expected phase %q, got %q", phase, team.Status.Phase)
	}).Should(Succeed())
}

// waitForPVC polls until the named PVC exists.
func waitForPVC(name, namespace string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, nn(name, namespace), &corev1.PersistentVolumeClaim{})).To(Succeed())
	}).Should(Succeed())
}

// waitForJob polls until the named Job exists.
func waitForJob(name, namespace string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, nn(name, namespace), &batchv1.Job{})).To(Succeed())
	}).Should(Succeed())
}

// waitForPod polls until the named Pod exists.
func waitForPod(name, namespace string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, nn(name, namespace), &corev1.Pod{})).To(Succeed())
	}).Should(Succeed())
}

// completeJob sets a Job's status to Succeeded.
func completeJob(name, namespace string) {
	GinkgoHelper()
	var job batchv1.Job
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, nn(name, namespace), &job)).To(Succeed())
	}).Should(Succeed())
	job.Status.Succeeded = 1
	job.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobComplete,
		Status: corev1.ConditionTrue,
	}}
	Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())
}

// failJob sets a Job's status to failed past its backoff limit.
func failJob(name, namespace string) {
	GinkgoHelper()
	var job batchv1.Job
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, nn(name, namespace), &job)).To(Succeed())
	}).Should(Succeed())
	limit := int32(3)
	job.Spec.BackoffLimit = &limit
	Expect(k8sClient.Update(ctx, &job)).To(Succeed())
	job.Status.Failed = 3
	Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())
}

// succeedPod sets a Pod's status.phase to Succeeded.
func succeedPod(name, namespace string) {
	GinkgoHelper()
	var pod corev1.Pod
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, nn(name, namespace), &pod)).To(Succeed())
	}).Should(Succeed())
	pod.Status.Phase = corev1.PodSucceeded
	Expect(k8sClient.Status().Update(ctx, &pod)).To(Succeed())
}

// failPod sets a Pod's status.phase to Failed.
func failPod(name, namespace string) {
	GinkgoHelper()
	var pod corev1.Pod
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, nn(name, namespace), &pod)).To(Succeed())
	}).Should(Succeed())
	pod.Status.Phase = corev1.PodFailed
	Expect(k8sClient.Status().Update(ctx, &pod)).To(Succeed())
}

// advanceThroughInit creates the team, waits for Initializing, then completes the init Job.
func advanceThroughInit(name, namespace string) {
	GinkgoHelper()
	waitForPhase(name, namespace, "Initializing")
	completeJob(name+"-init", namespace)
}

// backdateStartedAt patches the team's status.startedAt to the given duration in the past
// so that timeout/budget checks see the team as having run long enough to trip them.
// Uses a status subresource Patch so it can't be clobbered by an in-flight reconcile.
func backdateStartedAt(name, namespace string, ago time.Duration) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var t claudev1alpha1.AgentTeam
		g.Expect(k8sClient.Get(ctx, nn(name, namespace), &t)).To(Succeed())
		patch := client.MergeFrom(t.DeepCopy())
		past := metav1.NewTime(time.Now().Add(-ago))
		t.Status.StartedAt = &past
		g.Expect(k8sClient.Status().Patch(ctx, &t, patch)).To(Succeed())
	}).Should(Succeed())
}

// pokeTeam bumps an annotation on the team to trigger an immediate reconcile instead of
// waiting for the next RequeueAfter interval.
func pokeTeam(name, namespace string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var t claudev1alpha1.AgentTeam
		g.Expect(k8sClient.Get(ctx, nn(name, namespace), &t)).To(Succeed())
		if t.Annotations == nil {
			t.Annotations = map[string]string{}
		}
		t.Annotations["test.kagents.dev/poke"] = time.Now().Format(time.RFC3339Nano)
		g.Expect(k8sClient.Update(ctx, &t)).To(Succeed())
	}).Should(Succeed())
}

// expectPodGone polls until the pod is either deleted or has a DeletionTimestamp set.
// envtest has no kubelet to finalize graceful pod deletion, so terminating pods may linger.
func expectPodGone(name, namespace string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var pod corev1.Pod
		err := k8sClient.Get(ctx, nn(name, namespace), &pod)
		if errors.IsNotFound(err) {
			return
		}
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(pod.DeletionTimestamp).NotTo(BeNil(), "pod %s should be deleted or terminating", name)
	}).Should(Succeed())
}

// --- Integration Tests ---

var _ = Describe("AgentTeam controller", func() {

	Describe("Pending phase — coding mode", func() {
		var (
			team      *claudev1alpha1.AgentTeam
			namespace string
		)

		BeforeEach(func() {
			namespace = testNS()
			team = codingTeam("ct-pending", namespace)
			Expect(k8sClient.Create(ctx, team)).To(Succeed())
		})

		It("creates the team-state and repo PVCs", func() {
			waitForPVC(team.Name+"-team-state", namespace)
			waitForPVC(team.Name+"-repo", namespace)
		})

		It("creates the init Job", func() {
			waitForJob(team.Name+"-init", namespace)
		})

		It("transitions phase to Initializing", func() {
			waitForPhase(team.Name, namespace, "Initializing")
		})

		It("sets the startedAt timestamp", func() {
			Eventually(func(g Gomega) {
				var t claudev1alpha1.AgentTeam
				g.Expect(k8sClient.Get(ctx, nn(team.Name, namespace), &t)).To(Succeed())
				g.Expect(t.Status.StartedAt).NotTo(BeNil())
			}).Should(Succeed())
		})

		It("owns all child resources via owner references", func() {
			waitForPVC(team.Name+"-team-state", namespace)
			waitForJob(team.Name+"-init", namespace)

			var pvc corev1.PersistentVolumeClaim
			Expect(k8sClient.Get(ctx, nn(team.Name+"-team-state", namespace), &pvc)).To(Succeed())
			Expect(pvc.OwnerReferences).To(HaveLen(1))
			Expect(pvc.OwnerReferences[0].Name).To(Equal(team.Name))

			var job batchv1.Job
			Expect(k8sClient.Get(ctx, nn(team.Name+"-init", namespace), &job)).To(Succeed())
			Expect(job.OwnerReferences).To(HaveLen(1))
			Expect(job.OwnerReferences[0].Name).To(Equal(team.Name))
		})
	})

	Describe("Pending phase — Cowork mode", func() {
		var (
			team      *claudev1alpha1.AgentTeam
			namespace string
		)

		BeforeEach(func() {
			namespace = testNS()
			team = coworkTeam("cw-pending", namespace)
			Expect(k8sClient.Create(ctx, team)).To(Succeed())
		})

		It("creates the output PVC", func() {
			waitForPVC(team.Name+"-output", namespace)
		})

		It("does not create a repo PVC or init Job", func() {
			// Cowork mode has no init Job so the reconciler transitions
			// Pending → Initializing → Running without pausing. Wait for
			// Running, then confirm the repo PVC and init Job were never created.
			waitForPhase(team.Name, namespace, "Running")

			Expect(k8sClient.Get(ctx, nn(team.Name+"-repo", namespace),
				&corev1.PersistentVolumeClaim{})).To(MatchError(errors.IsNotFound, "IsNotFound"))

			Expect(k8sClient.Get(ctx, nn(team.Name+"-init", namespace),
				&batchv1.Job{})).To(MatchError(errors.IsNotFound, "IsNotFound"))
		})

		It("deploys lead and teammate pods with no repo volume or WORKTREE_PATH", func() {
			waitForPhase(team.Name, namespace, "Running")
			waitForPod(team.Name+"-lead", namespace)
			waitForPod(team.Name+"-writer", namespace)

			// Verify no repo volume is mounted on either pod.
			for _, podName := range []string{team.Name + "-lead", team.Name + "-writer"} {
				var pod corev1.Pod
				Expect(k8sClient.Get(ctx, nn(podName, namespace), &pod)).To(Succeed())

				volumeNames := []string{}
				for _, v := range pod.Spec.Volumes {
					volumeNames = append(volumeNames, v.Name)
				}
				Expect(volumeNames).NotTo(ContainElement("repo"), "cowork pod %s should not have a repo volume", podName)

				env := envMap(pod)
				Expect(env).NotTo(HaveKey("WORKTREE_PATH"), "cowork pod %s should not have WORKTREE_PATH", podName)
			}
		})
	})

	Describe("Initializing phase — coding mode", func() {
		var (
			team      *claudev1alpha1.AgentTeam
			namespace string
		)

		BeforeEach(func() {
			namespace = testNS()
			team = codingTeam("ct-init", namespace)
			Expect(k8sClient.Create(ctx, team)).To(Succeed())
			waitForPhase(team.Name, namespace, "Initializing")
		})

		It("stays in Initializing while the init Job is still running", func() {
			// The Job exists but status is not yet Succeeded.
			waitForJob(team.Name+"-init", namespace)
			Consistently(func(g Gomega) {
				var t claudev1alpha1.AgentTeam
				g.Expect(k8sClient.Get(ctx, nn(team.Name, namespace), &t)).To(Succeed())
				g.Expect(t.Status.Phase).To(Equal("Initializing"))
			}).WithTimeout(3 * time.Second).Should(Succeed())
		})

		It("sets Failed phase when the init Job exhausts its backoff limit", func() {
			failJob(team.Name+"-init", namespace)
			waitForPhase(team.Name, namespace, "Failed")
		})

		It("deploys lead and teammate pods when the init Job succeeds", func() {
			completeJob(team.Name+"-init", namespace)
			waitForPod(team.Name+"-lead", namespace)
			waitForPod(team.Name+"-worker", namespace)
		})

		It("sets the lead pod's role label", func() {
			completeJob(team.Name+"-init", namespace)
			waitForPod(team.Name+"-lead", namespace)

			var pod corev1.Pod
			Expect(k8sClient.Get(ctx, nn(team.Name+"-lead", namespace), &pod)).To(Succeed())
			Expect(pod.Labels["kagents.dev/role"]).To(Equal("lead"))
		})

		It("sets the teammate pod's role label and WORKTREE_PATH env var", func() {
			completeJob(team.Name+"-init", namespace)
			waitForPod(team.Name+"-worker", namespace)

			var pod corev1.Pod
			Expect(k8sClient.Get(ctx, nn(team.Name+"-worker", namespace), &pod)).To(Succeed())
			Expect(pod.Labels["kagents.dev/role"]).To(Equal("teammate"))

			env := envMap(pod)
			Expect(env["WORKTREE_PATH"]).To(Equal("worktrees/worker"))
		})

		It("transitions to Running after pods are deployed", func() {
			completeJob(team.Name+"-init", namespace)
			waitForPhase(team.Name, namespace, "Running")
		})

		It("owns the agent pods via owner references", func() {
			completeJob(team.Name+"-init", namespace)
			waitForPod(team.Name+"-lead", namespace)

			var pod corev1.Pod
			Expect(k8sClient.Get(ctx, nn(team.Name+"-lead", namespace), &pod)).To(Succeed())
			Expect(pod.OwnerReferences).To(HaveLen(1))
			Expect(pod.OwnerReferences[0].Name).To(Equal(team.Name))
		})
	})

	Describe("Running phase", func() {
		var (
			team      *claudev1alpha1.AgentTeam
			namespace string
		)

		BeforeEach(func() {
			namespace = testNS()
			team = codingTeam("ct-run", namespace)
			Expect(k8sClient.Create(ctx, team)).To(Succeed())
			advanceThroughInit(team.Name, namespace)
			waitForPhase(team.Name, namespace, "Running")
		})

		It("transitions to Completed when all pods succeed", func() {
			succeedPod(team.Name+"-lead", namespace)
			succeedPod(team.Name+"-worker", namespace)
			waitForPhase(team.Name, namespace, "Completed")
		})

		It("stamps completedAt when Completed", func() {
			succeedPod(team.Name+"-lead", namespace)
			succeedPod(team.Name+"-worker", namespace)
			waitForPhase(team.Name, namespace, "Completed")

			var t claudev1alpha1.AgentTeam
			Expect(k8sClient.Get(ctx, nn(team.Name, namespace), &t)).To(Succeed())
			Expect(t.Status.CompletedAt).NotTo(BeNil())
		})

		It("transitions to Failed when any pod fails", func() {
			failPod(team.Name+"-lead", namespace)
			waitForPhase(team.Name, namespace, "Failed")
		})

		It("syncs pod phases into status.teammates", func() {
			succeedPod(team.Name+"-lead", namespace)

			Eventually(func(g Gomega) {
				var t claudev1alpha1.AgentTeam
				g.Expect(k8sClient.Get(ctx, nn(team.Name, namespace), &t)).To(Succeed())
				g.Expect(t.Status.Lead).NotTo(BeNil())
				g.Expect(t.Status.Lead.Phase).To(Equal("Completed"))
			}).Should(Succeed())
		})

		It("deletes all team pods during terminal phase and sets a stable completedAt", func() {
			succeedPod(team.Name+"-lead", namespace)
			succeedPod(team.Name+"-worker", namespace)
			waitForPhase(team.Name, namespace, "Completed")

			// Verify reconcileTerminal deletes the pods.
			expectPodGone(team.Name+"-lead", namespace)
			expectPodGone(team.Name+"-worker", namespace)

			// Verify completedAt is set and stable across reconciles.
			var first metav1.Time
			Eventually(func(g Gomega) {
				var t claudev1alpha1.AgentTeam
				g.Expect(k8sClient.Get(ctx, nn(team.Name, namespace), &t)).To(Succeed())
				g.Expect(t.Status.CompletedAt).NotTo(BeNil())
				first = *t.Status.CompletedAt
			}).Should(Succeed())

			// Poke to trigger another reconcile and confirm completedAt does not change.
			pokeTeam(team.Name, namespace)
			Consistently(func(g Gomega) {
				var t claudev1alpha1.AgentTeam
				g.Expect(k8sClient.Get(ctx, nn(team.Name, namespace), &t)).To(Succeed())
				g.Expect(t.Status.CompletedAt).NotTo(BeNil())
				g.Expect(t.Status.CompletedAt.Time).To(Equal(first.Time), "completedAt should be stable across reconciles")
			}).WithTimeout(3 * time.Second).Should(Succeed())
		})
	})

	Describe("DependsOn ordering", func() {
		var (
			team      *claudev1alpha1.AgentTeam
			namespace string
		)

		BeforeEach(func() {
			namespace = testNS()
			team = &claudev1alpha1.AgentTeam{
				ObjectMeta: metav1.ObjectMeta{Name: "ct-deps", Namespace: namespace},
				Spec: claudev1alpha1.AgentTeamSpec{
					Repository: &claudev1alpha1.RepositorySpec{URL: "https://github.com/example/repo", Branch: "main"},
					Auth:       claudev1alpha1.AuthSpec{APIKeySecret: "api-key"},
					Lead:       claudev1alpha1.LeadSpec{Model: "opus", Prompt: "Lead"},
					Teammates: []claudev1alpha1.TeammateSpec{
						{Name: "first", Model: "sonnet", Prompt: "First"},
						{Name: "second", Model: "sonnet", Prompt: "Second", DependsOn: []string{"first"}},
					},
				},
			}
			Expect(k8sClient.Create(ctx, team)).To(Succeed())
			advanceThroughInit(team.Name, namespace)
			waitForPhase(team.Name, namespace, "Running")
		})

		It("spawns first teammate but not second before first completes", func() {
			waitForPod(team.Name+"-first", namespace)

			// Second should NOT yet exist.
			Consistently(func(g Gomega) {
				err := k8sClient.Get(ctx, nn(team.Name+"-second", namespace), &corev1.Pod{})
				g.Expect(errors.IsNotFound(err)).To(BeTrue(), "second teammate should be blocked")
			}).WithTimeout(3 * time.Second).Should(Succeed())
		})

		It("spawns second teammate once first succeeds", func() {
			waitForPod(team.Name+"-first", namespace)
			succeedPod(team.Name+"-first", namespace)
			waitForPod(team.Name+"-second", namespace)
		})
	})

	Describe("Approval gates", func() {
		var (
			team      *claudev1alpha1.AgentTeam
			namespace string
		)

		BeforeEach(func() {
			namespace = testNS()
			team = &claudev1alpha1.AgentTeam{
				ObjectMeta: metav1.ObjectMeta{Name: "ct-gate", Namespace: namespace},
				Spec: claudev1alpha1.AgentTeamSpec{
					Repository: &claudev1alpha1.RepositorySpec{URL: "https://github.com/example/repo", Branch: "main"},
					Auth:       claudev1alpha1.AuthSpec{APIKeySecret: "api-key"},
					Lead:       claudev1alpha1.LeadSpec{Model: "opus", Prompt: "Lead"},
					Teammates: []claudev1alpha1.TeammateSpec{
						{Name: "gated", Model: "sonnet", Prompt: "Needs approval"},
					},
					Lifecycle: &claudev1alpha1.LifecycleSpec{
						ApprovalGates: []claudev1alpha1.ApprovalGateSpec{
							{Event: "spawn-gated", Channel: "none"},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, team)).To(Succeed())
			advanceThroughInit(team.Name, namespace)
			waitForPhase(team.Name, namespace, "Running")
		})

		It("blocks the gated teammate and sets pendingApproval status", func() {
			// Lead deploys; gated teammate should NOT.
			waitForPod(team.Name+"-lead", namespace)
			Consistently(func(g Gomega) {
				err := k8sClient.Get(ctx, nn(team.Name+"-gated", namespace), &corev1.Pod{})
				g.Expect(errors.IsNotFound(err)).To(BeTrue(), "gated teammate should be blocked")
			}).WithTimeout(3 * time.Second).Should(Succeed())

			// Status should reflect the pending approval.
			Eventually(func(g Gomega) {
				var t claudev1alpha1.AgentTeam
				g.Expect(k8sClient.Get(ctx, nn(team.Name, namespace), &t)).To(Succeed())
				g.Expect(t.Status.Teammates).NotTo(BeEmpty())
				g.Expect(t.Status.Teammates[0].PendingApproval).To(Equal("spawn-gated"))
			}).Should(Succeed())
		})

		It("spawns the teammate after the approval annotation is applied", func() {
			waitForPod(team.Name+"-lead", namespace)

			// Apply the approval annotation.
			var t claudev1alpha1.AgentTeam
			Expect(k8sClient.Get(ctx, nn(team.Name, namespace), &t)).To(Succeed())
			if t.Annotations == nil {
				t.Annotations = map[string]string{}
			}
			t.Annotations["approved.kagents.dev/spawn-gated"] = "true"
			Expect(k8sClient.Update(ctx, &t)).To(Succeed())

			// Teammate should now be spawned.
			waitForPod(team.Name+"-gated", namespace)
		})
	})

	Describe("CRD validation", func() {
		var namespace string

		BeforeEach(func() {
			namespace = testNS()
		})

		It("rejects an invalid model enum value", func() {
			team := codingTeam("invalid-model", namespace)
			team.Spec.Lead.Model = "gpt-4o" // not in enum
			err := k8sClient.Create(ctx, team)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("model"))
		})

		It("rejects a teammates list with zero items", func() {
			team := codingTeam("no-teammates", namespace)
			team.Spec.Teammates = []claudev1alpha1.TeammateSpec{}
			err := k8sClient.Create(ctx, team)
			Expect(err).To(HaveOccurred())
		})

		It("rejects a teammates list exceeding 16 items", func() {
			team := codingTeam("too-many-teammates", namespace)
			team.Spec.Teammates = make([]claudev1alpha1.TeammateSpec, 17)
			for i := range team.Spec.Teammates {
				team.Spec.Teammates[i] = claudev1alpha1.TeammateSpec{
					Name:   fmt.Sprintf("tm%02d", i),
					Model:  "sonnet",
					Prompt: "work",
				}
			}
			err := k8sClient.Create(ctx, team)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("Lifecycle enforcement", func() {
		It("transitions to TimedOut and removes pods when Running phase exceeds the configured timeout", func() {
			namespace := testNS()
			team := codingTeam("ct-timeout-run", namespace)
			// Use a long timeout so the team can advance through init without tripping it.
			team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{Timeout: "24h"}
			Expect(k8sClient.Create(ctx, team)).To(Succeed())

			advanceThroughInit(team.Name, namespace)
			waitForPhase(team.Name, namespace, "Running")
			waitForPod(team.Name+"-lead", namespace)
			waitForPod(team.Name+"-worker", namespace)

			// Now backdate StartedAt past the 24h timeout to trigger TimedOut on next reconcile.
			backdateStartedAt(team.Name, namespace, 25*time.Hour)
			pokeTeam(team.Name, namespace)

			waitForPhase(team.Name, namespace, "TimedOut")
			expectPodGone(team.Name+"-lead", namespace)
			expectPodGone(team.Name+"-worker", namespace)
		})

		It("transitions to BudgetExceeded and removes pods when the estimated cost exceeds the limit", func() {
			namespace := testNS()
			team := codingTeam("ct-budget", namespace)
			limit := "0.01"
			team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{
				Timeout:     "24h", // long enough that the timeout does not fire first
				BudgetLimit: &limit,
			}
			Expect(k8sClient.Create(ctx, team)).To(Succeed())

			advanceThroughInit(team.Name, namespace)
			waitForPhase(team.Name, namespace, "Running")
			waitForPod(team.Name+"-lead", namespace)
			waitForPod(team.Name+"-worker", namespace)

			// Backdate StartedAt far enough for estimateCost() to exceed the tiny budget,
			// but well within the 24h timeout.
			backdateStartedAt(team.Name, namespace, 1*time.Hour)
			pokeTeam(team.Name, namespace)

			waitForPhase(team.Name, namespace, "BudgetExceeded")
			expectPodGone(team.Name+"-lead", namespace)
			expectPodGone(team.Name+"-worker", namespace)
		})

		It("transitions to TimedOut during Initializing without waiting for the init Job to fail", func() {
			namespace := testNS()
			team := codingTeam("ct-timeout-init", namespace)
			// Use a long timeout so the team can reach Initializing first.
			team.Spec.Lifecycle = &claudev1alpha1.LifecycleSpec{Timeout: "24h"}
			Expect(k8sClient.Create(ctx, team)).To(Succeed())

			// Reach Initializing with the init Job still running (never completed/failed).
			waitForPhase(team.Name, namespace, "Initializing")
			waitForJob(team.Name+"-init", namespace)

			// Now backdate past the 24h timeout while the Job is still running.
			backdateStartedAt(team.Name, namespace, 25*time.Hour)
			pokeTeam(team.Name, namespace)

			waitForPhase(team.Name, namespace, "TimedOut")

			// The init Job should still exist and still be running — we proved the timeout
			// check short-circuits the init wait rather than requiring failJob/completeJob.
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, nn(team.Name+"-init", namespace), &job)).To(Succeed())
			Expect(job.Status.Succeeded).To(BeZero())
			Expect(job.Status.Failed).To(BeZero())
		})
	})
})

// envMap extracts env vars from the first container of a pod into a map.
func envMap(pod corev1.Pod) map[string]string {
	m := map[string]string{}
	for _, e := range pod.Spec.Containers[0].Env {
		m[e.Name] = e.Value
	}
	return m
}
