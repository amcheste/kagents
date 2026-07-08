//go:build e2e

// Package e2e_test contains end-to-end tests that exercise the operator against
// the live Anthropic API using the real claude-code-runner image. Unlike the
// acceptance suite (which uses busybox and never hits the API), these tests
// verify that the full AgentTeam coordination story — mailbox/task PVCs,
// agent pod startup, real Claude Code execution — works against production
// infrastructure.
//
// Prerequisites:
//
//	export ANTHROPIC_API_KEY=sk-ant-...
//	make e2e-up      # create Kind cluster with real runner image
//	make test-e2e    # run this suite
//	make e2e-down    # tear down cluster
//
// The operator must be deployed WITHOUT --agent-image=busybox, so agent pods
// start the real Claude Code runner. The ANTHROPIC_API_KEY env var is required
// both for deploying the cluster (baked into a K8s Secret) and for the tests
// themselves, which re-use it to populate per-test Secrets.
//
// This suite is deliberately small (one test by default). Each run costs real
// API credits, so the goal is minimum viable verification, not exhaustive
// coverage. The bounded prompt + budgetLimit caps worst-case spend per run.
package e2e_test

import (
	"context"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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
	apiKey    string
)

const (
	operatorNamespace  = "claude-teams-system"
	operatorDeployment = "controller-manager"
)

// TestE2E is the Ginkgo entry point.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "AgentTeam E2E Suite (real Claude API)")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.Background())

	apiKey = os.Getenv("ANTHROPIC_API_KEY")
	Expect(apiKey).NotTo(BeEmpty(),
		"ANTHROPIC_API_KEY must be set — this suite hits the real Anthropic API")

	cfg, err := ctrl.GetConfig()
	Expect(err).NotTo(HaveOccurred(), "could not get cluster config — is KUBECONFIG set?")

	scheme := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(claudev1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(appsv1.AddToScheme(scheme)).To(Succeed())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	// Wait for the operator deployment before running the test. The operator
	// must be deployed by make e2e-up before this point; we only verify it
	// came up cleanly.
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
	// Real Claude Code runs take longer than the busybox acceptance suite.
	// 10 minutes accommodates image pull + pod scheduling + a few retries,
	// while the operator's own Lifecycle.Timeout caps the per-team budget.
	SetDefaultEventuallyTimeout(10 * time.Minute)
	SetDefaultEventuallyPollingInterval(5 * time.Second)
}

// --- Shared helpers ---

// testNS creates a fresh namespace and registers DeferCleanup to delete it.
func testNS() string {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "e2e-"},
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

// createAPIKeySecret writes the real ANTHROPIC_API_KEY into a Secret the team can reference.
func createAPIKeySecret(name, namespace string) {
	GinkgoHelper()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		StringData: map[string]string{"ANTHROPIC_API_KEY": apiKey},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
}
