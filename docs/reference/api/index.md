# API Reference

## Packages
- [kagents.dev/v1alpha1](#kagentsdevv1alpha1)


## kagents.dev/v1alpha1

Package v1alpha1 contains API Schema definitions for the claude v1alpha1 API group.

Package v1alpha1 contains API Schema definitions for the claude v1alpha1 API group.

### Resource Types
- [AgentTeam](#agentteam)
- [AgentTeamRun](#agentteamrun)
- [AgentTeamSchedule](#agentteamschedule)
- [AgentTeamTemplate](#agentteamtemplate)
- [AgentTeamTrigger](#agentteamtrigger)



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


#### AgentTeamSchedule



AgentTeamSchedule creates AgentTeamRun instances on a cron schedule.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `kagents.dev/v1alpha1` | | |
| `kind` _string_ | `AgentTeamSchedule` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AgentTeamScheduleSpec](#agentteamschedulespec)_ |  |  |  |
| `status` _[AgentTeamScheduleStatus](#agentteamschedulestatus)_ |  |  |  |


#### AgentTeamScheduleSpec



AgentTeamScheduleSpec defines a cron-triggered pattern for creating
AgentTeamRun instances. On each fire, the operator creates one
AgentTeamRun from the referenced AgentTeamTemplate, parameterized
with the schedule's repository/workspace/auth/lead/lifecycle fields.
The existing AgentTeamRun controller then turns that Run into an
AgentTeam — schedules don't create teams directly, they produce
the same Run objects a human would create by hand.



_Appears in:_
- [AgentTeamSchedule](#agentteamschedule)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `schedule` _string_ | Schedule is a five-field cron expression in the operator's local<br />time zone (e.g. "0 6 * * MON" for Monday 06:00). Parsed by<br />robfig/cron/v3 with the standard parser. |  |  |
| `templateRef` _[TemplateReference](#templatereference)_ | TemplateRef names the AgentTeamTemplate to instantiate on each fire. |  |  |
| `auth` _[AuthSpec](#authspec)_ | Auth is forwarded to every created AgentTeamRun. |  |  |
| `lead` _[LeadSpec](#leadspec)_ | Lead configures the team lead for every created AgentTeamRun. |  |  |
| `repository` _[RepositorySpec](#repositoryspec)_ | Repository overrides the repository for each Run (coding mode). |  | Optional: \{\} <br /> |
| `workspace` _[WorkspaceSpec](#workspacespec)_ | Workspace overrides the workspace for each Run (Cowork mode). |  | Optional: \{\} <br /> |
| `lifecycle` _[LifecycleSpec](#lifecyclespec)_ | Lifecycle overrides forwarded to every created AgentTeamRun. |  | Optional: \{\} <br /> |
| `historyLimit` _integer_ | HistoryLimit caps how many completed Runs are retained in<br />status.runs[] and on the cluster. Once exceeded, the oldest<br />completed Runs are deleted. Set to 0 (or omit) to keep all Runs. |  | Minimum: 0 <br />Optional: \{\} <br /> |


#### AgentTeamScheduleStatus



AgentTeamScheduleStatus reports cron progress and Run history.



_Appears in:_
- [AgentTeamSchedule](#agentteamschedule)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `lastScheduledAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | LastScheduledAt is when the most recent fire occurred (the schedule<br />time that triggered Run creation, not the wall-clock reconcile time). |  | Optional: \{\} <br /> |
| `nextScheduledAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | NextScheduledAt is the time of the next scheduled fire as computed<br />from the cron expression. |  | Optional: \{\} <br /> |
| `activeRun` _string_ | ActiveRun names the in-flight AgentTeamRun, if any. Empty when no<br />run is currently executing. |  | Optional: \{\} <br /> |
| `runs` _[ScheduledRunStatus](#scheduledrunstatus) array_ | Runs is the recent history of Runs this schedule created. Truncated<br />to HistoryLimit (oldest entries dropped first). |  | Optional: \{\} <br /> |


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
| `pipeline` _[PipelineSpec](#pipelinespec)_ | Pipeline declares an ordered set of stages with explicit fan-out/merge<br />semantics. When set, the operator derives each teammate's effective<br />dependencies from the stage graph instead of the per-teammate DependsOn<br />field, which becomes mutually exclusive (enforced by CEL validation<br />on this spec). Inputs[].From still contributes regardless. |  | Optional: \{\} <br /> |
| `harness` _string_ | Harness selects the agent runtime that powers this team's pods.<br />Today the only supported value is "claude-code" (Anthropic's native<br />Claude Code Agent Teams protocol), which is also the default when<br />omitted. The field exists so the operator's API stays neutral to a<br />single agent runtime; future harnesses for other team-based agent<br />systems can plug in behind the same CRD without an API break. | claude-code | Enum: [claude-code] <br />Optional: \{\} <br /> |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#localobjectreference-v1-core) array_ | ImagePullSecrets are credentials for pulling private container<br />images, including OCI-distributed skills. The same secrets are<br />applied to agent pods (for the runner image) and to skill-puller<br />init containers (for pulling skill artifacts via ORAS). Use<br />kubernetes.io/dockerconfigjson Secrets — the operator mounts them<br />into the init container so ORAS can resolve registry credentials<br />from $DOCKER_CONFIG. |  | Optional: \{\} <br /> |


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
| `pipeline` _[PipelineStatus](#pipelinestatus)_ | Pipeline reports stage-level progress when spec.pipeline is set.<br />Recomputed every reconcile from teammate pod phases; cleared if<br />spec.pipeline is removed. |  | Optional: \{\} <br /> |
| `delivery` _[DeliveryStatus](#deliverystatus) array_ | Delivery records the outcome of every DeliveryTarget dispatched<br />by OnComplete=deliver. Populated once executeOnComplete has run;<br />each entry is independent — partial success is normal and the<br />team is not rolled back when individual targets fail. |  | Optional: \{\} <br /> |
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


#### AgentTeamTrigger



AgentTeamTrigger creates AgentTeamRun instances in response to events.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `kagents.dev/v1alpha1` | | |
| `kind` _string_ | `AgentTeamTrigger` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AgentTeamTriggerSpec](#agentteamtriggerspec)_ |  |  |  |
| `status` _[AgentTeamTriggerStatus](#agentteamtriggerstatus)_ |  |  |  |


#### AgentTeamTriggerSpec



AgentTeamTriggerSpec defines a webhook-driven AgentTeamRun creation
pattern. The kagents-trigger ingress deployment watches AgentTeamTrigger
resources, matches incoming HTTP requests against TriggerSource.Webhook,
applies HMAC validation + ConcurrencyPolicy, and creates one
AgentTeamRun per accepted event. The existing AgentTeamRun controller
then turns that Run into a team.

The trigger listener intentionally runs as its own Deployment
(kagents-trigger) rather than inside the operator manager: it's an
internet-reachable surface and shouldn't live inside the leader-elected
controller pod.



_Appears in:_
- [AgentTeamTrigger](#agentteamtrigger)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `trigger` _[TriggerSource](#triggersource)_ | Trigger defines which external signal fires this trigger.<br />Today only Webhook is supported; future expansion (watchResource,<br />schedule-on-event, etc.) is anticipated by the wrapper struct. |  |  |
| `templateRef` _[TemplateReference](#templatereference)_ | TemplateRef names the AgentTeamTemplate to instantiate on each fire. |  |  |
| `auth` _[AuthSpec](#authspec)_ | Auth is forwarded to every created AgentTeamRun. |  |  |
| `lead` _[LeadSpec](#leadspec)_ | Lead configures the team lead for every created AgentTeamRun. |  |  |
| `repository` _[RepositorySpec](#repositoryspec)_ | Repository overrides the repository for each Run (coding mode). |  | Optional: \{\} <br /> |
| `workspace` _[WorkspaceSpec](#workspacespec)_ | Workspace overrides the workspace for each Run (Cowork mode). |  | Optional: \{\} <br /> |
| `lifecycle` _[LifecycleSpec](#lifecyclespec)_ | Lifecycle overrides forwarded to every created AgentTeamRun. |  | Optional: \{\} <br /> |
| `payloadInjection` _[PayloadInjectionSpec](#payloadinjectionspec)_ | PayloadInjection configures how the incoming webhook payload is<br />surfaced to the agent pods. The kagents-trigger creates a ConfigMap<br />in the trigger's namespace carrying the request body, and adds a<br />matching read-only volume mount to the resulting team's workspace<br />inputs at PayloadInjection.MountPath. |  | Optional: \{\} <br /> |
| `concurrencyPolicy` _string_ | ConcurrencyPolicy governs what happens when a webhook fires while<br />a previous run is still in-flight.<br />  - Allow   (default) — always create a new Run.<br />  - Forbid  — reject the webhook with 409 if ActiveRun is set.<br />  - Replace — delete the active Run, then create a new one. | Allow | Enum: [Allow Forbid Replace] <br />Optional: \{\} <br /> |


#### AgentTeamTriggerStatus



AgentTeamTriggerStatus reports trigger bookkeeping.



_Appears in:_
- [AgentTeamTrigger](#agentteamtrigger)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `lastTriggeredAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | LastTriggeredAt is the most recent wall-clock time the listener<br />accepted a webhook for this trigger and created a Run. |  | Optional: \{\} <br /> |
| `activeRun` _string_ | ActiveRun names the in-flight AgentTeamRun, if any. Cleared once<br />the underlying Run reaches a terminal phase. |  | Optional: \{\} <br /> |
| `totalRuns` _integer_ | TotalRuns is the total number of Runs this trigger has produced<br />over its lifetime. |  |  |
| `runs` _[TriggerRunStatus](#triggerrunstatus) array_ | Runs is the recent history of Runs this trigger created. The<br />reconciler maintains this from labeled AgentTeamRuns in the same<br />namespace. |  | Optional: \{\} <br /> |


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
- [AgentTeamScheduleSpec](#agentteamschedulespec)
- [AgentTeamSpec](#agentteamspec)
- [AgentTeamTriggerSpec](#agentteamtriggerspec)

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


#### DeliveryStatus



DeliveryStatus records the result of a single DeliveryTarget dispatch.



_Appears in:_
- [AgentTeamStatus](#agentteamstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ | Type mirrors the originating DeliveryTarget.Type. |  |  |
| `target` _string_ | Target is a short human-readable label describing where this<br />delivery went (e.g. "#reports" for slack, the URL for webhook). |  | Optional: \{\} <br /> |
| `deliveredAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | DeliveredAt is when the sender finished — whether successfully<br />or not. |  |  |
| `success` _boolean_ | Success is true iff the sender returned no error. |  |  |
| `error` _string_ | Error carries the sender's failure message when Success is false. |  | Optional: \{\} <br /> |


#### DeliveryTarget



DeliveryTarget describes one artifact delivery destination fired when
OnComplete=deliver. The Type discriminator selects which fields are
meaningful — webhook + slack are functional in v0.8.0; email and
google-drive are accepted at the API level and dispatched to senders
that currently return a "not implemented" error recorded in
status.delivery[].

Across all types the operator never persists credentials itself; the
sender pulls them from CredentialsSecret at dispatch time so a
compromised operator pod can't enumerate Slack tokens / SMTP
passwords / Drive service-account keys at rest.



_Appears in:_
- [LifecycleSpec](#lifecyclespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ | Type names the delivery backend. |  | Enum: [webhook slack email google-drive] <br /> |
| `artifactPath` _string_ | ArtifactPath is the file path within the team's output workspace<br />(typically /workspace/output) to attach to or send as the<br />delivery body. Optional for delivery types that carry their<br />message inline (e.g. a plain Slack notification with no file). |  | Optional: \{\} <br /> |
| `message` _string_ | Message is the human-readable text that accompanies the delivery<br />(Slack message text, webhook notes, email body lead-in). |  | Optional: \{\} <br /> |
| `url` _string_ | URL is the destination for the webhook delivery type. |  | Optional: \{\} <br /> |
| `channel` _string_ | Channel is the destination for the slack delivery type<br />(e.g. "#reports"). The slack sender reads<br />CredentialsSecret["slack-webhook-url"] to know where to post. |  | Optional: \{\} <br /> |
| `to` _string array_ | To is the recipient list for the email delivery type. |  | Optional: \{\} <br /> |
| `subject` _string_ | Subject is the message subject for the email delivery type. |  | Optional: \{\} <br /> |
| `attachmentPath` _string_ | AttachmentPath is a file path within the team's output workspace<br />to attach to the email delivery. Equivalent to ArtifactPath but<br />kept distinct because some emails attach + reference a separate<br />artifact in the body. |  | Optional: \{\} <br /> |
| `folder` _string_ | Folder is the destination folder for the google-drive delivery<br />type. |  | Optional: \{\} <br /> |
| `credentialsSecret` _string_ | CredentialsSecret names a Secret in the team's namespace carrying<br />authentication for this target. Expected keys per type:<br />  - slack:        "slack-webhook-url"   — full https://hooks.slack.com/... URL<br />  - email:        "smtp-host", "smtp-port", "smtp-username", "smtp-password"<br />  - google-drive: "service-account.json"<br />Not required for webhook; the URL is in the spec. |  | Optional: \{\} <br /> |


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
- [AgentTeamScheduleSpec](#agentteamschedulespec)
- [AgentTeamSpec](#agentteamspec)
- [AgentTeamTriggerSpec](#agentteamtriggerspec)

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
- [AgentTeamScheduleSpec](#agentteamschedulespec)
- [AgentTeamSpec](#agentteamspec)
- [AgentTeamTemplateSpec](#agentteamtemplatespec)
- [AgentTeamTriggerSpec](#agentteamtriggerspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `timeout` _string_ | Timeout is the maximum duration the team can run (e.g. "4h", "30m"). | 4h |  |
| `budgetLimit` _string_ | BudgetLimit is the maximum API spend in USD before the team is terminated (e.g. "10.00"). |  | Optional: \{\} <br /> |
| `onComplete` _string_ | OnComplete determines what happens when the team finishes. | notify | Enum: [create-pr push-branch notify deliver none] <br /> |
| `delivery` _[DeliveryTarget](#deliverytarget) array_ | Delivery is the list of artifact delivery targets fired when<br />OnComplete=deliver. Each target is dispatched independently;<br />per-target success/failure is recorded in status.delivery[].<br />Delivery failure is best-effort — the team is not rolled back to<br />Failed if a target rejects the request. |  | Optional: \{\} <br /> |
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


#### PayloadInjectionSpec



PayloadInjectionSpec configures payload mounting on triggered teams.



_Appears in:_
- [AgentTeamTriggerSpec](#agentteamtriggerspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `mountPath` _string_ | MountPath is the absolute file path inside agent pods where the<br />incoming webhook payload appears (e.g. "/workspace/data/trigger-payload.json").<br />The directory is created if it doesn't exist; the file is read-only. |  |  |


#### PipelineSpec



PipelineSpec models a multi-stage workflow with explicit fan-out/merge
as an alternative to flat per-teammate DependsOn. Each teammate is
listed in exactly one stage; the stage graph determines spawn ordering.



_Appears in:_
- [AgentTeamSpec](#agentteamspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `stages` _[StageSpec](#stagespec) array_ | Stages is the ordered list of stages. Ordering is for readability;<br />runtime ordering follows the StageSpec.DependsOn graph. |  | MinItems: 1 <br /> |


#### PipelineStatus



PipelineStatus reports stage-level progress for a pipelined team.



_Appears in:_
- [AgentTeamStatus](#agentteamstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `currentStage` _string_ | CurrentStage names the lowest-indexed stage that has not yet<br />reached Completed. Empty when every stage is Completed. |  | Optional: \{\} <br /> |
| `stagesCompleted` _integer_ | StagesCompleted is the count of stages whose teammates have all<br />reached Succeeded. |  |  |
| `stagesTotal` _integer_ | StagesTotal is the total number of stages declared on the spec. |  |  |
| `stages` _[StageStatus](#stagestatus) array_ | Stages reports per-stage detail. |  | Optional: \{\} <br /> |


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
- [AgentTeamScheduleSpec](#agentteamschedulespec)
- [AgentTeamSpec](#agentteamspec)
- [AgentTeamTriggerSpec](#agentteamtriggerspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `url` _string_ | URL is the git clone URL. |  |  |
| `branch` _string_ | Branch to clone and work from. | main |  |
| `worktreeStrategy` _string_ | WorktreeStrategy determines how git worktrees are managed. | per-teammate | Enum: [per-teammate shared] <br /> |
| `credentialsSecret` _string_ | CredentialsSecret references a Secret containing git credentials.<br />The secret should contain either 'ssh-privatekey' or 'token'. |  | Optional: \{\} <br /> |


#### ScheduledRunStatus



ScheduledRunStatus records a single fire's bookkeeping.



_Appears in:_
- [AgentTeamScheduleStatus](#agentteamschedulestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the AgentTeamRun resource name. |  |  |
| `scheduledAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | ScheduledAt is the cron-computed time this run was triggered for. |  |  |
| `phase` _string_ | Phase mirrors the underlying AgentTeamRun's last observed status.phase. |  | Optional: \{\} <br /> |


#### ScopeSpec



ScopeSpec restricts file access for a teammate.



_Appears in:_
- [TeammateSpec](#teammatespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `includePaths` _string array_ | IncludePaths lists paths the teammate should focus on. |  | Optional: \{\} <br /> |
| `excludePaths` _string array_ | ExcludePaths lists paths the teammate should not modify. |  | Optional: \{\} <br /> |


#### SkillSource



SkillSource identifies where to load a skill from. Exactly one of
ConfigMap or OCI must be set (enforced by CEL on SkillSpec).

ConfigMap is simplest and lives entirely within the cluster — good
for skills authored alongside the team CRs. OCI distributes skills
as registry artifacts so they can be versioned, signed, shared
across clusters, and pulled from public or private registries.



_Appears in:_
- [SkillSpec](#skillspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `configMap` _string_ | ConfigMap references a ConfigMap in the same namespace.<br />Each key in the ConfigMap becomes a file in the skill directory. |  | Optional: \{\} <br /> |
| `oci` _string_ | OCI is an OCI artifact reference containing the skill files<br />(e.g. "ghcr.io/org/skills/web-research:v1"). The operator runs<br />an `oras pull` init container at pod startup to materialize the<br />skill onto an emptyDir; the main container then sees the files<br />under ~/.claude/skills/\{name\}/. Private registries are supported<br />via spec.imagePullSecrets.<br />Re-pull semantics: the init container runs once per pod start,<br />so the artifact is re-pulled on every pod create. There is no<br />shared cache between pods — operators who want one should pin<br />to immutable digests so the registry can short-circuit identical<br />pulls cheaply. |  | Optional: \{\} <br /> |


#### SkillSpec



SkillSpec defines a Claude Code skill to mount into an agent pod.



_Appears in:_
- [LeadSpec](#leadspec)
- [TeammateSpec](#teammatespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the skill directory name under .claude/skills/. |  |  |
| `source` _[SkillSource](#skillsource)_ | Source identifies where to load the skill from. |  |  |


#### StageSpec



StageSpec defines one stage of a pipeline.



_Appears in:_
- [PipelineSpec](#pipelinespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the unique identifier for this stage within the pipeline. |  | Pattern: `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$` <br /> |
| `teammates` _string array_ | Teammates names the teammates that participate in this stage. Names<br />must match spec.teammates[].name. A teammate may not appear in more<br />than one stage. |  | MinItems: 1 <br /> |
| `dependsOn` _string array_ | DependsOn names earlier stages this one waits on. Every teammate in<br />every listed stage must reach Succeeded before any teammate in this<br />stage is spawned. |  | Optional: \{\} <br /> |
| `fan` _string_ | Fan documents the stage's relationship to its dependencies and is<br />informational in v0.8.0 — both values currently behave identically.<br />"parallel" (default) signals a normal fan-out stage; "merge" signals<br />a synthesis stage that consumes outputs from multiple upstream<br />branches. Distinct runtime semantics are reserved for a future<br />version. | parallel | Enum: [parallel merge] <br />Optional: \{\} <br /> |
| `approvalRequired` _boolean_ | ApprovalRequired gates the entire stage on a human approval<br />annotation `approved.kagents.dev/stage-\{name\}=true` on the<br />AgentTeam. No teammate in this stage spawns until the annotation<br />is present. |  | Optional: \{\} <br /> |


#### StageStatus



StageStatus reports a single stage's runtime state.



_Appears in:_
- [PipelineStatus](#pipelinestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name matches the StageSpec.Name. |  |  |
| `phase` _string_ | Phase is one of Waiting, PendingApproval, Running, Completed, Failed. |  | Enum: [Waiting PendingApproval Running Completed Failed] <br />Optional: \{\} <br /> |
| `startedAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | StartedAt is when the first teammate in this stage was spawned. |  | Optional: \{\} <br /> |
| `completedAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | CompletedAt is when the last teammate in this stage reached Succeeded. |  | Optional: \{\} <br /> |
| `teammatesReady` _string_ | TeammatesReady reports completed-vs-total teammates for this stage<br />in the form "N/M" (e.g. "2/3"). |  | Optional: \{\} <br /> |


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
- [AgentTeamScheduleSpec](#agentteamschedulespec)
- [AgentTeamTriggerSpec](#agentteamtriggerspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name of the AgentTeamTemplate in the same namespace. |  |  |


#### TriggerRunStatus



TriggerRunStatus records a single triggered fire.



_Appears in:_
- [AgentTeamTriggerStatus](#agentteamtriggerstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the AgentTeamRun resource name. |  |  |
| `triggeredAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#time-v1-meta)_ | TriggeredAt is when the listener accepted the webhook. |  |  |
| `phase` _string_ | Phase mirrors the AgentTeamRun's status.phase. |  | Optional: \{\} <br /> |


#### TriggerSource



TriggerSource is a discriminated union of supported trigger types.
Exactly one inner field should be set.



_Appears in:_
- [AgentTeamTriggerSpec](#agentteamtriggerspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `webhook` _[WebhookTriggerSpec](#webhooktriggerspec)_ | Webhook fires the trigger when an HTTP POST arrives at the<br />configured Path on the kagents-trigger service. |  | Optional: \{\} <br /> |


#### WebhookSpec



WebhookSpec configures event notifications.



_Appears in:_
- [ObservabilitySpec](#observabilityspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `url` _string_ | URL to POST events to. |  |  |
| `events` _string array_ | Events to send notifications for. |  | MinItems: 1 <br /> |


#### WebhookTriggerSpec



WebhookTriggerSpec configures a webhook trigger.



_Appears in:_
- [TriggerSource](#triggersource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `path` _string_ | Path is the URL path on the trigger service that fires this trigger<br />(e.g. "/hooks/new-deal"). Each trigger's Path must be unique within<br />the cluster; the listener matches incoming requests by exact prefix. |  | Pattern: `^/[A-Za-z0-9._/-]+$` <br /> |
| `secret` _string_ | Secret names a Secret in the trigger's namespace containing the<br />shared secret used to validate the request's HMAC signature<br />(key: "hmac-secret"). When empty, HMAC validation is skipped —<br />recommended only for traffic that's already authenticated upstream<br />(e.g. by an ingress with mTLS). |  | Optional: \{\} <br /> |


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
- [AgentTeamScheduleSpec](#agentteamschedulespec)
- [AgentTeamSpec](#agentteamspec)
- [AgentTeamTriggerSpec](#agentteamtriggerspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `inputs` _[WorkspaceInputSpec](#workspaceinputspec) array_ | Inputs are read-only volumes mounted into all agent pods. |  | Optional: \{\} <br /> |
| `output` _[WorkspaceOutputSpec](#workspaceoutputspec)_ | Output configures the shared writable output volume. |  | Optional: \{\} <br /> |


