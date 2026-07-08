# Migration Guide

This document covers breaking changes you need to apply when upgrading between kagents versions.

## v0.7.x → v0.8.0 — API group `claude.amcheste.io` → `kagents.dev`

### What's changing

The CRD API group is moving from `claude.amcheste.io` to `kagents.dev`, matching the project's owned domain and brand. This is a deliberate, one-time **clean break** — there is no conversion webhook and no automated data migration. The change is made while the project is pre-1.0 with effectively no production installs to protect; the cost of doing it now is far smaller than the cost of doing it later.

What changes:

| | Before (`v0.7.x`) | After (`v0.8.0`) |
|---|---|---|
| **API group** | `claude.amcheste.io` | `kagents.dev` |
| **CRD `apiVersion`** | `claude.amcheste.io/v1alpha1` | `kagents.dev/v1alpha1` |
| **CRD names** | `agentteams.claude.amcheste.io` etc. | `agentteams.kagents.dev` etc. |
| **Approval annotation prefix** | `approved.claude.amcheste.io/{event}` | `approved.kagents.dev/{event}` |
| **CRD kinds (`AgentTeam`, `AgentTeamTemplate`, `AgentTeamRun`)** | unchanged | unchanged |
| **Spec fields** | unchanged | unchanged |
| **Operator behavior** | unchanged | unchanged |

The schema itself (every field, every default, every validation rule) is identical between the two groups. Only the *name* changes.

### Why a clean break

A conversion webhook would let v0.7.x CRs keep working under the new group during a deprecation window, but it adds significant machinery: a webhook server, cert-manager (or equivalent) cert wiring, conversion logic, and ongoing maintenance through versioning churn. For a pre-1.0 project with effectively no external installs, that cost is not justified — the migration is one `kubectl` session per cluster, documented below.

### Migration procedure

> **Read this in full before starting.** Step 2 deletes your existing AgentTeams; back them up in step 1 if you want to preserve any in-flight work.

```bash
# 1. (Optional) Back up any AgentTeams you want to keep
kubectl get agentteams,agentteamtemplates,agentteamruns -A -o yaml > kagents-backup.yaml

# 2. Remove the old operator and CRDs (this cascades to and deletes all CRs)
helm uninstall <your-release-name>  # e.g. helm uninstall kagents
kubectl delete crd \
  agentteams.claude.amcheste.io \
  agentteamtemplates.claude.amcheste.io \
  agentteamruns.claude.amcheste.io

# 3. Install the v0.8.0 build (CRDs register under kagents.dev)
helm install kagents ./charts/kagents
# or from OCI:
# helm install kagents oci://ghcr.io/amcheste/charts/kagents --version 0.8.0

# 4. If you backed up CRs in step 1, re-apply them after find/replacing
#    apiVersion: claude.amcheste.io/v1alpha1
#    →
#    apiVersion: kagents.dev/v1alpha1
sed -i.bak 's|apiVersion: claude.amcheste.io/v1alpha1|apiVersion: kagents.dev/v1alpha1|g' kagents-backup.yaml
kubectl apply -f kagents-backup.yaml
```

### Approval gate annotations

If you use approval gates, the annotation prefix moves with the group. Anywhere you set `approved.claude.amcheste.io/{event}=true` on an AgentTeam, switch to `approved.kagents.dev/{event}=true` after the migration.

```bash
# Before (v0.7.x)
kubectl annotate agentteam my-team approved.claude.amcheste.io/spawn-reviewer=true

# After (v0.8.0)
kubectl annotate agentteam my-team approved.kagents.dev/spawn-reviewer=true
```

### Rollback

If you need to revert during the cutover, the inverse procedure applies: `helm uninstall kagents`, delete the `kagents.dev` CRDs, install the v0.7.x build, re-apply your backup CRs without changing the apiVersion. There is no in-place rollback once CRs are under the new group.

### After the migration

- `kubectl get crd` should show only `*.kagents.dev` CRDs (no `*.claude.amcheste.io` entries).
- `kubectl get agentteams.kagents.dev -A` lists your teams under the new group.
- Existing approval annotations under `approved.claude.amcheste.io/*` will be ignored by v0.8.0; re-annotate under `approved.kagents.dev/*`.

## Future migrations

This document tracks API-breaking changes between released versions. Pre-1.0, breaking changes are noted here as they happen; post-1.0, breaking changes require a major-version bump and conform to the standard K8s deprecation policy.
