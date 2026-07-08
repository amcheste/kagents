# KubeCon NA 2026 — Talk Prep

**Project:** **kagents** — run Claude Code Agent Teams as a Kubernetes operator. Site: [kagents.dev](https://kagents.dev) (in progress). Repo: [`claude-teams-operator`](https://github.com/amcheste/kagents).
**Conference:** KubeCon + CloudNativeCon North America 2026
**Dates:** November 9–12, 2026 — Salt Lake City, Utah
**CFP:** Submitted (May 2026) — deadline was **May 31, 2026 at 11:59pm MT**.

---

## Talk Concept

**Working Title:** *Cloud-Native AI Teams: Building a Kubernetes Operator for Orchestrating Claude Code Agent Squads*

**The pitch:** Most AI agent orchestration frameworks treat Kubernetes as an afterthought — they bolt LLM workloads onto K8s rather than building cloud-native. This talk shows what you gain by going the other way: designing a proper K8s operator where agent teams are first-class resources with CRDs, reconciliation loops, RBAC, and native observability.

**The dogfooding hook:** The operator was built *with* Claude Code agent teams — the same system it's designed to run. That recursive story is compelling and unusual.

**Target audience:** Platform engineers, SRE teams, and developers curious about running AI agent workloads at scale.

**Format:** 35-minute talk (preferred) or 25-minute slot. Demo-heavy.

---

## Key Themes to Surface During Development

These are the angles that will make the talk land. When you hit one of these in the code, log it in the "Interesting Problems" section below.

1. **State management is the hard part** — K8s reconcilers are designed for short, idempotent operations. Agent turns are long-running and stateful. How do you model "agent is mid-conversation" in a CRD status?

2. **The ReadWriteMany constraint** — Requiring RWX PVCs (NFS, EFS) is a real deployment cost. What does this mean for clusters that don't have it? Are there workarounds?

3. **Budget tracking without native APIs** — Claude Code doesn't expose real-time token counts externally. Estimation-based tracking is a pragmatic compromise worth explaining.

4. **Per-agent RBAC** — K8s ServiceAccounts naturally scope what each agent pod can touch. This is a free security win you don't get with non-native orchestrators.

5. **Crash recovery and re-spawn** — `RestartPolicy: Never` + operator re-spawn with fresh context. What does the agent "remember" via the task list vs. what's lost?

6. **Git worktrees as concurrency primitive** — Using worktrees to prevent merge conflicts between concurrent agents is an elegant solution worth highlighting.

7. **The operator-as-coordinator pattern** — The operator isn't just lifecycle management; it's the coordination bus. Contrast this with peer-to-peer agent architectures.

---

## Competitive Landscape — Gastown

The closest comparable project is **Gas Town** and its Kubernetes operator. Understanding the distinction is important for the talk narrative and for design decisions during build.

### What Gastown Is

Gas Town (https://github.com/gastownhall/gastown) is a multi-agent orchestration system for coordinating AI coding agents. It uses a custom protocol built on git-backed hooks:
- **Mayor** — primary AI coordinator (user-facing entry point)
- **Polecats** — individual worker agents with persistent identity but ephemeral sessions
- **Convoys / Beads** — custom work-tracking units stored as git-backed issues
- **Molecules** — TOML workflow templates with checkpoint recovery
- **Witness / Deacon / Dogs** — three-tier watchdog system for health monitoring
- **Refinery** — per-rig merge queue processor with Bors-style bisecting logic

The **Gastown Operator** (https://github.com/boshu2/gastown-operator) extends this to Kubernetes — it treats Polecats (individual agents) as pods and uses K8s as a horizontal scaling platform. Only Polecat CRs spawn actual pods; everything else is CRDs with no pod footprint. It has enterprise features: OpenShift support, FIPS-compliant images, supply chain security (SBOM, Trivy, provenance).

Their core value prop: *"Queue 50 issues. Dispatch 50 polecats. Close your laptop. Come back to PRs."*

### Our Differentiation

**1. Protocol fidelity, not reimplementation.**
Gastown invents its own coordination protocol (Convoys, Beads, Molecules — all custom). This operator wraps Anthropic's *native* Claude Code Agent Teams protocol exactly as designed (`CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`, file-based JSON mailboxes at `~/.claude/teams/`, shared task list). When Anthropic ships protocol improvements, we inherit them automatically. Gastown has to manually track and reimplement every change.

**2. Team-as-a-resource vs. agent-as-a-resource.**
Gastown's fundamental CRD is a single agent (Polecat). Our fundamental CRD is a team (`AgentTeam`) — a higher-level abstraction that declares roles, budget, quality gates, and coordination topology in one resource. `AgentTeamTemplate` lets you define reusable team patterns (e.g., "3-agent security review"). This is more declarative and composable.

**3. K8s as coordination fabric, not just scaling infrastructure.**
Gastown uses K8s to scale agents horizontally — K8s is the execution platform. We use K8s primitives to *do* coordination work: ServiceAccounts scope what each agent pod can touch, PVCs are the communication medium (shared mailboxes), RBAC enforces capability boundaries per role. This is the cloud-native philosophical difference KubeCon audiences care about.

**4. Use case fit.**
Gastown's sweet spot: many independent tasks, many agents, async batch execution. Our sweet spot: one complex task, a small team of agents collaborating deeply with tight coordination. These are complementary, not competing — but collaborative multi-step work (lead + implementer + reviewer on a single feature) is a use case Gastown doesn't elegantly model.

**5. The dogfooding story.**
Built with the same tool it runs. Gastown doesn't have this.

### One-Line Differentiation for the Proposal

> *"Gastown builds a new protocol on top of K8s. We bring Anthropic's native protocol into K8s."*

### Honest Gaps vs. Gastown (acknowledge in the talk)

- Gastown is more mature with production-hardened operational features (watchdogs, merge queue, stall detection)
- Gastown's git-backed persistence is more durable than PVC-based communication — if a cluster dies, git survives
- Gastown operator already has enterprise features (OpenShift, FIPS, supply chain security) we're still building toward
- Gastown has a battle-tested CLI (`kubectl-gt`) designed for both human operators and agents

Acknowledging this honestly makes the talk more credible. Frame it as: "here's the existing landscape and here's the specific gap we fill."

---

## Interesting Problems Encountered

*Claude Code: when you hit something non-obvious, surprising, or that required a design decision, log it here with a brief note. These are the raw materials for the talk narrative.*

<!-- FORMAT:
### [Date] Short title
What happened, what you tried, what you decided. One paragraph is enough.
-->

### 2026-04-14 Proving RWX without an RWX provisioner
The mailbox-exchange claim (`~/.claude/teams/{team}/inboxes/{agent}.json` visible across two pods) needed a smoke test we could run anywhere, including single-node Kind. The real blocker is that Kind's built-in `local-path-provisioner` only exposes ReadWriteOnce — it literally cannot advertise RWX. The acceptance setup works around this with a StorageClass alias named `nfs` that points back at `rancher.io/local-path`, giving PVCs the *name* they expect while leaning on the fact that on a single-node cluster every pod runs on the same node, so a hostPath volume is visible to every pod simultaneously. That is not "true" RWX — it's a coincidence of topology that makes RWX-semantics tests pass. The trade-off for the talk: we can prove the *architectural claim* (file-based coordination works when pods share a mount) on any laptop, but must label real multi-node deployments as requiring an actual RWX backend (NFS, EFS, Filestore, etc.). The smoke test (`hack/mailbox-smoke-test.sh`) runs against whatever the cluster offers and reports the effective access mode in its PASS line so this distinction doesn't get lost.

---

## Demo Milestones

Track what's working and could be shown on stage.

- [ ] CRDs install cleanly (`kubectl apply -f config/crd/`)
- [ ] Operator starts and watches `AgentTeam` resources
- [ ] PVC creation + init Job (Phase 1 reconciler)
- [ ] Lead + teammate pods spin up from a sample `AgentTeam`
- [ ] Mailbox files appear on the shared PVC between pods
- [ ] A real coding task runs end-to-end in Kind
- [ ] `kubectl describe agentteam` shows meaningful status
- [ ] Budget estimation visible in status / metrics
- [ ] Prometheus metrics scraping
- [ ] Clean demo script (2-minute happy path for on-stage use)

---

## Proposal Draft

*Fill this in once the demo milestones are mostly green.*

**Title:**

**Abstract (500 chars):**

**Talk outline:**

**Speaker bio:**

**Demo description:**

---

## Useful Links

- CNCF CFP: https://sessionize.com/kubecon-cloudnativecon-north-america-2026/
- LF Events page: https://events.linuxfoundation.org/kubecon-cloudnativecon-north-america/
- CNCF talk submission tips: https://www.cncf.io/blog/2023/11/13/tips-for-submitting-a-talk-to-kubecon-cloudnativecon/
- Prior art: KubeCon talks on AI/ML operators for reference framing
