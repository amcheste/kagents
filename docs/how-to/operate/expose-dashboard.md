# Expose the dashboard

The dashboard ships with kagents but is **off by default**. Installing the chart alone gives you the controller and CRDs only. This guide walks through enabling it and exposing it for the three most common scenarios.

For why the dashboard is off by default and what it can show, see the [Operations explanation](../../explanation/operations.md).

## Enable the dashboard

The dashboard is a chart sub-component gated on `dashboard.enabled`:

```bash
helm upgrade kagents \
  oci://ghcr.io/amcheste/charts/kagents \
  --namespace claude-teams-system --reuse-values \
  --set dashboard.enabled=true
```

This deploys:

- A read-only `Deployment` running the dashboard binary
- A `ClusterIP` Service on port 8080
- A dedicated `ServiceAccount` with read-only RBAC on AgentTeam CRs and Pods/log
- Templates for an optional Ingress (off by default)

Verify the deployment:

```bash
kubectl rollout status deployment/kagents-dashboard \
  --namespace claude-teams-system --timeout=60s
```

## Scenario 1: dev / first-look (port-forward)

For local development or a quick "is it working" check, port-forward the Service:

```bash
kubectl port-forward -n claude-teams-system svc/kagents-dashboard 8080:8080
```

Open http://localhost:8080. You'll see the team list view; click any team for the detail page with live SSE updates.

`port-forward` is fine for dev but is a single-user tunnel through your local kubeconfig. Don't rely on it for shared access.

## Scenario 2: production (Ingress with basic auth)

For a small-team production deployment, expose the dashboard via an Ingress with basic auth in front. Most ingress controllers can do this without a separate auth proxy.

### a. Create the basic-auth secret

```bash
htpasswd -bc auth admin "$(openssl rand -base64 24)"
kubectl create secret generic dashboard-basic-auth \
  --namespace claude-teams-system \
  --from-file=auth
```

The `auth` file contains an htpasswd-formatted line; the `nginx` ingress controller (and most others) read this format directly.

### b. Configure the Ingress via Helm values

```yaml title="dashboard-values.yaml"
dashboard:
  enabled: true
  ingress:
    enabled: true
    className: nginx          # or "traefik", "alb", whatever your cluster uses
    annotations:
      nginx.ingress.kubernetes.io/auth-type: basic
      nginx.ingress.kubernetes.io/auth-secret: dashboard-basic-auth
      nginx.ingress.kubernetes.io/auth-realm: "kagents dashboard"
      cert-manager.io/cluster-issuer: letsencrypt-prod   # if using cert-manager
    hosts:
      - host: kagents.example.com
        paths:
          - path: /
            pathType: Prefix
    tls:
      - hosts: [kagents.example.com]
        secretName: kagents-dashboard-tls
```

Apply it:

```bash
helm upgrade kagents \
  oci://ghcr.io/amcheste/charts/kagents \
  --namespace claude-teams-system --reuse-values \
  -f dashboard-values.yaml
```

Set the DNS for `kagents.example.com` to the Ingress controller's external IP. Once cert-manager provisions the TLS cert (1-3 minutes), browse to https://kagents.example.com and authenticate with the password you generated.

## Scenario 3: corporate (oauth2-proxy + identity provider)

For larger teams that already have an OIDC identity provider (Okta, Auth0, Google Workspace, GitHub, etc.), put [`oauth2-proxy`](https://oauth2-proxy.github.io/oauth2-proxy/) in front of the dashboard.

The pattern:

1. Deploy oauth2-proxy as a separate Deployment + Service in the same namespace
2. Point your Ingress at oauth2-proxy instead of the dashboard
3. Configure oauth2-proxy's `--upstream` flag to forward authenticated requests to `http://kagents-dashboard:8080`

This is a standard pattern with extensive documentation in the oauth2-proxy project. The dashboard itself doesn't need to change. It stays on the internal Service, and oauth2-proxy handles all authentication and group/role checks before requests reach it.

## Scoping the dashboard to one namespace

By default the dashboard sees AgentTeams in **every** namespace (a `ClusterRoleBinding` grants read across the cluster). To restrict it to a single namespace, e.g. when teams in different namespaces belong to different tenants:

```bash
helm upgrade kagents \
  oci://ghcr.io/amcheste/charts/kagents \
  --namespace claude-teams-system --reuse-values \
  --set dashboard.enabled=true \
  --set dashboard.namespace=dev-agents
```

This:

- Passes `--namespace=dev-agents` to the dashboard binary, so it only lists teams from that namespace
- Generates a `RoleBinding` scoped to `dev-agents` instead of a `ClusterRoleBinding`

## Verifying

Once the dashboard is reachable, deploy a quick test team and open the detail view:

```bash
kubectl apply -n dev-agents -f config/samples/auth-refactor-team.yaml
```

The list view should show the team. Click in. The detail page streams live status updates via SSE; killing a teammate pod with `kubectl delete pod ...` should cause the page to redraw within a second or two.

## Where to look next

- [Operations explanation](../../explanation/operations.md). What the dashboard's metrics and alerts look like
- [Configure shared storage](shared-storage.md). Sizing and tuning the PVC backends
- [Set budget alerts](budget-alerts.md). Wiring webhook alerts on cost overruns
