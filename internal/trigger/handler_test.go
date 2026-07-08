package trigger

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// triggerScheme builds the same scheme the production listener uses,
// for use with the fake client.
func triggerScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, claudev1alpha1.AddToScheme(s))
	return s
}

func fixedNow() time.Time {
	return time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
}

func newHandler(c client.Client) *Handler {
	return &Handler{Client: c, Now: fixedNow}
}

func newTrigger(name, path string) *claudev1alpha1.AgentTeamTrigger {
	return &claudev1alpha1.AgentTeamTrigger{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns"},
		Spec: claudev1alpha1.AgentTeamTriggerSpec{
			Trigger: claudev1alpha1.TriggerSource{
				Webhook: &claudev1alpha1.WebhookTriggerSpec{Path: path},
			},
			TemplateRef: claudev1alpha1.TemplateReference{Name: "tmpl"},
			Auth:        claudev1alpha1.AuthSpec{APIKeySecret: "anthropic"},
			Lead:        claudev1alpha1.LeadSpec{Model: "opus", Prompt: "go"},
		},
	}
}

func post(t *testing.T, h *Handler, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestHealthz(t *testing.T) {
	h := newHandler(fake.NewClientBuilder().WithScheme(triggerScheme(t)).Build())
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestMethodNotAllowed(t *testing.T) {
	h := newHandler(fake.NewClientBuilder().WithScheme(triggerScheme(t)).Build())
	req := httptest.NewRequest(http.MethodGet, "/hooks/anything", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestUnknownPath(t *testing.T) {
	h := newHandler(fake.NewClientBuilder().WithScheme(triggerScheme(t)).Build())
	rr := post(t, h, "/hooks/nonexistent", []byte(`{}`), nil)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestSuccessfulFire_NoHMAC_CreatesRunAndPayloadCM(t *testing.T) {
	trig := newTrigger("new-deal", "/hooks/new-deal")
	trig.Spec.PayloadInjection = &claudev1alpha1.PayloadInjectionSpec{
		MountPath: "/workspace/data/trigger-payload.json",
	}
	c := fake.NewClientBuilder().WithScheme(triggerScheme(t)).WithObjects(trig).Build()
	h := newHandler(c)

	body := []byte(`{"deal":"acme"}`)
	rr := post(t, h, "/hooks/new-deal", body, nil)
	assert.Equal(t, http.StatusAccepted, rr.Code)
	assert.Contains(t, rr.Body.String(), `"trigger":"new-deal"`)

	// Run created with expected fields.
	want := "new-deal-" + fixedNowUnix()
	var run claudev1alpha1.AgentTeamRun
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: want, Namespace: "ns"}, &run))
	assert.Equal(t, "tmpl", run.Spec.TemplateRef.Name)
	assert.Equal(t, "new-deal", run.Labels["kagents.dev/trigger"])
	require.NotNil(t, run.Spec.Workspace, "PayloadInjection should add a workspace input even if Workspace was nil on the trigger")
	require.Len(t, run.Spec.Workspace.Inputs, 1)
	assert.Equal(t, want+"-payload", run.Spec.Workspace.Inputs[0].ConfigMap)
	assert.Equal(t, "/workspace/data", run.Spec.Workspace.Inputs[0].MountPath)

	// Payload ConfigMap exists with the request body.
	var cm corev1.ConfigMap
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: want + "-payload", Namespace: "ns"}, &cm))
	assert.Equal(t, string(body), cm.Data[payloadConfigMapKey])
}

func TestHMAC_ValidSignatureAccepts(t *testing.T) {
	trig := newTrigger("signed", "/hooks/signed")
	trig.Spec.Trigger.Webhook.Secret = "hmac-secret-obj"

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "hmac-secret-obj", Namespace: "ns"},
		Data:       map[string][]byte{"hmac-secret": []byte("supersecret")},
	}
	c := fake.NewClientBuilder().WithScheme(triggerScheme(t)).WithObjects(trig, secret).Build()
	h := newHandler(c)

	body := []byte(`{"event":"ok"}`)
	mac := hmac.New(sha256.New, []byte("supersecret"))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	rr := post(t, h, "/hooks/signed", body, map[string]string{signatureHeader: sig})
	assert.Equal(t, http.StatusAccepted, rr.Code)
}

func TestHMAC_BadSignatureRejects(t *testing.T) {
	trig := newTrigger("signed", "/hooks/signed")
	trig.Spec.Trigger.Webhook.Secret = "hmac-secret-obj"

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "hmac-secret-obj", Namespace: "ns"},
		Data:       map[string][]byte{"hmac-secret": []byte("supersecret")},
	}
	c := fake.NewClientBuilder().WithScheme(triggerScheme(t)).WithObjects(trig, secret).Build()
	h := newHandler(c)

	rr := post(t, h, "/hooks/signed", []byte(`{"event":"ok"}`),
		map[string]string{signatureHeader: "sha256=" + strings.Repeat("00", 32)})
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestHMAC_MissingHeaderRejects(t *testing.T) {
	trig := newTrigger("signed", "/hooks/signed")
	trig.Spec.Trigger.Webhook.Secret = "hmac-secret-obj"

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "hmac-secret-obj", Namespace: "ns"},
		Data:       map[string][]byte{"hmac-secret": []byte("supersecret")},
	}
	c := fake.NewClientBuilder().WithScheme(triggerScheme(t)).WithObjects(trig, secret).Build()
	h := newHandler(c)

	rr := post(t, h, "/hooks/signed", []byte(`{}`), nil)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestConcurrencyForbid_BlocksWhenActive(t *testing.T) {
	trig := newTrigger("forbid", "/hooks/forbid")
	trig.Spec.ConcurrencyPolicy = "Forbid"
	trig.Status.ActiveRun = "forbid-prior"
	c := fake.NewClientBuilder().WithScheme(triggerScheme(t)).WithObjects(trig).Build()
	h := newHandler(c)

	rr := post(t, h, "/hooks/forbid", []byte(`{}`), nil)
	assert.Equal(t, http.StatusConflict, rr.Code)

	// No new run created.
	var runs claudev1alpha1.AgentTeamRunList
	require.NoError(t, c.List(context.Background(), &runs))
	assert.Empty(t, runs.Items)
}

func TestConcurrencyReplace_DeletesPriorAndFires(t *testing.T) {
	trig := newTrigger("replace", "/hooks/replace")
	trig.Spec.ConcurrencyPolicy = "Replace"
	trig.Status.ActiveRun = "replace-prior"

	prior := &claudev1alpha1.AgentTeamRun{
		ObjectMeta: metav1.ObjectMeta{Name: "replace-prior", Namespace: "ns"},
	}
	c := fake.NewClientBuilder().WithScheme(triggerScheme(t)).WithObjects(trig, prior).Build()
	h := newHandler(c)

	rr := post(t, h, "/hooks/replace", []byte(`{}`), nil)
	assert.Equal(t, http.StatusAccepted, rr.Code)

	// Prior run is gone.
	var priorAfter claudev1alpha1.AgentTeamRun
	err := c.Get(context.Background(), types.NamespacedName{Name: "replace-prior", Namespace: "ns"}, &priorAfter)
	assert.True(t, errors.IsNotFound(err), "Replace policy should delete the in-flight run")

	// New run exists.
	want := "replace-" + fixedNowUnix()
	var newRun claudev1alpha1.AgentTeamRun
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: want, Namespace: "ns"}, &newRun))
}

func TestBodyTooLarge_Rejects(t *testing.T) {
	trig := newTrigger("big", "/hooks/big")
	c := fake.NewClientBuilder().WithScheme(triggerScheme(t)).WithObjects(trig).Build()
	h := newHandler(c)

	body := bytes.Repeat([]byte("a"), maxBodyBytes+1)
	rr := post(t, h, "/hooks/big", body, nil)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
}

func TestSplitMountDir(t *testing.T) {
	t.Parallel()
	dir, base := splitMountDir("/workspace/data/trigger-payload.json")
	assert.Equal(t, "/workspace/data", dir)
	assert.Equal(t, "trigger-payload.json", base)

	dir, base = splitMountDir("/single")
	assert.Equal(t, "/single", dir)
	assert.Equal(t, "", base)
}

// fixedNowUnix returns the unix-second string for fixedNow() — used to
// construct the deterministic run name the handler would produce under
// the same clock.
func fixedNowUnix() string {
	return "1780315200" // 2026-06-01T12:00:00Z
}
