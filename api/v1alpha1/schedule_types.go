package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentTeamScheduleSpec defines a cron-triggered pattern for creating
// AgentTeamRun instances. On each fire, the operator creates one
// AgentTeamRun from the referenced AgentTeamTemplate, parameterized
// with the schedule's repository/workspace/auth/lead/lifecycle fields.
// The existing AgentTeamRun controller then turns that Run into an
// AgentTeam — schedules don't create teams directly, they produce
// the same Run objects a human would create by hand.
type AgentTeamScheduleSpec struct {
	// Schedule is a five-field cron expression in the operator's local
	// time zone (e.g. "0 6 * * MON" for Monday 06:00). Parsed by
	// robfig/cron/v3 with the standard parser.
	Schedule string `json:"schedule"`

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

	// HistoryLimit caps how many completed Runs are retained in
	// status.runs[] and on the cluster. Once exceeded, the oldest
	// completed Runs are deleted. Set to 0 (or omit) to keep all Runs.
	// +kubebuilder:validation:Minimum=0
	// +optional
	HistoryLimit int32 `json:"historyLimit,omitempty"`
}

// AgentTeamScheduleStatus reports cron progress and Run history.
type AgentTeamScheduleStatus struct {
	// LastScheduledAt is when the most recent fire occurred (the schedule
	// time that triggered Run creation, not the wall-clock reconcile time).
	// +optional
	LastScheduledAt *metav1.Time `json:"lastScheduledAt,omitempty"`

	// NextScheduledAt is the time of the next scheduled fire as computed
	// from the cron expression.
	// +optional
	NextScheduledAt *metav1.Time `json:"nextScheduledAt,omitempty"`

	// ActiveRun names the in-flight AgentTeamRun, if any. Empty when no
	// run is currently executing.
	// +optional
	ActiveRun string `json:"activeRun,omitempty"`

	// Runs is the recent history of Runs this schedule created. Truncated
	// to HistoryLimit (oldest entries dropped first).
	// +optional
	Runs []ScheduledRunStatus `json:"runs,omitempty"`
}

// ScheduledRunStatus records a single fire's bookkeeping.
type ScheduledRunStatus struct {
	// Name is the AgentTeamRun resource name.
	Name string `json:"name"`

	// ScheduledAt is the cron-computed time this run was triggered for.
	ScheduledAt metav1.Time `json:"scheduledAt"`

	// Phase mirrors the underlying AgentTeamRun's last observed status.phase.
	// +optional
	Phase string `json:"phase,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Schedule",type=string,JSONPath=`.spec.schedule`
// +kubebuilder:printcolumn:name="Last",type=date,JSONPath=`.status.lastScheduledAt`
// +kubebuilder:printcolumn:name="Next",type=date,JSONPath=`.status.nextScheduledAt`
// +kubebuilder:printcolumn:name="Active",type=string,JSONPath=`.status.activeRun`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentTeamSchedule creates AgentTeamRun instances on a cron schedule.
type AgentTeamSchedule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentTeamScheduleSpec   `json:"spec,omitempty"`
	Status AgentTeamScheduleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentTeamScheduleList contains a list of AgentTeamSchedule.
type AgentTeamScheduleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentTeamSchedule `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentTeamSchedule{}, &AgentTeamScheduleList{})
}
