package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentTeamTriggerSpec defines a webhook-driven AgentTeamRun creation
// pattern. The kagents-trigger ingress deployment watches AgentTeamTrigger
// resources, matches incoming HTTP requests against TriggerSource.Webhook,
// applies HMAC validation + ConcurrencyPolicy, and creates one
// AgentTeamRun per accepted event. The existing AgentTeamRun controller
// then turns that Run into a team.
//
// The trigger listener intentionally runs as its own Deployment
// (kagents-trigger) rather than inside the operator manager: it's an
// internet-reachable surface and shouldn't live inside the leader-elected
// controller pod.
type AgentTeamTriggerSpec struct {
	// Trigger defines which external signal fires this trigger.
	// Today only Webhook is supported; future expansion (watchResource,
	// schedule-on-event, etc.) is anticipated by the wrapper struct.
	Trigger TriggerSource `json:"trigger"`

	// TemplateRef names the AgentTeamTemplate to instantiate on each fire.
	TemplateRef TemplateReference `json:"templateRef"`

	// Auth is forwarded to every created AgentTeamRun.
	Auth AuthSpec `json:"auth"`

	// Lead configures the team lead for every created AgentTeamRun.
	Lead LeadSpec `json:"lead"`

	// Repository overrides the repository for each Run (coding mode).
	// +optional
	Repository *RepositorySpec `json:"repository,omitempty"`

	// Workspace overrides the workspace for each Run (Cowork mode).
	// +optional
	Workspace *WorkspaceSpec `json:"workspace,omitempty"`

	// Lifecycle overrides forwarded to every created AgentTeamRun.
	// +optional
	Lifecycle *LifecycleSpec `json:"lifecycle,omitempty"`

	// PayloadInjection configures how the incoming webhook payload is
	// surfaced to the agent pods. The kagents-trigger creates a ConfigMap
	// in the trigger's namespace carrying the request body, and adds a
	// matching read-only volume mount to the resulting team's workspace
	// inputs at PayloadInjection.MountPath.
	// +optional
	PayloadInjection *PayloadInjectionSpec `json:"payloadInjection,omitempty"`

	// ConcurrencyPolicy governs what happens when a webhook fires while
	// a previous run is still in-flight.
	//
	//   - Allow   (default) — always create a new Run.
	//   - Forbid  — reject the webhook with 409 if ActiveRun is set.
	//   - Replace — delete the active Run, then create a new one.
	// +kubebuilder:validation:Enum=Allow;Forbid;Replace
	// +kubebuilder:default=Allow
	// +optional
	ConcurrencyPolicy string `json:"concurrencyPolicy,omitempty"`
}

// TriggerSource is a discriminated union of supported trigger types.
// Exactly one inner field should be set.
type TriggerSource struct {
	// Webhook fires the trigger when an HTTP POST arrives at the
	// configured Path on the kagents-trigger service.
	// +optional
	Webhook *WebhookTriggerSpec `json:"webhook,omitempty"`
}

// WebhookTriggerSpec configures a webhook trigger.
type WebhookTriggerSpec struct {
	// Path is the URL path on the trigger service that fires this trigger
	// (e.g. "/hooks/new-deal"). Each trigger's Path must be unique within
	// the cluster; the listener matches incoming requests by exact prefix.
	// +kubebuilder:validation:Pattern=`^/[A-Za-z0-9._/-]+$`
	Path string `json:"path"`

	// Secret names a Secret in the trigger's namespace containing the
	// shared secret used to validate the request's HMAC signature
	// (key: "hmac-secret"). When empty, HMAC validation is skipped —
	// recommended only for traffic that's already authenticated upstream
	// (e.g. by an ingress with mTLS).
	// +optional
	Secret string `json:"secret,omitempty"`
}

// PayloadInjectionSpec configures payload mounting on triggered teams.
type PayloadInjectionSpec struct {
	// MountPath is the absolute file path inside agent pods where the
	// incoming webhook payload appears (e.g. "/workspace/data/trigger-payload.json").
	// The directory is created if it doesn't exist; the file is read-only.
	MountPath string `json:"mountPath"`
}

// AgentTeamTriggerStatus reports trigger bookkeeping.
type AgentTeamTriggerStatus struct {
	// LastTriggeredAt is the most recent wall-clock time the listener
	// accepted a webhook for this trigger and created a Run.
	// +optional
	LastTriggeredAt *metav1.Time `json:"lastTriggeredAt,omitempty"`

	// ActiveRun names the in-flight AgentTeamRun, if any. Cleared once
	// the underlying Run reaches a terminal phase.
	// +optional
	ActiveRun string `json:"activeRun,omitempty"`

	// TotalRuns is the total number of Runs this trigger has produced
	// over its lifetime.
	TotalRuns int64 `json:"totalRuns,omitempty"`

	// Runs is the recent history of Runs this trigger created. The
	// reconciler maintains this from labeled AgentTeamRuns in the same
	// namespace.
	// +optional
	Runs []TriggerRunStatus `json:"runs,omitempty"`
}

// TriggerRunStatus records a single triggered fire.
type TriggerRunStatus struct {
	// Name is the AgentTeamRun resource name.
	Name string `json:"name"`

	// TriggeredAt is when the listener accepted the webhook.
	TriggeredAt metav1.Time `json:"triggeredAt"`

	// Phase mirrors the AgentTeamRun's status.phase.
	// +optional
	Phase string `json:"phase,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Path",type=string,JSONPath=`.spec.trigger.webhook.path`
// +kubebuilder:printcolumn:name="Last",type=date,JSONPath=`.status.lastTriggeredAt`
// +kubebuilder:printcolumn:name="Active",type=string,JSONPath=`.status.activeRun`
// +kubebuilder:printcolumn:name="Total",type=integer,JSONPath=`.status.totalRuns`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentTeamTrigger creates AgentTeamRun instances in response to events.
type AgentTeamTrigger struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentTeamTriggerSpec   `json:"spec,omitempty"`
	Status AgentTeamTriggerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentTeamTriggerList contains a list of AgentTeamTrigger.
type AgentTeamTriggerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentTeamTrigger `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentTeamTrigger{}, &AgentTeamTriggerList{})
}
