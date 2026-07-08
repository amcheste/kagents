package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
	"github.com/amcheste/kagents/internal/dashboard"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(claudev1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		listenAddr string
		namespace  string
	)
	flag.StringVar(&listenAddr, "listen", ":8080", "HTTP listen address.")
	flag.StringVar(&namespace, "namespace", "", "Restrict the dashboard to one namespace. Empty = all namespaces visible to the SA.")

	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	log := ctrl.Log.WithName("dashboard")

	cfg := ctrl.GetConfigOrDie()

	crClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Error(err, "build CR client")
		os.Exit(1)
	}

	// Typed clientset is needed only for the Pods/log subresource. It
	// lives alongside the controller-runtime client rather than replacing
	// it because the latter has cleaner CRD ergonomics.
	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Error(err, "build typed clientset")
		os.Exit(1)
	}

	srv := &dashboard.Server{
		CRClient:   crClient,
		KubeClient: kubeClient,
		Namespace:  namespace,
	}

	httpSrv := &http.Server{
		Addr:              listenAddr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		// No write timeout — log streams are long-lived by design.
	}

	// Shutdown plumbing: SIGTERM/SIGINT triggers a graceful drain.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		log.Info("dashboard listening", "addr", listenAddr, "namespace", namespace)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error(err, "ListenAndServe")
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	log.Info("shutdown signal received, draining connections")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Error(err, "graceful shutdown failed")
		os.Exit(1)
	}
}
