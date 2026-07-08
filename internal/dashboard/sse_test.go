package dashboard

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// readSSEFrames consumes SSE-formatted bytes off the response body until
// either the deadline elapses or the stream ends. Returns the parsed
// frames so tests can assert on what fired.
//
// Each frame is the SSE wire-format triple: optional `event: <name>` line,
// one or more `data: ...` lines, terminated by an empty line.
func readSSEFrames(t *testing.T, body io.Reader, deadline time.Duration) []sseFrame {
	t.Helper()
	type readResult struct {
		frame sseFrame
		err   error
	}

	frames := make([]sseFrame, 0)
	doneCh := make(chan struct{})
	frameCh := make(chan sseFrame, 16)

	go func() {
		defer close(doneCh)
		buf := make([]byte, 4096)
		var pending []byte
		for {
			n, err := body.Read(buf)
			if n > 0 {
				pending = append(pending, buf[:n]...)
				for {
					sep := strings.Index(string(pending), "\n\n")
					if sep < 0 {
						break
					}
					raw := string(pending[:sep])
					pending = pending[sep+2:]
					if f, ok := parseSSEFrame(raw); ok {
						frameCh <- f
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()

	timer := time.NewTimer(deadline)
	defer timer.Stop()
	for {
		select {
		case f := <-frameCh:
			frames = append(frames, f)
		case <-timer.C:
			return frames
		case <-doneCh:
			// Drain any remaining frames before returning.
			for {
				select {
				case f := <-frameCh:
					frames = append(frames, f)
				default:
					return frames
				}
			}
		}
	}
}

type sseFrame struct {
	event string
	data  string
}

func parseSSEFrame(raw string) (sseFrame, bool) {
	var f sseFrame
	var dataLines []string
	for _, line := range strings.Split(raw, "\n") {
		switch {
		case strings.HasPrefix(line, "event: "):
			f.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		case strings.HasPrefix(line, ": "):
			// SSE comment — skip; used as a heartbeat
		}
	}
	if f.event == "" && len(dataLines) == 0 {
		return f, false
	}
	f.data = strings.Join(dataLines, "\n")
	return f, true
}

// --- /api/htmx/teams/sse (list SSE) ---

func TestSSEListHandler_EmitsHeadersAndInitialFrame(t *testing.T) {
	srv := newServer(richTeam("alpha", "ns1"))

	httpSrv := httptest.NewServer(srv.Routes())
	defer httpSrv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, httpSrv.URL+"/api/htmx/teams/sse", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))
	assert.Equal(t, "no", resp.Header.Get("X-Accel-Buffering"))

	// First frame should arrive within ~ssePollInterval (250ms) — give it
	// generous slack to avoid CI flakes.
	frames := readSSEFrames(t, resp.Body, 2*time.Second)
	require.NotEmpty(t, frames, "expected at least one SSE frame within 2s")

	first := frames[0]
	assert.Equal(t, "update", first.event)
	assert.Contains(t, first.data, "alpha", "frame body should contain the team name")
}

func TestSSEListHandler_NoFrameWhenStateUnchanged(t *testing.T) {
	// After the initial frame, state is stable — no further frames should
	// fire. Keep the connection open just past one poll interval and assert
	// only one frame arrived.
	srv := newServer(richTeam("alpha", "ns1"))
	httpSrv := httptest.NewServer(srv.Routes())
	defer httpSrv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, httpSrv.URL+"/api/htmx/teams/sse", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	frames := readSSEFrames(t, resp.Body, 2*time.Second)
	// One initial "update" frame; no follow-ups in 2 seconds of stable state.
	require.Len(t, frames, 1, "stable state should produce exactly one initial frame")
}

func TestSSEListHandler_EmitsAdditionalFrameOnTeamCreation(t *testing.T) {
	// Start with one team, mid-stream create a second one, expect a second
	// frame within a couple of poll intervals. Single readSSEFrames call
	// with the mutation triggered partway through the read window — calling
	// readSSEFrames twice on the same body races the spawned reader.
	srv := newServer(richTeam("alpha", "ns1"))
	httpSrv := httptest.NewServer(srv.Routes())
	defer httpSrv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, httpSrv.URL+"/api/htmx/teams/sse", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Trigger the cluster mutation 1s into the read window so we capture
	// both the initial frame and the post-mutation frame in one stream.
	go func() {
		time.Sleep(1 * time.Second)
		_ = srv.CRClient.Create(context.Background(), richTeam("beta", "ns1"))
	}()

	frames := readSSEFrames(t, resp.Body, 4*time.Second)
	require.GreaterOrEqual(t, len(frames), 2,
		"expected at least 2 frames (initial + post-creation), got %d", len(frames))
	combined := strings.Join(framesData(frames), "")
	assert.Contains(t, combined, "alpha")
	assert.Contains(t, combined, "beta", "frame stream must include the new team after creation")
}

// --- /api/teams/{ns}/{name}/events (detail SSE) ---

func TestSSEDetailHandler_HappyPath(t *testing.T) {
	srv := newServer(richTeam("alpha", "ns1"))
	httpSrv := httptest.NewServer(srv.Routes())
	defer httpSrv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		httpSrv.URL+"/api/teams/ns1/alpha/events", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	frames := readSSEFrames(t, resp.Body, 2*time.Second)
	require.NotEmpty(t, frames)
	assert.Equal(t, "update", frames[0].event)
	assert.Contains(t, frames[0].data, "alpha")
	assert.Contains(t, frames[0].data, "reviewer", "fragment should include teammate rows")
}

func TestSSEDetailHandler_DeletedTeamEmitsDeletedEventAndCloses(t *testing.T) {
	// Single read window covering both the initial frame and the deletion
	// (split readSSEFrames calls race the goroutine reading the body).
	srv := newServer(richTeam("alpha", "ns1"))
	httpSrv := httptest.NewServer(srv.Routes())
	defer httpSrv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		httpSrv.URL+"/api/teams/ns1/alpha/events", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Delete the team 1s into the read window so we capture initial
	// "update" + the eventual "deleted" frame in one stream.
	go func() {
		time.Sleep(1 * time.Second)
		team := &claudev1alpha1.AgentTeam{}
		_ = srv.CRClient.Get(context.Background(),
			types.NamespacedName{Name: "alpha", Namespace: "ns1"}, team)
		_ = srv.CRClient.Delete(context.Background(), team)
	}()

	frames := readSSEFrames(t, resp.Body, 4*time.Second)
	require.NotEmpty(t, frames)
	last := frames[len(frames)-1]
	assert.Equal(t, "deleted", last.event,
		"streaming a deleted team should emit a 'deleted' event before closing")
}

func TestSSEDetailHandler_BadPathReturns404(t *testing.T) {
	srv := newServer()
	rec := do(t, srv, http.MethodGet, "/api/teams/ns1/team/events/extra")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSSEDetailHandler_RejectsNonGet(t *testing.T) {
	srv := newServer(richTeam("alpha", "ns1"))
	rec := do(t, srv, http.MethodPost, "/api/teams/ns1/alpha/events")
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// --- concurrent subscribers + cleanup on disconnect ---

func TestSSEListHandler_ConcurrentSubscribersAllReceive(t *testing.T) {
	srv := newServer(richTeam("alpha", "ns1"))
	httpSrv := httptest.NewServer(srv.Routes())
	defer httpSrv.Close()

	const subscribers = 5
	var got int32
	var wg sync.WaitGroup

	for i := 0; i < subscribers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
				httpSrv.URL+"/api/htmx/teams/sse", nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			frames := readSSEFrames(t, resp.Body, 2*time.Second)
			if len(frames) > 0 {
				atomic.AddInt32(&got, 1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(subscribers), atomic.LoadInt32(&got),
		"every concurrent subscriber should receive at least one frame")
}

func TestSSEListHandler_ClientDisconnectStopsHandlerCleanly(t *testing.T) {
	// The handler's poll loop is gated by request context cancellation, so
	// closing the connection client-side must terminate the goroutine.
	// Without this, a tab-close storm leaks goroutines forever.
	//
	// We can't observe goroutine count directly without runtime tricks, so
	// the proof is: the handler returns from ServeHTTP within the request
	// timeout. If it leaked, the httptest server's Close would block
	// indefinitely.
	srv := newServer(richTeam("alpha", "ns1"))
	httpSrv := httptest.NewServer(srv.Routes())

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		httpSrv.URL+"/api/htmx/teams/sse", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	_ = readSSEFrames(t, resp.Body, 1*time.Second) // consume initial frame
	cancel()
	resp.Body.Close()

	// The Close call below would hang if the handler goroutine were stuck.
	// We give it a generous 5s window.
	closed := make(chan struct{})
	go func() {
		httpSrv.Close()
		close(closed)
	}()
	select {
	case <-closed:
		// good
	case <-time.After(5 * time.Second):
		t.Fatal("httptest server failed to close in 5s — handler likely leaked")
	}
}

// --- writeSSEFrame + hashString unit tests ---

func TestWriteSSEFrame_FormatsPerSpec(t *testing.T) {
	rec := httptest.NewRecorder()
	flusher := dummyFlusher{rec}
	writeSSEFrame(rec, flusher, "update", "line1\nline2\nline3")

	out := rec.Body.String()
	// Each data line gets its own `data: ` prefix. Frame ends with blank line.
	assert.Contains(t, out, "event: update\n")
	assert.Contains(t, out, "data: line1\n")
	assert.Contains(t, out, "data: line2\n")
	assert.Contains(t, out, "data: line3\n")
	assert.True(t, strings.HasSuffix(out, "\n\n"), "frame must end with empty line")
}

func TestHashString_StableAcrossCalls(t *testing.T) {
	h1 := hashString("hello")
	h2 := hashString("hello")
	assert.Equal(t, h1, h2)
	assert.NotEqual(t, h1, hashString("hello!"))
}

// dummyFlusher pairs a ResponseRecorder with a no-op Flush so writeSSEFrame
// can call its flusher.Flush(). httptest.ResponseRecorder doesn't implement
// http.Flusher on its own.
type dummyFlusher struct{ *httptest.ResponseRecorder }

func (dummyFlusher) Flush() {}

// framesData concatenates the data bodies of multiple SSE frames into a
// single string for substring assertions.
func framesData(frames []sseFrame) []string {
	out := make([]string, 0, len(frames))
	for _, f := range frames {
		out = append(out, f.data)
	}
	return out
}

// Quiet unused-import warning if a future refactor drops the symbol.
var _ = fmt.Sprintf
var _ client.Client
