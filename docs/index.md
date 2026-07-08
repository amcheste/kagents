---
hide:
  - navigation
  - toc
---

# kagents

**Run Claude Code Agent Teams as a Kubernetes operator.**

[Get started in 5 minutes :material-rocket-launch:](tutorials/getting-started.md){ .md-button .md-button--primary }
[View on GitHub :material-github:](https://github.com/amcheste/kagents){ .md-button }

---

## Quick install

```bash
helm install kagents \
  oci://ghcr.io/amcheste/charts/kagents \
  --namespace claude-teams-system --create-namespace
```

## Why kagents

<div class="grid cards" markdown>

-   :material-protocol:{ .lg .middle } **Native protocol fidelity**

    ---

    Wraps Anthropic's file-based mailbox protocol exactly as designed. No custom RPC layer to maintain, no protocol translation, no behavior drift when Claude Code ships an update.

-   :material-account-group:{ .lg .middle } **Team as a first-class resource**

    ---

    One `AgentTeam` CRD declares roles, budget, quality gates, and coordination topology. `AgentTeamTemplate` lets you reuse common team patterns ("3-agent security review", "fullstack feature team"), each instantiable in one line.

-   :material-kubernetes:{ .lg .middle } **K8s as coordination fabric**

    ---

    ServiceAccounts scope what each agent pod can touch. RWX PVCs hold the shared mailboxes. RBAC enforces per-agent capability boundaries. The cluster does the coordination work. Kagents just wires it up.

-   :material-recycle-variant:{ .lg .middle } **Dogfooded**

    ---

    Built with the same Claude Code agent teams it operates. Every release is shipped by an agent team running in production. The recursion is intentional.

</div>

## What you'll find here

<div class="grid cards" markdown>

-   :material-school: **[Tutorials](tutorials/index.md)**

    Step-by-step walkthroughs from zero to a running AgentTeam.

-   :material-cog: **[How-to guides](how-to/index.md)**

    Recipes for specific operational tasks. Install on a cloud, expose the dashboard, tune budgets.

-   :material-book-open-variant: **[Reference](reference/index.md)**

    CRD field reference, Helm values, CLI flags.

-   :material-lightbulb: **[Explanation](explanation/index.md)**

    How and why kagents works the way it does. The architecture, the design tradeoffs.

</div>

---

<div class="grid cards" markdown>

-   :fontawesome-brands-github:{ .lg .middle } **Source code**

    Apache 2.0. Issues, PRs, and Discussions welcome.

    [github.com/amcheste/kagents](https://github.com/amcheste/kagents)

-   :material-presentation:{ .lg .middle } **Talk**

    *Reconciling Agent Teams: A Kubernetes Operator for Claude Code*. KubeCon NA 2026 (submitted).

</div>
