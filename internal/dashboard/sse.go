package dashboard

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// ssePollInterval bounds how often the SSE handler re-fetches state and
// fires a frame. 250ms gives the dashboard a "near-instant" feel without
// hammering the K8s API — at one viewer per minute the load is 240 list
// calls/min, well under the cluster's read budget. The matching
// `hx-trigger="every 5s"` on the polling templates was 12 calls/min;
// SSE is more chatty but only when something is actually moving (the
// state-hash gate below skips emit when nothing changed).
const ssePollInterval = 250 * time.Millisecond

// sseHeartbeat keeps the connection alive across proxies that idle-close
// silent streams. Sent as an SSE comment so HTMX doesn't dispatch an event.
const sseHeartbeat = 15 * time.Second

// sseDetailHandler streams team state to a single client. The handler holds
// the connection open until either the client disconnects or the team's
// observed state stops changing for the request lifetime.
//
// State changes are detected by hashing the user-visible fields
// (phase, ready, tasks, teammate phases / restart counts, conditions);
// frames are only emitted on hash transitions. ResourceVersion alone is
// insufficient because a teammate Pod phase change doesn't bump the
// AgentTeam's RV.
func (s *Server) sseDetailHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/teams/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 3 || parts[2] != "events" || parts[0] == "" || parts[1] == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	ns, name := parts[0], parts[1]

	flusher, err := beginSSE(w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	logger := log.FromContext(r.Context()).WithValues("ns", ns, "team", name)
	logger.V(1).Info("SSE detail subscriber connected")
	defer logger.V(1).Info("SSE detail subscriber disconnected")

	streamUntilDone(r.Context(), flusher, w, func() (string, error) {
		return s.renderDetailFrame(r.Context(), ns, name)
	})
}

// sseListHandler streams aggregate team-list state. Same hash-and-emit
// model as the detail handler; emits a fresh <tbody> fragment so HTMX
// can swap it directly into the list page.
func (s *Server) sseListHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, err := beginSSE(w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	logger := log.FromContext(r.Context())
	logger.V(1).Info("SSE list subscriber connected")
	defer logger.V(1).Info("SSE list subscriber disconnected")

	streamUntilDone(r.Context(), flusher, w, func() (string, error) {
		return s.renderListFrame(r)
	})
}

// streamUntilDone runs the poll loop. It calls render() every
// ssePollInterval, computes a hash of the rendered output, and writes a
// frame on transition. Emits a heartbeat comment every sseHeartbeat to
// survive proxy idle timeouts.
//
// Exits when the request context cancels (client disconnect) or render
// returns a permanent error (e.g. team deleted mid-stream — we report it
// once and bail rather than spinning).
func streamUntilDone(ctx context.Context, flusher http.Flusher, w io.Writer, render func() (string, error)) {
	ticker := time.NewTicker(ssePollInterval)
	defer ticker.Stop()

	heartbeat := time.NewTicker(sseHeartbeat)
	defer heartbeat.Stop()

	var lastHash uint64
	first := true

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			// SSE comment — HTMX ignores; proxies see traffic.
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			body, err := render()
			if err != nil {
				if errors.IsNotFound(err) {
					// Team gone. Tell the client so the UI can show a
					// "deleted" state, then close the stream.
					writeSSEFrame(w, flusher, "deleted", "team no longer exists")
					return
				}
				// Transient — log via the wider system, keep streaming
				// rather than killing a session over a flaky read.
				continue
			}
			h := hashString(body)
			if !first && h == lastHash {
				continue
			}
			lastHash = h
			first = false
			writeSSEFrame(w, flusher, "update", body)
		}
	}
}

// renderDetailFrame fetches the team and renders the detail body fragment.
// Returns the rendered HTML so the caller can hash + emit.
func (s *Server) renderDetailFrame(ctx context.Context, ns, name string) (string, error) {
	var team claudev1alpha1.AgentTeam
	if err := s.CRClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, &team); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := renderFragment(&buf, "detail_body", &team); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// renderListFrame respects the same namespace-scoping precedence as the
// JSON list endpoint, so a viewer with a `?namespace=` query string in
// their URL sees their preferred scope reflected over SSE.
func (s *Server) renderListFrame(r *http.Request) (string, error) {
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = s.Namespace
	}
	var teams claudev1alpha1.AgentTeamList
	opts := []client.ListOption{}
	if ns != "" {
		opts = append(opts, client.InNamespace(ns))
	}
	if err := s.CRClient.List(r.Context(), &teams, opts...); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := renderFragment(&buf, "list_rows", teams.Items); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// beginSSE writes the SSE response headers and returns the underlying
// Flusher. Errors when the response writer doesn't support flushing —
// shouldn't happen with net/http, but checking up front beats a silent
// non-streaming response.
func beginSSE(w http.ResponseWriter) (http.Flusher, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming unsupported (no http.Flusher)")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Helps proxies that buffer otherwise — Nginx in particular needs this.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	return flusher, nil
}

// writeSSEFrame emits a single SSE event. The data field can contain
// embedded newlines — SSE protocol requires each line to be prefixed with
// "data: ", so we split and rewrite.
func writeSSEFrame(w io.Writer, flusher http.Flusher, eventName, payload string) {
	fmt.Fprintf(w, "event: %s\n", eventName)
	for _, line := range strings.Split(payload, "\n") {
		fmt.Fprintf(w, "data: %s\n", line)
	}
	fmt.Fprint(w, "\n") // empty line ends the frame
	flusher.Flush()
}

// hashString returns a stable 64-bit FNV hash of s. Used to detect
// rendered-output changes between polls without retaining the prior
// rendered string in memory.
func hashString(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}
