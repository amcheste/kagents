// Package v1alpha1 contains API Schema definitions for the claude v1alpha1 API group.
// +kubebuilder:object:generate=true
// +groupName=kagents.dev
package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentTeamSpec defines the desired state of an AgentTeam.
type AgentTeamSpec struct {
	// Repository configuration for the codebase agents will work on.
	// Use this for coding tasks. Optional when Workspace is set.
	// +optional
	Repository *RepositorySpec `json:"repository,omitempty"`

	// Workspace configures non-git inputs and outputs for Cowork teams.
	// Use this for knowledge-work tasks (documents, reports, email, etc.).
	// +optional
	Workspace *WorkspaceSpec `json:"workspace,omitempty"`

	// Auth configures how agents authenticate with the Anthropic API.
	Auth AuthSpec `json:"auth"`

	// Lead configures the team lead agent.
	Lead LeadSpec `json:"lead"`

	// Teammates defines the worker agents in the team.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	Teammates []TeammateSpec `json:"teammates"`

	// Coordination configures how agents communicate.
	// +optional
	Coordination *CoordinationSpec `json:"coordination,omitempty"`

	// Lifecycle configures team runtime behavior and budget.
	// +optional
	Lifecycle *LifecycleSpec `json:"lifecycle,omitempty"`

	// QualityGates configures validation before marking team complete.
	// +optional
	QualityGates *QualityGateSpec `json:"qualityGates,omitempty"`

	// Observability configures metrics and notifications.
	// +optional
	Observability *ObservabilitySpec `json:"observability,omitempty"`

	// Harness selects the agent runtime that powers this team's pods.
	// Today the only supported value is "claude-code" (Anthropic's native
	// Claude Code Agent Teams protocol), which is also the default when
	// omitted. The field exists so the operator's API stays neutral to a
	// single agent runtime; future harnesses for other team-based agent
	// systems can plug in behind the same CRD without an API break.
	// +kubebuilder:validation:Enum=claude-code
	// +kubebuilder:default="claude-code"
	// +optional
	Harness string `json:"harness,omitempty"`
}

// RepositorySpec defines the git repository configuration.
type RepositorySpec struct {
	// URL is the git clone URL.
	URL string `json:"url"`

	// Branch to clone and work from.
	// +kubebuilder:default="main"
	Branch string `json:"branch,omitempty"`

	// WorktreeStrategy determines how git worktrees are managed.
	// +kubebuilder:validation:Enum=per-teammate;shared
	// +kubebuilder:default="per-teammate"
	WorktreeStrategy string `json:"worktreeStrategy,omitempty"`

	// CredentialsSecret references a Secret containing git credentials.
	// The secret should contain either 'ssh-privatekey' or 'token'.
	// +optional
	CredentialsSecret string `json:"credentialsSecret,omitempty"`
}

// AuthSpec defines Anthropic API authentication.
type AuthSpec struct {
	// APIKeySecret references a Secret containing ANTHROPIC_API_KEY.
	// +optional
	APIKeySecret string `json:"apiKeySecret,omitempty"`

	// OAuthSecret references a Secret containing OAuth tokens for subscription auth.
	// +optional
	OAuthSecret string `json:"oauthSecret,omitempty"`
}

// LeadSpec defines the team lead configuration.
type LeadSpec struct {
	// Model to use for the team lead.
	// +kubebuilder:validation:Enum=opus;sonnet;haiku
	// +kubebuilder:default="opus"
	Model string `json:"model,omitempty"`

	// Prompt is the initial instruction for the team lead.
	Prompt string `json:"prompt"`

	// PermissionMode controls how the lead handles permission requests.
	// +kubebuilder:validation:Enum=auto-accept;plan;default
	// +kubebuilder:default="auto-accept"
	PermissionMode string `json:"permissionMode,omitempty"`

	// Skills to mount into .claude/skills/ for the lead agent.
	// +optional
	Skills []SkillSpec `json:"skills,omitempty"`

	// MCPServers configures Model Context Protocol connections for the lead agent.
	// +optional
	MCPServers []MCPServerSpec `json:"mcpServers,omitempty"`

	// Resources defines compute resources for the lead pod.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// SkillSource identifies where to load a skill from. Exactly one field should be set.
type SkillSource struct {
	// ConfigMap references a ConfigMap in the same namespace.
	// Each key in the ConfigMap becomes a file in the skill directory.
	// +optional
	ConfigMap string `json:"configMap,omitempty"`

	// OCI is an OCI artifact reference containing the skill files (e.g. "ghcr.io/org/skills/web-research:v1").
	// TODO: OCI skill pulling is not yet implemented; use ConfigMap instead.
	// +optional
	OCI string `json:"oci,omitempty"`
}

// SkillSpec defines a Claude Code skill to mount into an agent pod.
type SkillSpec struct {
	// Name is the skill directory name under .claude/skills/.
	Name string `json:"name"`

	// Source identifies where to load the skill from.
	Source SkillSource `json:"source"`
}

// MCPServerSpec configures a Model Context Protocol server for an agent.
type MCPServerSpec struct {
	// Name identifies this MCP server in the agent's config.
	Name string `json:"name"`

	// URL is the MCP server endpoint.
	URL string `json:"url"`

	// CredentialsSecret references a Secret containing an 'apiKey' key for bearer auth.
	// +optional
	CredentialsSecret string `json:"credentialsSecret,omitempty"`
}

// WorkspaceInputSpec defines a read-only input mounted into the agent pod.
type WorkspaceInputSpec struct {
	// ConfigMap references a ConfigMap to mount as a directory.
	// +optional
	ConfigMap string `json:"configMap,omitempty"`

	// PVC references an existing PersistentVolumeClaim to mount read-only.
	// +optional
	PVC string `json:"pvc,omitempty"`

	// MountPath is where to mount this input inside the container.
	MountPath string `json:"mountPath"`
}

// WorkspaceOutputSpec defines the writable output volume for a Cowork team.
type WorkspaceOutputSpec struct {
	// PVC is the name of an existing PVC to use. If empty, the operator creates one named "{team}-output".
	// +optional
	PVC string `json:"pvc,omitempty"`

	// StorageClass for the auto-created PVC. Defaults to "nfs".
	// +optional
	StorageClass string `json:"storageClass,omitempty"`

	// Size of the auto-created PVC.
	// +kubebuilder:default="5Gi"
	Size string `json:"size,omitempty"`

	// MountPath inside the container where the output volume is mounted.
	// +kubebuilder:default="/workspace/output"
	MountPath string `json:"mountPath,omitempty"`
}

// WorkspaceSpec configures non-git inputs and outputs for Cowork teams.
// Use this instead of (or alongside) Repository for knowledge-work tasks.
type WorkspaceSpec struct {
	// Inputs are read-only volumes mounted into all agent pods.
	// +optional
	Inputs []WorkspaceInputSpec `json:"inputs,omitempty"`

	// Output configures the shared writable output volume.
	// +optional
	Output *WorkspaceOutputSpec `json:"output,omitempty"`
}

// ApprovalGateSpec pauses execution before a named event until human approval is recorded.
// Approval is granted by adding the annotation approved.kagents.dev/{event}=true to the AgentTeam.
type ApprovalGateSpec struct {
	// Event is the gate identifier. Use "spawn-{teammate-name}" to gate spawning a specific teammate.
	Event string `json:"event"`

	// Channel is how the approval request notification is sent.
	// +kubebuilder:validation:Enum=webhook;none
	// +kubebuilder:default="none"
	Channel string `json:"channel,omitempty"`

	// WebhookURL to POST when this gate is triggered (used when channel is "webhook").
	// +optional
	WebhookURL string `json:"webhookUrl,omitempty"`
}

// TeammateSpec defines a single teammate agent.
type TeammateSpec struct {
	// Name is the unique identifier for this teammate.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`
	Name string `json:"name"`

	// Model to use for this teammate.
	// +kubebuilder:validation:Enum=opus;sonnet;haiku
	// +kubebuilder:default="sonnet"
	Model string `json:"model,omitempty"`

	// Prompt is the spawn instruction for this teammate.
	Prompt string `json:"prompt"`

	// Scope restricts which files this teammate can access.
	// +optional
	Scope *ScopeSpec `json:"scope,omitempty"`

	// DependsOn lists teammate names that must complete before this one starts.
	// +optional
	DependsOn []string `json:"dependsOn,omitempty"`

	// Outputs declares the artifacts this teammate produces. Each entry
	// records a file path the teammate's prompt is expected to write.
	// On completion the operator records every declared output in
	// AgentTeam.Status.Artifacts and makes them available to any
	// downstream teammate that consumes them via Inputs.
	// +optional
	Outputs []OutputSpec `json:"outputs,omitempty"`

	// Inputs declares the upstream-produced artifacts this teammate
	// consumes. Each entry names a producer teammate (From) and an
	// artifact basename (Artifact); the operator (a) treats From as
	// an implicit dependency — this teammate is not spawned until the
	// producer reaches Completed — and (b) wires an init container that
	// stages the artifact at MountPath on this teammate's pod before
	// the main container starts.
	// +optional
	Inputs []InputSpec `json:"inputs,omitempty"`

	// Skills to mount into .claude/skills/ for this teammate.
	// +optional
	Skills []SkillSpec `json:"skills,omitempty"`

	// MCPServers configures Model Context Protocol connections for this teammate.
	// +optional
	MCPServers []MCPServerSpec `json:"mcpServers,omitempty"`

	// Resources defines compute resources for this teammate's pod.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// OutputSpec declares a file an agent produces. Downstream teammates
// consume it by declaring a matching InputSpec; the operator also
// records each output in AgentTeam.Status.Artifacts on completion.
type OutputSpec struct {
	// Path is the absolute filesystem path on the producer pod where the
	// teammate writes the artifact. For Cowork teams this is typically a
	// path under the team's output mount (e.g. /workspace/output/findings.md).
	Path string `json:"path"`

	// Description is an optional human-readable summary of the artifact.
	// +optional
	Description string `json:"description,omitempty"`
}

// InputSpec declares an artifact this teammate consumes from an upstream
// teammate's outputs. The operator (a) treats From as an implicit
// dependency — this teammate is not spawned until the producer reaches
// Completed — and (b) wires an init container that copies the named
// artifact onto MountPath on this teammate's pod before the main
// container starts. The final on-pod path is {MountPath}/{Artifact}.
type InputSpec struct {
	// From names the upstream teammate that produces the artifact.
	From string `json:"from"`

	// Artifact is the basename of the producer's output file
	// (e.g. "findings.md" for an output path of /workspace/output/findings.md).
	// The operator resolves the full source path by scanning the named
	// producer's Outputs[] for an entry whose Path basename matches.
	Artifact string `json:"artifact"`

	// MountPath is the absolute directory path on this teammate's pod
	// where the artifact will be made available. The operator creates
	// an emptyDir at MountPath and stages the artifact there via an
	// init container; the main container sees {MountPath}/{Artifact}.
	MountPath string `json:"mountPath"`
}

// ScopeSpec restricts file access for a teammate.
type ScopeSpec struct {
	// IncludePaths lists paths the teammate should focus on.
	// +optional
	IncludePaths []string `json:"includePaths,omitempty"`

	// ExcludePaths lists paths the teammate should not modify.
	// +optional
	ExcludePaths []string `json:"excludePaths,omitempty"`
}

// CoordinationSpec configures inter-agent communication.
type CoordinationSpec struct {
	// MailboxBackend determines how mailbox messages are transported.
	// +kubebuilder:validation:Enum=shared-volume;redis;nats
	// +kubebuilder:default="shared-volume"
	MailboxBackend string `json:"mailboxBackend,omitempty"`

	// TaskBackend determines how the shared task list is stored.
	// +kubebuilder:validation:Enum=shared-volume;beads
	// +kubebuilder:default="shared-volume"
	TaskBackend string `json:"taskBackend,omitempty"`

	// Beads configures optional Beads integration for persistent tracking.
	// +optional
	Beads *BeadsSpec `json:"beads,omitempty"`
}

// BeadsSpec configures Beads integration.
type BeadsSpec struct {
	// Enabled turns on Beads tracking.
	Enabled bool `json:"enabled"`

	// DoltServerService is the K8s service name for the Dolt SQL server.
	// +optional
	DoltServerService string `json:"doltServerService,omitempty"`

	// DoltServerPort is the port for the Dolt SQL server.
	// +kubebuilder:default=3306
	DoltServerPort int32 `json:"doltServerPort,omitempty"`
}

// LifecycleSpec controls team runtime behavior.
type LifecycleSpec struct {
	// Timeout is the maximum duration the team can run (e.g. "4h", "30m").
	// +kubebuilder:default="4h"
	Timeout string `json:"timeout,omitempty"`

	// BudgetLimit is the maximum API spend in USD before the team is terminated (e.g. "10.00").
	// +optional
	BudgetLimit *string `json:"budgetLimit,omitempty"`

	// OnComplete determines what happens when the team finishes.
	// +kubebuilder:validation:Enum=create-pr;push-branch;notify;none
	// +kubebuilder:default="notify"
	OnComplete string `json:"onComplete,omitempty"`

	// PullRequest configures PR creation when onComplete is "create-pr".
	// +optional
	PullRequest *PullRequestSpec `json:"pullRequest,omitempty"`

	// ApprovalGates pause execution before specified events until human approval is recorded.
	// Grant approval by annotating the AgentTeam: kubectl annotate agentteam <name> approved.kagents.dev/<event>=true
	// +optional
	ApprovalGates []ApprovalGateSpec `json:"approvalGates,omitempty"`

	// MaxRestarts bounds how many times each teammate pod may be re-spawned
	// after a Failed phase before the team itself is marked Failed. The lead
	// pod is not subject to this limit; a lead crash always fails the team.
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxRestarts *int32 `json:"maxRestarts,omitempty"`

	// GitHubTokenSecret names a Secret in the team's namespace carrying a
	// GitHub token under the key GITHUB_TOKEN. Used by OnComplete=create-pr
	// (and OnComplete=push-branch, once implemented) to authenticate against
	// the GitHub REST API.
	// +optional
	GitHubTokenSecret string `json:"githubTokenSecret,omitempty"`

	// PRTitleTemplate overrides the title template used by OnComplete=create-pr.
	// Available variables: .TeamName, .Namespace. When empty, falls back to
	// Spec.Lifecycle.PullRequest.TitleTemplate, then to the default
	// "claude-teams: {{.TeamName}}".
	// +optional
	PRTitleTemplate string `json:"prTitleTemplate,omitempty"`

	// GitCredentialsSecret names a Secret in the team's namespace carrying git
	// push credentials. The Secret must contain either 'ssh-privatekey' or
	// 'token'. Used by OnComplete=push-branch (and OnComplete=create-pr when
	// push-branch runs ahead of it). Falls back to Spec.Repository.CredentialsSecret
	// when unset, so teams that already configured clone credentials with push
	// scope don't need to duplicate.
	// +optional
	GitCredentialsSecret string `json:"gitCredentialsSecret,omitempty"`

	// ConsolidatedBranchTemplate is a Go template rendered to produce the
	// branch name pushed by OnComplete=push-branch. Available variables:
	// .TeamName, .Namespace. When empty, defaults to "teams/{{.TeamName}}".
	// +optional
	ConsolidatedBranchTemplate string `json:"consolidatedBranchTemplate,omitempty"`
}

// PullRequestSpec configures automatic PR creation.
type PullRequestSpec struct {
	// TargetBranch is the branch to open the PR against.
	// +kubebuilder:default="main"
	TargetBranch string `json:"targetBranch,omitempty"`

	// TitleTemplate is a Go template for the PR title.
	// Available variables: .TeamName, .Namespace
	TitleTemplate string `json:"titleTemplate,omitempty"`

	// Reviewers to request on the PR.
	// +optional
	Reviewers []string `json:"reviewers,omitempty"`

	// Labels to apply to the PR.
	// +optional
	Labels []string `json:"labels,omitempty"`
}

// QualityGateSpec configures validation steps.
type QualityGateSpec struct {
	// RequireTests ensures tests pass before completion.
	RequireTests bool `json:"requireTests,omitempty"`

	// RequireLint ensures linting passes before completion.
	RequireLint bool `json:"requireLint,omitempty"`

	// ValidationScript is a custom script to run before marking complete.
	// +optional
	ValidationScript string `json:"validationScript,omitempty"`
}

// ObservabilitySpec configures monitoring and notifications.
type ObservabilitySpec struct {
	// Metrics configures Prometheus metrics exposition.
	// +optional
	Metrics *MetricsSpec `json:"metrics,omitempty"`

	// LogLevel controls operator log verbosity for this team.
	// +kubebuilder:validation:Enum=debug;info;warn;error
	// +kubebuilder:default="info"
	LogLevel string `json:"logLevel,omitempty"`

	// Webhook configures event notifications.
	// +optional
	Webhook *WebhookSpec `json:"webhook,omitempty"`
}

// MetricsSpec configures Prometheus metrics.
type MetricsSpec struct {
	// Enabled turns on metrics exposition.
	Enabled bool `json:"enabled"`

	// Port for the metrics endpoint.
	// +kubebuilder:default=9090
	Port int32 `json:"port,omitempty"`
}

// WebhookSpec configures event notifications.
type WebhookSpec struct {
	// URL to POST events to.
	URL string `json:"url"`

	// Events to send notifications for.
	// +kubebuilder:validation:MinItems=1
	Events []string `json:"events"`
}

// --- Status Types ---

// AgentTeamStatus defines the observed state of an AgentTeam.
type AgentTeamStatus struct {
	// Phase is the current lifecycle phase of the team.
	// +kubebuilder:validation:Enum=Pending;Initializing;Running;Completed;Failed;TimedOut;BudgetExceeded
	Phase string `json:"phase,omitempty"`

	// StartedAt is when the team began execution.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// CompletedAt is when the team finished execution.
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`

	// TotalTokensUsed is the estimated total tokens consumed.
	TotalTokensUsed int64 `json:"totalTokensUsed,omitempty"`

	// EstimatedCost is the estimated API cost in USD (e.g. "4.50").
	EstimatedCost string `json:"estimatedCost,omitempty"`

	// Ready reports how many teammate pods are ready vs. declared, in the form
	// "running+completed/total" (e.g. "3/5"). Shown in `kubectl get` output.
	// +optional
	Ready string `json:"ready,omitempty"`

	// Lead reports the team lead's status.
	// +optional
	Lead *AgentStatus `json:"lead,omitempty"`

	// Teammates reports each teammate's status.
	// +optional
	Teammates []TeammateStatus `json:"teammates,omitempty"`

	// Tasks reports aggregate task progress.
	// +optional
	Tasks *TaskSummary `json:"tasks,omitempty"`

	// PullRequest reports PR creation status.
	// +optional
	PullRequest *PullRequestStatus `json:"pullRequest,omitempty"`

	// ConsolidatedBranch is the branch name pushed by OnComplete=push-branch.
	// Populated once the push-branch Job succeeds; OnComplete=create-pr reads
	// this as the PR head branch when set, in place of Spec.Repository.Branch.
	// +optional
	ConsolidatedBranch string `json:"consolidatedBranch,omitempty"`

	// Artifacts records the files produced by teammates that declared
	// Outputs in their spec. Populated as each producer teammate reaches
	// Completed; the operator does not retroactively scan teammate pods
	// for undeclared files.
	// +optional
	Artifacts []ArtifactStatus `json:"artifacts,omitempty"`

	// Conditions represent the latest available observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// AgentStatus reports a single agent's state.
type AgentStatus struct {
	// PodName is the name of the agent's pod.
	PodName string `json:"podName,omitempty"`

	// Phase of this agent.
	// +kubebuilder:validation:Enum=Pending;Running;Idle;Completed;Failed;Waiting
	Phase string `json:"phase,omitempty"`
}

// TeammateStatus reports a teammate's state.
type TeammateStatus struct {
	AgentStatus `json:",inline"`

	// Name matches the teammate's spec name.
	Name string `json:"name"`

	// TasksCompleted is the number of tasks this teammate has finished.
	TasksCompleted int `json:"tasksCompleted,omitempty"`

	// TasksClaimed is the number of tasks currently owned by this teammate.
	TasksClaimed int `json:"tasksClaimed,omitempty"`

	// PendingApproval is the approval gate event this teammate is waiting on, if any.
	// +optional
	PendingApproval string `json:"pendingApproval,omitempty"`

	// RestartCount is the number of times this teammate's pod has been
	// re-spawned after a Failed phase. The team is marked Failed when any
	// teammate's RestartCount reaches Spec.Lifecycle.MaxRestarts.
	// +optional
	RestartCount int32 `json:"restartCount,omitempty"`
}

// ArtifactStatus records a single artifact produced by a teammate that
// declared a matching OutputSpec.
type ArtifactStatus struct {
	// Name is the basename of the artifact file (e.g. "findings.md").
	Name string `json:"name"`

	// Path is the producer pod's filesystem path where the artifact was
	// written (mirrors OutputSpec.Path).
	Path string `json:"path"`

	// ProducedBy is the name of the teammate that produced this artifact.
	ProducedBy string `json:"producedBy"`

	// ProducedAt is when the operator recorded the artifact — typically
	// the first reconcile after the producer pod reached Succeeded.
	ProducedAt metav1.Time `json:"producedAt"`
}

// TaskSummary reports aggregate task progress.
type TaskSummary struct {
	Total      int `json:"total"`
	Completed  int `json:"completed"`
	InProgress int `json:"inProgress"`
	Pending    int `json:"pending"`
}

// PullRequestStatus reports PR creation state.
type PullRequestStatus struct {
	URL   string `json:"url,omitempty"`
	State string `json:"state,omitempty"`
}

// --- Top-Level Resource Definitions ---

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Tasks Done",type=integer,JSONPath=`.status.tasks.completed`
// +kubebuilder:printcolumn:name="Cost",type=string,JSONPath=`.status.estimatedCost`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentTeam is the Schema for the agentteams API.
type AgentTeam struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentTeamSpec   `json:"spec,omitempty"`
	Status AgentTeamStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentTeamList contains a list of AgentTeam.
type AgentTeamList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentTeam `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentTeam{}, &AgentTeamList{})
}
