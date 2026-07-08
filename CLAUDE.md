# kagents

A Kubernetes operator that runs Claude Code Agent Teams as distributed pods on a K8s cluster.

## Project Overview

This operator brings Anthropic's native Claude Code Agent Teams feature — which normally runs locally via tmux — into Kubernetes. It preserves the native coordination protocol (file-based JSON mailboxes + shared task list) by mounting shared ReadWriteMany PVCs across agent pods.

### Architecture

- **Operator** (Go, controller-runtime) watches `AgentTeam` CRDs and reconciles pods
- **Lead pod** runs Claude Code as team lead with `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`
- **Teammate pods** run Claude Code instances that communicate via shared filesystem
- **Shared PVCs** hold the mailbox JSON files (`~/.claude/teams/`) and task list (`~/.claude/tasks/`)
- **Repo PVC** holds the git clone with per-teammate worktrees

### CRDs

- `AgentTeam` — primary resource: defines lead + teammates + repo + budget + quality gates
- `AgentTeamTemplate` — reusable team patterns (e.g., "3-agent security review")
- `AgentTeamRun` — instantiates a template against a specific repo/branch
- `AgentTeamSchedule` — cron-based recurring AgentTeamRuns
- `AgentTeamTrigger` — event-triggered runs via the kagents-trigger webhook listener

## Build & Test Commands

```bash
make build          # Build operator binary
make test           # Run unit tests (with -race)
make test-integration  # Run envtest integration tests (no cluster needed)
make lint           # Run linter
make manifests      # Generate CRD manifests from Go types
make generate       # Generate deepcopy methods
make docker-build   # Build operator container image
make docker-build-runner  # Build Claude Code runner image
make kind-create    # Create Kind dev cluster with NFS
make kind-load      # Load images into Kind
make install        # Install CRDs
make deploy         # Deploy operator
make sample         # Deploy sample AgentTeam
```

## Key Design Decisions

1. **File-based mailbox on shared PVC** — matches native Agent Teams protocol exactly. Each agent reads/writes JSON inbox files at `~/.claude/teams/{team}/inboxes/{agent}.json`. No protocol translation needed.

2. **ReadWriteMany PVC required** — multiple pods must read/write the same mailbox files. Requires NFS, EFS, or similar CSI driver. The Kind setup script installs an NFS provisioner.

3. **Per-teammate git worktrees** — prevents merge conflicts between concurrent agents. Each teammate works on an isolated worktree branched from the target.

4. **Budget tracking is estimation-based** — Claude Code doesn't expose real-time token counts externally. Operator estimates from session duration × model cost rates.

5. **Pods use RestartPolicy: Never** — Agent Teams can't resume sessions. If a pod crashes, the operator re-spawns with fresh context and the task list tells it what's left to do.

## File Structure

```
api/v1alpha1/           # CRD type definitions (kubebuilder markers)
  agentteam_types.go    # AgentTeam spec + status (pipeline, skills, delivery)
  template_types.go     # AgentTeamTemplate + AgentTeamRun
  schedule_types.go     # AgentTeamSchedule
  trigger_types.go      # AgentTeamTrigger
  groupversion_info.go  # Scheme registration

internal/controller/    # Reconciliation logic (all controllers implemented)
internal/budget/        # Token usage + cost estimation
internal/webhook/       # Async event notifications
internal/metrics/       # Prometheus metrics
internal/github/        # GitHub REST client (onComplete: create-pr)
internal/dashboard/     # Read-only web UI server (HTMX + SSE)
internal/delivery/      # onComplete: deliver targets (Slack, webhook)
internal/harness/       # Harness adapter seam (spec.harness, default claude-code)
internal/trigger/       # kagents-trigger webhook listener
internal/claude/        # Claude Code interaction helpers (still a stub — doc.go only;
                        # agent pods speak the coordination protocol themselves)

cmd/manager/main.go     # Operator entrypoint
cmd/dashboard/main.go   # Dashboard entrypoint
cmd/trigger/main.go     # Trigger listener entrypoint
docker/                 # Dockerfiles: operator, runner, dashboard, trigger
hack/                   # Dev scripts (Kind setup, acceptance/E2E setup)
config/samples/         # Example CRs (coding, cowork, pipeline, schedule, trigger)
charts/kagents/         # Production Helm chart (CRDs bundled in crds/)
```

## Reconciliation Phases

The `AgentTeam` reconciler is fully implemented and moves teams through:

1. **reconcilePending** — Create PVCs, run init Job (clone repo, create worktrees)
2. **reconcileInitializing** — Wait for init, deploy lead + teammate pods
3. **reconcileRunning** — Monitor health, track budget, handle crashes, check completion
4. **reconcileTerminal** — Cleanup pods, archive logs, execute onComplete (create-pr, push-branch, deliver)

## Dependencies

- Go 1.26+
- controller-runtime v0.24
- kubebuilder markers for CRD generation
- Kind + Helm for local development
- NFS provisioner for ReadWriteMany PVCs

## API Group

`kagents.dev/v1alpha1`

## Testing

- Unit tests for reconciler logic (mock client)
- Integration tests with envtest (controller-runtime test framework)
- Acceptance tests against a Kind cluster (busybox agents, no API key)
- E2E tests against Kind cluster with real Claude Code (requires API key)

## License

Apache 2.0

---

## KubeCon NA 2026

This project is being developed with the goal of presenting at KubeCon NA 2026 (November 9–12, Salt Lake City). See `KUBECON.md` for the full talk framing.

### Release Timeline

Issues are tracked in **Linear** (team AMC, project claude-teams-operator); GitHub milestones mirror the Linear ones for public visibility. The KubeCon CFP has been **submitted** (May 2026) — see KUBECON.md.

| Version | GitHub Milestone | Due | What it unlocks |
|---------|-----------------|-----|-----------------|
| **v0.1.0** ✅ | Initial Release | Apr 13 2026 | Core operator, 50+25+19 tests, CI |
| **v0.2.0** ✅ | Foundation & Real Runner | Apr 19 2026 | Real `claude-code-runner` image, E2E test, mailbox PVC validation, talk-ready `describe` output |
| **v0.3.0** ✅ | Observability & Budget | Apr 24 2026 | Prometheus metrics, `internal/budget` package, webhook engine, Grafana dashboard (plus early Resilience/RBAC previews) |
| **v0.4.0** ✅ | Resilience & RBAC | Aug 31 2026 | Crash re-spawn ✅, per-agent ServiceAccounts ✅, `onComplete: create-pr` ✅, `onComplete: push-branch` ✅ |
| **v0.5.0** ✅ | Template Engine & Helm | Sep 30 2026 | `AgentTeamTemplate`/`AgentTeamRun` controllers ✅, production Helm chart ✅, CONTRIBUTING.md ✅ |
| **v0.6.0** ✅ | Operator Dashboard | Oct 5 2026 | Web UI for running AgentTeams: backend API, list + detail views (HTMX + Go templates), live SSE updates, Helm packaging |
| **v0.7.0** ✅ | Documentation Site | Jun 30 2026 | mkdocs-material docs site (Diátaxis nav, tutorials, how-tos, concepts, auto-generated API reference), kagents brand, community baseline (COC, CONTRIBUTING, SECURITY), OSSF Scorecard supply-chain hardening, controller-runtime 0.24 + k8s 0.36 + Go 1.26 toolchain |
| **v0.8.0** ✅ | kagents rebrand + Knowledge Work Orchestrator | Jul 2026 | Clean-break rebrand (module `github.com/amcheste/kagents`, API group `kagents.dev`, chart `charts/kagents`, MIGRATION.md), harness adapter seam (`spec.harness`), pipeline stages with fan-out/merge/approval gates, output routing, `AgentTeamSchedule` + `AgentTeamTrigger` CRDs, `onComplete: deliver` (Slack/webhook), OCI skill distribution, pipeline-aware observability |
| — | QA hardening — A+ practices | rolling | AMC-122–128: required substantive CI checks, `-race`, govulncheck, Codecov, flake elimination, golangci-lint re-enable, second E2E scenario |
| **v1.0.0** | KubeCon Demo Polish | Oct 26 2026 | Demo script, dashboard presentation mode for stage, real-API test promotion to CI |

**KubeCon talk:** November 9–12 2026, Salt Lake City. CFP submitted May 2026.

### Current Priority (post-v0.8.0)

The next highest-value issues (all in Linear):
1. **AMC-122–128** — the QA hardening milestone: branch protection requiring substantive checks (A), `-race` on tests (B), govulncheck (C), Codecov (D), `time.Sleep` flake elimination + `t.Parallel()` (E), golangci-lint re-enable (F), second real-API E2E scenario (G)
2. **AMC-43 / AMC-44** — KubeCon demo-polish items (v1.0.0): the 2-minute on-stage demo script and promoting the real-API acceptance test to CI on merges to main

### Ask of Claude Code

As you build, help capture the story. When you hit something non-obvious — a surprising constraint, a design tradeoff, an elegant solution, or something that broke in an unexpected way — add a short entry to the **"Interesting Problems Encountered"** section in `KUBECON.md`. One paragraph is enough. These notes become the raw material for the talk narrative.

Specifically worth logging:
- Anything awkward about modeling long-running agent state in a K8s reconciler
- Constraints imposed by the RWX PVC requirement
- Tradeoffs in the budget estimation approach
- Anything surprising about crash recovery / re-spawn behavior
- Moments where K8s primitives (RBAC, ServiceAccounts, worktrees) solved a problem elegantly
