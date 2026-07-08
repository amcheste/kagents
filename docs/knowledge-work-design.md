# Technical Design — kagents Knowledge Work Orchestrator

> **Status:** Draft for review
> **Audience:** implementers of the rebrand and the knowledge-work features
> **Companion docs:** [Product Vision](product-vision.md) · [PRD](knowledge-work-prd.md)

## Scope and reading order

This document specifies two bodies of work, in the order they execute:

- **Part I — Rebrand & harness abstraction.** Neutralize the Claude-specific identity (module path, API group, brand) and introduce a thin harness-adapter seam. Lands *first* so everything below is born under the `kagents.dev` API group. Maps to the **kagents rebrand & harness abstraction** milestone.
- **Part II — Knowledge-work CRD architecture.** Output routing, pipelines, scheduling, event triggers, result delivery, OCI skills, and pipeline-aware observability. Maps to the **Knowledge Work Orchestrator** milestone.

Read [ARCHITECTURE.md](../ARCHITECTURE.md) first for the current operator design (phases, PVC layout, coordination protocol). This document describes the *deltas* from that baseline.

A guiding constraint throughout: **the operator already does not speak the agent coordination protocol** — it only provisions the shared PVC and lets the agent harness manage mailboxes/tasks. That existing separation is what makes both the harness abstraction and the knowledge-work features tractable without a rewrite.

---

# Part I — Rebrand & harness abstraction

## I.1 The harness adapter seam

### Goal

Make the agent runtime an explicit, swappable seam so that "kagents runs Claude Code" becomes "kagents runs Claude Code *today*, via one adapter." Do this **without** building a plugin SPI, registry, or second adapter — those are deferred until a real second harness exists (the YAGNI guardrail from the [vision](product-vision.md#long-term-direction-agnostic-to-the-agent-harness)).

### Where the boundary goes

Today the Claude coupling is concentrated in a small surface. The seam formalizes it:

| Concern | Today | After |
|---|---|---|
| Runner image | hardcoded `claude-code-runner` | provided by the selected harness adapter |
| Pod env / launch | `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`, `~/.claude/teams` + `~/.claude/tasks` symlinks in `entrypoint.sh` | the claude-code adapter contributes these; the operator just applies what the adapter returns |
| Valid models | `opus\|sonnet\|haiku` enum in the CRD | adapter declares its model set; CRD field becomes a free-ish string validated by the adapter |
| Budget rates | hardcoded rate table in `internal/budget` | adapter supplies per-model rates |
| Everything else (PVCs, Jobs, RBAC, scheduling, lifecycle, routing, delivery) | operator core | **unchanged — already harness-neutral** |

### The interface (internal, thin)

A single internal Go interface, consulted by the pod builder. Illustrative shape:

```go
// internal/harness/harness.go
type Harness interface {
    // Name is the spec.harness enum value, e.g. "claude-code".
    Name() string
    // RunnerImage returns the default image for this harness (overridable).
    RunnerImage() string
    // Decorate mutates a teammate/lead PodSpec to add the harness-specific
    // env, volume mounts, and command needed for the coordination protocol.
    Decorate(pod *corev1.PodSpec, role AgentRole, team *AgentTeam) error
    // Models returns the valid model identifiers and their budget rates.
    Models() map[string]ModelRate
}
```

`spec.harness` (default `claude-code`) selects the adapter from a small static map in `cmd/manager`. **No dynamic registry, no out-of-tree plugins** in this phase. Adding a second harness later means: implement the interface, add one map entry — the seam is in the right place to make that contained.

### CRD change

```yaml
spec:
  harness: claude-code   # enum, default "claude-code"; omitted == claude-code
```

Backward compatible: existing CRs omit it and get identical behavior. This is the *only* API addition in Part I — everything else in Part I is renames and internal refactoring.

### Validation note

Use a CEL validation rule (`x-kubernetes-validations`) on the model field to defer model-set validation to runtime where the adapter is known, rather than a static enum. (See [I.4](#i4-no-admission-webhooks-use-cel) on why we avoid admission webhooks.)

## I.2 Module path migration

`github.com/amcheste/claude-teams-operator` → `github.com/amcheste/kagents`.

- Mechanical: `go.mod` module directive + every internal import across `api/`, `cmd/`, `internal/`, `test/`; Dockerfile build targets; Makefile; CI workflow refs.
- Regenerate: `make generate manifests` (regenerates deepcopy headers and CRD paths).
- Atomic PR — the module path cannot be half-migrated. Land/close open PRs first.
- **Depends on the repo rename** so the module path matches the canonical repo location.

## I.3 API group migration — clean break

`claude.amcheste.io/v1alpha1` → `kagents.dev/v1alpha1`. Version stays `v1alpha1`; only the group changes. Using the owned domain as the group is the K8s convention (`cert-manager.io`, `argoproj.io`).

**Clean break** (decided): no conversion webhook. Acceptable pre-1.0 with effectively no external installs to protect.

Surface to change:
- `+groupName=kagents.dev` marker + `api/v1alpha1/groupversion_info.go`
- Regenerated CRDs (`config/crd/bases/`, `charts/*/crds/`), deepcopy, RBAC role
- CRD resource names change implicitly: `agentteams.claude.amcheste.io` → `agentteams.kagents.dev`
- **Annotation keys** derived from the group: `approved.claude.amcheste.io/{event}` → `approved.kagents.dev/{event}` (audit `internal/controller` + ARCHITECTURE.md §Approval Gates)
- All `apiVersion:` in `config/samples/`, docs, tutorials, README
- `docs/reference/api/` regenerated

### `MIGRATION.md` (new, user-facing)

Because it's a clean break, any existing install migrates by re-creating resources:

```bash
# 1. Export any in-flight CRs you want to keep (optional; re-apply with new apiVersion)
kubectl get agentteams,agentteamtemplates,agentteamruns -A -o yaml > teams-backup.yaml

# 2. Remove the old CRDs (cascades to CRs) and operator
helm uninstall kagents
kubectl delete crd agentteams.claude.amcheste.io \
  agentteamtemplates.claude.amcheste.io agentteamruns.claude.amcheste.io

# 3. Install the kagents.dev build
helm install kagents ./charts/kagents

# 4. Re-apply CRs after find/replacing apiVersion: claude.amcheste.io/v1alpha1
#    → apiVersion: kagents.dev/v1alpha1
```

MIGRATION.md states plainly: this is a one-time breaking change made deliberately while the project is pre-1.0; no automated data migration is provided.

## I.4 No admission webhooks — use CEL

Several knowledge-work features need cross-field validation (e.g. "`pipeline` and flat `dependsOn` are mutually exclusive"). The project currently ships **no admission webhooks** by design (no webhook server, no cert wiring). We keep it that way and use **CRD CEL validation rules** (`x-kubernetes-validations`, GA since k8s 1.25; we're on 0.36):

```go
// +kubebuilder:validation:XValidation:rule="!(has(self.pipeline) && self.teammates.exists(t, has(t.dependsOn)))",message="spec.pipeline and spec.teammates[].dependsOn are mutually exclusive"
```

This keeps the no-webhook architecture, avoids cert-manager/Service plumbing, and pushes validation into the API server. Any AMC issue text that says "validation webhook" should be read as "CEL validation rule."

## I.5 Backward compatibility summary

| Change | Breaks existing CRs? | Mitigation |
|---|---|---|
| Module path | No (internal only) | — |
| API group (clean break) | **Yes** — must re-apply under new group | MIGRATION.md; pre-1.0, ~no external users |
| `spec.harness` added | No — defaults to `claude-code` | Omitted == current behavior |
| Image/chart renames | Deploy-time only | New tags + Helm value defaults |

---

# Part II — Knowledge-work CRD architecture

All examples below use the post-rebrand `kagents.dev/v1alpha1` group. These features are designed *together* so they compose; [II.8](#ii8-how-the-pieces-compose) is the load-bearing section.

## II.1 Output routing

Structured artifact handoff: a teammate's declared outputs become a downstream teammate's mounted inputs.

```yaml
teammates:
  - name: researcher
    prompt: "Analyze data; write findings to /workspace/output/findings.md"
    outputs:
      - path: /workspace/output/findings.md
        description: Research findings summary
  - name: report-writer
    prompt: "Read /workspace/stage/research/findings.md; produce the Q3 report"
    inputs:
      - from: researcher
        artifact: findings.md
        mountPath: /workspace/stage/research/
```

**Types:** `OutputSpec{path, description}`, `InputSpec{from, artifact, mountPath}` on `TeammateSpec`; `ArtifactStatus{name, producedBy, size, producedAt}` on status.

**Operator behavior** on teammate pod `Succeeded`:
1. Verify declared `outputs[].path` exist on the output PVC; missing files → teammate error status.
2. Record `status.artifacts`.
3. For each downstream teammate whose `inputs[].from` matches, make artifacts available at `mountPath` (copy within the shared output PVC; see design note).
4. Spawn downstream teammate when its inputs are satisfied **and** its ordering constraint is met.

**Design note — how artifacts move:** all agents already share the output PVC (RWX). "Routing" is therefore a *copy/symlink within one volume*, not a cross-volume transfer — cheap, no new volume plumbing. The operator runs the copy itself (it has no need to speak the agent protocol to move files). For coding mode, the analogous handoff is the existing git-worktree/branch mechanism; output routing is a Cowork-mode concern.

## II.2 Pipeline stages

`spec.pipeline` models multi-stage workflows with explicit fan-out/merge, as an alternative to flat per-teammate `dependsOn`.

```yaml
spec:
  pipeline:
    stages:
      - name: research
        teammates: [data-analyst]
      - name: analysis
        teammates: [market-analyst, financial-analyst, competitive-analyst]
        dependsOn: [research]
        fan: parallel          # all start once dependsOn stages complete (default)
      - name: synthesis
        teammates: [report-writer]
        dependsOn: [analysis]
        fan: merge             # starts only after ALL teammates in dependsOn stages Succeed
      - name: distribution
        teammates: [email-drafter]
        dependsOn: [synthesis]
        approvalRequired: true # stage-level approval gate (reuses annotation mechanism)
```

**Types:** `PipelineSpec{stages []StageSpec}`, `StageSpec{name, teammates, dependsOn, fan, approvalRequired}` on spec; `PipelineStatus{currentStage, stagesCompleted, stagesTotal, stages []StageStatus}` on status.

**Relationship to existing `dependsOn`:** pipeline is *stage-level* ordering; the existing per-teammate `dependsOn` is *teammate-level*. They're mutually exclusive (CEL rule, [I.4](#i4-no-admission-webhooks-use-cel)) to avoid two competing ordering systems in one CR. The reconciler's existing dependency-aware spawn logic (ARCHITECTURE.md §DependsOn) generalizes to stages — a stage is "ready" when its `dependsOn` stages reach the required completion (`merge` = all teammates Succeeded).

**`approvalRequired`** reuses the existing approval-gate annotation mechanism (`approved.kagents.dev/stage-{name}`), not a new system.

## II.3 AgentTeamSchedule

A new CRD that instantiates teams on a cron schedule — the operator's "CronJob for knowledge work."

```yaml
apiVersion: kagents.dev/v1alpha1
kind: AgentTeamSchedule
metadata: {name: weekly-standup-summary, namespace: cowork-agents}
spec:
  schedule: "0 6 * * MON"
  templateRef: {name: standup-summarizer}
  auth: {apiKeySecret: anthropic-api-key}
  workspace: {inputs: [{configMap: team-channels}]}
  lifecycle: {timeout: "1h", budgetLimit: "10.00"}
  historyLimit: 5
```

**Controller:** new `AgentTeamScheduleReconciler`. Mirrors the upstream `CronJob` controller pattern — parse with `robfig/cron`, on each reconcile determine if a run is due for the current window, create an `AgentTeamRun` from `templateRef` + overrides, GC completed runs beyond `historyLimit`. **Idempotency** via a deterministic run name per window (`{schedule}-{unix-window}`) so a requeue can't double-fire.

**Status:** `lastScheduledAt`, `nextScheduledAt`, `activeRun`, `runs[]`.

## II.4 AgentTeamTrigger

A new CRD that instantiates teams in response to external events.

```yaml
apiVersion: kagents.dev/v1alpha1
kind: AgentTeamTrigger
metadata: {name: new-deal-onboarding, namespace: cowork-agents}
spec:
  trigger:
    webhook: {path: /hooks/new-deal, secret: webhook-hmac-secret}
    # watchResource: {apiVersion: v1, kind: ConfigMap, namespace: sales, labelSelector: "type=new-deal"}
  templateRef: {name: deal-onboarding-team}
  auth: {apiKeySecret: anthropic-api-key}
  payloadInjection: {mountPath: /workspace/data/trigger-payload.json}
  concurrencyPolicy: Allow   # Allow | Forbid | Replace
```

**Controller + server:** a lightweight HTTP server for webhook triggers, validated by HMAC when `secret` is set; on a valid event it creates an `AgentTeamRun` and stores the payload as a ConfigMap mounted at `payloadInjection.mountPath`. `concurrencyPolicy` governs overlap.

**Design decision — server topology:** run the webhook listener as its **own Deployment/Service** (`kagents-trigger`), *not* folded into the operator's manager process. Rationale: it's an ingress-exposed, internet-reachable surface with a very different security/scaling profile from the reconcile loop; coupling it to the manager would put a public endpoint in the leader-elected controller pod. This mirrors how the dashboard already ships as a separate, optional sub-deployment. Gated behind a Helm value (`trigger.enabled`, default false).

## II.5 Result delivery

A new `onComplete: deliver` mode plus a `delivery[]` list of targets.

```yaml
lifecycle:
  onComplete: deliver
  delivery:
    - {type: slack,        channel: "#reports", artifactPath: /workspace/output/q3-report.pdf, message: "Q3 report ready."}
    - {type: email,        to: [team@acme.com], subject: "Q3 Report", attachmentPath: /workspace/output/q3-report.pdf, credentialsSecret: smtp-credentials}
    - {type: google-drive, folder: "Shared Reports/Q3", artifactPath: /workspace/output/, credentialsSecret: gdrive-service-account}
    - {type: webhook,      url: "https://hooks.example.com/reports", artifactPath: /workspace/output/q3-report.pdf}
```

**Pattern:** delivery runs as a **short-lived Job after teammates complete and quality gates pass** — structurally identical to the existing `push-branch`/`create-pr` finalization Job ([reuse `runFinalization`](../internal/controller/agentteam_controller.go)). Each target type is a package under `internal/delivery/` (`slack`, `email`, `gdrive`, `webhook`). Implement **webhook first** (simplest), then slack, email, gdrive.

**Failure semantics:** delivery failure is recorded in `status.delivery[]` but does **not** roll the team back to `Failed` — the knowledge work is done; delivery is best-effort post-processing. This matches the issue and is the right call (don't lose a completed $5 report because Slack 500'd).

**Security:** the delivery Job (not the operator) consumes the `credentialsSecret`s, mounted only into that Job's pod — the operator never reads SMTP/Drive creds, consistent with the existing MCP-credential handling in ARCHITECTURE.md §MCP Servers.

## II.6 OCI skill distribution

Extend skill sources from ConfigMap-only to OCI artifacts.

```yaml
teammates:
  - name: financial-analyst
    skills:
      - {name: financial-analysis, source: {oci: "ghcr.io/<org>/kagents-skills/financial-analysis:v2"}}
      - {name: report-writing,     source: {configMap: report-writing-skill}}  # still supported
```

**Mechanism:** when a skill has `source.oci`, the operator adds an **init container** (using `oras` or `crane`) that pulls the artifact into a shared `emptyDir`, which the main container mounts at `~/.claude/skills/{name}/`. ConfigMap skills keep working (backward compatible). Private registries via `spec.imagePullSecrets`. Cache by digest to avoid re-pulling.

**Skill artifact convention:** `SKILL.md` (required) + optional `examples/`, `templates/`, pushed with `oras` under a `application/vnd.kagents.skill.v1+*` media type. Ships with `docs/skills-authoring.md`.

**Harness interaction:** skills are a Claude-Code concept today (`~/.claude/skills/`). The *mount path* is harness-specific, so the skill-mount step is contributed by the harness adapter ([I.1](#i1-the-harness-adapter-seam-amc-155)); the OCI *pull* is harness-neutral.

## II.7 Pipeline-aware observability

The status fields introduced by output routing (`status.artifacts`) and pipelines (`status.pipeline`) are the data; this is the surfacing layer.

**Prometheus metrics:** `kagents_team_stage_duration_seconds` (histogram), `kagents_team_artifacts_produced_total` (counter), `kagents_team_pipeline_stage_active` (gauge), `kagents_team_delivery_success_total` / `_failure_total` (counters). *(Note the metric prefix rebrands `claude_` → `kagents_`; fold this into the API-group migration sweep so dashboards/alerts change once.)*

**Dashboard:** stage progress bar in the team detail view; artifact list with download links. Extends the existing SSE-driven dashboard.

**`kubectl describe`:** pipeline stage summary in the printer columns / additional-printer output.

## II.8 How the pieces compose

The features are deliberately orthogonal layers, not overlapping mechanisms:

```
AgentTeamSchedule ─┐
AgentTeamTrigger  ─┼─► AgentTeamRun ─► AgentTeam ──► pipeline (stage ordering)
(manual apply)    ─┘                                   └► teammates ──► output routing (data flow)
                                                                          └► onComplete: deliver (targets)
                                       observability (status + metrics) wraps all of it
```

Key composition decisions:

1. **One instantiation path.** `AgentTeamSchedule` and `AgentTeamTrigger` both create an `AgentTeamRun`, which already knows how to resolve a template into an `AgentTeam`. They do **not** each reimplement team creation — they're thin producers in front of the existing `AgentTeamRun` controller. This is the single most important reuse decision; it keeps template-resolution logic in one place.
2. **Ordering vs data flow are separate axes.** `pipeline` (or `dependsOn`) decides *when* a teammate runs; output routing decides *what data* it starts with. A stage can fan out to 3 analysts (ordering) each of whom consumes the researcher's `findings.md` (data flow). They compose; neither subsumes the other.
3. **Delivery is a finalization mode,** reusing the `runFinalization` Job pattern alongside `create-pr`/`push-branch` — not a new lifecycle phase.
4. **`spec.harness` sits underneath all of it.** Pipelines, schedules, triggers, routing, and delivery are harness-neutral; only model selection and the runner pod touch the adapter. None of the Part II CRDs may hardcode `claude`/model assumptions.

## II.9 Phasing

Per the "rebrand first" decision, and a suggested renumber (rebrand = v0.8.0, knowledge work = v0.9.0):

**Milestone 1 — rebrand:** repo rename → module path → API group (clean break) → harness seam → image/chart names. Strictly first.

**Milestone 2 — knowledge work, suggested order:**
1. **Output routing** + **pipeline** — the core primitives; everything else references them.
2. **AgentTeamSchedule** + **AgentTeamTrigger** — instantiation, both on the shared `AgentTeamRun` path.
3. **Delivery** + **OCI skills** — integrations; delivery starts with the webhook target.
4. **Observability** — cross-cutting, lands last so it can surface all the above.
5. **README/positioning reframe** — lands with or just after the features it describes.

## II.10 Testing strategy

| Layer | What | How |
|---|---|---|
| Harness seam regression | default `claude-code` adapter produces a pod spec byte-identical to today's | golden-ish unit test comparing built PodSpec before/after the refactor |
| Output routing | artifact verification, routing copy, downstream spawn, missing-file error | unit (fake client) + envtest (real spawn ordering) |
| Pipeline | stage transitions, `parallel`/`merge` semantics, CEL mutual-exclusivity with `dependsOn` | unit for transition logic; envtest for a 4-stage sample; apply-time test for the CEL rule |
| Schedule | cron parsing, due-window detection, idempotent run naming, `historyLimit` GC | unit + envtest (fake clock) |
| Trigger | HMAC validation, payload ConfigMap injection, `concurrencyPolicy` | unit (httptest) for the server; envtest for run creation |
| Delivery | each target type; failure-doesn't-fail-team | unit per `internal/delivery/*` with mock endpoints |
| OCI skills | pull → mount, ConfigMap fallback, digest cache | unit for resolution; e2e (real `oras` pull from ghcr) |
| Migration | no `kagents.dev` / old module path remains; CRDs install under new group | grep gate in CI + envtest install smoke |

New CRDs (`AgentTeamSchedule`, `AgentTeamTrigger`) each get their own envtest integration suite mirroring the existing `agentteamrun_integration_test.go` pattern.

## II.11 Open questions for review

1. **Renumber?** Confirm rebrand = v0.8.0 and Knowledge Work → v0.9.0, or keep numbers and rely on sequencing.
2. **Trigger server topology** — separate `kagents-trigger` Deployment (proposed) vs. folding into the manager. I argue separate (public ingress surface). Agree?
3. **OCI registry org** — `ghcr.io/<org>/kagents-skills/*`; which org/namespace is canonical for first-party skills?
4. **Delivery scope for the first cut** — issue lists Slack/email/Drive/webhook. Proposed order ships webhook + Slack first; email/Drive can trail. OK to land delivery incrementally rather than all four at once?
5. **Schedule/Trigger → Run vs Team** — both produce `AgentTeamRun` (template-based) in this design. If a schedule/trigger should ever run a template-less inline team, that's a second path; proposed to *not* support that initially (always go through a template).
