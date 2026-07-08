<div align="center">

<img src="assets/banner.png" alt="kagents banner" width="800" />

# kagents

**Kubernetes-native AI knowledge work orchestration.**

[![Validate](https://github.com/amcheste/kagents/actions/workflows/validate.yml/badge.svg)](https://github.com/amcheste/kagents/actions/workflows/validate.yml)
[![Version](https://img.shields.io/github/v/tag/amcheste/kagents?label=version&sort=semver&color=0B0B0C)](https://github.com/amcheste/kagents/releases)
[![License](https://img.shields.io/badge/License-Apache_2.0-1F4D3A.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.23-00ADD8)](go.mod)

</div>

---

> **kagents** is the project brand. The implementation lives in the [`kagents`](https://github.com/amcheste/kagents) repository and ships under the `kagents.dev/v1alpha1` API group. Documentation site: [kagents.dev](https://kagents.dev) (under construction).

kagents is a Kubernetes operator for orchestrating teams of AI agents that produce **knowledge work** — research, analysis, reporting, document drafts, operational runbooks — declaratively, on your cluster, with budget caps, RBAC, observability, and human-in-the-loop approval gates built in. Teams are `AgentTeam` resources; the operator runs them, tracks their cost, and (in v0.8.0+) delivers the artifacts they produce to wherever humans actually consume them. **Coding is also supported** as a mode — and was the first use case kagents was built for — but it's now one mode among several rather than the headline.

Under the hood, kagents wraps Anthropic's native [Claude Code Agent Teams](https://docs.anthropic.com/en/docs/claude-code/agent-teams) protocol exactly — file-based mailboxes + a shared task list on a `ReadWriteMany` volume, no protocol translation. A `spec.harness` field (default `claude-code`) exists so a future team-based agent runtime can plug in behind the same operator API; today, Claude Code is the only supported harness. See [`docs/product-vision.md`](docs/product-vision.md) for where this is going.

## Modes

The operator supports two distinct use cases controlled by a single field on the `AgentTeam` spec:

| Mode | Use when | Key field |
|------|----------|-----------|
| **Cowork** | Agents produce knowledge artifacts — reports, documents, analysis, drafts, emails | `spec.workspace` |
| **Coding** | Agents work on a git repository | `spec.repository` |

Both modes share the same coordination protocol (shared PVCs, mailboxes, task lists) and all the cross-mode capabilities (skills, MCP servers, approval gates, budget/timeout, templates).

## Features

- **Cowork mode.** Run knowledge-work teams that produce documents, reports, and emails. Mount inputs from ConfigMaps/PVCs; collect outputs.
- **Native Agent Teams protocol.** Wraps Anthropic's file-based mailbox + task list format on ReadWriteMany PVCs; no protocol translation.
- **Harness-agnostic CRD.** `spec.harness` (default `claude-code`) so a future team-based agent runtime can plug in behind the same operator API.
- **Skills as CRD fields.** Mount Claude Code skills from ConfigMaps into each agent's `~/.claude/skills/`.
- **MCP servers per agent.** Configure Model Context Protocol connections per teammate.
- **Approval gates.** Pause spawning specific teammates until a human applies an annotation.
- **Budget enforcement.** Terminate the team if estimated API cost exceeds a configured limit.
- **Timeout enforcement.** Terminate the team after a configurable wall-clock duration.
- **`dependsOn` ordering.** Spawn teammates only after their declared dependencies complete.
- **Reusable templates.** Define team patterns with `AgentTeamTemplate`, instantiate with `AgentTeamRun`.
- **Per-teammate git worktrees** *(coding mode)*. Each coding agent works on an isolated branch to prevent merge conflicts.

## Quick Start

### Prerequisites

- Kubernetes 1.28+
- ReadWriteMany PVC support (NFS, EFS, or a compatible CSI driver. See [ARCHITECTURE.md § Storage Requirements](ARCHITECTURE.md#storage-requirements) for options)
- Claude Code CLI access (Max subscription or API key)
- Opus 4.6 model access (required for Agent Teams)

### Install on an existing cluster (Helm)

```bash
# 1. Install the operator (CRDs + controller + RBAC)
helm install kagents \
  oci://ghcr.io/amcheste/charts/kagents \
  --namespace kagents-system --create-namespace

# 2. Create an API key secret in the namespace where your teams will run
kubectl create namespace cowork-agents
kubectl create secret generic anthropic-api-key \
  --namespace cowork-agents \
  --from-literal=ANTHROPIC_API_KEY=sk-ant-...

# 3. Apply a sample team
kubectl apply -n cowork-agents -f \
  https://raw.githubusercontent.com/amcheste/kagents/main/config/samples/auth-refactor-team.yaml

# 4. Watch the team progress
kubectl get agentteams -n cowork-agents -w
kubectl describe agentteam auth-refactor -n cowork-agents
```

### Local development with Kind

For contributors and anyone who wants to run the full stack from source:

```bash
# 1. Create a Kind cluster with NFS provisioner
make kind-create

# 2. Build and load images into Kind
make docker-build docker-build-runner kind-load

# 3. Install CRDs and deploy the operator
make install deploy

# 4. Create your API key secret
kubectl create secret generic anthropic-api-key \
  --namespace cowork-agents \
  --from-literal=ANTHROPIC_API_KEY=sk-ant-...

# 5. Apply a sample team
kubectl apply -f config/samples/auth-refactor-team.yaml
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full dev loop (testing, linting, manifest regeneration).

## Example: Cowork Team

A four-agent team that produces a Q3 business report — research, drafting, design, and gated email follow-up:

```yaml
apiVersion: kagents.dev/v1alpha1
kind: AgentTeam
metadata:
  name: q3-report
  namespace: cowork-agents
spec:
  workspace:
    inputs:
      - configMap: "quarterly-data"
        mountPath: "/workspace/data"
    output:
      mountPath: "/workspace/output"
      size: "5Gi"

  auth:
    apiKeySecret: "anthropic-api-key"

  lead:
    model: "opus"
    prompt: "Coordinate the Q3 business report. Assign research, writing, and design."

  teammates:
    - name: "researcher"
      model: "sonnet"
      prompt: "Analyse the data in /workspace/data and produce a findings summary."
      skills:
        - name: "data-analysis"
          source:
            configMap: "data-analysis-skill"

    - name: "email-drafter"
      model: "sonnet"
      prompt: "Draft follow-up emails for all Q3 prospects."
      mcpServers:
        - name: "gmail"
          url: "https://gmail.mcp.example.com/mcp"

  lifecycle:
    timeout: "3h"
    budgetLimit: "15.00"
    approvalGates:
      - event: "spawn-email-drafter"
        channel: "webhook"
        webhookUrl: "https://hooks.example.com/approvals"
```

Grant approval after reviewing the researcher's output:

```bash
kubectl annotate agentteam q3-report \
  "approved.kagents.dev/spawn-email-drafter=true" \
  -n cowork-agents
```

## Example: Coding Team

A three-agent team migrating from JWT to OAuth2, with a PR opened on completion:

```yaml
apiVersion: kagents.dev/v1alpha1
kind: AgentTeam
metadata:
  name: auth-refactor
  namespace: dev-agents
spec:
  repository:
    url: "git@github.com:acme/backend.git"
    branch: "main"
    credentialsSecret: "git-credentials"

  auth:
    apiKeySecret: "anthropic-api-key"

  lead:
    model: "opus"
    prompt: |
      Coordinate the migration from JWT to OAuth2.
      Assign backend-api, frontend-auth, and test-coverage to their tracks.
      Validate integration when all tracks complete.

  teammates:
    - name: "backend-api"
      model: "sonnet"
      prompt: "Implement OAuth2 endpoints. Remove JWT middleware."
      scope:
        includePaths: ["src/api/auth/", "src/middleware/"]

    - name: "test-coverage"
      model: "sonnet"
      prompt: "Write comprehensive tests for the OAuth2 migration."
      dependsOn: ["backend-api"]

  lifecycle:
    timeout: "2h"
    budgetLimit: "30.00"
    onComplete: "create-pr"
    pullRequest:
      targetBranch: "main"
      titleTemplate: "feat(auth): migrate from JWT to OAuth2"
```

## What kagents is not

- **Not a general workflow engine.** kagents orchestrates AI agent *teams* with the agent protocol and human-in-the-loop semantics baked in. For arbitrary containerized DAGs, use [Argo Workflows](https://argoproj.github.io/workflows/) or [Temporal](https://temporal.io/).
- **Not a model or an agent.** kagents runs a harness (Claude Code today); it doesn't ship the intelligence.
- **Not a replacement for human judgment.** Approval gates and delivery review remain first-class.

## CRD Reference

### AgentTeam

The primary resource. Defines the full team, its workspace, lifecycle, and observability config.

| Field | Type | Description |
|-------|------|-------------|
| `spec.harness` | `string` | Agent runtime adapter. Defaults to `"claude-code"`. |
| `spec.workspace` | `WorkspaceSpec` | Input/output volumes (Cowork mode). Optional when `spec.repository` is set. |
| `spec.repository` | `RepositorySpec` | Git repo config (coding mode). Optional when `spec.workspace` is set. |
| `spec.auth` | `AuthSpec` | API key or OAuth secret reference. |
| `spec.lead` | `LeadSpec` | Lead agent model, prompt, skills, and MCP servers. |
| `spec.teammates` | `[]TeammateSpec` | Worker agents with optional `dependsOn`, `scope`, `skills`, `mcpServers`. |
| `spec.lifecycle.timeout` | `string` | Max duration, e.g. `"4h"`. Defaults to `"4h"`. |
| `spec.lifecycle.budgetLimit` | `string` | Max USD spend, e.g. `"10.00"`. No limit if unset. |
| `spec.lifecycle.onComplete` | `string` | `create-pr` \| `push-branch` \| `notify` \| `none` |
| `spec.lifecycle.approvalGates` | `[]ApprovalGateSpec` | Human-in-the-loop gates before spawning a teammate. |

### AgentTeamTemplate

A reusable team pattern. Does not run on its own. Instantiate with `AgentTeamRun`.

### AgentTeamRun

Instantiates an `AgentTeamTemplate` against a specific repo or workspace.

```yaml
apiVersion: kagents.dev/v1alpha1
kind: AgentTeamRun
metadata:
  name: q4-security-review
spec:
  templateRef:
    name: fullstack-review
  repository:
    url: "git@github.com:acme/platform.git"
    branch: "release/4.0"
    credentialsSecret: "git-credentials"
  auth:
    apiKeySecret: "anthropic-api-key"
  lead:
    model: "opus"
    prompt: "Run a full security, performance, and test quality review."
```

## Status

Watch team progress. The `Ready` column reports `running+completed/total` teammates, so `2/3` means two of three workers are up (or have finished) while one is still spawning or blocked on a dependency:

```bash
kubectl get agentteams -A
# NAME           PHASE      READY   TASKS DONE   COST    AGE
# q3-report      Running    2/3     7            $1.42   14m
# auth-refactor  Completed  3/3     12           $3.80   2h
```

Inspect details, including operator events emitted at every phase transition:

```bash
kubectl describe agentteam q3-report -n cowork-agents
# Status:
#   Phase: Running
#   Ready: 2/3
#   Estimated Cost: 1.42
#   Lead:
#     Pod Name: q3-report-lead
#     Phase: Running
#   Teammates:
#     - Name: researcher,    Phase: Running
#     - Name: email-drafter, Phase: Waiting  (approval-gated: spawn-email-drafter)
# Events:
#   Normal  Initializing  5m   agentteam-controller  Provisioned PVCs and launched workspace
#   Normal  Running       4m   agentteam-controller  All agent pods started
```

## Approval Gates

Approval gates block a teammate from being spawned until a human applies an annotation.

```bash
# Inspect which teammates are waiting
kubectl get agentteam my-team -o jsonpath='{.status.teammates[*].pendingApproval}'

# Grant approval
kubectl annotate agentteam my-team \
  "approved.kagents.dev/spawn-email-drafter=true"
```

If `channel: webhook` is set, the operator POSTs a JSON payload to `webhookUrl` when the gate is triggered, allowing an external system to present the approval to a human and then apply the annotation.

## Documentation

This README is the entry point. For deeper dives, every topic lives in a dedicated in-repo document:

| Document | Read when you want to… |
|----------|-----------------------|
| [docs/product-vision.md](docs/product-vision.md) | Understand where the project is going — knowledge-work orchestration, harness-agnostic future, positioning, success metrics. |
| [ARCHITECTURE.md](ARCHITECTURE.md) | Understand how the operator models Agent Teams. Phase state machine, PVC layout, RWX storage backends, coordination protocol, key design tradeoffs. |
| [TESTING.md](TESTING.md) | See the test strategy (unit / integration / acceptance / E2E), how to run each suite, and what each one actually verifies. |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Set up a dev environment, run the full build/test loop, follow the branch + PR workflow, and walk through "How to add a new reconciler feature." |
| [docs/helm-values.md](docs/helm-values.md) | Tune the Helm chart. Every value documented with defaults and production override recipes. |
| [SECURITY.md](SECURITY.md) | Report a vulnerability or review the project's security policy. |
| [MIGRATION.md](MIGRATION.md) | Upgrade between kagents versions. Currently covers the v0.7.x → v0.8.0 API-group migration (`claude.amcheste.io` → `kagents.dev`). |

## Development

Common Makefile targets (full loop in [CONTRIBUTING.md](CONTRIBUTING.md)):

```bash
make build        # Build operator binary
make test         # Run all tests
make lint         # Run golangci-lint
make manifests    # Regenerate CRD manifests
make generate     # Regenerate deepcopy methods
```

## License

Apache 2.0
</content>
</invoke>