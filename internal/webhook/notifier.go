package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// DefaultTimeout bounds a single webhook POST. A slow webhook must never stall
// the reconciler, so deliveries run in a background goroutine with this
// context deadline.
const DefaultTimeout = 5 * time.Second

// Notifier posts JSON event payloads to a configured webhook URL. The zero
// value is not usable — construct via NewNotifier. A nil *Notifier is safe to
// call SendEvent on and treats every call as a no-op, which lets controller
// code emit events unconditionally when no webhook is configured.
type Notifier struct {
	url    string
	events map[string]struct{}
	client *http.Client

	// inFlight lets tests wait for background POSTs to drain without exposing
	// the internal goroutine to production callers. Production callers never
	// need to wait; the runtime eats goroutine leaks at process exit.
	inFlight sync.WaitGroup
}

// NewNotifier returns a Notifier configured to POST to url for the given event
// types. Events not in the list are silently dropped by SendEvent. Returns nil
// when url is empty so callers can unconditionally construct a notifier from
// an optional CRD field and rely on the nil-safe SendEvent.
func NewNotifier(url string, events []string) *Notifier {
	if url == "" {
		return nil
	}
	set := make(map[string]struct{}, len(events))
	for _, e := range events {
		set[e] = struct{}{}
	}
	return &Notifier{
		url:    url,
		events: set,
		client: &http.Client{Timeout: DefaultTimeout},
	}
}

// SendEvent dispatches an event to the configured webhook. It wraps payload in
// the standard envelope `{event, timestamp, ...payload}` and POSTs the result
// as JSON.
//
// Delivery happens in a background goroutine with a short timeout so a slow
// webhook cannot stall the reconciler. SendEvent returns before the POST
// completes; delivery errors are logged against the caller's context logger
// and not returned.
//
// SendEvent returns synchronously only when the payload cannot be marshaled
// (programmer error) or the event type is not subscribed (no-op, returns nil).
// A nil receiver returns nil immediately.
func (n *Notifier) SendEvent(ctx context.Context, eventType string, payload map[string]interface{}) error {
	if n == nil {
		return nil
	}
	if _, subscribed := n.events[eventType]; !subscribed {
		return nil
	}

	envelope := make(map[string]interface{}, len(payload)+2)
	for k, v := range payload {
		envelope[k] = v
	}
	envelope["event"] = eventType
	envelope["timestamp"] = time.Now().UTC().Format(time.RFC3339)

	body, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	logger := log.FromContext(ctx).WithValues("webhook", n.url, "event", eventType)

	n.inFlight.Add(1)
	go func() {
		defer n.inFlight.Done()
		n.post(logger, body)
	}()
	return nil
}

func (n *Notifier) post(logger logr.Logger, body []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader(body))
	if err != nil {
		logger.Error(err, "build webhook request")
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		logger.Error(err, "webhook POST failed")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		logger.Error(fmt.Errorf("status %d", resp.StatusCode), "webhook returned non-2xx")
	}
}
