// Package delivery dispatches team-completion notifications to external
// systems (webhook, Slack, email, Google Drive) when an AgentTeam ends
// with OnComplete=deliver.
//
// The senders implemented here run in-process from the reconciler — fast,
// simple HTTPS requests with metadata about the completed team. They do
// NOT stream artifact file contents: the operator pod doesn't mount the
// team's output PVC, so transferring files end-to-end would require a
// Job pattern (designed for, but deferred). The MVP delivers notifications
// referencing the team's artifacts; downstream systems are expected to
// fetch the actual files from their persisted location.
//
// New backends register in NewDispatcher() — keep the surface small
// (Send is the only interface method) so adding one is contained.
package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// ErrNotImplemented is returned by senders whose backend is declared in
// the API but not yet wired. Status records it as a per-target failure
// rather than failing the whole team — this is exactly the partial-
// success path the design doc calls out.
var ErrNotImplemented = errors.New("delivery type not yet implemented")

// Sender dispatches a single DeliveryTarget. Implementations should
// return nil on success and a descriptive error on failure; the
// reconciler records the failure in status.delivery[] and does not
// surface it as a team-level failure.
type Sender interface {
	Send(ctx context.Context, c client.Client, target claudev1alpha1.DeliveryTarget, team *claudev1alpha1.AgentTeam) error
}

// SenderFunc adapts a plain function to the Sender interface — useful
// for tests that want to inject a captured-call recorder without
// declaring a new type.
type SenderFunc func(ctx context.Context, c client.Client, target claudev1alpha1.DeliveryTarget, team *claudev1alpha1.AgentTeam) error

// Send implements Sender.
func (f SenderFunc) Send(ctx context.Context, c client.Client, target claudev1alpha1.DeliveryTarget, team *claudev1alpha1.AgentTeam) error {
	return f(ctx, c, target, team)
}

// Dispatcher selects the right Sender for a given DeliveryTarget.Type.
// One Dispatcher is shared across the reconciler for the lifetime of
// the operator; tests construct their own with stub senders.
type Dispatcher struct {
	senders map[string]Sender
}

// NewDispatcher returns a Dispatcher wired with the production senders.
// Email and Google Drive are present at the API level but currently
// return ErrNotImplemented — operators see a per-target failure in
// status.delivery rather than a runtime panic.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		senders: map[string]Sender{
			"webhook":      &WebhookSender{},
			"slack":        &SlackSender{},
			"email":        notImplemented("email"),
			"google-drive": notImplemented("google-drive"),
		},
	}
}

// SetSender overrides the sender for a single type. Tests use this to
// inject a recorder without rebuilding the whole dispatcher.
func (d *Dispatcher) SetSender(typ string, s Sender) {
	if d.senders == nil {
		d.senders = make(map[string]Sender)
	}
	d.senders[typ] = s
}

// Send dispatches the target to its matching Sender. Returns
// ErrNotImplemented when no sender is registered for the type, so the
// reconciler can record a clean failure rather than silently dropping.
func (d *Dispatcher) Send(ctx context.Context, c client.Client, target claudev1alpha1.DeliveryTarget, team *claudev1alpha1.AgentTeam) error {
	s, ok := d.senders[target.Type]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotImplemented, target.Type)
	}
	return s.Send(ctx, c, target, team)
}

// TargetLabel produces a short human-readable description for status
// reporting (rendered into DeliveryStatus.Target). Backend-specific:
// the URL for webhook, the channel for slack, the To list for email,
// the folder for drive.
func TargetLabel(t claudev1alpha1.DeliveryTarget) string {
	switch t.Type {
	case "webhook":
		return t.URL
	case "slack":
		return t.Channel
	case "email":
		return strings.Join(t.To, ", ")
	case "google-drive":
		return t.Folder
	}
	return ""
}

// notImplemented returns a Sender that always fails with ErrNotImplemented
// wrapped to include the type name. Used as the registry entry for
// declared-but-unimplemented backends.
func notImplemented(typ string) Sender {
	return SenderFunc(func(ctx context.Context, c client.Client, target claudev1alpha1.DeliveryTarget, team *claudev1alpha1.AgentTeam) error {
		return fmt.Errorf("%w: %s", ErrNotImplemented, typ)
	})
}
