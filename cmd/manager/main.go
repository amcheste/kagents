package main

import (
	"flag"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
	"github.com/amcheste/kagents/internal/controller"
	"github.com/amcheste/kagents/internal/harness"
	"github.com/amcheste/kagents/internal/metrics"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(claudev1alpha1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var probeAddr string
	var enableLeaderElection bool
	var agentImage string
	var initImage string
	var skipInitScript bool
	var pvcAccessMode string
	var agentCommand string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	flag.StringVar(&agentImage, "agent-image", "", "Override the container image used for agent pods (default: ghcr.io/amcheste/claude-code-runner:latest).")
	flag.StringVar(&initImage, "init-image", "", "Override the container image used for the repo init Job (default: alpine/git:latest).")
	flag.BoolVar(&skipInitScript, "skip-init-script", false, "Replace the init Job git-clone script with a no-op exit 0. Use in acceptance tests where no real repo is available.")
	flag.StringVar(&pvcAccessMode, "pvc-access-mode", "", "Override PVC access mode for all operator-managed PVCs (ReadWriteMany|ReadWriteOnce). Defaults to ReadWriteMany. Set to ReadWriteOnce for single-node clusters like Kind.")
	flag.StringVar(&agentCommand, "agent-command", "", "Override the agent container command as a comma-separated list (e.g. sh,-c,sleep 30 && exit 0). Used in acceptance tests to keep pods alive long enough to observe.")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	metrics.RegisterMetrics()

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "kagents.amcheste.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	reconciler := &controller.AgentTeamReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		AgentImage:     agentImage,
		InitImage:      initImage,
		SkipInitScript: skipInitScript,
		Harnesses:      harness.DefaultRegistry(),
	}
	if agentCommand != "" {
		reconciler.AgentCommand = strings.Split(agentCommand, ",")
	}
	if pvcAccessMode != "" {
		reconciler.PVCAccessMode = corev1.PersistentVolumeAccessMode(pvcAccessMode)
	}
	if err = reconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AgentTeam")
		os.Exit(1)
	}

	templateReconciler := &controller.AgentTeamTemplateReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	if err = templateReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AgentTeamTemplate")
		os.Exit(1)
	}

	runReconciler := &controller.AgentTeamRunReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	if err = runReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AgentTeamRun")
		os.Exit(1)
	}

	scheduleReconciler := &controller.AgentTeamScheduleReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	if err = scheduleReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AgentTeamSchedule")
		os.Exit(1)
	}

	triggerReconciler := &controller.AgentTeamTriggerReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	if err = triggerReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AgentTeamTrigger")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
