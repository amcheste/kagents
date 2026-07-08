# Helm Values Reference

Complete reference for the [`kagents` Helm chart](../charts/kagents). Every key from [`values.yaml`](../charts/kagents/values.yaml) is documented here.

> **Conventions used below:**
> - **Default** is what ships when you `helm install` without overrides.
> - **Required** marks values you almost always want to set.
> - **Production** marks values worth tuning before going live.

## Quick install

```bash
helm install kagents \
  oci://ghcr.io/amcheste/charts/kagents \
  --namespace claude-teams-system --create-namespace
```

To override values, use `--set key=value` or `--values overrides.yaml`. To inspect what would be installed: `helm template ...` or `helm install --dry-run ...`.

---

## Top-level

| Key | Default | Description |
|---|---|---|
| `replicaCount` | `1` | Number of operator replicas. Use `> 1` only with `leaderElection.enabled=true`. |

## Operator image

| Key | Default | Description |
|---|---|---|
| `image.repository` | `ghcr.io/amcheste/kagents` | Operator container image. Pinned to the release tag at chart publish time. |
| `image.tag` | `latest` | Operator image tag. Released charts pin this to the matching release version. |
| `image.pullPolicy` | `IfNotPresent` | One of `Always`, `IfNotPresent`, `Never`. |

## Agent runner image

The image used for every spawned agent pod (lead + teammates). Configurable per-team at the `AgentTeam` spec level too; this value is the cluster-wide default.

| Key | Default | Description |
|---|---|---|
| `claudeCodeImage.repository` | `ghcr.io/amcheste/claude-code-runner` | Runner image. |
| `claudeCodeImage.tag` | `latest` | Runner tag. Released charts pin this to the release version. |
| `claudeCodeImage.pullPolicy` | `IfNotPresent` | Pull policy for the runner image. |

## Image pull secrets

| Key | Default | Description |
|---|---|---|
| `imagePullSecrets` | `[]` | List of secret references used for both operator and runner images. **Required** when pulling from a private registry. Example: `imagePullSecrets: [{name: ghcr-creds}]`. |

## ServiceAccount

| Key | Default | Description |
|---|---|---|
| `serviceAccount.create` | `true` | If false, the chart will not create a ServiceAccount; you must reference an existing one via `serviceAccount.name`. |
| `serviceAccount.name` | `kagents` | Name of the operator's ServiceAccount. The hand-written ClusterRole binds to this name. |

## Resources

The operator pod is single-replica and lightweight by default. Bump limits if you run with leader election or watch many namespaces.

| Key | Default | Description |
|---|---|---|
| `resources.requests.cpu` | `100m` | CPU request. |
| `resources.requests.memory` | `128Mi` | Memory request. |
| `resources.limits.cpu` | `500m` | CPU limit. |
| `resources.limits.memory` | `256Mi` | Memory limit. |

## Storage

Defaults applied to PVCs the operator creates per AgentTeam. **Required:** the storage class must support `ReadWriteMany` for multi-pod teams (NFS, EFS, CephFS). See [ARCHITECTURE.md § Storage Requirements](../ARCHITECTURE.md#storage-requirements).

| Key | Default | Description |
|---|---|---|
| `storage.storageClassName` | `nfs` | StorageClass name used for team-state and repo PVCs. |
| `storage.teamStateSize` | `5Gi` | Size of the team-state PVC (mailboxes + task list). |
| `storage.repoSize` | `20Gi` | Size of the per-team repo PVC (clones + worktrees). |

## Metrics. Service + ServiceMonitor

| Key | Default | Description |
|---|---|---|
| `metrics.enabled` | `true` | Renders a `Service` exposing port `metrics.port`. Disable on stripped-down clusters. |
| `metrics.port` | `8080` | Operator metrics port. |
| `metrics.serviceMonitor.enabled` | `false` | **Production:** set to `true` when running with kube-prometheus-stack. Requires the `monitoring.coreos.com` CRDs. |
| `metrics.serviceMonitor.namespace` | `""` | Namespace for the ServiceMonitor. Defaults to the release namespace. Set this to the Prometheus namespace when using a namespace-scoped selector. |
| `metrics.serviceMonitor.interval` | `30s` | Prometheus scrape interval. |
| `metrics.serviceMonitor.additionalLabels` | `{}` | Extra labels on the ServiceMonitor. Match your Prometheus CR's selector, e.g. `{release: kube-prometheus-stack}`. |

## Metrics. Grafana dashboard

Renders a ConfigMap holding a 10-panel Grafana dashboard for Claude team observability. With kube-prometheus-stack, the Grafana sidecar auto-imports any ConfigMap carrying the configured label.

| Key | Default | Description |
|---|---|---|
| `metrics.grafanaDashboard.enabled` | `false` | **Production:** set `true` to ship the bundled dashboard. |
| `metrics.grafanaDashboard.namespace` | `""` | Namespace for the dashboard ConfigMap. Defaults to the release namespace. Many setups need this to match the Grafana namespace so the sidecar picks it up. |
| `metrics.grafanaDashboard.sidecarLabel` | `grafana_dashboard` | Label key the Grafana sidecar watches. Set to `""` to disable the sidecar label. |
| `metrics.grafanaDashboard.sidecarLabelValue` | `"1"` | Label value paired with `sidecarLabel`. |
| `metrics.grafanaDashboard.additionalLabels` | `{}` | Extra labels on the dashboard ConfigMap. |

## Health probes

| Key | Default | Description |
|---|---|---|
| `healthProbe.port` | `8081` | Port serving `/healthz` (liveness) and `/readyz` (readiness). |

## Leader election

| Key | Default | Description |
|---|---|---|
| `leaderElection.enabled` | `false` | **Production HA:** required when `replicaCount > 1`. Backed by Kubernetes `Lease` objects in the operator namespace. |

## Default policy for AgentTeams

Cluster-wide defaults applied to teams that don't set them on their own spec.

| Key | Default | Description |
|---|---|---|
| `defaultBudgetLimit` | `50.00` | Default USD budget cap when an AgentTeam doesn't set `lifecycle.budgetLimit`. |
| `defaultTimeout` | `4h` | Default wall-clock timeout when an AgentTeam doesn't set `lifecycle.timeout`. |

## Logging

| Key | Default | Description |
|---|---|---|
| `logLevel` | `info` | Operator log level. One of `debug`, `info`, `warn`, `error`. Use `debug` to trace reconcile decisions. |

## Pod scheduling

| Key | Default | Description |
|---|---|---|
| `nodeSelector` | `{}` | Standard Kubernetes node selector for the operator pod. |
| `tolerations` | `[]` | Tolerations for the operator pod. |
| `affinity` | `{}` | Pod affinity / anti-affinity rules. |

## Security context

The chart ships a hardened profile by default. Override only when a cluster policy (e.g. PSP/OPA) demands different values.

| Key | Default | Description |
|---|---|---|
| `podSecurityContext.runAsNonRoot` | `true` | Refuse to start as root. |
| `podSecurityContext.runAsUser` | `65532` | Distroless `nonroot` UID. |
| `podSecurityContext.runAsGroup` | `65532` | Distroless `nonroot` GID. |
| `podSecurityContext.fsGroup` | `65532` | Mounted volume group ownership. |
| `podSecurityContext.seccompProfile.type` | `RuntimeDefault` | Use the runtime's default seccomp profile. |
| `containerSecurityContext.allowPrivilegeEscalation` | `false` | Block setuid escalation. |
| `containerSecurityContext.readOnlyRootFilesystem` | `true` | The operator does not write outside `/tmp` so this is safe. |
| `containerSecurityContext.capabilities.drop` | `[ALL]` | The operator's only network needs are outbound HTTP to the K8s API. |

## Extensibility

Escape hatches for ad-hoc tuning that doesn't justify a dedicated value.

| Key | Default | Description |
|---|---|---|
| `extraArgs` | `[]` | Additional CLI args appended to the operator command. Example: `extraArgs: ["--zap-log-level=debug"]`. |
| `extraEnv` | `{}` | Extra env vars merged into the operator-config ConfigMap. Example: `extraEnv: {CUSTOM_FLAG: "value"}`. |
| `podAnnotations` | `{}` | Annotations applied to the operator pod template (in addition to the chart's `checksum/config` annotation). |

---

## Common override recipes

### Production with full observability

```yaml
metrics:
  serviceMonitor:
    enabled: true
    additionalLabels:
      release: kube-prometheus-stack
  grafanaDashboard:
    enabled: true
    namespace: monitoring   # match your Grafana install
leaderElection:
  enabled: true
replicaCount: 2
```

### Single-node Kind / minikube

The default `nfs` storage class won't exist; alias it to your local provisioner first (see [hack/kind-setup.sh](../hack/kind-setup.sh)). Then:

```yaml
storage:
  storageClassName: nfs   # alias created by hack script
```

### Private registry pull

```yaml
imagePullSecrets:
  - name: ghcr-creds   # docker-registry secret in the release namespace
image:
  repository: registry.internal/kagents
  tag: 0.5.0
claudeCodeImage:
  repository: registry.internal/claude-code-runner
  tag: 0.5.0
```

### Debug logging without a chart upgrade

```bash
kubectl edit configmap <release>-config -n claude-teams-system
# Change LOG_LEVEL: info → debug
kubectl rollout restart deployment/<release>-controller-manager -n claude-teams-system
```

---

## Regenerating this document

This doc is hand-written to keep the rendered output controllable. When `values.yaml` changes, update this file in the same PR. Future automation (`helm-docs`) is tracked in [#89](https://github.com/amcheste/kagents/issues/89).
