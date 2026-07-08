//go:build acceptance

// Package acceptance_test contains tests that run against a live Kubernetes cluster
// (Kind or otherwise) with the operator deployed. Unlike envtest integration tests
// these exercise real RBAC, real image scheduling, and real pod lifecycle events.
//
// Prerequisites:
//
//	make acceptance-up   # create Kind cluster + deploy operator
//	make test-acceptance # run this suite
//	make acceptance-down # tear down cluster
//
// The operator must be deployed with --agent-image=busybox:latest
// --init-image=busybox:latest --skip-init-script so that agent containers
// actually exit 0 and drive real phase transitions.
package acceptance_test

import (
	"context"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

var (
	k8sClient client.Client
	ctx       context.Context
	cancel    context.CancelFunc
)

const operatorNamespace = "claude-teams-system"
const operatorDeployment = "controller-manager"

// TestAcceptance is the Ginkgo entry point.
func TestAcceptance(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "AgentTeam Acceptance Suite")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.Background())

	cfg, err := ctrl.GetConfig()
	Expect(err).NotTo(HaveOccurred(), "could not get cluster config — is KUBECONFIG set?")

	scheme := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(claudev1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(batchv1.AddToScheme(scheme)).To(Succeed())
	Expect(appsv1.AddToScheme(scheme)).To(Succeed())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	// Wait for the operator deployment to be ready before running any tests.
	GinkgoWriter.Println("waiting for operator to be ready…")
	Eventually(func(g Gomega) {
		var d appsv1.Deployment
		g.Expect(k8sClient.Get(ctx,
			types.NamespacedName{Name: operatorDeployment, Namespace: operatorNamespace}, &d,
		)).To(Succeed())
		g.Expect(d.Status.ReadyReplicas).To(BeNumerically(">=", 1))
	}).WithTimeout(2 * time.Minute).WithPolling(3 * time.Second).Should(Succeed(),
		"operator deployment never became ready")
	GinkgoWriter.Println("operator is ready")
})

var _ = AfterSuite(func() {
	cancel()
})

func init() {
	SetDefaultEventuallyTimeout(3 * time.Minute)
	SetDefaultEventuallyPollingInterval(2 * time.Second)
}

// --- Shared helpers ---

// testNS creates a fresh namespace and registers DeferCleanup to delete it.
func testNS() string {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "acctest-"},
	}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	DeferCleanup(func() {
		_ = k8sClient.Delete(ctx, ns)
	})
	return ns.Name
}

// nn is a shortcut for types.NamespacedName.
func nn(name, namespace string) types.NamespacedName {
	return types.NamespacedName{Name: name, Namespace: namespace}
}

// dummyAPIKeySecret creates a Secret that satisfies the operator's apiKeySecret reference.
func dummyAPIKeySecret(name, namespace string) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		StringData: map[string]string{"ANTHROPIC_API_KEY": "test-key-acceptance"},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
}

// waitForPhase polls until the AgentTeam reaches the expected phase.
func waitForPhase(name, namespace, phase string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var t claudev1alpha1.AgentTeam
		g.Expect(k8sClient.Get(ctx, nn(name, namespace), &t)).To(Succeed())
		g.Expect(t.Status.Phase).To(Equal(phase))
	}).Should(Succeed(), "waiting for phase %s", phase)
}

// waitForPVC polls until a PVC with the given name exists.
func waitForPVC(name, namespace string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, nn(name, namespace), &corev1.PersistentVolumeClaim{})).To(Succeed())
	}).Should(Succeed(), "waiting for PVC %s", name)
}

// waitForJob polls until a Job with the given name exists.
func waitForJob(name, namespace string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, nn(name, namespace), &batchv1.Job{})).To(Succeed())
	}).Should(Succeed(), "waiting for Job %s", name)
}

// waitForPod polls until a Pod with the given name exists.
func waitForPod(name, namespace string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, nn(name, namespace), &corev1.Pod{})).To(Succeed())
	}).Should(Succeed(), "waiting for Pod %s", name)
}

// waitForPodPhase polls until a Pod reaches the expected phase.
func waitForPodPhase(name, namespace string, phase corev1.PodPhase) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var p corev1.Pod
		g.Expect(k8sClient.Get(ctx, nn(name, namespace), &p)).To(Succeed())
		g.Expect(p.Status.Phase).To(Equal(phase))
	}).Should(Succeed(), "waiting for Pod %s phase %s", name, phase)
}

// minimalCoworkTeam builds a simple Cowork-mode AgentTeam (no git repo).
func minimalCoworkTeam(name, namespace, apiKeySecret string) *claudev1alpha1.AgentTeam {
	return &claudev1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: claudev1alpha1.AgentTeamSpec{
			Auth: claudev1alpha1.AuthSpec{APIKeySecret: apiKeySecret},
			Lead: claudev1alpha1.LeadSpec{
				Model:  "sonnet",
				Prompt: "You are the team lead. Summarise the task and exit.",
			},
			Teammates: []claudev1alpha1.TeammateSpec{
				{
					Name:   "worker",
					Model:  "haiku",
					Prompt: "You are the worker. Confirm receipt and exit.",
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
}

// getenvOrSkip returns the value of an environment variable or skips the test.
func getenvOrSkip(key string) string {
	v := os.Getenv(key)
	if v == "" {
		Skip("env var " + key + " not set")
	}
	return v
}

// notFound checks whether the error is a Kubernetes not-found error.
func notFound(err error) bool {
	return errors.IsNotFound(err)
}
