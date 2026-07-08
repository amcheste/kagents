# Product Requirements — kagents Knowledge Work Orchestrator

> **Status:** Draft for review
> **Audience:** maintainer + contributors scoping and accepting the v0.8.x/v0.9.x work
> **Companion docs:** [Product Vision](product-vision.md) (why) · this doc (what) · [Technical Design](knowledge-work-design.md) (how)

## How to read this

The [vision](product-vision.md) sets direction; this PRD turns it into concrete, acceptance-testable requirements; the [design](knowledge-work-design.md) says how each is built. Requirements here are intentionally **outcome-level** — detailed mechanism and field-by-field specs live in the design doc.

Requirements are grouped as **FR-0** (identity & harness — the rebrand, ships first) and **FR-1…FR-8** (the knowledge-work capabilities). Each maps to a milestone.

## Personas

- **Platform / DevOps engineer (operator).** Installs and governs kagents on a shared cluster. Cares about RBAC, multi-tenancy, cost ceilings, observability, and a clean upgrade story.
- **Ops / SRE team (automator).** Wires kagents into event streams and schedules. Cares about triggers, cron, concurrency control, and delivery to existing channels.
- **Knowledge worker / analyst (consumer).** Wants a finished artifact — a report, a draft, an analysis — without operating anything. Invokes a team from a template; receives a result.

## Requirements overview

| Req | Capability | Milestone | Priority |
|-----|-----------|-----------|----------|
| FR-0a | Repo / module / brand rename | Rebrand | High |
| FR-0b | API group → `kagents.dev` (clean break) | Rebrand | High |
| FR-0c | Harness adapter seam (`spec.harness`) | Rebrand | High |
| FR-1 | Pipeline stages (fan-out/merge) | Knowledge Work | High |
| FR-2 | Output routing between teammates | Knowledge Work | High |
| FR-3 | Cron scheduling (`AgentTeamSchedule`) | Knowledge Work | High |
| FR-4 | Event triggers (`AgentTeamTrigger`) | Knowledge Work | Medium |
| FR-5 | Result delivery (Slack/email/Drive/webhook) | Knowledge Work | Medium |
| FR-6 | OCI skill distribution | Knowledge Work | Medium |
| FR-7 | Pipeline-aware observability | Knowledge Work | Low |
| FR-8 | Positioning / README reframe | Knowledge Work | Medium |

---

## FR-0 — Identity & harness abstraction (ships first)

> **Story (operator):** "As a platform engineer evaluating kagents, I want a project whose name and API don't tie me to one vendor, so I can adopt it as a durable platform capability rather than a Claude-specific tool."

**Requirements**
- The project is named **kagents**; repo, module path, images, and Helm chart use that name (FR-0a).
- The CRD API group is **`kagents.dev`** (FR-0b). Migration is a documented clean break (`MIGRATION.md`); no automated conversion.
- An optional **`spec.harness`** field (default `claude-code`) selects the agent runtime; harness-specific concerns (runner image, protocol env/mounts, model set, budget rates) live behind an adapter, not in the operator core or CRD API (FR-0c).

**Acceptance**
- [ ] `helm install kagents ./charts/kagents` deploys cleanly; CRDs register under `kagents.dev`.
- [ ] No `claude.amcheste.io` or old module path remains except intentional historical artifacts (CI grep gate).
- [ ] Existing `AgentTeam` CRs with `spec.harness` omitted behave identically to today (regression test).
- [ ] `MIGRATION.md` documents the cutover and is linked from README.

**Non-goal:** a second harness adapter, plugin SPI, or registry — deferred until a real second harness exists.

## FR-1 — Pipeline stages

> **Story (consumer):** "As an analyst, I want to declare research → parallel analysis → synthesis → distribution as ordered stages, so a complex workflow runs in the right order without me babysitting it."

**Requirements**
- `spec.pipeline.stages[]` with `dependsOn`, `fan: parallel|merge`, and `approvalRequired`.
- Mutually exclusive with per-teammate `dependsOn` (enforced via CEL, not an admission webhook).
- `status.pipeline` reports current stage and per-stage phase.

**Acceptance**
- [ ] A 4-stage sample runs with correct fan-out (parallel) and merge (wait-for-all) semantics.
- [ ] `approvalRequired` stage blocks until the approval annotation is set.
- [ ] A CR with both `pipeline` and flat `dependsOn` is rejected at apply time.

## FR-2 — Output routing

> **Story (consumer):** "I want one teammate's output file to automatically become the next teammate's input, so stages share work product without manual copying."

**Requirements**
- `outputs[]` / `inputs[]` on `TeammateSpec`; artifacts moved within the shared output PVC.
- `status.artifacts[]` records produced artifacts; missing declared outputs surface as teammate errors.

**Acceptance**
- [ ] Researcher→writer sample: the writer starts with the researcher's artifact mounted at its `inputs[].mountPath`.
- [ ] `status.artifacts` populated after production; a missing declared output is reported as an error.

## FR-3 — Cron scheduling (`AgentTeamSchedule`)

> **Story (automator):** "As an SRE, I want a weekly report team to run on a cron schedule with a retained run history, like a CronJob."

**Requirements**
- New CRD; `schedule` (cron), `templateRef` + overrides, `historyLimit`.
- Idempotent per window (no double-fire on requeue); `status` tracks last/next run and history; old runs GC'd beyond `historyLimit`.
- Instantiates via the existing `AgentTeamRun` path (no bespoke team creation).

**Acceptance**
- [ ] A schedule fires once per window and not twice on controller restart.
- [ ] `historyLimit` prunes completed runs; `status.nextScheduledAt` is accurate.

## FR-4 — Event triggers (`AgentTeamTrigger`)

> **Story (automator):** "As an SRE, I want an external webhook (e.g. a closed incident) to spin up a team, with the event payload available to the agents."

**Requirements**
- New CRD; webhook trigger with HMAC validation; payload injected as a ConfigMap at a configurable mount path; `concurrencyPolicy: Allow|Forbid|Replace`.
- Trigger listener runs as its **own deployment** (`kagents-trigger`, Helm-gated), not inside the manager.
- Instantiates via the existing `AgentTeamRun` path.

**Acceptance**
- [ ] A valid signed POST creates a team; an invalid HMAC is rejected.
- [ ] Payload is readable by agents at the declared mount path.
- [ ] `concurrencyPolicy` enforced (Forbid/Replace behave correctly under overlap).

## FR-5 — Result delivery

> **Story (consumer):** "When the report is done, deliver it to `#reports` / my inbox / a Drive folder — I shouldn't have to fetch it from a PVC."

**Requirements**
- `onComplete: deliver` + `delivery[]` targets: webhook, slack, email, google-drive.
- Runs as a finalization Job after teammates complete and quality gates pass; consumes credential secrets in the Job (not the operator).
- Delivery failure is recorded in `status.delivery[]` but does **not** fail the team.

**Acceptance**
- [ ] Webhook + Slack targets deliver a completed artifact; failures are surfaced in status without flipping the team to Failed.
- [ ] Email + Drive land subsequently (delivery may ship incrementally — see open decisions).

## FR-6 — OCI skill distribution

> **Story (operator):** "I want to distribute and version agent skills as OCI artifacts from a registry, not hand-maintained ConfigMaps."

**Requirements**
- `skills[].source.oci` pulled via an init container into a shared volume; ConfigMap source remains supported (backward compatible); private registries via `imagePullSecrets`; digest-based caching.
- Skill packaging convention + `docs/skills-authoring.md`.

**Acceptance**
- [ ] An OCI skill pulls and mounts at the harness skill path; a ConfigMap skill still works in the same team.
- [ ] A published sample skill is pullable from the chosen registry.

## FR-7 — Pipeline-aware observability

> **Story (operator):** "I want stage progress, artifacts, and delivery outcomes visible in metrics, the dashboard, and `kubectl describe` — not just tokens and restarts."

**Requirements**
- `status.pipeline` + `status.artifacts` surfaced; new `kagents_*` Prometheus metrics (stage duration, artifacts produced, stage active, delivery success/failure); dashboard stage bar + artifact list; `kubectl describe` stage summary.

**Acceptance**
- [ ] New metrics emit during a pipeline run; dashboard shows stage progress and artifacts.

## FR-8 — Positioning / README reframe

> **Story (evaluator):** "I want the README to lead with knowledge work (Cowork example first, coding second), so the project's purpose is immediately clear."

**Requirements**
- README leads with a Cowork example; coding example second; the "not a general workflow engine" non-goal stated; brand/positioning aligned with the [vision](product-vision.md).

**Acceptance**
- [ ] README opens with a knowledge-work example and the one-line positioning; non-goals section present.

---

## Non-functional requirements

- **Cost bounds.** Every team path (manual, scheduled, triggered) honors `lifecycle.budgetLimit`; schedules/triggers must not be able to spawn unbounded concurrent spend (`concurrencyPolicy`, `historyLimit`).
- **Reliability.** Schedules are idempotent per window; delivery is best-effort and isolated from team success; crash-respawn semantics (existing) are unchanged.
- **Security.** Trigger ingress validates HMAC and runs out-of-process from the manager; delivery/credential secrets are consumed only by the Job that needs them, never read by the operator; per-agent RBAC (existing) unchanged.
- **Backward compatibility.** Within the `kagents.dev` group, all new fields are additive and optional; an existing minimal `AgentTeam` keeps working. The one intentional break is the API-group migration (FR-0b), documented in MIGRATION.md.
- **Observability.** Every new control path emits metrics and events; no silent failures.

## Phasing

1. **Rebrand milestone (first):** FR-0a → FR-0b → FR-0c (repo rename → module path → API group → harness seam → image/chart names).
2. **Knowledge Work milestone (second), suggested order:** FR-1 + FR-2 (core) → FR-3 + FR-4 (instantiation) → FR-5 + FR-6 (integrations) → FR-7 (observability) → FR-8 (positioning).

## Dependencies & risks

- **Sequencing risk:** building knowledge-work CRDs before the API-group migration would create CRs under the old group and force a double migration. Mitigated by rebrand-first.
- **Clean-break risk:** any real deployed user must re-apply resources. Mitigated by pre-1.0 timing + MIGRATION.md; revisit if adoption grows before the migration lands.
- **Trigger ingress is new attack surface.** Mitigated by HMAC + separate, optional, Helm-gated deployment.
- **Delivery credential sprawl** (SMTP, Drive, Slack). Mitigated by Job-scoped secret consumption and incremental rollout (webhook/Slack first).
- **Harness over-abstraction.** Mitigated by the explicit YAGNI guardrail: thin internal seam, one adapter, no SPI until harness #2.

## Out of scope / non-goals

- **Not a general workflow engine.** kagents orchestrates AI agent *teams* with the agent protocol and human-in-the-loop semantics baked in. For arbitrary containerized DAGs, use Argo Workflows or Temporal.
- **Not a model or an agent.** kagents runs a harness (Claude Code today); it doesn't ship the intelligence.
- **Not a replacement for human judgment.** Approval gates and delivery review remain first-class.
- **No second harness adapter / plugin SPI yet** (deferred, not abandoned — see vision).

## Open decisions (carried from the design doc)

1. **Renumber** rebrand → v0.8.0 and Knowledge Work → v0.9.0, or keep numbers and rely on sequencing?
2. **Trigger topology** — separate `kagents-trigger` deployment (proposed) vs. in-manager.
3. **Canonical OCI registry** org/namespace for first-party skills.
4. **Delivery incrementally** (webhook+Slack first, email+Drive later) vs. all four at once.
5. **Schedule/Trigger always via template** (`AgentTeamRun`) vs. supporting inline template-less teams later.
