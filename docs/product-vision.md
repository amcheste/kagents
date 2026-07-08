# Product Vision — kagents

> **Status:** Draft for review
> **Audience:** maintainers, contributors, and anyone evaluating the project's direction
> **Companion docs:** [Product Requirements](knowledge-work-prd.md) · [Technical Design](knowledge-work-design.md)

## The one-liner

**Gas Town runs your coding agents. kagents runs your business.**

kagents is a Kubernetes operator for orchestrating teams of AI agents that do **knowledge work** — research, analysis, reporting, document production, operational runbooks — declaratively, on a schedule or an event, with the budget controls, RBAC, observability, and delivery integrations you'd expect from any production workload on Kubernetes.

Coding is one mode. Knowledge work is the headline.

## The problem

Most valuable knowledge work inside a company is still done the same way it was twenty years ago: a person opens a blank document, gathers inputs by hand, thinks, writes, and sends the result to someone else. AI agents can now do large parts of this — but the way we *run* them hasn't caught up with the way we run everything else.

Today, agent-driven knowledge work is:

- **Manual.** A human is in the driver's seat for every step — kicking off the agent, feeding it context, copying the output somewhere useful.
- **Unscalable.** Throughput is bounded by human attention. You can't run the same analysis across 50 inputs without 50 sessions babysat by a person.
- **Unrepeatable.** "Run the competitive analysis again next month" means starting from scratch, with no guarantee the method — or the cost — is the same.
- **Ephemeral and ungoverned.** Agents run on laptops. There's no audit trail, no cost accounting, no access boundaries, no scheduling, no automatic delivery of the result to where humans actually consume it.

The agents are ready. The *operational substrate* for running them as durable, repeatable, governed business processes is missing.

## The vision

**Declarative AI team orchestration on Kubernetes.**

You describe — as a Kubernetes resource — a team of agents and the work you want done. The platform runs it: on demand, on a cron schedule, or in response to an event. It enforces budget caps, scopes each agent's access with RBAC, emits metrics and traces, gates risky steps on human approval, and delivers the finished artifact to Slack, email, or Drive without anyone copy-pasting.

The same control-plane primitives that made Kubernetes the standard for running cloud software — declarative desired state, reconciliation loops, RBAC, scheduling, observability, garbage collection — become the control plane for AI knowledge work. Not bolted on. Native.

A weekly market-analysis report becomes a `AgentTeamSchedule`. An incident postmortem draft becomes an `AgentTeamTrigger` fired by a PagerDuty webhook. A multi-stage research-to-publication workflow becomes a `pipeline` with explicit fan-out and merge. The output shows up in the right Slack channel every Monday at 8am, costs are capped and tracked, and a human signs off before anything leaves the building.

## Why Kubernetes, why us, why now

Multi-agent frameworks mostly treat infrastructure as an afterthought — they're Python libraries you wrap in your own glue and run wherever. kagents inverts that: the orchestration *is* the cluster. That buys things you otherwise have to rebuild badly:

- **Declarative desired state + reconciliation** — the work is a resource; the operator drives reality toward it and recovers from failure.
- **RBAC and ServiceAccounts** — each agent pod gets exactly the access it needs and nothing more, enforced by the cluster, not by hope.
- **Scheduling** — `CronJob`-grade recurring runs and event triggers are first-class, not a wrapper script.
- **Observability** — Prometheus metrics, structured events, and per-run cost tracking ride the same rails as the rest of your platform.
- **Multi-tenancy and isolation** — namespaces, quotas, and network policy already exist; we use them.

And it builds on a protocol-fidelity bet that's already paying off: kagents wraps Anthropic's *native* Claude Code / Cowork agent-team protocol (file-based mailboxes + shared task list on a shared volume) rather than reinventing coordination. When the upstream protocol improves, we inherit it.

## Target users

| Persona | What they want | What kagents gives them |
|---------|----------------|--------------------------|
| **Platform / DevOps engineer** | A safe, multi-tenant way to let teams run AI workloads on shared infra | An operator with RBAC, budget caps, quotas, and observability already wired into their existing cluster |
| **Ops / SRE team** | Automation that reacts to events and runs on schedules | `AgentTeamTrigger` (webhook-driven) and `AgentTeamSchedule` (cron) with delivery to their existing channels |
| **Knowledge worker / analyst** | "Give me the report" without operating anything | A declarative team spec (often from a reusable `AgentTeamTemplate`) that produces and delivers an artifact |

The platform engineer installs and governs it. The knowledge worker consumes it. That split is the point — knowledge work becomes a *platform capability*, not a per-person craft.

## Positioning and competitive landscape

> The space is moving fast; treat the specifics below as a snapshot to verify, not gospel. The *category distinctions* are the durable part.

- **Gas Town / gastown-operator** — multi-agent **coding** orchestration, with a Kubernetes operator that treats individual agents as pods. Invents its own coordination protocol (Convoys, Beads, Molecules). Sweet spot: many independent coding tasks, batch-dispatched. kagents differs by (a) generalizing beyond coding to all knowledge work and (b) wrapping the *native* agent protocol rather than a bespoke one.
- **Anthropic Cowork** — the native knowledge-work experience for Claude agent teams (documents, reports, research), run locally. This is precisely the experience kagents lifts onto Kubernetes and makes schedulable, governed, and multi-tenant. We are complementary, not competitive: Cowork is the protocol; we're the production substrate.
- **CrewAI / LangGraph / AutoGen** — Python frameworks for multi-agent orchestration. Powerful, but library-shaped: you write code, manage your own runtime, and bring your own scheduling, RBAC, cost control, and delivery. kagents is declarative and cloud-native — the unit of work is a YAML resource, not a Python program.
- **Multiclaude and similar** — multi-agent Claude orchestrators in the local/CLI tradition. Same lineage as the native protocol; kagents is the Kubernetes-native operator form. *(Verify current capabilities before citing specifics publicly.)*

The one-line wedge: **everyone else either runs coding agents, or runs agents as a library you operate yourself. kagents runs knowledge-work agent teams the way Kubernetes runs everything else — declaratively, on a schedule, governed, and delivered.**

## Long-term direction: agnostic to the agent harness

Today kagents runs Claude Code / Cowork agent teams, and the implementation wraps that native protocol faithfully. That's the right bet *now* — it's the most capable team-based knowledge-agent harness available, and protocol fidelity means we inherit its improvements for free.

But the durable value of kagents is **not** "a way to run Claude on Kubernetes." It's the **declarative control plane for knowledge-work agent teams** — the resources, scheduling, triggers, RBAC, budget control, observability, and delivery — which are defined independently of any one agent runtime. The Claude Code protocol lives behind an *adapter boundary*. Long term, a different team-based agent harness should be able to plug in behind the same `AgentTeam` / pipeline / schedule / trigger / delivery API, and a platform team's investment in templates, pipelines, and paved roads carries over unchanged.

Concretely, the design should keep the harness-specific surface (how an agent pod is launched, how the mailbox/task protocol is mounted, what image runs) isolated from the orchestration surface (what a team *is*, when it runs, who can run it, where its output goes). We are not building that pluggability in v0.8.0 — but we should not make decisions that foreclose it. When in doubt, push harness-specific assumptions down into the runner/adapter layer rather than up into the CRD API.

> Positioning consequence: kagents is ultimately **agnostic to the type of knowledge-agent-team harness**. Claude Code is the first (and, today, only) supported harness — not a permanent dependency.

## Product principles

1. **Cloud-native first.** If Kubernetes already solves it (scheduling, RBAC, GC, observability), we use the primitive rather than reinvent it.
2. **Protocol fidelity today, harness-agnostic tomorrow.** We currently wrap the native Claude Code / Cowork agent-team protocol exactly — no translation layer to drift out of sync. But that protocol is an *adapter*, not the foundation: the operator's resources (teams, pipelines, schedules, triggers, delivery) are defined independently of any single agent runtime, so a different team-based knowledge-agent harness can plug in behind the same Kubernetes API. See [Long-term direction](#long-term-direction-agnostic-to-the-agent-harness).
3. **Human-in-the-loop by design.** Approval gates, budget caps, and delivery review are first-class — autonomy is opt-in, not assumed.
4. **Observable by default.** Every run has metrics, events, cost accounting, and a pipeline-aware status. If you can't see it, we didn't ship it.
5. **Composable.** Templates, pipelines, schedules, triggers, and delivery targets are independent primitives that combine. Small surfaces, declared together.
6. **Backward compatible.** Existing `AgentTeam` CRs keep working unchanged. Knowledge-work features are additive.

## Flagship use cases

1. **Recurring reports.** "Every Monday 8am, a 3-agent team pulls last week's metrics, writes a narrative summary, and posts it to `#leadership`." → `AgentTeamSchedule` + `pipeline` + `onComplete: deliver` (Slack).
2. **Event-triggered workflows.** "When an incident closes, draft a postmortem from the timeline and open it for review." → `AgentTeamTrigger` (webhook) + delivery to a doc + approval gate.
3. **Multi-stage analysis.** "Research → three parallel analyst takes (market, financial, competitive) → synthesis → exec summary." → `pipeline` with fan-out and merge, structured artifact handoff between stages.
4. **Document pipelines.** "Draft → review → revise → publish," with each stage's output becoming the next stage's input automatically. → `pipeline` + output routing + delivery.

Each of these is a declarative resource a platform team can offer as a paved road, and a knowledge worker can invoke from a template.

## What success looks like

We'll know the pivot is working when these move:

- **Teams running** — count of `AgentTeam` / scheduled / triggered runs over time (adoption).
- **Artifacts delivered** — completed runs that produced and delivered a result a human used (outcome, not just activity).
- **Cost per artifact** — estimated spend per delivered artifact, trending down as templates and pipelines mature (efficiency).
- **Time-to-artifact** — wall-clock from trigger to delivered result (responsiveness).
- **Human-approval rate** — fraction of gated steps approved without edits (trust in autonomy, watched over time).

Vanity metric to avoid: raw token spend or pod-hours with no artifact attached. Activity isn't the goal; *delivered, trusted knowledge work* is.

## Scope and non-goals

- **Not a general workflow engine.** Argo Workflows and Temporal orchestrate arbitrary containerized DAGs. kagents orchestrates *AI agent teams* specifically, with the agent protocol and human-in-the-loop semantics baked in. If you want a generic DAG runner, use one.
- **Not a model or an agent.** We don't ship the intelligence; we run Claude Code / Cowork agent teams. Our value is the operational substrate.
- **Not a replacement for human judgment.** Approval gates and delivery review are load-bearing. The product makes humans faster and processes repeatable; it doesn't remove the human from consequential decisions.

## Relationship to the existing roadmap

kagents didn't start here — it's earned the right to this pivot. v0.1–v0.7 delivered the operator core, crash resilience and per-agent RBAC, the template/run controllers, the dashboard, and a full documentation site. Knowledge-work mode (Cowork) already exists as a runtime path. **v0.8.0 is where knowledge work stops being a secondary mode and becomes the headline** — adding pipelines, scheduling, event triggers, structured artifact handoff, result delivery, and OCI-distributed skills, plus a positioning reframe that leads with Cowork.

See the [PRD](knowledge-work-prd.md) for the feature-by-feature requirements and the [Technical Design](knowledge-work-design.md) for how it's built.
