//go:build e2e

package e2e_test

import (
	"bytes"
	"io"
	"regexp"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// Real Claude Code runs against the live API. One run per commit is deliberate —
// see package doc for rationale.
var _ = Describe("Real Claude API end-to-end", func() {

	Describe("Cowork mode — single teammate writes a file", func() {
		var (
			namespace string
			team      *claudev1alpha1.AgentTeam
		)

		BeforeEach(func() {
			namespace = testNS()
			createAPIKeySecret("anthropic-key", namespace)

			budget := "0.10"
			team = &claudev1alpha1.AgentTeam{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "e2e-hello-world",
					Namespace: namespace,
				},
				Spec: claudev1alpha1.AgentTeamSpec{
					Auth: claudev1alpha1.AuthSpec{APIKeySecret: "anthropic-key"},
					Lead: claudev1alpha1.LeadSpec{
						Model: "haiku",
						// The lead's only job is to confirm readiness and exit.
						// We're testing the coordination plumbing, not multi-turn
						// delegation — that's a v0.3.0 concern.
						Prompt: "Reply with the single word 'ready' and stop.",
						// auto-accept lets the teammate use the Write tool without
						// interactive approval — required for any non-trivial task
						// inside a pod.
						PermissionMode: "auto-accept",
					},
					Teammates: []claudev1alpha1.TeammateSpec{
						{
							Name:  "writer",
							Model: "haiku",
							// The prompt is deliberately directive: exact file path,
							// exact expected content shape, single-turn action. This
							// minimizes both cost and flakiness.
							//
							// Note: teammate pods always run with auto-accept; there
							// is no PermissionMode field on TeammateSpec by design.
							Prompt: "Use the Write tool to create the file " +
								"/workspace/output/hello.go with this exact content " +
								"and nothing else:\n\n" +
								"package main\n\n" +
								"import \"fmt\"\n\n" +
								"func main() {\n" +
								"\tfmt.Println(\"hello, world\")\n" +
								"}\n\n" +
								"After the file is written, reply 'done' and stop.",
						},
					},
					Workspace: &claudev1alpha1.WorkspaceSpec{
						Output: &claudev1alpha1.WorkspaceOutputSpec{
							StorageClass: "nfs",
							Size:         "100Mi",
						},
					},
					Lifecycle: &claudev1alpha1.LifecycleSpec{
						// Operator-level timeout: bounds worst-case pod lifetime if
						// Claude gets stuck. BudgetLimit bounds worst-case spend.
						Timeout:     "5m",
						BudgetLimit: &budget,
						OnComplete:  "notify",
					},
				},
			}
			Expect(k8sClient.Create(ctx, team)).To(Succeed())
		})

		It("reaches Completed phase and writes hello.go to the output PVC", func() {
			// Step 1: team runs to completion.
			Eventually(func(g Gomega) {
				var got claudev1alpha1.AgentTeam
				g.Expect(k8sClient.Get(ctx, nn(team.Name, namespace), &got)).To(Succeed())
				g.Expect(got.Status.Phase).To(Equal("Completed"),
					"team is still %s; events: see kubectl describe", got.Status.Phase)
			}).Should(Succeed(), "team never reached Completed")

			// Step 2: status.ready reflects the single running+completed teammate.
			// This exercises the field added for issue #7.
			var final claudev1alpha1.AgentTeam
			Expect(k8sClient.Get(ctx, nn(team.Name, namespace), &final)).To(Succeed())
			Expect(final.Status.Ready).To(Equal("1/1"))

			// Step 3: the file survives on the output PVC. Team pods have been
			// deleted by reconcileTerminal, so we spin up a verifier pod that
			// mounts the PVC and cats the file.
			content := readFileFromOutputPVC(namespace, team.Name+"-output", "/out/hello.go")
			Expect(content).To(MatchRegexp(`package\s+main`))
			Expect(content).To(MatchRegexp(`func\s+main\s*\(\s*\)`))
			Expect(content).To(ContainSubstring("hello, world"))
		})
	})
})

// readFileFromOutputPVC creates a short-lived pod that mounts the given PVC
// at /out and prints the requested file to stdout. Returns the captured stdout.
//
// We use a pod (not a Job) so we can stream logs even if the pod is in Pending
// briefly — logs API handles both cases. Mount is read-only because the team
// has already terminated and we don't want to accidentally alter state.
func readFileFromOutputPVC(namespace, pvcName, filePath string) string {
	GinkgoHelper()

	podName := "verify-output-" + randSuffix()
	verifier := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: namespace},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:    "cat",
				Image:   "busybox:latest",
				Command: []string{"sh", "-c", "cat " + filePath},
				VolumeMounts: []corev1.VolumeMount{{
					Name:      "out",
					MountPath: "/out",
					ReadOnly:  true,
				}},
			}},
			Volumes: []corev1.Volume{{
				Name: "out",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: pvcName,
						ReadOnly:  true,
					},
				},
			}},
		},
	}
	Expect(k8sClient.Create(ctx, verifier)).To(Succeed())
	DeferCleanup(func() {
		_ = k8sClient.Delete(ctx, verifier)
	})

	// Wait for the verifier to finish (Succeeded or Failed).
	Eventually(func(g Gomega) {
		var p corev1.Pod
		g.Expect(k8sClient.Get(ctx, nn(podName, namespace), &p)).To(Succeed())
		g.Expect(p.Status.Phase).To(Or(
			Equal(corev1.PodSucceeded),
			Equal(corev1.PodFailed),
		))
	}).WithTimeout(3 * time.Minute).WithPolling(3 * time.Second).
		Should(Succeed(), "verifier pod never terminated")

	// Read logs. controller-runtime's typed client doesn't expose log
	// streaming, so drop down to client-go.
	cfg, err := ctrl.GetConfig()
	Expect(err).NotTo(HaveOccurred())
	cs, err := kubernetes.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred())

	req := cs.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{})
	stream, err := req.Stream(ctx)
	Expect(err).NotTo(HaveOccurred())
	defer stream.Close()

	var buf bytes.Buffer
	_, err = io.Copy(&buf, stream)
	Expect(err).NotTo(HaveOccurred())

	// Verify the pod actually succeeded — if the file was missing, busybox
	// cat exits non-zero and we want the test to fail with a clear message.
	var p corev1.Pod
	Expect(k8sClient.Get(ctx, nn(podName, namespace), &p)).To(Succeed())
	Expect(p.Status.Phase).To(Equal(corev1.PodSucceeded),
		"verifier pod failed — file %q likely does not exist on PVC %q. Pod logs:\n%s",
		filePath, pvcName, buf.String())

	return buf.String()
}

// randSuffix returns a short lowercase string suitable for appending to resource
// names within a test namespace. We avoid importing uuid for such a small need.
var randSuffixRegex = regexp.MustCompile(`[^a-z0-9]`)

func randSuffix() string {
	// Ginkgo's GenerateName-like helper isn't exported; use timestamp ns.
	t := time.Now().UnixNano()
	s := make([]byte, 0, 8)
	for i := 0; i < 8; i++ {
		c := byte('a' + t%26)
		s = append(s, c)
		t /= 26
	}
	return randSuffixRegex.ReplaceAllString(string(s), "x")
}
