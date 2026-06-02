package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// WebhookSender POSTs a JSON metadata envelope to a configured URL.
// The payload is intentionally small — team identity, artifacts, the
// message — because the operator pod doesn't read artifact file bytes
// (see internal/delivery/delivery.go package doc). Downstream systems
// that want file contents are expected to fetch from persisted storage.
type WebhookSender struct {
	// Client is the HTTP client used. Tests inject a server-rooted
	// client; production leaves it nil and a default is used.
	Client *http.Client
}

// webhookPayload is the JSON shape the webhook receiver gets. Fields
// are intentionally stable (and snake_case for cross-language consumers)
// so downstream automation can rely on this contract.
type webhookPayload struct {
	Event     string                          `json:"event"`
	Team      string                          `json:"team"`
	Namespace string                          `json:"namespace"`
	Phase     string                          `json:"phase"`
	Message   string                          `json:"message,omitempty"`
	Artifacts []claudev1alpha1.ArtifactStatus `json:"artifacts,omitempty"`
}

// Send POSTs the payload as application/json. A non-2xx response is
// treated as failure and surfaces in status.delivery[].
func (w *WebhookSender) Send(ctx context.Context, _ client.Client, target claudev1alpha1.DeliveryTarget, team *claudev1alpha1.AgentTeam) error {
	if target.URL == "" {
		return fmt.Errorf("webhook delivery requires url")
	}
	body, err := json.Marshal(webhookPayload{
		Event:     "team.completed",
		Team:      team.Name,
		Namespace: team.Namespace,
		Phase:     team.Status.Phase,
		Message:   target.Message,
		Artifacts: team.Status.Artifacts,
	})
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	httpClient := w.Client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "kagents/0.8")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %s", resp.Status)
	}
	return nil
}
