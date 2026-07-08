package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, claudev1alpha1.AddToScheme(s))
	return s
}

func sampleTeam() *claudev1alpha1.AgentTeam {
	return &claudev1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "ns"},
		Status: claudev1alpha1.AgentTeamStatus{
			Phase: "Completed",
			Artifacts: []claudev1alpha1.ArtifactStatus{
				{Name: "report.pdf", ProducedBy: "writer"},
			},
		},
	}
}

// --- Dispatcher ---

func TestDispatcher_RoutesByType(t *testing.T) {
	t.Parallel()
	d := NewDispatcher()

	called := false
	d.SetSender("webhook", SenderFunc(func(_ context.Context, _ client.Client, _ claudev1alpha1.DeliveryTarget, _ *claudev1alpha1.AgentTeam) error {
		called = true
		return nil
	}))
	require.NoError(t, d.Send(context.Background(), nil,
		claudev1alpha1.DeliveryTarget{Type: "webhook"}, sampleTeam()))
	assert.True(t, called)
}

func TestDispatcher_UnknownTypeReturnsNotImplemented(t *testing.T) {
	t.Parallel()
	d := NewDispatcher()
	err := d.Send(context.Background(), nil,
		claudev1alpha1.DeliveryTarget{Type: "carrier-pigeon"}, sampleTeam())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotImplemented)
}

func TestDispatcher_EmailReturnsNotImplemented(t *testing.T) {
	t.Parallel()
	d := NewDispatcher()
	err := d.Send(context.Background(), nil,
		claudev1alpha1.DeliveryTarget{Type: "email"}, sampleTeam())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotImplemented)
}

func TestDispatcher_GoogleDriveReturnsNotImplemented(t *testing.T) {
	t.Parallel()
	d := NewDispatcher()
	err := d.Send(context.Background(), nil,
		claudev1alpha1.DeliveryTarget{Type: "google-drive"}, sampleTeam())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotImplemented)
}

func TestTargetLabel(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   claudev1alpha1.DeliveryTarget
		want string
	}{
		{"webhook", claudev1alpha1.DeliveryTarget{Type: "webhook", URL: "https://x"}, "https://x"},
		{"slack", claudev1alpha1.DeliveryTarget{Type: "slack", Channel: "#reports"}, "#reports"},
		{"email", claudev1alpha1.DeliveryTarget{Type: "email", To: []string{"a@b", "c@d"}}, "a@b, c@d"},
		{"google-drive", claudev1alpha1.DeliveryTarget{Type: "google-drive", Folder: "Reports/Q3"}, "Reports/Q3"},
		{"unknown", claudev1alpha1.DeliveryTarget{Type: "?"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, TargetLabel(tc.in))
		})
	}
}

// --- WebhookSender ---

func TestWebhookSender_PostsPayload(t *testing.T) {
	t.Parallel()
	var capturedBody []byte
	var capturedCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCT = r.Header.Get("Content-Type")
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender := &WebhookSender{}
	target := claudev1alpha1.DeliveryTarget{Type: "webhook", URL: srv.URL, Message: "Q3 brief done."}
	require.NoError(t, sender.Send(context.Background(), nil, target, sampleTeam()))

	assert.Equal(t, "application/json", capturedCT)
	var got webhookPayload
	require.NoError(t, json.Unmarshal(capturedBody, &got))
	assert.Equal(t, "team.completed", got.Event)
	assert.Equal(t, "alpha", got.Team)
	assert.Equal(t, "ns", got.Namespace)
	assert.Equal(t, "Completed", got.Phase)
	assert.Equal(t, "Q3 brief done.", got.Message)
	require.Len(t, got.Artifacts, 1)
	assert.Equal(t, "report.pdf", got.Artifacts[0].Name)
}

func TestWebhookSender_Non2xxIsFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	sender := &WebhookSender{}
	err := sender.Send(context.Background(), nil,
		claudev1alpha1.DeliveryTarget{Type: "webhook", URL: srv.URL}, sampleTeam())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestWebhookSender_NoURLFails(t *testing.T) {
	t.Parallel()
	err := (&WebhookSender{}).Send(context.Background(), nil,
		claudev1alpha1.DeliveryTarget{Type: "webhook"}, sampleTeam())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "url")
}

// --- SlackSender ---

func TestSlackSender_PostsToWebhookURLFromSecret(t *testing.T) {
	t.Parallel()
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "slack-creds", Namespace: "ns"},
		Data:       map[string][]byte{"slack-webhook-url": []byte(srv.URL)},
	}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(secret).Build()
	sender := &SlackSender{}
	target := claudev1alpha1.DeliveryTarget{
		Type: "slack", Channel: "#reports", Message: "hello", CredentialsSecret: "slack-creds",
	}
	require.NoError(t, sender.Send(context.Background(), c, target, sampleTeam()))

	var got slackPayload
	require.NoError(t, json.Unmarshal(capturedBody, &got))
	assert.Equal(t, "#reports", got.Channel)
	assert.Contains(t, got.Text, "hello")
	assert.Contains(t, got.Text, "report.pdf", "artifact list appears in the message body")
}

func TestSlackSender_MissingSecretFails(t *testing.T) {
	t.Parallel()
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
	err := (&SlackSender{}).Send(context.Background(), c,
		claudev1alpha1.DeliveryTarget{Type: "slack", Channel: "#x"}, sampleTeam())
	require.Error(t, err)
}

func TestSlackSender_MissingWebhookURLKeyFails(t *testing.T) {
	t.Parallel()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "bad", Namespace: "ns"},
		Data:       map[string][]byte{"wrong-key": []byte("x")},
	}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(secret).Build()
	err := (&SlackSender{}).Send(context.Background(), c,
		claudev1alpha1.DeliveryTarget{Type: "slack", Channel: "#x", CredentialsSecret: "bad"}, sampleTeam())
	require.Error(t, err)
}

func TestSlackSender_Non2xxIsFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "ns"},
		Data:       map[string][]byte{"slack-webhook-url": []byte(srv.URL)},
	}
	c := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(secret).Build()
	err := (&SlackSender{}).Send(context.Background(), c,
		claudev1alpha1.DeliveryTarget{Type: "slack", Channel: "#x", CredentialsSecret: "creds"}, sampleTeam())
	require.Error(t, err)
}

func TestFormatSlackMessage_DefaultWhenMessageEmpty(t *testing.T) {
	t.Parallel()
	team := sampleTeam()
	team.Status.Artifacts = nil
	msg := formatSlackMessage(claudev1alpha1.DeliveryTarget{Type: "slack"}, team)
	assert.Contains(t, msg, "alpha")
	assert.Contains(t, msg, "Completed")
}

func TestFormatSlackMessage_IncludesArtifactList(t *testing.T) {
	t.Parallel()
	msg := formatSlackMessage(claudev1alpha1.DeliveryTarget{Message: "custom"}, sampleTeam())
	assert.Contains(t, msg, "custom")
	assert.Contains(t, msg, "report.pdf")
	assert.Contains(t, msg, "writer")
}

// --- ErrNotImplemented sentinel ---

func TestErrNotImplemented_IsCheckable(t *testing.T) {
	t.Parallel()
	err := (&Dispatcher{}).Send(context.Background(), nil,
		claudev1alpha1.DeliveryTarget{Type: "missing"}, sampleTeam())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotImplemented))
}
