package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// SlackSender posts a formatted notification to Slack via a webhook URL
// the operator pulls from the team's CredentialsSecret at dispatch time
// (key: "slack-webhook-url"). The operator never sees the webhook URL
// at rest — it's loaded only when a delivery fires.
type SlackSender struct {
	// Client is the HTTP client used. Tests inject a server-rooted
	// client; production leaves it nil and a default is used.
	Client *http.Client
}

// slackPayload is the minimum Slack webhook payload shape: text +
// optional channel override. Slack ignores channel when the webhook URL
// is already channel-scoped; including it is harmless and helps when
// the workspace uses a generic incoming-webhook integration.
type slackPayload struct {
	Text    string `json:"text"`
	Channel string `json:"channel,omitempty"`
}

// Send fetches the Slack webhook URL from CredentialsSecret and POSTs
// the formatted message. A non-2xx response is treated as failure.
func (s *SlackSender) Send(ctx context.Context, c client.Client, target claudev1alpha1.DeliveryTarget, team *claudev1alpha1.AgentTeam) error {
	if target.CredentialsSecret == "" {
		return fmt.Errorf("slack delivery requires credentialsSecret")
	}

	var secret corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Name: target.CredentialsSecret, Namespace: team.Namespace}, &secret); err != nil {
		return fmt.Errorf("loading credentials %s: %w", target.CredentialsSecret, err)
	}
	webhookURL := string(secret.Data["slack-webhook-url"])
	if webhookURL == "" {
		return fmt.Errorf("secret %s is missing key %q", target.CredentialsSecret, "slack-webhook-url")
	}

	body, err := json.Marshal(slackPayload{
		Text:    formatSlackMessage(target, team),
		Channel: target.Channel,
	})
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	httpClient := s.Client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
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
		return fmt.Errorf("slack returned %s", resp.Status)
	}
	return nil
}

// formatSlackMessage builds a one-line summary plus an artifact list.
// Slack ignores Markdown in text by default; the asterisks below render
// as bold in mrkdwn-mode webhooks and as literal characters otherwise.
func formatSlackMessage(target claudev1alpha1.DeliveryTarget, team *claudev1alpha1.AgentTeam) string {
	msg := target.Message
	if msg == "" {
		msg = fmt.Sprintf("Team *%s* finished with phase *%s*.", team.Name, team.Status.Phase)
	}
	if len(team.Status.Artifacts) == 0 {
		return msg
	}
	buf := bytes.NewBufferString(msg)
	buf.WriteString("\nArtifacts:\n")
	for _, a := range team.Status.Artifacts {
		fmt.Fprintf(buf, "• %s (by %s)\n", a.Name, a.ProducedBy)
	}
	return buf.String()
}
