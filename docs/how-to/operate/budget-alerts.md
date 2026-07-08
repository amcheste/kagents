# Set budget alerts

This guide covers the four ways to limit and observe spend on a kagents installation: per-team budget limits, the chart-wide default, webhook events on threshold crossings, and Prometheus alert rules.

For how the budget estimate is computed and its honest limitations, see the [Operations explanation](../../explanation/operations.md).

## Per-team `budgetLimit`

The hard stop. When a team's `status.estimatedCostUsd` crosses `spec.lifecycle.budgetLimit`, the operator deletes all the team's pods and transitions the phase to `BudgetExceeded`.

```yaml
apiVersion: kagents.dev/v1alpha1
kind: AgentTeam
metadata:
  name: nightly-security-review
spec:
  # ...
  lifecycle:
    timeout: 4h
    budgetLimit: "10.00"   # USD
```

There's no grace period. The team stops the moment the estimate crosses. The estimate is conservative-to-the-low-side (~50K input + 5K output tokens per agent per minute is a rough ballpark), so set the limit with **2x headroom** over what you actually want to spend.

## Chart-wide default

For teams that don't set their own `budgetLimit`, the operator falls back to a chart-level default. The default value is **$50.00**. Override at install time:

```bash
helm upgrade kagents \
  oci://ghcr.io/amcheste/charts/kagents \
  --namespace claude-teams-system --reuse-values \
  --set defaultBudgetLimit=15.00
```

This is a safety net, not a recommendation. Every team should set its own `budgetLimit` based on the work it's doing. The default exists to prevent a misconfigured team from running unbounded.

## Webhook events on threshold crossings

The operator fires a `budget.warning` webhook event when a team's estimated cost crosses **80% of its `budgetLimit`**. Useful as an early warning before the hard stop fires.

### Configure the webhook URL

Set the chart-level webhook URL (applies to all teams unless overridden per-team):

```bash
helm upgrade kagents \
  oci://ghcr.io/amcheste/charts/kagents \
  --namespace claude-teams-system --reuse-values \
  --set webhook.defaultUrl=https://hooks.example.com/kagents
```

### Payload shape

Each event POSTs a JSON body:

```json
{
  "type": "budget.warning",
  "team": {
    "namespace": "dev-agents",
    "name": "auth-refactor",
    "phase": "Running"
  },
  "budget": {
    "limitUsd": 10.00,
    "estimatedCostUsd": 8.42,
    "percentOfLimit": 84.2
  },
  "timestamp": "2026-05-02T14:33:21Z"
}
```

### Wire to Slack

For a Slack notification, point the webhook at an [Incoming Webhook URL](https://api.slack.com/messaging/webhooks) and translate the payload via a small Cloud Function or a dedicated webhook-relay service (Slack expects its own message format, not the kagents one).

A minimal relay in Cloud Run / AWS Lambda / Azure Functions:

```python title="kagents-to-slack.py"
import json, os, urllib.request

def handler(event):
    body = json.loads(event["body"])
    if body["type"] != "budget.warning":
        return {"statusCode": 204}

    msg = {
        "text": f":warning: kagents budget warning: "
                f"team `{body['team']['namespace']}/{body['team']['name']}` "
                f"at {body['budget']['percentOfLimit']:.1f}% of "
                f"${body['budget']['limitUsd']:.2f} limit "
                f"(${body['budget']['estimatedCostUsd']:.2f} estimated)"
    }
    req = urllib.request.Request(
        os.environ["SLACK_WEBHOOK_URL"],
        data=json.dumps(msg).encode(),
        headers={"Content-Type": "application/json"}
    )
    urllib.request.urlopen(req)
    return {"statusCode": 200}
```

### Wire to PagerDuty

PagerDuty's [Events API v2](https://developer.pagerduty.com/docs/events-api-v2/overview/) accepts a similar relay pattern. The dedup key should combine team namespace + name so repeated `budget.warning` events for the same team collapse to a single incident.

## Prometheus alert rules

For teams that already have a Prometheus + Alertmanager stack, alert directly on the metrics the chart exposes. The relevant series:

- `claude_team_cost_usd{team_name=...}`. Current estimated cost
- `claude_team_budget_remaining_usd{team_name=...}`. `limit - cost`

### Alert: budget about to be exceeded

```yaml title="kagents-alerts.yaml"
groups:
  - name: kagents-budget
    rules:
      - alert: KagentsBudgetWarning
        expr: |
          (claude_team_cost_usd / on(team_name) group_left
            (claude_team_cost_usd + claude_team_budget_remaining_usd))
            > 0.80
        for: 1m
        labels:
          severity: warning
        annotations:
          summary: "kagents team {{ $labels.team_name }} at {{ $value | humanizePercentage }} of budget"
          description: |
            Team {{ $labels.namespace }}/{{ $labels.team_name }} has
            consumed {{ $value | humanizePercentage }} of its budget.
            Hard stop fires at 100%.

      - alert: KagentsBudgetExceeded
        expr: claude_team_budget_remaining_usd <= 0
        for: 30s
        labels:
          severity: critical
        annotations:
          summary: "kagents team {{ $labels.team_name }} hit budget limit and was terminated"
```

Apply via your Prometheus operator's `PrometheusRule` CRD if you're using kube-prometheus-stack:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: kagents-budget
  namespace: monitoring
  labels:
    release: kube-prometheus-stack
spec:
  groups:
    # ...same as above
```

### Alert: aggregate cost across all running teams

For total spend visibility:

```yaml
- alert: KagentsAggregateCostHigh
  expr: sum(claude_team_cost_usd) > 100
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "Total in-flight kagents cost exceeds $100"
    description: |
      Aggregate estimated cost across all running teams: ${{ $value }}.
      Investigate which teams are running with: kubectl get agentteams -A
```

## Cross-checking against actual spend

The operator's estimate is a heuristic, not a meter. Reconcile against ground truth at least weekly:

- Pull actual spend from the [Anthropic Console](https://console.anthropic.com/) usage API
- Compare against the `claude_team_cost_usd` Prometheus history for the same time window
- Adjust your `budgetLimit` headroom factor based on the observed estimate-vs-actual ratio

If the estimate is consistently 50% low, double your `budgetLimit` headroom. If it's 200% high, you can tighten limits.

## Where to look next

- [Operations explanation](../../explanation/operations.md). How the budget is computed in detail
- [Expose the dashboard](expose-dashboard.md). Visual cost view per team
- [Configure shared storage](shared-storage.md). The other recurring cost on a kagents install
