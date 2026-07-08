// Package dashboard implements the read-only HTTP API that backs the
// kagents web UI. The API exposes AgentTeam list/detail,
// pod log streaming, and a health endpoint. Live-update SSE will land in
// the v0.6.0 follow-up (#139) — the Server type already carries a context
// so adding it doesn't require an API rewrite.
//
// The dashboard ships as a separate binary (cmd/dashboard) running in its
// own pod with its own ServiceAccount, narrowly scoped to read AgentTeam
// CRs and Pods + Pods/log. It never mutates anything.
package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// Server hosts the dashboard HTTP API. CRClient reads CRDs (AgentTeam,
// AgentTeamRun, AgentTeamTemplate); KubeClient is used for pod log streaming
// because controller-runtime's client doesn't expose the Pods/log subresource.
//
// Both clients are swappable in tests via the exported fields.
type Server struct {
	// CRClient reads kagents.dev/v1alpha1 resources.
	CRClient client.Client

	// KubeClient streams pod logs via the typed clientset. Optional — when
	// nil, the /logs endpoint returns 503 instead of panicking. Lets unit
	// tests stand up a Server without faking the entire clientset.
	KubeClient kubernetes.Interface

	// Namespace, when non-empty, scopes List operations to a single
	// namespace. Empty string means cluster-scoped (all namespaces).
	Namespace string
}

// Routes returns the HTTP handler ready for ListenAndServe. The mux is
// rebuilt every call so changes to Server fields take effect on the next
// request — convenient for tests.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.healthz)
	mux.HandleFunc("/api/teams", s.listTeams)
	mux.HandleFunc("/api/teams/", s.routeTeamDetail) // covers /{ns}/{name} and /{ns}/{name}/logs/{agent}

	// HTML views (server-rendered HTMX). Same data as /api/teams/* but as
	// HTML pages with `hx-trigger="every 5s"` polling baked in.
	mux.HandleFunc("/", s.routeRoot) // / → list view; anything else → 404
	mux.HandleFunc("/teams/", s.routeDetailHTML)

	// HTMX fragment endpoints. Return JUST the table body / detail body so
	// the polling swaps don't reload the entire page.
	mux.HandleFunc("/api/htmx/teams", s.htmxListRows)
	mux.HandleFunc("/api/htmx/teams/", s.htmxDetailBody)

	// Server-Sent Events. Both endpoints emit fragment HTML on state
	// changes — HTMX's sse extension renders them via sse-swap. The
	// detail SSE handler is registered on /api/teams/ and dispatches
	// internally, sharing prefix space with the JSON detail/log routes.
	mux.HandleFunc("/api/htmx/teams/sse", s.sseListHandler)
	// detail SSE matches /api/teams/{ns}/{name}/events; routeTeamDetail
	// already owns /api/teams/, so the SSE path is dispatched from inside
	// it via routeTeamDetail's path-parts switch.
	return mux
}

// healthz is the liveness probe. Returns 200 with a tiny JSON body whenever
// the process is up and able to handle HTTP. Does not test cluster
// connectivity — that's a deliberate split, mirroring the operator's own
// healthz/readyz separation.
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// listTeams handles GET /api/teams. Lists every AgentTeam visible to the
// dashboard's ServiceAccount. Honors a `namespace` query parameter that
// overrides the Server's default namespace when set.
func (s *Server) listTeams(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ctx := r.Context()
	logger := log.FromContext(ctx)

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = s.Namespace
	}

	var teams claudev1alpha1.AgentTeamList
	opts := []client.ListOption{}
	if ns != "" {
		opts = append(opts, client.InNamespace(ns))
	}
	if err := s.CRClient.List(ctx, &teams, opts...); err != nil {
		logger.Error(err, "list AgentTeams")
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("listing teams: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, teams.Items)
}

// routeTeamDetail dispatches /api/teams/{ns}/{name} and
// /api/teams/{ns}/{name}/logs/{agent} to the right handler. Hand-rolled
// because net/http's mux doesn't support path parameters and adding a
// router dependency would inflate the dashboard's image for one route.
func (s *Server) routeTeamDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Trim "/api/teams/" prefix and split — expecting one of:
	//   {ns}/{name}                     → team detail
	//   {ns}/{name}/events              → SSE state stream
	//   {ns}/{name}/logs/{agent}        → pod log stream
	rest := strings.TrimPrefix(r.URL.Path, "/api/teams/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	switch len(parts) {
	case 2:
		s.teamDetail(w, r, parts[0], parts[1])
	case 3:
		if parts[2] != "events" {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		s.sseDetailHandler(w, r)
	case 4:
		if parts[2] != "logs" {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		s.streamLogs(w, r, parts[0], parts[1], parts[3])
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

// teamDetail handles GET /api/teams/{ns}/{name}. Returns the full
// AgentTeam object including spec + status. Returns 404 when the team
// doesn't exist.
func (s *Server) teamDetail(w http.ResponseWriter, r *http.Request, ns, name string) {
	ctx := r.Context()
	logger := log.FromContext(ctx)

	var team claudev1alpha1.AgentTeam
	if err := s.CRClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, &team); err != nil {
		if errors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("AgentTeam %s/%s not found", ns, name))
			return
		}
		logger.Error(err, "get AgentTeam", "ns", ns, "name", name)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("getting team: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, team)
}

// streamLogs handles GET /api/teams/{ns}/{name}/logs/{agent} by streaming
// the named agent's pod logs back to the client via chunked transfer.
//
// The response is text/plain; the operator's runner image already emits
// structured JSON if you want to parse it client-side. ?follow=true tails
// the logs until the client disconnects.
//
// Pod naming follows the operator's convention: <team>-<agent>. A stale
// pod name (deleted before logs are read) returns 404 instead of 500 so
// the UI doesn't show a stack trace for a routine race.
func (s *Server) streamLogs(w http.ResponseWriter, r *http.Request, ns, team, agent string) {
	ctx := r.Context()
	logger := log.FromContext(ctx)

	if s.KubeClient == nil {
		writeError(w, http.StatusServiceUnavailable, "log streaming not configured (KubeClient is nil)")
		return
	}

	// The dashboard intentionally doesn't validate that the AgentTeam
	// exists before streaming — that would double-RTT every log request.
	// The pod-not-found check below catches dangling cases.
	podName := team + "-" + agent
	follow := r.URL.Query().Get("follow") == "true"

	req := s.KubeClient.CoreV1().Pods(ns).GetLogs(podName, &corev1.PodLogOptions{
		Follow:    follow,
		Container: "claude-code",
	})

	stream, err := req.Stream(ctx)
	if err != nil {
		if errors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("pod %s/%s not found", ns, podName))
			return
		}
		logger.Error(err, "open pod log stream", "ns", ns, "pod", podName)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("opening log stream: %v", err))
		return
	}
	defer stream.Close()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	// Flush per chunk so the client sees output as it arrives instead of
	// after the buffer fills.
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, err := stream.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return // client disconnected
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return // EOF or stream error — both terminate the response cleanly
		}
	}
}

// --- HTTP helpers ---

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// joinPath is exported for symmetry with future route additions.
var _ = path.Join

// Tick is the interval at which the dashboard refreshes its in-memory
// state in future SSE work (#139). Reserved here so the value lives in
// one place once that lands.
const Tick = 2 * time.Second
