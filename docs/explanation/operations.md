# Operations

Three concerns once a team is running: how much it costs, what each agent can touch, and how you see what it's doing.

## Budget tracking

Claude Code does not expose real-time token usage to the outside world. The operator estimates cost from elapsed time and the model assigned to each agent.

### How the estimate works

The estimator (in [`internal/budget`](https://github.com/amcheste/kagents/tree/main/internal/budget)) treats every active agent session as if it consumes a fixed token rate per minute. The rate per million tokens uses Anthropic's published list price, applied to a **heuristic of 50,000 input + 5,000 output tokens per active minute** per agent.

| Model | Input ($/M tokens) | Output ($/M tokens) | Approx. cost / minute / agent |
|-------|-------------------:|--------------------:|------------------------------:|
| `opus` | $5.00 | $25.00 | $0.375 |
| `sonnet` | $3.00 | $15.00 | $0.225 |

The cost ticks up monotonically while pods are in `Running`. The reconciler aggregates per-teammate cost into `status.estimatedCostUsd`.

### What triggers BudgetExceeded

The reconciler compares `status.estimatedCostUsd` against `spec.lifecycle.budgetLimit` on every reconcile loop. When the estimate crosses the limit:

1. Phase transitions to `BudgetExceeded`
2. All agent pods are deleted via owner-reference cascade
3. `status.completedAt` is stamped
4. A `webhook.budgetExceeded` event fires (if configured)

There's no grace period. The team stops the moment the estimate crosses. Set the limit with headroom.

### Honest tradeoffs

This is the lightest-touch approach available without instrumenting Claude Code. The honest limitations:

- **Estimate, not measurement.** Real token usage depends on prompt length, context window growth, and how often the agent reaches for tools. The estimate can be off by 2-3x in either direction.
- **Heuristic is per-active-minute.** An agent waiting on `dependsOn` doesn't accrue cost; one running flat out at the same rate as one mostly idle does. The heuristic averages the difference away.
- **Rate table is hardcoded.** The token-per-minute heuristic and the per-million prices live in `internal/budget/tracker.go`. Adjusting them requires a code change and rebuild. Config-via-Helm-values is on the roadmap.

For production, set `budgetLimit` ~2x what you actually want to spend, and treat the budget as a circuit breaker rather than a precise meter. Real cost tracking via instrumented Claude Code or sidecar log parsing is on the roadmap; until then, the [Anthropic console](https://console.anthropic.com/) is the source of truth for accounting.

## Per-agent RBAC

Every agent pod in a team gets its own `ServiceAccount`, `Role`, and `RoleBinding`. The lead and each teammate are isolated: a compromised teammate cannot read a peer's secrets or PVCs.

### What gets created

For an `AgentTeam` with a lead and three teammates, the operator creates:

- 1 `ServiceAccount` for the lead
- 1 `Role` granting access to the lead's secrets and the team-state PVC
- 1 `RoleBinding` binding the SA to the Role
- 3 `ServiceAccount`s, one per teammate
- 3 `Role`s, scoped to that teammate's secrets and PVCs only
- 3 `RoleBindings`

All eight resources are owned by the `AgentTeam`. Deleting the team garbage-collects everything.

### What each agent can do

The Roles use `resourceNames` to scope by name, not just by type. A teammate's Role grants:

| Resource | Verbs | Scope |
|----------|------:|-------|
| `secrets` | `get` | Only the API key Secret + that teammate's git credentials Secret |
| `persistentvolumeclaims` | `get`, `list`, `watch` | Only the PVCs this team uses |

Notably absent:

- No `pods`. Agents cannot list or exec into peer pods.
- No `pods/exec`. The teammate cannot escape the pod by `kubectl exec`.
- No `configmaps`. Skill ConfigMaps are mounted by the operator; the agent cannot enumerate or read other ConfigMaps.

### What this defends against

The threat model is "a teammate's prompt is malicious or compromised." The blast radius from that scenario is:

- ✅ Cannot read another teammate's secrets (different SA)
- ✅ Cannot exec into the lead pod (no `pods/exec`)
- ✅ Cannot enumerate cluster state (no list verbs on namespace-wide resources)
- ⚠️ Can write to the shared `team-state` PVC. A malicious teammate could poison the task list or write to a peer's inbox. This is inherent to the file-based protocol; mitigations would require Claude Code to authenticate writes.
- ⚠️ Can write to the shared `repo` PVC. Worktrees are isolated by branch, but the agent could `cd` to a peer's worktree.

The RBAC model handles the K8s side cleanly; the filesystem-level threats need protocol-level signing to fully address. For most use cases. Internal CI, trusted prompts. The filesystem trust model is acceptable.

## Observability

The operator exposes Prometheus metrics, ships a Grafana dashboard, and fires webhook events on key state transitions.

### Prometheus metrics

The operator binary exposes `/metrics` on port 8080 by default. Two
prefix families are emitted in parallel: the original `claude_*` series
that have been there since v0.3.0 (kept stable to avoid breaking
existing dashboards), and a `kagents_*` family added in v0.8.0 that
covers knowledge-work observability (pipeline stages, artifacts,
delivery). Both stream from the same `/metrics` endpoint.

| Metric | Type | Description |
|--------|------|-------------|
| `claude_team_active_total` | gauge | Count of teams in non-terminal phases |
| `claude_team_duration_seconds` | histogram | Wall-clock time from `Pending` to a terminal phase |
| `claude_teammate_tokens_total` | counter | Estimated tokens consumed per teammate / model |
| `claude_team_cost_usd` | gauge | Current `status.estimatedCostUsd` |
| `claude_team_tasks_completed_total` | counter | Tasks marked complete in the shared task list |
| `claude_teammate_restarts_total` | counter | Pod restarts per teammate |
| `claude_team_budget_remaining_usd` | gauge | `budgetLimit - estimatedCostUsd` |
| `claude_teammate_idle_seconds` | histogram | Time between task completions per teammate |
| `kagents_team_pipeline_stage_active` | gauge | 1 while a pipeline stage is in `Running`, 0 otherwise. Labels: `team`, `namespace`, `stage` |
| `kagents_team_stage_duration_seconds` | histogram | Stage wall-clock duration observed once at the `Running → Completed` transition |
| `kagents_team_artifacts_produced_total` | counter | Artifacts appended to `status.artifacts` per teammate |
| `kagents_team_delivery_success_total` | counter | Successful `onComplete: deliver` dispatches, by target type |
| `kagents_team_delivery_failure_total` | counter | Failed deliveries by target type |

Wire them to Prometheus by enabling the chart's ServiceMonitor:

```bash
helm upgrade kagents ./charts/kagents \
  --set metrics.serviceMonitor.enabled=true
```

### Grafana dashboard

The chart ships a curated Grafana dashboard as a ConfigMap with the `grafana_dashboard: "1"` label. With the standard `kube-prometheus-stack`, the Grafana sidecar auto-imports it within ~30 seconds.

```bash
helm upgrade kagents ./charts/kagents \
  --set metrics.serviceMonitor.enabled=true \
  --set metrics.grafanaDashboard.enabled=true
```

The dashboard's panels cover active team count, cost rate, per-teammate task throughput, restart count, and idle-time distribution.

### Webhook events

The operator's webhook engine POSTs JSON payloads to a configured URL on key transitions. Events that fire:

| Event type | When |
|------------|------|
| `team.started` | The team transitions to `Running` |
| `teammate.error` | A teammate pod enters `CrashLoopBackOff` or `Error` |
| `budget.warning` | Estimated cost crosses 80% of `budgetLimit` |
| `completed` | An approval gate is hit; reconciler is waiting on `kubectl annotate` |

Configure via the chart's `webhook` values. Each event includes the team name, namespace, phase, and a payload-type-specific extras object.

### Approval gates

Approval gates pause spawning a specific teammate until a human applies an annotation. They're useful when one agent's output should be reviewed before subsequent agents see it.

```yaml
spec:
  lifecycle:
    approvalGates:
      - event: "spawn-email-drafter"
        channel: "webhook"
        webhookUrl: "https://hooks.example.com/approvals"
```

When the reconciler would otherwise spawn the gated teammate, it instead:

1. Marks the teammate's `status.pendingApproval` field
2. Fires a `completed` webhook event with the gate name
3. Waits for the annotation `approved.kagents.dev/spawn-email-drafter=true`

Grant approval:

```bash
kubectl annotate agentteam my-team \
  approved.kagents.dev/spawn-email-drafter=true
```

Within 30 seconds (the default reconcile interval), the gated teammate spawns and joins the team.

## Where to look next

- [Resource model](resources.md). What an `AgentTeam` looks like under the hood
- [Coordination protocol](coordination.md). How the agents actually talk to each other
- [How-to guides](../how-to/index.md). Concrete operational recipes (coming in v0.7.0)
