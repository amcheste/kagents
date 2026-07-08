# Claude Code Agent Teams Kubernetes Operator

## RFC: `claude-teams-operator`

**Author:** Alan Chester
**Status:** Draft
**Date:** April 2026

---

## Problem Statement

Claude Code Agent Teams coordinate multiple Claude Code instances via local tmux sessions, file-based JSON mailboxes (`~/.claude/teams/`), and a shared task list (`~/.claude/tasks/`). This architecture is limited to a single machine's resources — CPU, memory, and API rate limits all bottleneck on one host.

For teams that want to run 10+ agent teammates in parallel across a codebase, or run overnight autonomous coding jobs, there's no way to distribute Agent Teams across a Kubernetes cluster — despite K8s being the natural fit for ephemeral, coordinated compute.

**Gas Town's operator** solves a related but different problem: it orchestrates Polecats (Gas Town's worker abstraction) as K8s pods. This operator targets **native Claude Code Agent Teams** — preserving Anthropic's built-in coordination primitives (shared task list, mailbox messaging, teammate spawning) while distributing execution across pods.

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                    Kubernetes Cluster (Kind / EKS / GKE)    │
│                                                             │
│  ┌───────────────────────────────────────────────────────┐  │
│  │              claude-teams-operator (Deployment)        │  │
│  │                                                       │  │
│  │  • Watches AgentTeam CRs                              │  │
│  │  • Reconciles team lead + teammate pods               │  │
│  │  • Manages shared volumes for mailbox/task state      │  │
│  │  • Monitors teammate health, restarts crashed agents  │  │
│  │  • Exposes metrics (tokens, task completion, costs)    │  │
│  └───────────────────────────────────────────────────────┘  │
│                                                             │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐   │
│  │ team-lead│  │teammate-1│  │teammate-2│  │teammate-3│   │
│  │   (Pod)  │  │   (Pod)  │  │   (Pod)  │  │   (Pod)  │   │
│  │          │  │          │  │          │  │          │   │
│  │ claude   │  │ claude   │  │ claude   │  │ claude   │   │
│  │ --model  │  │ --model  │  │ --model  │  │ --model  │   │
│  │ opus     │  │ sonnet   │  │ sonnet   │  │ sonnet   │   │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘   │
│       │              │              │              │         │
│  ┌────┴──────────────┴──────────────┴──────────────┴────┐   │
│  │           Shared PVC: team-state-volume               │   │
│  │                                                       │   │
│  │  /teams/{name}/inboxes/*.json    ← Mailbox system     │   │
│  │  /tasks/{name}/*.json            ← Shared task list   │   │
│  │  /teams/{name}/config.json       ← Team config        │   │
│  └───────────────────────────────────────────────────────┘   │
│                                                             │
│  ┌───────────────────────────────────────────────────────┐  │
│  │           Shared PVC: repo-volume                     │  │
│  │                                                       │  │
│  │  Git worktrees per teammate (isolated branches)       │  │
│  │  CLAUDE.md, AGENTS.md, .claude/ config                │  │
│  └───────────────────────────────────────────────────────┘  │
│                                                             │
│  ┌───────────────────────────────────────────────────────┐  │
│  │           ConfigMap: claude-config                     │  │
│  │                                                       │  │
│  │  CLAUDE.md content                                    │  │
│  │  AGENTS.md content                                    │  │
│  │  Permission settings                                  │  │
│  └───────────────────────────────────────────────────────┘  │
│                                                             │
│  ┌───────────────────────────────────────────────────────┐  │
│  │           Secret: claude-credentials                  │  │
│  │                                                       │  │
│  │  ANTHROPIC_API_KEY or OAuth tokens                    │  │
│  │  Git credentials (SSH key or token)                   │  │
│  └───────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

---

## Custom Resource Definitions

### 1. AgentTeam (primary CRD)

```yaml
apiVersion: kagents.dev/v1alpha1
kind: AgentTeam
metadata:
  name: auth-refactor
  namespace: dev-agents
spec:
  # --- Repository Configuration ---
  repository:
    url: "git@github.com:acme/backend.git"
    branch: "main"
    # Each teammate gets an isolated git worktree
    worktreeStrategy: "per-teammate"  # or "shared" for read-heavy tasks
    credentialsSecret: "git-credentials"

  # --- Authentication ---
  auth:
    # Option 1: API key (Console billing)
    apiKeySecret: "anthropic-api-key"
    # Option 2: OAuth subscription (Pro/Max/Team/Enterprise)
    # oauthSecret: "claude-oauth-token"

  # --- Team Lead Configuration ---
  lead:
    model: "opus"           # opus | sonnet
    prompt: |
      You are the team lead for refactoring the auth module from JWT to OAuth2.
      Break the work into parallel tracks. Coordinate teammates. Synthesize results.
      Create a PR when all work is validated.
    permissionMode: "auto-accept"  # auto-accept | plan | default
    resources:
      requests:
        cpu: "500m"
        memory: "1Gi"
      limits:
        cpu: "1"
        memory: "2Gi"

  # --- Teammate Definitions ---
  teammates:
    - name: "backend-api"
      model: "sonnet"
      prompt: "Implement OAuth2 endpoints in /api/auth/*. Update route handlers."
      scope:
        includePaths:
          - "src/api/auth/"
          - "src/middleware/"
        excludePaths:
          - "src/frontend/"
      resources:
        requests:
          cpu: "500m"
          memory: "1Gi"

    - name: "frontend-auth"
      model: "sonnet"
      prompt: "Update frontend auth components in src/components/auth/*."
      scope:
        includePaths:
          - "src/components/auth/"
          - "src/hooks/useAuth.ts"

    - name: "test-coverage"
      model: "sonnet"
      prompt: "Write integration and unit tests for the new OAuth2 flow."
      scope:
        includePaths:
          - "tests/auth/"
          - "tests/integration/"
      # This teammate depends on backend-api completing first
      dependsOn:
        - "backend-api"

  # --- Coordination Settings ---
  coordination:
    # How teammates communicate
    mailboxBackend: "shared-volume"  # shared-volume | redis | nats
    # Task list storage
    taskBackend: "shared-volume"     # shared-volume | beads
    # Optional: Beads integration for persistent tracking
    beads:
      enabled: false
      doltServerService: ""          # e.g., "dolt-beads.default.svc"

  # --- Lifecycle & Budget ---
  lifecycle:
    # Maximum time the team can run
    timeout: "4h"
    # Maximum total API spend (USD) before terminating
    budgetLimit: 50.00
    # What to do when the team finishes
    onComplete: "create-pr"          # create-pr | push-branch | notify | none
    # PR configuration
    pullRequest:
      targetBranch: "main"
      titleTemplate: "feat(auth): {{.TeamName}} - OAuth2 migration"
      reviewers:
        - "alan-cam"
      labels:
        - "agent-generated"
        - "needs-review"

  # --- Quality Gates (maps to Claude Code hooks) ---
  qualityGates:
    requireTests: true
    requireLint: true
    # Custom validation script run before marking team complete
    validationScript: |
      #!/bin/bash
      npm run test && npm run lint && npm run typecheck

  # --- Observability ---
  observability:
    # Expose Prometheus metrics
    metrics:
      enabled: true
      port: 9090
    # Stream agent logs to stdout (picked up by cluster logging)
    logLevel: "info"
    # Optional: send events to a webhook
    webhook:
      url: "https://hooks.slack.com/services/T.../B.../xxx"
      events:
        - "team.started"
        - "task.completed"
        - "teammate.error"
        - "team.completed"
        - "budget.warning"

status:
  phase: "Running"            # Pending | Initializing | Running | Completed | Failed | TimedOut
  startedAt: "2026-04-03T14:00:00Z"
  completedAt: null
  totalTokensUsed: 2450000
  estimatedCost: 18.75
  lead:
    podName: "auth-refactor-lead-xyz"
    phase: "Running"
  teammates:
    - name: "backend-api"
      podName: "auth-refactor-backend-api-abc"
      phase: "Running"
      tasksCompleted: 3
      tasksClaimed: 1
    - name: "frontend-auth"
      podName: "auth-refactor-frontend-auth-def"
      phase: "Idle"
      tasksCompleted: 2
      tasksClaimed: 0
    - name: "test-coverage"
      podName: "auth-refactor-test-coverage-ghi"
      phase: "Waiting"          # Blocked on backend-api
      tasksCompleted: 0
      tasksClaimed: 0
  tasks:
    total: 12
    completed: 5
    inProgress: 2
    pending: 5
  pullRequest:
    url: ""
    state: ""
  conditions:
    - type: "TeamsReady"
      status: "True"
      lastTransitionTime: "2026-04-03T14:01:00Z"
    - type: "BudgetHealthy"
      status: "True"
      message: "18.75 / 50.00 USD used"
```

### 2. AgentTeamTemplate (reusable team patterns)

```yaml
apiVersion: kagents.dev/v1alpha1
kind: AgentTeamTemplate
metadata:
  name: fullstack-review
  namespace: dev-agents
spec:
  description: "Standard 3-agent review team for full-stack PRs"
  teammates:
    - name: "security"
      model: "opus"
      prompt: "Audit for vulnerabilities, auth issues, injection risks."
    - name: "performance"
      model: "sonnet"
      prompt: "Profile hot paths, check for N+1 queries, review caching."
    - name: "test-quality"
      model: "sonnet"
      prompt: "Review test coverage, edge cases, flaky test patterns."
  coordination:
    mailboxBackend: "shared-volume"
    taskBackend: "shared-volume"
  lifecycle:
    timeout: "1h"
    budgetLimit: 20.00
```

### 3. AgentTeamRun (instance of a template)

```yaml
apiVersion: kagents.dev/v1alpha1
kind: AgentTeamRun
metadata:
  name: review-pr-482
  namespace: dev-agents
spec:
  templateRef:
    name: "fullstack-review"
  repository:
    url: "git@github.com:acme/backend.git"
    branch: "feature/new-dashboard"
  auth:
    apiKeySecret: "anthropic-api-key"
  lead:
    prompt: "Review PR #482 comprehensively using the team template."
  lifecycle:
    onComplete: "notify"
```

---

## Operator Reconciliation Logic

### Controller Loop (pseudocode)

```
func Reconcile(agentTeam):
    1. VALIDATE spec (auth, repo, teammate count ≤ 16)

    2. CREATE/UPDATE shared PVCs:
       - team-state-volume  (ReadWriteMany — mailbox + task JSON files)
       - repo-volume        (ReadWriteMany — git repo + worktrees)

    3. INIT JOB: Clone repo, create git worktrees per teammate
       - Run as a short-lived Job
       - Copy CLAUDE.md, AGENTS.md into place
       - Initialize ~/.claude/teams/{name}/ directory structure

    4. DEPLOY LEAD POD:
       - Mount both PVCs
       - Inject auth credentials from Secret
       - Set CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1
       - Set CLAUDE_CODE_TEAM_NAME={team-name}
       - Pass lead prompt via stdin or --prompt flag
       - Run: claude --model {model} --dangerously-skip-permissions \
              --permission-mode {mode} -p "{prompt}"

    5. DEPLOY TEAMMATE PODS (respecting dependsOn ordering):
       - For each teammate in spec.teammates:
         - Wait if dependsOn teammates haven't completed
         - Mount shared PVCs (same team-state + repo volumes)
         - Set CLAUDE_CODE_TEAM_NAME and CLAUDE_CODE_AGENT_NAME
         - Inject spawn prompt
         - Apply scope restrictions (symlink worktree paths)

    6. MONITOR LOOP:
       - Poll task list JSON for completion status
       - Watch pod health (CrashLoopBackOff → restart with context)
       - Track token usage via Claude Code's usage reporting
       - Check budget limits → terminate if exceeded
       - Detect idle teammates → notify lead or terminate

    7. ON COMPLETION:
       - Merge worktrees back to target branch
       - Run quality gate validation script
       - Create PR if configured
       - Fire webhook notifications
       - Update AgentTeam status

    8. CLEANUP:
       - Delete teammate pods
       - Delete lead pod
       - Retain PVCs for configurable retention period
       - Archive logs
```

### Key Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Mailbox transport | Shared PVC (file-based JSON) | Matches native Agent Teams protocol exactly — JSON inbox files. No protocol translation needed. |
| Git isolation | Worktrees per teammate | Prevents merge conflicts between concurrent agents. Each works on isolated branch. |
| PVC access mode | ReadWriteMany (RWX) | Multiple pods must read/write mailbox files. Requires NFS, EFS, or similar CSI driver. |
| Pod image | `anthropic/claude-code:latest` | Official Claude Code container. Falls back to custom image with `claude` CLI installed. |
| Model per teammate | Configurable | Lead on Opus for planning, teammates on Sonnet for cost efficiency. |
| Budget enforcement | Operator-side polling | Claude Code doesn't expose real-time token counts. Operator estimates from session duration + model. |

---

## Container Image

The operator needs a Claude Code container image. Since Anthropic doesn't publish one, we'd build:

```dockerfile
FROM node:22-slim

# Install Claude Code
RUN npm install -g @anthropic-ai/claude-code

# Install git (for worktrees)
RUN apt-get update && apt-get install -y git openssh-client && rm -rf /var/lib/apt/lists/*

# Create claude home directory
RUN mkdir -p /home/claude/.claude/teams /home/claude/.claude/tasks
WORKDIR /workspace

# Entrypoint script handles auth + prompt injection
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
```

---

## Operator Project Structure

```
claude-teams-operator/
├── cmd/
│   └── manager/
│       └── main.go              # Operator entrypoint
├── api/
│   └── v1alpha1/
│       ├── agentteam_types.go   # AgentTeam CRD types
│       ├── template_types.go    # AgentTeamTemplate CRD types
│       ├── run_types.go         # AgentTeamRun CRD types
│       └── zz_generated.deepcopy.go
├── internal/
│   ├── controller/
│   │   ├── agentteam_controller.go    # Main reconciler
│   │   ├── template_controller.go
│   │   └── run_controller.go
│   ├── claude/
│   │   ├── session.go           # Claude Code session management
│   │   ├── mailbox.go           # Mailbox file I/O
│   │   ├── tasklist.go          # Task list file I/O
│   │   └── worktree.go          # Git worktree management
│   ├── budget/
│   │   └── tracker.go           # Token usage estimation + limits
│   ├── webhook/
│   │   └── notifier.go          # Slack/webhook notifications
│   └── metrics/
│       └── prometheus.go        # Prometheus metrics exporter
├── config/
│   ├── crd/                     # Generated CRD manifests
│   ├── rbac/                    # RBAC for the operator
│   ├── manager/                 # Operator deployment
│   └── samples/                 # Example AgentTeam CRs
├── hack/
│   └── kind-setup.sh            # Kind cluster + NFS provisioner
├── charts/
│   └── claude-teams-operator/   # Helm chart
│       ├── Chart.yaml
│       ├── values.yaml
│       └── templates/
├── Dockerfile                   # Operator image
├── Dockerfile.claude-code       # Claude Code runner image
├── Makefile
├── go.mod
└── README.md
```

---

## Kind Development Setup

```bash
#!/bin/bash
# hack/kind-setup.sh

# Create Kind cluster with extra mounts for PVCs
cat <<EOF | kind create cluster --name claude-teams --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
  - role: worker
  - role: worker
  - role: worker
EOF

# Install NFS provisioner for ReadWriteMany PVCs
helm repo add nfs-ganesha https://kubernetes-sigs.github.io/nfs-ganesha-server-and-external-provisioner/
helm install nfs-server nfs-ganesha/nfs-server-provisioner \
  --namespace nfs --create-namespace \
  --set persistence.enabled=true \
  --set persistence.size=50Gi \
  --set storageClass.name=nfs \
  --set storageClass.defaultClass=false

# Install the operator
helm install claude-teams-operator ./charts/claude-teams-operator \
  --namespace claude-teams-system --create-namespace

# Create the API key secret
kubectl create secret generic anthropic-api-key \
  --namespace dev-agents \
  --from-literal=ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}

# Create git credentials secret
kubectl create secret generic git-credentials \
  --namespace dev-agents \
  --from-file=ssh-privatekey=${HOME}/.ssh/id_ed25519

# Deploy a sample team
kubectl apply -f config/samples/auth-refactor-team.yaml
```

---

## Prometheus Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `claude_team_active_total` | Gauge | Number of active AgentTeams |
| `claude_team_duration_seconds` | Histogram | Team runtime distribution |
| `claude_teammate_tokens_total` | Counter | Tokens consumed per teammate |
| `claude_team_cost_usd` | Gauge | Estimated cost per team |
| `claude_team_tasks_completed_total` | Counter | Tasks completed across teams |
| `claude_teammate_restarts_total` | Counter | Teammate pod restarts |
| `claude_team_budget_remaining_usd` | Gauge | Remaining budget per team |
| `claude_teammate_idle_seconds` | Histogram | Time teammates spend idle |

---

## Comparison: This Operator vs. Alternatives

| Feature | claude-teams-operator | gastown-operator | Multiclaude |
|---------|----------------------|------------------|-------------|
| Coordination model | Native Agent Teams (mailbox + task list) | Beads/Dolt + Polecats | Supervisor + subagents |
| K8s native | Yes (CRDs, operator pattern) | Yes (CRDs) | No (local CLI) |
| Budget controls | Built-in per-team limits | None | None |
| Quality gates | Hook-based validation | Manual | PR review |
| Multi-model support | Per-teammate model selection | Same model | Same model |
| Observability | Prometheus + webhooks | gt feed dashboard | Terminal UI |
| Git strategy | Worktrees per teammate | Worktrees per polecat | Worktrees |
| Beads integration | Optional | Required | Optional |
| Complexity | Medium | High | Low |
| Target user | Platform teams, CI/CD | Power users | Small teams |

---

## Roadmap

### v0.1.0 — MVP
- [ ] AgentTeam CRD with lead + teammate pods
- [ ] Shared PVC for mailbox and task state
- [ ] Git clone + worktree init job
- [ ] Basic budget tracking (time-based estimation)
- [ ] Webhook notifications on completion
- [ ] Kind development setup script

### v0.2.0 — Quality & Templates
- [ ] AgentTeamTemplate + AgentTeamRun CRDs
- [ ] Quality gate hooks (lint, test, typecheck)
- [ ] Automatic PR creation via GitHub API
- [ ] Prometheus metrics exporter

### v0.3.0 — Advanced Coordination
- [ ] Optional Beads/Dolt backend for task tracking
- [ ] Redis-backed mailbox for lower latency
- [ ] Teammate auto-scaling (spawn more if tasks pile up)
- [ ] CronJob-based scheduled team runs

### v0.4.0 — Enterprise
- [ ] Multi-repo support (monorepo teammate scoping)
- [ ] RBAC per-team (who can create/view teams)
- [ ] Cost allocation labels (team, project, department)
- [ ] Integration with ArgoCD for GitOps-triggered teams

---

## Open Questions

1. **Claude Code container image**: Anthropic doesn't publish an official Docker image. We'd need to build and maintain one. Risk: Claude Code updates could break the image.

2. **RWX PVC requirement**: ReadWriteMany is needed for the file-based mailbox protocol. Not all cloud providers support this cheaply. Alternative: Redis or NATS as mailbox transport, but this requires translating the JSON inbox protocol.

3. **Authentication at scale**: Max subscription has rolling 5-hour rate limits. Running 10+ teammates on one Max account will hit limits fast. API billing (Console) may be more predictable but costlier. Need guidance on best auth strategy per deployment size.

4. **Lead-less mode**: Could teammates self-organize without a lead pod? The native protocol requires a lead to spawn teammates. Alternative: operator acts as the lead (creates task list, manages mailboxes) and teammates are pure workers.

5. **Stateful recovery**: If a teammate pod crashes mid-task, can it resume? Native Agent Teams don't support session resumption. The operator would need to re-spawn the teammate with the task context injected as a fresh prompt.

---

## License

Apache 2.0.
