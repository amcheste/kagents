# API Reference

## Packages
- [kagents.dev/v1alpha1](#kagentsdevv1alpha1)


## kagents.dev/v1alpha1

Package v1alpha1 contains API Schema definitions for the claude v1alpha1 API group.

Package v1alpha1 contains API Schema definitions for the claude v1alpha1 API group.

### Resource Types
- [AgentTeam](#agentteam)
- [AgentTeamRun](#agentteamrun)
- [AgentTeamTemplate](#agentteamtemplate)



#### AgentStatus



AgentStatus reports a single agent's state.



_Appears in:_
- [AgentTeamStatus](#agentteamstatus)
- [TeammateStatus](#teammatestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `podName` _string_ | PodName is the name of the agent's pod. |  |  |
| `phase` _string_ | Phase of this agent. |  | Enum: [Pending Running Idle Completed Failed Waiting] <br /> |


#### AgentTeam



AgentTeam is the Schema for the agentteams API.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `kagents.dev/v1alpha1` | | |
| `kind` _string_ | `AgentTeam` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AgentTeamSpec](#agentteamspec)_ |  |  |  |
| `status` _[AgentTeamStatus](#agentteamstatus)_ |  |  |  |


#### AgentTeamRun



AgentTeamRun is an instance of an AgentTeamTemplate applied to a specific repository.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `kagents.dev/v1alpha1` | | |
| `kind` _string_ | `AgentTeamRun` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AgentTeamRunSpec](#agentteamrunspec)_ |  |  |  |
| `status` _[AgentTeamStatus](#agentteamstatus)_ |  |  |  |


#### AgentTeamRunSpec



AgentTeamRunSpec defines an instance of a template applied to a specific repo.



_Appears in:_
- [AgentTeamRun](#agentteamrun)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `templateRef` _[TemplateReference](#templatereference)_ | TemplateRef references the AgentTeamTemplate to instantiate. |  |  |
| `repository` _[RepositorySpec](#repositoryspec)_ | Repository configuration for this run (coding mode). |  | Optional: \{\} <br /> |
| `workspace` _[WorkspaceSpec](#workspacespec)_ | Workspace configures inputs/outputs for this run (Cowork mode). |  | Optional: \{\} <br /> |
| `auth` _[AuthSpec](#authspec)_ | Auth configures API authentication for this run. |  |  |
| `lead` _[LeadSpec](#leadspec)_ | Lead configures the team lead for this run. |  |  |
| `lifecycle` _[LifecycleSpec](#lifecyclespec)_ | Lifecycle overrides for this run. |  | Optional: \{\} <br /> |


#### AgentTeamSpec



AgentTeamSpec defines the desired state of an AgentTeam.



_Appears in:_
- [AgentTeam](#agentteam)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `repository` _[RepositorySpec](#repositoryspec)_ | Repository configuration for the codebase agents will work on.<br />Use this for coding tasks. Optional when Workspace is set. |  | Optional: \{\} <br /> |
| `workspace` _[WorkspaceSpec](#workspacespec)_ | Workspace configures non-git inputs and outputs for Cowork teams.<br />Use this for knowledge-work tasks (documents, reports, email, etc.). |  | Optional: \{\} <br /> |
| `auth` _[AuthSpec](#authspec)_ | Auth configures how agents authenticate with the Anthropic API. |  |  |
| `lead` _[LeadSpec](#leadspec)_ | Lead configures the team lead agent. |  |  |
| `teammates` _[TeammateSpec](#teammatespec) array_ | Teammates defines the worker agents in the team. |  | MaxItems: 16 <br />MinItems: 1 <br /> |
| `coordination` _[CoordinationSpec](#coordinationspec)_ | Coordination configures how agents communicate. |  | Optional: \{\} <br /> |
| `lifecycle` _[LifecycleSpec](#lifecyclespec)_ | Lifecycle configures team runtime behavior and budget. |  | Optional: \{\} <br /> |
| `qualityGates` _[QualityGateSpec](#qualitygatespec)_ | QualityGates configures validation before marking team complete. |  | Optional: \{\} <br /> |
| `observability` _[ObservabilitySpec](#observabilityspec)_ | Observability configures metrics and notifications. |  | Optional: \{\} <br /> |
| `harness` _string_ | Harness selects the agent runtime that powers this team's pods.<br />Today the only supported value is "claude-code" (Anthropic's native<br />Claude Code Agent Teams protocol), which is also the default when<br />omitted. The field exists so the operator's API stays neutral to a<br />single agent runtime; future harnesses for other team-based agent<br />systems can plug in behind the same CRD without an API break. | claude-code | Enum: [claude-code] <br />Optional: \{\} <br /> |


#### AgentTeamStatus



AgentTeamStatus defines the observed state of an AgentTeam.



_Appears in:_
- [AgentTeam](#agentteam)
- [AgentTeamRun](#agentteamrun)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `phase` _string_ | Phase is the current lifecycle phase of the team. |  | Enum: [Pending Initializing Running Completed Failed TimedOut BudgetExceeded] <br /> |
| `startedAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | StartedAt is when the team began execution. |  | Optional: \{\} <br /> |
| `completedAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | CompletedAt is when the team finished execution. |  | Optional: \{\} <br /> |
| `totalTokensUsed` _integer_ | TotalTokensUsed is the estimated total tokens consumed. |  |  |
| `estimatedCost` _string_ | EstimatedCost is the estimated API cost in USD (e.g. "4.50"). |  |  |
| `ready` _string_ | Ready reports how many teammate pods are ready vs. declared, in the form<br />"running+completed/total" (e.g. "3/5"). Shown in `kubectl get` output. |  | Optional: \{\} <br /> |
| `lead` _[AgentStatus](#agentstatus)_ | Lead reports the team lead's status. |  | Optional: \{\} <br /> |
| `teammates` _[TeammateStatus](#teammatestatus) array_ | Teammates reports each teammate's status. |  | Optional: \{\} <br /> |
| `tasks` _[TaskSummary](#tasksummary)_ | Tasks reports aggregate task progress. |  | Optional: \{\} <br /> |
| `pullRequest` _[PullRequestStatus](#pullrequeststatus)_ | PullRequest reports PR creation status. |  | Optional: \{\} <br /> |
| `consolidatedBranch` _string_ | ConsolidatedBranch is the branch name pushed by OnComplete=push-branch.<br />Populated once the push-branch Job succeeds; OnComplete=create-pr reads<br />this as the PR head branch when set, in place of Spec.Repository.Branch. |  | Optional: \{\} <br /> |
| `artifacts` _[ArtifactStatus](#artifactstatus) array_ | Artifacts records the files produced by teammates that declared<br />Outputs in their spec. Populated as each producer teammate reaches<br />Completed; the operator does not retroactively scan teammate pods<br />for undeclared files. |  | Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#condition-v1-meta) array_ | Conditions represent the latest available observations. |  | Optional: \{\} <br /> |


#### AgentTeamTemplate



AgentTeamTemplate is a reusable team definition.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `kagents.dev/v1alpha1` | | |
| `kind` _string_ | `AgentTeamTemplate` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AgentTeamTemplateSpec](#agentteamtemplatespec)_ |  |  |  |
| `status` _[AgentTeamTemplateStatus](#agentteamtemplatestatus)_ |  |  |  |


#### AgentTeamTemplateSpec



AgentTeamTemplateSpec defines a reusable team pattern.



_Appears in:_
- [AgentTeamTemplate](#agentteamtemplate)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `description` _string_ | Description explains the template's purpose. |  |  |
| `teammates` _[TeammateSpec](#teammatespec) array_ | Teammates defines the worker agents in the template. |  | MaxItems: 16 <br />MinItems: 1 <br /> |
| `coordination` _[CoordinationSpec](#coordinationspec)_ | Coordination configures how agents communicate. |  | Optional: \{\} <br /> |
| `lifecycle` _[LifecycleSpec](#lifecyclespec)_ | Lifecycle configures default runtime behavior. |  | Optional: \{\} <br /> |
| `qualityGates` _[QualityGateSpec](#qualitygatespec)_ | QualityGates configures default validation steps. |  | Optional: \{\} <br /> |


#### AgentTeamTemplateStatus



AgentTeamTemplateStatus reports validation state for an AgentTeamTemplate.
The reconciler validates teammate references and writes a Ready condition;
AgentTeamRun controllers should refuse to instantiate templates where
Ready is false.



_Appears in:_
- [AgentTeamTemplate](#agentteamtemplate)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `ready` _boolean_ | Ready is true when the template has passed validation and is safe to<br />instantiate via an AgentTeamRun. |  | Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#condition-v1-meta) array_ | Conditions track the latest validation state with structured reasons. |  | Optional: \{\} <br /> |


#### ApprovalGateSpec



ApprovalGateSpec pauses execution before a named event until human approval is recorded.
Approval is granted by adding the annotation approved.kagents.dev/{event}=true to the AgentTeam.



_Appears in:_
- [LifecycleSpec](#lifecyclespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `event` _string_ | Event is the gate identifier. Use "spawn-\{teammate-name\}" to gate spawning a specific teammate. |  |  |
| `channel` _string_ | Channel is how the approval request notification is sent. | none | Enum: [webhook none] <br /> |
| `webhookUrl` _string_ | WebhookURL to POST when this gate is triggered (used when channel is "webhook"). |  | Optional: \{\} <br /> |


#### ArtifactStatus



ArtifactStatus records a single artifact produced by a teammate that
declared a matching OutputSpec.



_Appears in:_
- [AgentTeamStatus](#agentteamstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the basename of the artifact file (e.g. "findings.md"). |  |  |
| `path` _string_ | Path is the producer pod's filesystem path where the artifact was<br />written (mirrors OutputSpec.Path). |  |  |
| `producedBy` _string_ | ProducedBy is the name of the teammate that produced this artifact. |  |  |
| `producedAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | ProducedAt is when the operator recorded the artifact — typically<br />the first reconcile after the producer pod reached Succeeded. |  |  |


#### AuthSpec



AuthSpec defines Anthropic API authentication.



_Appears in:_
- [AgentTeamRunSpec](#agentteamrunspec)
- [AgentTeamSpec](#agentteamspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiKeySecret` _string_ | APIKeySecret references a Secret containing ANTHROPIC_API_KEY. |  | Optional: \{\} <br /> |
| `oauthSecret` _string_ | OAuthSecret references a Secret containing OAuth tokens for subscription auth. |  | Optional: \{\} <br /> |


#### BeadsSpec



BeadsSpec configures Beads integration.



_Appears in:_
- [CoordinationSpec](#coordinationspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ | Enabled turns on Beads tracking. |  |  |
| `doltServerService` _string_ | DoltServerService is the K8s service name for the Dolt SQL server. |  | Optional: \{\} <br /> |
| `doltServerPort` _integer_ | DoltServerPort is the port for the Dolt SQL server. | 3306 |  |


#### CoordinationSpec



CoordinationSpec configures inter-agent communication.



_Appears in:_
- [AgentTeamSpec](#agentteamspec)
- [AgentTeamTemplateSpec](#agentteamtemplatespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `mailboxBackend` _string_ | MailboxBackend determines how mailbox messages are transported. | shared-volume | Enum: [shared-volume redis nats] <br /> |
| `taskBackend` _string_ | TaskBackend determines how the shared task list is stored. | shared-volume | Enum: [shared-volume beads] <br /> |
| `beads` _[BeadsSpec](#beadsspec)_ | Beads configures optional Beads integration for persistent tracking. |  | Optional: \{\} <br /> |


#### InputSpec



InputSpec declares an artifact this teammate consumes from an upstream
teammate's outputs. The operator (a) treats From as an implicit
dependency — this teammate is not spawned until the producer reaches
Completed — and (b) wires an init container that copies the named
artifact onto MountPath on this teammate's pod before the main
container starts. The final on-pod path is {MountPath}/{Artifact}.



_Appears in:_
- [TeammateSpec](#teammatespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `from` _string_ | From names the upstream teammate that produces the artifact. |  |  |
| `artifact` _string_ | Artifact is the basename of the producer's output file<br />(e.g. "findings.md" for an output path of /workspace/output/findings.md).<br />The operator resolves the full source path by scanning the named<br />producer's Outputs[] for an entry whose Path basename matches. |  |  |
| `mountPath` _string_ | MountPath is the absolute directory path on this teammate's pod<br />where the artifact will be made available. The operator creates<br />an emptyDir at MountPath and stages the artifact there via an<br />init container; the main container sees \{MountPath\}/\{Artifact\}. |  |  |


#### LeadSpec



LeadSpec defines the team lead configuration.



_Appears in:_
- [AgentTeamRunSpec](#agentteamrunspec)
- [AgentTeamSpec](#agentteamspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `model` _string_ | Model to use for the team lead. | opus | Enum: [opus sonnet haiku] <br /> |
| `prompt` _string_ | Prompt is the initial instruction for the team lead. |  |  |
| `permissionMode` _string_ | PermissionMode controls how the lead handles permission requests. | auto-accept | Enum: [auto-accept plan default] <br /> |
| `skills` _[SkillSpec](#skillspec) array_ | Skills to mount into .claude/skills/ for the lead agent. |  | Optional: \{\} <br /> |
| `mcpServers` _[MCPServerSpec](#mcpserverspec) array_ | MCPServers configures Model Context Protocol connections for the lead agent. |  | Optional: \{\} <br /> |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#resourcerequirements-v1-core)_ | Resources defines compute resources for the lead pod. |  | Optional: \{\} <br /> |


#### LifecycleSpec



LifecycleSpec controls team runtime behavior.



_Appears in:_
- [AgentTeamRunSpec](#agentteamrunspec)
- [AgentTeamSpec](#agentteamspec)
- [AgentTeamTemplateSpec](#agentteamtemplatespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `timeout` _string_ | Timeout is the maximum duration the team can run (e.g. "4h", "30m"). | 4h |  |
| `budgetLimit` _string_ | BudgetLimit is the maximum API spend in USD before the team is terminated (e.g. "10.00"). |  | Optional: \{\} <br /> |
| `onComplete` _string_ | OnComplete determines what happens when the team finishes. | notify | Enum: [create-pr push-branch notify none] <br /> |
| `pullRequest` _[PullRequestSpec](#pullrequestspec)_ | PullRequest configures PR creation when onComplete is "create-pr". |  | Optional: \{\} <br /> |
| `approvalGates` _[ApprovalGateSpec](#approvalgatespec) array_ | ApprovalGates pause execution before specified events until human approval is recorded.<br />Grant approval by annotating the AgentTeam: kubectl annotate agentteam <name> approved.kagents.dev/<event>=true |  | Optional: \{\} <br /> |
| `maxRestarts` _integer_ | MaxRestarts bounds how many times each teammate pod may be re-spawned<br />after a Failed phase before the team itself is marked Failed. The lead<br />pod is not subject to this limit; a lead crash always fails the team. | 3 | Minimum: 0 <br />Optional: \{\} <br /> |
| `githubTokenSecret` _string_ | GitHubTokenSecret names a Secret in the team's namespace carrying a<br />GitHub token under the key GITHUB_TOKEN. Used by OnComplete=create-pr<br />(and OnComplete=push-branch, once implemented) to authenticate against<br />the GitHub REST API. |  | Optional: \{\} <br /> |
| `prTitleTemplate` _string_ | PRTitleTemplate overrides the title template used by OnComplete=create-pr.<br />Available variables: .TeamName, .Namespace. When empty, falls back to<br />Spec.Lifecycle.PullRequest.TitleTemplate, then to the default<br />"claude-teams: \{\{.TeamName\}\}". |  | Optional: \{\} <br /> |
| `gitCredentialsSecret` _string_ | GitCredentialsSecret names a Secret in the team's namespace carrying git<br />push credentials. The Secret must contain either 'ssh-privatekey' or<br />'token'. Used by OnComplete=push-branch (and OnComplete=create-pr when<br />push-branch runs ahead of it). Falls back to Spec.Repository.CredentialsSecret<br />when unset, so teams that already configured clone credentials with push<br />scope don't need to duplicate. |  | Optional: \{\} <br /> |
| `consolidatedBranchTemplate` _string_ | ConsolidatedBranchTemplate is a Go template rendered to produce the<br />branch name pushed by OnComplete=push-branch. Available variables:<br />.TeamName, .Namespace. When empty, defaults to "teams/\{\{.TeamName\}\}". |  | Optional: \{\} <br /> |


#### MCPServerSpec



MCPServerSpec configures a Model Context Protocol server for an agent.



_Appears in:_
- [LeadSpec](#leadspec)
- [TeammateSpec](#teammatespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name identifies this MCP server in the agent's config. |  |  |
| `url` _string_ | URL is the MCP server endpoint. |  |  |
| `credentialsSecret` _string_ | CredentialsSecret references a Secret containing an 'apiKey' key for bearer auth. |  | Optional: \{\} <br /> |


#### MetricsSpec



MetricsSpec configures Prometheus metrics.



_Appears in:_
- [ObservabilitySpec](#observabilityspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ | Enabled turns on metrics exposition. |  |  |
| `port` _integer_ | Port for the metrics endpoint. | 9090 |  |


#### ObservabilitySpec



ObservabilitySpec configures monitoring and notifications.



_Appears in:_
- [AgentTeamSpec](#agentteamspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `metrics` _[MetricsSpec](#metricsspec)_ | Metrics configures Prometheus metrics exposition. |  | Optional: \{\} <br /> |
| `logLevel` _string_ | LogLevel controls operator log verbosity for this team. | info | Enum: [debug info warn error] <br /> |
| `webhook` _[WebhookSpec](#webhookspec)_ | Webhook configures event notifications. |  | Optional: \{\} <br /> |


#### OutputSpec



OutputSpec declares a file an agent produces. Downstream teammates
consume it by declaring a matching InputSpec; the operator also
records each output in AgentTeam.Status.Artifacts on completion.



_Appears in:_
- [TeammateSpec](#teammatespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `path` _string_ | Path is the absolute filesystem path on the producer pod where the<br />teammate writes the artifact. For Cowork teams this is typically a<br />path under the team's output mount (e.g. /workspace/output/findings.md). |  |  |
| `description` _string_ | Description is an optional human-readable summary of the artifact. |  | Optional: \{\} <br /> |


#### PullRequestSpec



PullRequestSpec configures automatic PR creation.



_Appears in:_
- [LifecycleSpec](#lifecyclespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `targetBranch` _string_ | TargetBranch is the branch to open the PR against. | main |  |
| `titleTemplate` _string_ | TitleTemplate is a Go template for the PR title.<br />Available variables: .TeamName, .Namespace |  |  |
| `reviewers` _string array_ | Reviewers to request on the PR. |  | Optional: \{\} <br /> |
| `labels` _string array_ | Labels to apply to the PR. |  | Optional: \{\} <br /> |


#### PullRequestStatus



PullRequestStatus reports PR creation state.



_Appears in:_
- [AgentTeamStatus](#agentteamstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `url` _string_ |  |  |  |
| `state` _string_ |  |  |  |


#### QualityGateSpec



QualityGateSpec configures validation steps.



_Appears in:_
- [AgentTeamSpec](#agentteamspec)
- [AgentTeamTemplateSpec](#agentteamtemplatespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `requireTests` _boolean_ | RequireTests ensures tests pass before completion. |  |  |
| `requireLint` _boolean_ | RequireLint ensures linting passes before completion. |  |  |
| `validationScript` _string_ | ValidationScript is a custom script to run before marking complete. |  | Optional: \{\} <br /> |


#### RepositorySpec



RepositorySpec defines the git repository configuration.



_Appears in:_
- [AgentTeamRunSpec](#agentteamrunspec)
- [AgentTeamSpec](#agentteamspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `url` _string_ | URL is the git clone URL. |  |  |
| `branch` _string_ | Branch to clone and work from. | main |  |
| `worktreeStrategy` _string_ | WorktreeStrategy determines how git worktrees are managed. | per-teammate | Enum: [per-teammate shared] <br /> |
| `credentialsSecret` _string_ | CredentialsSecret references a Secret containing git credentials.<br />The secret should contain either 'ssh-privatekey' or 'token'. |  | Optional: \{\} <br /> |


#### ScopeSpec



ScopeSpec restricts file access for a teammate.



_Appears in:_
- [TeammateSpec](#teammatespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `includePaths` _string array_ | IncludePaths lists paths the teammate should focus on. |  | Optional: \{\} <br /> |
| `excludePaths` _string array_ | ExcludePaths lists paths the teammate should not modify. |  | Optional: \{\} <br /> |


#### SkillSource



SkillSource identifies where to load a skill from. Exactly one field should be set.



_Appears in:_
- [SkillSpec](#skillspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `configMap` _string_ | ConfigMap references a ConfigMap in the same namespace.<br />Each key in the ConfigMap becomes a file in the skill directory. |  | Optional: \{\} <br /> |
| `oci` _string_ | OCI is an OCI artifact reference containing the skill files (e.g. "ghcr.io/org/skills/web-research:v1"). |  | Optional: \{\} <br /> |


#### SkillSpec



SkillSpec defines a Claude Code skill to mount into an agent pod.



_Appears in:_
- [LeadSpec](#leadspec)
- [TeammateSpec](#teammatespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the skill directory name under .claude/skills/. |  |  |
| `source` _[SkillSource](#skillsource)_ | Source identifies where to load the skill from. |  |  |


#### TaskSummary



TaskSummary reports aggregate task progress.



_Appears in:_
- [AgentTeamStatus](#agentteamstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `total` _integer_ |  |  |  |
| `completed` _integer_ |  |  |  |
| `inProgress` _integer_ |  |  |  |
| `pending` _integer_ |  |  |  |


#### TeammateSpec



TeammateSpec defines a single teammate agent.



_Appears in:_
- [AgentTeamSpec](#agentteamspec)
- [AgentTeamTemplateSpec](#agentteamtemplatespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the unique identifier for this teammate. |  | Pattern: `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$` <br /> |
| `model` _string_ | Model to use for this teammate. | sonnet | Enum: [opus sonnet haiku] <br /> |
| `prompt` _string_ | Prompt is the spawn instruction for this teammate. |  |  |
| `scope` _[ScopeSpec](#scopespec)_ | Scope restricts which files this teammate can access. |  | Optional: \{\} <br /> |
| `dependsOn` _string array_ | DependsOn lists teammate names that must complete before this one starts. |  | Optional: \{\} <br /> |
| `outputs` _[OutputSpec](#outputspec) array_ | Outputs declares the artifacts this teammate produces. Each entry<br />records a file path the teammate's prompt is expected to write.<br />On completion the operator records every declared output in<br />AgentTeam.Status.Artifacts and makes them available to any<br />downstream teammate that consumes them via Inputs. |  | Optional: \{\} <br /> |
| `inputs` _[InputSpec](#inputspec) array_ | Inputs declares the upstream-produced artifacts this teammate<br />consumes. Each entry names a producer teammate (From) and an<br />artifact basename (Artifact); the operator (a) treats From as<br />an implicit dependency — this teammate is not spawned until the<br />producer reaches Completed — and (b) wires an init container that<br />stages the artifact at MountPath on this teammate's pod before<br />the main container starts. |  | Optional: \{\} <br /> |
| `skills` _[SkillSpec](#skillspec) array_ | Skills to mount into .claude/skills/ for this teammate. |  | Optional: \{\} <br /> |
| `mcpServers` _[MCPServerSpec](#mcpserverspec) array_ | MCPServers configures Model Context Protocol connections for this teammate. |  | Optional: \{\} <br /> |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#resourcerequirements-v1-core)_ | Resources defines compute resources for this teammate's pod. |  | Optional: \{\} <br /> |


#### TeammateStatus



TeammateStatus reports a teammate's state.



_Appears in:_
- [AgentTeamStatus](#agentteamstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `podName` _string_ | PodName is the name of the agent's pod. |  |  |
| `phase` _string_ | Phase of this agent. |  | Enum: [Pending Running Idle Completed Failed Waiting] <br /> |
| `name` _string_ | Name matches the teammate's spec name. |  |  |
| `tasksCompleted` _integer_ | TasksCompleted is the number of tasks this teammate has finished. |  |  |
| `tasksClaimed` _integer_ | TasksClaimed is the number of tasks currently owned by this teammate. |  |  |
| `pendingApproval` _string_ | PendingApproval is the approval gate event this teammate is waiting on, if any. |  | Optional: \{\} <br /> |
| `restartCount` _integer_ | RestartCount is the number of times this teammate's pod has been<br />re-spawned after a Failed phase. The team is marked Failed when any<br />teammate's RestartCount reaches Spec.Lifecycle.MaxRestarts. |  | Optional: \{\} <br /> |


#### TemplateReference



TemplateReference points to an AgentTeamTemplate.



_Appears in:_
- [AgentTeamRunSpec](#agentteamrunspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name of the AgentTeamTemplate in the same namespace. |  |  |


#### WebhookSpec



WebhookSpec configures event notifications.



_Appears in:_
- [ObservabilitySpec](#observabilityspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `url` _string_ | URL to POST events to. |  |  |
| `events` _string array_ | Events to send notifications for. |  | MinItems: 1 <br /> |


#### WorkspaceInputSpec



WorkspaceInputSpec defines a read-only input mounted into the agent pod.



_Appears in:_
- [WorkspaceSpec](#workspacespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `configMap` _string_ | ConfigMap references a ConfigMap to mount as a directory. |  | Optional: \{\} <br /> |
| `pvc` _string_ | PVC references an existing PersistentVolumeClaim to mount read-only. |  | Optional: \{\} <br /> |
| `mountPath` _string_ | MountPath is where to mount this input inside the container. |  |  |


#### WorkspaceOutputSpec



WorkspaceOutputSpec defines the writable output volume for a Cowork team.



_Appears in:_
- [WorkspaceSpec](#workspacespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `pvc` _string_ | PVC is the name of an existing PVC to use. If empty, the operator creates one named "\{team\}-output". |  | Optional: \{\} <br /> |
| `storageClass` _string_ | StorageClass for the auto-created PVC. Defaults to "nfs". |  | Optional: \{\} <br /> |
| `size` _string_ | Size of the auto-created PVC. | 5Gi |  |
| `mountPath` _string_ | MountPath inside the container where the output volume is mounted. | /workspace/output |  |


#### WorkspaceSpec



WorkspaceSpec configures non-git inputs and outputs for Cowork teams.
Use this instead of (or alongside) Repository for knowledge-work tasks.



_Appears in:_
- [AgentTeamRunSpec](#agentteamrunspec)
- [AgentTeamSpec](#agentteamspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `inputs` _[WorkspaceInputSpec](#workspaceinputspec) array_ | Inputs are read-only volumes mounted into all agent pods. |  | Optional: \{\} <br /> |
| `output` _[WorkspaceOutputSpec](#workspaceoutputspec)_ | Output configures the shared writable output volume. |  | Optional: \{\} <br /> |


