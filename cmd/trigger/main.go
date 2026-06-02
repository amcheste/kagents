// Command kagents-trigger is the HTTP webhook listener that fires
// AgentTeamRun resources in response to incoming events matching
// AgentTeamTrigger CRDs.
//
// The listener intentionally runs as its own Deployment — separate
// from the operator manager — because it's an internet-reachable
// surface that doesn't belong in the leader-elected controller pod.
// See docs/knowledge-work-design.md §II.4.
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
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
	"github.com/amcheste/kagents/internal/trigger"
)

var (
	scheme = runtime.NewScheme()
	log    = ctrl.Log.WithName("kagents-trigger")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(claudev1alpha1.AddToScheme(scheme))
}

func main() {
	var addr string
	flag.StringVar(&addr, "listen-address", ":8090", "HTTP listen address for incoming webhooks.")
	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Build a client without a cache so the listener doesn't have to wait
	// for cache sync at startup. Trigger volume is expected to be low
	// (one List per webhook) and this avoids coupling the listener's
	// readiness to the broader cluster's CRD bootstrap state.
	cfg, err := ctrl.GetConfig()
	if err != nil {
		log.Error(err, "could not load kubeconfig")
		os.Exit(1)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Error(err, "could not build client")
		os.Exit(1)
	}

	handler := &trigger.Handler{Client: c, Now: time.Now}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.Handle("/", handler)

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		log.Info("kagents-trigger listening", "addr", addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error(err, "listen failed")
			cancel()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")

	// Drain in-flight requests with a bounded timeout.
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	if err := server.Shutdown(shutCtx); err != nil {
		log.Error(err, "shutdown failed")
	}
}
