# Architecture

This document describes the design of the kagents: how it models Claude Code Agent Teams as Kubernetes resources, why key decisions were made, and how the components fit together.

> **Where the project is heading (kagents).** The project is pivoting to the harness-agnostic **kagents** brand — a Kubernetes-native knowledge-work orchestrator, with Claude Code as the first supported agent harness. The canonical forward plan lives in three docs:
> [Product Vision](docs/product-vision.md) (why) · [Product Requirements](docs/knowledge-work-prd.md) (what) · [Technical Design](docs/knowledge-work-design.md) (how).
> This document describes the system **as built today**; the design doc describes the deltas (rebrand, harness adapter seam, pipelines, scheduling, triggers, delivery, OCI skills) as they land.

## Overview

The operator watches `AgentTeam` custom resources and reconciles the cluster toward the desired state. At steady state, a running team looks like this:

```
AgentTeam CR
  │
  ├── team-state PVC (ReadWriteMany)      ← shared .claude/teams/ and .claude/tasks/
  ├── repo PVC (ReadWriteMany, coding)    ← git clone + per-teammate worktrees
  ├── output PVC (ReadWriteMany, Cowork)  ← shared writable output volume
  │
  ├── init Job (coding mode only)         ← clones repo, creates git worktrees
  │
  ├── lead Pod                            ← Claude Code (opus), team coordinator
  ├── teammate Pod: worker-a              ← Claude Code (sonnet), assigned tasks
  ├── teammate Pod: worker-b
  └── ...
```

All pods are owned by the `AgentTeam` via owner references, so deleting the CR cascades to all resources.

## Phase State Machine

The reconciler routes based on `status.phase`:

```
(new CR)
    │
    ▼
 Pending ──────────────────► Failed
    │                         ▲
    │ PVCs created,           │
    │ init Job started        │ init Job failed
    ▼                         │
Initializing ─────────────────┘
    │
    │ init Job succeeded,
    │ pods deployed
    ▼
 Running ──── timeout ──────► TimedOut
    │   └─── budget ────────► BudgetExceeded
    │   └─── pod failed ────► Failed
    │
    │ all pods Succeeded
    ▼
Completed
    │
    ▼
  (terminal: pods deleted, completedAt stamped)
```

Terminal phases (`Completed`, `Failed`, `TimedOut`, `BudgetExceeded`) trigger `reconcileTerminal`, which deletes all owned pods and stamps `status.completedAt`. The reconciler stops requeuing after that.

## Volume Layout

Agent Teams coordination requires multiple agents to share filesystem state. We use three PVCs, all requiring ReadWriteMany access (NFS or equivalent):

```
/var/claude-state/          ← team-state PVC
  teams/{team}/
    inboxes/{agent}.json    ← peer-to-peer mailboxes
  tasks/{team}/
    tasks.json              ← shared task list

/workspace/                 ← repo PVC (coding mode only)
  repo/                     ← git clone
  worktrees/
    {teammate-name}/        ← isolated git worktree per teammate

/workspace/output/          ← output PVC (Cowork mode only)
  ...                       ← agent-produced files
```

Each agent pod mounts the team-state PVC at `/var/claude-state`. The entrypoint symlinks the `teams/` and `tasks/` subdirectories into `~/.claude/` so Claude Code finds them at the expected paths:

```bash
ln -sfn /var/claude-state/teams ~/.claude/teams
ln -sfn /var/claude-state/tasks ~/.claude/tasks
```

This approach preserves the native Agent Teams protocol without modification while avoiding a conflict between the shared state and the agent's local `~/.claude/` configuration.

## Storage Requirements

All operator-managed PVCs. `team-state`, `repo`, and (in Cowork mode) `output`. Default to `ReadWriteMany` access on a StorageClass named `nfs`. The requirement is not incidental: the lead and every teammate pod must open the same mailbox and task files concurrently, and on a multi-node cluster they will generally land on different nodes. `ReadWriteOnce` can only bind to one node at a time, so it is not a viable default.

### Why ReadWriteMany

Each agent pod does two concurrent things against shared state:

- **Writing into peers' inboxes**. The lead writes `teams/{team}/inboxes/{teammate}.json`; each teammate writes to the lead's inbox and occasionally to other teammates'.
- **Claiming tasks**. Multiple teammates race to claim items from `tasks/{team}/tasks.json`.

If the backing PVC cannot be mounted on more than one node, the second pod will fail to schedule (`volume already attached to a different node`) and the team deadlocks before the first mailbox round-trip.

### Supported storage backends

The operator itself has no opinion about the CSI driver. It asks for a PVC with `accessModes: [ReadWriteMany]` and a `storageClassName` that you supply. The table below lists drivers known to satisfy the RWX contract:

| Platform | Driver | Notes |
|----------|--------|-------|
| Kind (multi-node dev) | `nfs-ganesha/nfs-server-provisioner` | Installed by `hack/kind-setup.sh` as StorageClass `nfs`. Real RWX over an in-cluster NFS server. |
| Kind (single-node acceptance) | `rancher.io/local-path` under the `nfs` StorageClass alias | Installed by `hack/acceptance-setup.sh`. See "Single-node fallback"; not true RWX. |
| Amazon EKS | [EFS CSI driver](https://github.com/kubernetes-sigs/aws-efs-csi-driver) | StorageClass pointing at an EFS file system. RWX natively. |
| Google GKE | [Filestore CSI driver](https://cloud.google.com/filestore/docs/csi-driver) | Enable the Filestore CSI add-on; Filestore instances advertise RWX. |
| Azure AKS | [Azure Files CSI driver](https://learn.microsoft.com/azure/aks/azure-files-csi) | SMB or NFS-protocol file shares; both support RWX. |
| Bare-metal / on-prem | [`nfs-subdir-external-provisioner`](https://github.com/kubernetes-sigs/nfs-subdir-external-provisioner), Longhorn, Rook/Ceph (CephFS) | Any CSI driver that advertises `ReadWriteMany` in its StorageClass. |

The StorageClass name the operator requests defaults to `nfs` and is overridable at install time via the Helm value `storage.storageClassName`.

### Single-node fallback

For laptops and CI. Kind, k3d, minikube. A full RWX provisioner is overkill. The operator accepts a `--pvc-access-mode=ReadWriteOnce` flag that switches every managed PVC from `ReadWriteMany` to `ReadWriteOnce`. This works **only** on single-node clusters, because every pod lands on the same node and a hostPath-backed RWO PVC is effectively visible to all of them.

`hack/acceptance-setup.sh` uses exactly this trick: it creates an alias StorageClass named `nfs` over `rancher.io/local-path` so the operator's PVC specs still validate, then sets `--pvc-access-mode=ReadWriteOnce` on the controller deployment.

The architectural claim. That a shared mount is sufficient to ferry mailbox JSON between pods. Can be verified on any single-node cluster with:

```bash
make acceptance-up
make mailbox-smoke-test
```

The smoke test reports the effective StorageClass and AccessMode on its PASS line so it is obvious whether you exercised real RWX or the single-node fallback.

> **Do not use `--pvc-access-mode=ReadWriteOnce` on a multi-node production cluster.** A second pod scheduled on a different node will fail to mount the PVC, and the team will deadlock.

## Coordination Protocol

The native Agent Teams protocol is file-based:

- **Mailboxes**. Each agent has a JSON inbox at `~/.claude/teams/{team}/inboxes/{agent}.json`. Agents read their own inbox for messages from teammates.
- **Task list**. A shared JSON file at `~/.claude/tasks/{team}/tasks.json`. The lead writes tasks; teammates claim and update them.

The operator does not implement or speak this protocol. It only creates the shared PVC that makes the filesystem visible to all pods. Claude Code manages the protocol itself.

## Coding Mode

When `spec.repository` is set, the operator runs an init Job before deploying pods. The init Job:

1. Clones the repository into `/workspace/repo`
2. Creates one git worktree per teammate at `/workspace/worktrees/{name}` on a dedicated branch `teammate-{name}`
3. Initialises the team-state directories and an empty task list

Each teammate pod receives `WORKTREE_PATH=worktrees/{name}`, and the entrypoint `cd`s to that path before launching Claude Code. The lead has no worktree path and works directly from `/workspace/repo`.

Per-worktree isolation prevents git conflicts between concurrent agents. Each agent commits to its own branch, and the lead (or an `onComplete` action) handles merging.

## Cowork Mode

When `spec.workspace` is set (and `spec.repository` is absent or minimal), the operator skips the init Job and instead:

- Creates an output PVC for writable agent output
- Mounts workspace inputs (ConfigMaps or existing PVCs) read-only into each pod
- Does not set `WORKTREE_PATH`. Agents work in `/workspace/output` or `/workspace/data`

The entrypoint detects the absence of a git repo gracefully and skips the `git log` startup output.

## Skills

Claude Code skills live under `~/.claude/skills/{name}/`. The operator mounts ConfigMap-backed skills at `/var/claude-skills/{name}/` and the entrypoint copies them to `~/.claude/skills/{name}/` before launching Claude Code.

Skills are per-agent. The same skill ConfigMap can be mounted into multiple pods independently, so different teammates can have different skill sets.

## MCP Servers

Each agent can declare MCP server connections in its spec. The operator creates a per-agent ConfigMap containing the `.mcp.json` configuration and mounts it at `/var/claude-mcp/`. The entrypoint copies it to `~/.mcp.json`.

Since MCP credential secrets cannot be read by the operator (and should not be), only the URL is written to the ConfigMap. Auth credentials (API keys, bearer tokens) are injected as environment variables via separate `credentialsSecret` references and consumed by the MCP server client inside Claude Code.

## Approval Gates

Approval gates prevent a teammate from being spawned until a human explicitly approves. The gate is identified by an event name, conventionally `spawn-{teammate-name}`.

When the reconciler would otherwise spawn a teammate, it first checks:

1. Is there an `ApprovalGateSpec` for this event?
2. If yes, does the `AgentTeam` have the annotation `approved.kagents.dev/{event}=true`?

If the annotation is absent, the teammate is not spawned. The reconciler marks the teammate's `status.pendingApproval` field and (if `channel: webhook`) POSTs a notification to the configured URL so an external system can present the approval request to a human.

Approval is granted by annotating the `AgentTeam`:

```bash
kubectl annotate agentteam my-team "approved.kagents.dev/spawn-email-drafter=true"
```

The next reconcile loop (within 30 seconds) sees the annotation and spawns the teammate.

## DependsOn Ordering

Teammates can declare `dependsOn`. A list of other teammate names that must reach `Succeeded` phase before this teammate is spawned. The check runs every reconcile loop:

- In `reconcileInitializing`: initial pod deployment respects dependency order
- In `reconcileRunning`: newly unblocked teammates are spawned automatically as their dependencies complete

This enables sequential pipelines (e.g. `researcher → writer → reviewer`) within a single `AgentTeam`.

## Budget Estimation

Claude Code does not expose real-time token counts externally. The operator estimates cost from elapsed time and model type:

| Model | Assumed rate | Cost per minute |
|-------|-------------|-----------------|
| opus | 1,500 tokens/min | $0.0225 |
| sonnet | 3,000 tokens/min | $0.0090 |

These are conservative estimates. The actual cost depends on prompt length, context window usage, and task complexity. Set `budgetLimit` with appropriate headroom.

When the estimate exceeds `budgetLimit`, the operator terminates all pods and sets the phase to `BudgetExceeded`. Real cost tracking is tracked as a future improvement.

## Key Design Decisions

### Why shared PVC over a message bus?

Agent Teams uses a file-based protocol. Rather than translating it to Redis or NATS, we preserve it exactly by mounting a shared filesystem. This means no changes to Claude Code itself, no protocol versioning concerns, and no additional infrastructure dependencies for simple deployments. The tradeoff is the requirement for ReadWriteMany PVC support. NFS or a cloud-native equivalent like EFS or GCP Filestore.

### Why RestartPolicy: Never?

Claude Code Agent Teams does not support session resumption. A crashed agent has lost its context window. We re-spawn with `RestartPolicy: Never` and rely on the shared task list to tell the fresh agent what work remains, rather than attempting a stateful restart.

### Why estimation-based budget tracking?

There is no public API to query Claude Code's real-time token usage from outside the process. Estimation from time × model is the only approach available without instrumenting the Claude Code binary itself.

### Why owner references for all child resources?

PVCs, Jobs, Pods, and ConfigMaps are all owned by the `AgentTeam`. Deleting the CR cascades to all child resources automatically via Kubernetes garbage collection. This prevents orphaned PVCs (which can be expensive) and makes teardown reliable.

## File Structure

```
api/v1alpha1/
  agentteam_types.go       # AgentTeam, AgentTeamTemplate, AgentTeamRun specs + status
  template_types.go        # AgentTeamTemplate and AgentTeamRun types
  groupversion_info.go     # API group and scheme registration
  zz_generated.deepcopy.go # Generated DeepCopy methods (do not edit)

internal/controller/
  agentteam_controller.go  # Reconciler: all phases, helpers, pod builder
  agentteam_controller_test.go

cmd/manager/
  main.go                  # Operator entrypoint: scheme setup, manager startup

config/
  crd/bases/               # Generated CRD manifests (do not edit)
  rbac/role.yaml           # Generated RBAC role (do not edit)
  samples/                 # Example AgentTeam and AgentTeamTemplate CRs

docker/
  Dockerfile.operator      # Distroless operator image
  Dockerfile.claude-code   # Claude Code runner image (agent pods)
  entrypoint.sh            # Agent pod startup: symlinks, skills, MCP, launch

charts/kagents/  # Helm chart

hack/
  kind-setup.sh            # Kind cluster + NFS provisioner dev setup
  boilerplate.go.txt       # License header for generated files
```

## Roadmap

- **OCI skill artifacts**. Pull skills from OCI registries instead of ConfigMaps
- **Real token tracking**. Instrument or sidecar Claude Code to capture actual usage
- **envtest integration tests**. Full reconcile loop tests against a real API server
- **Horizontal scaling**. Multiple operator replicas with leader election
- **Beads/Dolt integration**. Persistent task tracking across team runs
- **`AgentTeamRun` controller**. Reconciler for the template-instantiation CRD
