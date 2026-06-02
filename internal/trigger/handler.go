// Package trigger implements the kagents-trigger HTTP listener that
// converts incoming webhook events into AgentTeamRun resources.
//
// The listener runs as its own Deployment (kagents-trigger) — separate
// from the operator manager — because it's an internet-reachable
// surface that doesn't belong in the leader-elected controller pod.
// See docs/knowledge-work-design.md §II.4 for the design discussion.
package trigger

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

const (
	// triggerLabel is the AgentTeamRun label this listener stamps so the
	// reconciler can find runs by owning trigger.
	triggerLabel = "kagents.dev/trigger"

	// payloadConfigMapKey is the data key under which the listener stores
	// the request body inside the per-fire ConfigMap.
	payloadConfigMapKey = "payload.json"

	// signatureHeader matches GitHub's convention (other vendors that
	// support HMAC tend to follow the same shape).
	signatureHeader = "X-Hub-Signature-256"

	// maxBodyBytes caps the accepted webhook body to keep a misbehaving
	// upstream from exhausting memory or storage. 1 MiB is more than
	// every CI/PagerDuty/Linear webhook this listener will realistically
	// see and small enough to comfortably fit a ConfigMap (1 MiB hard limit).
	maxBodyBytes = 1 << 20
)

// Handler is the kagents-trigger HTTP handler. It serves on the
// configured TriggerSource.Webhook.Path of each AgentTeamTrigger CRD.
// One Handler instance handles all triggers cluster-wide.
type Handler struct {
	// Client is a controller-runtime client with the kagents.dev scheme
	// registered. Used to list triggers, look up the optional HMAC
	// Secret, and create payload ConfigMaps + AgentTeamRuns.
	Client client.Client

	// Now is the clock used for run-naming / status timestamps. Tests
	// inject a fixed clock; production passes time.Now.
	Now func() time.Time
}

// ServeHTTP implements http.Handler. The routing path is encoded as part
// of the URL: GET /healthz returns 200; everything else is treated as a
// webhook fire matched by the request path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/healthz":
		w.WriteHeader(http.StatusOK)
		return
	case r.Method != http.MethodPost:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		http.Error(w, "could not read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(body) > maxBodyBytes {
		http.Error(w, fmt.Sprintf("body exceeds %d bytes", maxBodyBytes), http.StatusRequestEntityTooLarge)
		return
	}

	trig, err := h.findTrigger(ctx, r.URL.Path)
	if err != nil {
		log.FromContext(ctx).Error(err, "lookup failed", "path", r.URL.Path)
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	if trig == nil {
		http.Error(w, "no trigger for path", http.StatusNotFound)
		return
	}

	// HMAC validation (skipped when no Secret is configured — documented
	// as ingress-trusted in WebhookTriggerSpec).
	if trig.Spec.Trigger.Webhook != nil && trig.Spec.Trigger.Webhook.Secret != "" {
		if err := h.verifySignature(ctx, trig, body, r.Header.Get(signatureHeader)); err != nil {
			log.FromContext(ctx).Info("rejecting webhook on bad signature", "trigger", trig.Name, "err", err)
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
	}

	// Concurrency policy. Allow (default) always proceeds.
	switch trig.Spec.ConcurrencyPolicy {
	case "Forbid":
		if trig.Status.ActiveRun != "" {
			http.Error(w, "previous run still active", http.StatusConflict)
			return
		}
	case "Replace":
		if trig.Status.ActiveRun != "" {
			if err := h.deleteRun(ctx, trig.Namespace, trig.Status.ActiveRun); err != nil && !apierrors.IsNotFound(err) {
				log.FromContext(ctx).Error(err, "could not delete prior run", "name", trig.Status.ActiveRun)
				http.Error(w, "could not replace active run", http.StatusInternalServerError)
				return
			}
		}
	}

	runName := h.runName(trig)
	if err := h.createRun(ctx, trig, runName, body); err != nil {
		log.FromContext(ctx).Error(err, "could not create run", "trigger", trig.Name)
		http.Error(w, "could not create run: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = io.WriteString(w, fmt.Sprintf(`{"run":%q,"trigger":%q,"namespace":%q}`,
		runName, trig.Name, trig.Namespace))
}

// findTrigger scans every AgentTeamTrigger cluster-wide for one whose
// configured webhook path matches the incoming request. The first
// match wins; operators are expected to keep webhook paths unique
// across namespaces (the kagents-trigger listener is cluster-scoped).
func (h *Handler) findTrigger(ctx context.Context, path string) (*claudev1alpha1.AgentTeamTrigger, error) {
	var triggers claudev1alpha1.AgentTeamTriggerList
	if err := h.Client.List(ctx, &triggers); err != nil {
		return nil, err
	}
	for i := range triggers.Items {
		t := &triggers.Items[i]
		if t.Spec.Trigger.Webhook == nil {
			continue
		}
		if t.Spec.Trigger.Webhook.Path == path {
			return t, nil
		}
	}
	return nil, nil
}

// verifySignature checks the X-Hub-Signature-256 header against an
// HMAC-SHA256 of the request body, keyed by the secret stored in the
// trigger's referenced Secret resource (key: "hmac-secret").
//
// Constant-time comparison is used so an attacker can't probe the
// secret with timing-based oracles.
func (h *Handler) verifySignature(ctx context.Context, trig *claudev1alpha1.AgentTeamTrigger, body []byte, headerValue string) error {
	if headerValue == "" {
		return errors.New("missing signature header")
	}
	if !strings.HasPrefix(headerValue, "sha256=") {
		return errors.New("unsupported signature scheme")
	}
	want, err := hex.DecodeString(strings.TrimPrefix(headerValue, "sha256="))
	if err != nil {
		return fmt.Errorf("malformed signature hex: %w", err)
	}

	secretName := trig.Spec.Trigger.Webhook.Secret
	var secret corev1.Secret
	if err := h.Client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: trig.Namespace}, &secret); err != nil {
		return fmt.Errorf("loading secret %s: %w", secretName, err)
	}
	keyBytes := secret.Data["hmac-secret"]
	if len(keyBytes) == 0 {
		return fmt.Errorf("secret %s is missing data key %q", secretName, "hmac-secret")
	}

	mac := hmac.New(sha256.New, keyBytes)
	mac.Write(body)
	got := mac.Sum(nil)
	if !hmac.Equal(got, want) {
		return errors.New("signature mismatch")
	}
	return nil
}

// deleteRun removes a named AgentTeamRun. Used by the Replace
// ConcurrencyPolicy to clear the in-flight run before firing a new one.
func (h *Handler) deleteRun(ctx context.Context, namespace, name string) error {
	run := &claudev1alpha1.AgentTeamRun{}
	if err := h.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, run); err != nil {
		return err
	}
	return h.Client.Delete(ctx, run)
}

// createRun creates the per-fire payload ConfigMap and the AgentTeamRun
// in a single sequence. If the ConfigMap already exists from a prior
// attempt, the listener treats AlreadyExists as success and proceeds.
// The Run's workspace.inputs is extended with the payload mount so the
// agent pods see the request body at PayloadInjection.MountPath.
func (h *Handler) createRun(ctx context.Context, trig *claudev1alpha1.AgentTeamTrigger, runName string, body []byte) error {
	// Create the payload ConfigMap when PayloadInjection is configured.
	var payloadCMName string
	if trig.Spec.PayloadInjection != nil {
		payloadCMName = runName + "-payload"
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      payloadCMName,
				Namespace: trig.Namespace,
				Labels:    map[string]string{triggerLabel: trig.Name},
			},
			Data: map[string]string{payloadConfigMapKey: string(body)},
		}
		if err := h.Client.Create(ctx, cm); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating payload ConfigMap: %w", err)
		}
	}

	workspace := trig.Spec.Workspace
	if trig.Spec.PayloadInjection != nil {
		// Splice the payload mount into the run's workspace inputs.
		// Copy first so we don't mutate the trigger's spec.
		base := claudev1alpha1.WorkspaceSpec{}
		if workspace != nil {
			base = *workspace
		}
		mountDir, _ := splitMountDir(trig.Spec.PayloadInjection.MountPath)
		base.Inputs = append(base.Inputs, claudev1alpha1.WorkspaceInputSpec{
			ConfigMap: payloadCMName,
			MountPath: mountDir,
		})
		workspace = &base
	}

	run := &claudev1alpha1.AgentTeamRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      runName,
			Namespace: trig.Namespace,
			Labels:    map[string]string{triggerLabel: trig.Name},
		},
		Spec: claudev1alpha1.AgentTeamRunSpec{
			TemplateRef: trig.Spec.TemplateRef,
			Repository:  trig.Spec.Repository,
			Workspace:   workspace,
			Auth:        trig.Spec.Auth,
			Lead:        trig.Spec.Lead,
			Lifecycle:   trig.Spec.Lifecycle,
		},
	}
	if err := h.Client.Create(ctx, run); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating run: %w", err)
	}
	return nil
}

// runName produces a per-fire AgentTeamRun name. Includes a unix
// timestamp so concurrent fires within the same second are unlikely to
// collide; AlreadyExists on the rare same-second collision is treated
// as success in createRun.
func (h *Handler) runName(trig *claudev1alpha1.AgentTeamTrigger) string {
	t := h.now()
	return fmt.Sprintf("%s-%d", trig.Name, t.UTC().Unix())
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// splitMountDir returns the directory portion of an absolute path,
// which is what WorkspaceInputSpec.MountPath expects (the ConfigMap
// projects keys as files under that directory). For a mountPath like
// "/workspace/data/trigger-payload.json", returns "/workspace/data".
func splitMountDir(absPath string) (string, string) {
	idx := strings.LastIndex(absPath, "/")
	if idx <= 0 {
		return absPath, ""
	}
	return absPath[:idx], absPath[idx+1:]
}
