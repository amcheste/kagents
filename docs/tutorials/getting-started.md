# Getting started

This tutorial walks you from a fresh laptop to a running AgentTeam in about 15 minutes. By the end you'll have:

- A local Kubernetes cluster with kagents installed
- A small Cowork-mode AgentTeam that researches a topic and writes a summary file
- The know-how to inspect what's happening with `kubectl` and the dashboard

You don't need any cloud accounts or external services. Everything runs on your laptop.

## Prerequisites

| Tool | Version | Why |
|------|---------|-----|
| [Docker](https://docs.docker.com/get-docker/) | latest | Runs the Kind cluster |
| [kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) | 0.25+ | Single-node Kubernetes for dev |
| [kubectl](https://kubernetes.io/docs/tasks/tools/) | 1.28+ | Interact with the cluster |
| [helm](https://helm.sh/docs/intro/install/) | 3.14+ | Install the operator chart |
| [Anthropic API key](https://console.anthropic.com/) | (any) | Required for agents to actually call Claude |

You'll also need the kagents repo cloned locally so you can use the included `make kind-create` setup script (which provisions a Kind cluster with the NFS-style RWX storage the operator needs):

```bash
git clone https://github.com/amcheste/kagents.git
cd kagents
```

## 1. Stand up a local cluster

```bash
make kind-create
```

This creates a Kind cluster named `claude-teams` with a local-path storage class aliased as `nfs`. On a single-node cluster every pod runs on the same node, so a hostPath volume is visible to all pods. That's our RWX-equivalent for laptop testing.

!!! note "Production deployments need a real RWX backend"
    For real multi-node clusters you'll need NFS, EFS, Filestore, or Azure Files. The Kind setup is a single-node convenience, not the production story. See the *Concept: file-based mailbox protocol* page (coming in v0.7.0) for why.

Verify the cluster is up:

```bash
kubectl cluster-info --context kind-claude-teams
```

## 2. Install kagents

```bash
helm install kagents \
  oci://ghcr.io/amcheste/charts/kagents \
  --namespace claude-teams-system --create-namespace
```

Wait for the operator pod to be ready:

```bash
kubectl rollout status deployment/kagents-controller-manager \
  --namespace claude-teams-system --timeout=120s
```

You should see `deployment "kagents-controller-manager" successfully rolled out`.

## 3. Create your Anthropic API key Secret

The operator reads this Secret from the namespace where your team runs (not the operator's namespace). Create a namespace for the team and put the key there:

```bash
kubectl create namespace dev-agents
kubectl create secret generic anthropic-api-key \
  --namespace dev-agents \
  --from-literal=ANTHROPIC_API_KEY=sk-ant-...
```

Replace `sk-ant-...` with your actual key from [console.anthropic.com](https://console.anthropic.com/).

!!! warning "Don't commit your API key"
    The Secret stays in the cluster. Never paste a real key into a manifest you'll commit. Use `kubectl create secret` from your shell as above, or sealed-secrets / external-secrets for production.

## 4. Apply your first AgentTeam

This is a small Cowork-mode team. No git repo, just an output volume. The lead coordinates a single writer agent that produces a Markdown file.

```yaml title="hello-team.yaml"
apiVersion: kagents.dev/v1alpha1
kind: AgentTeam
metadata:
  name: hello-team
  namespace: dev-agents
spec:
  workspace:
    output:
      mountPath: /workspace/output
      size: 1Gi

  auth:
    apiKeySecret: anthropic-api-key

  lead:
    model: opus
    prompt: |
      Coordinate a one-person team that writes a 200-word overview of
      Kubernetes operators to /workspace/output/overview.md. Make sure
      the file is written before declaring the work complete.

  teammates:
    - name: writer
      model: sonnet
      prompt: |
        Write a 200-word overview of Kubernetes operators to
        /workspace/output/overview.md. Keep it accessible to a reader
        who has never used Kubernetes before. Cover: what an operator
        is, what problem it solves, and one concrete example.

  lifecycle:
    timeout: 30m
    budgetLimit: "1.00"
```

Apply it:

```bash
kubectl apply -f hello-team.yaml
```

## 5. Watch the team run

```bash
kubectl get agentteams -n dev-agents -w
```

You'll see the team progress through phases:

| Phase | Meaning |
|-------|---------|
| `Pending` | Operator received the spec; PVCs being provisioned |
| `Initializing` | Init Job running (sets up worktrees / output volume) |
| `Running` | Agent pods are up and working |
| `Completed` | The lead reported the work done |
| `Failed` / `BudgetExceeded` / `TimedOut` | Terminal failure states |

A 200-word write usually finishes in 1–3 minutes.

When it reaches `Completed`, press Ctrl-C to stop watching.

## 6. Inspect what happened

The `describe` view shows everything in one place:

```bash
kubectl describe agentteam hello-team -n dev-agents
```

You'll see:

- The `Status` block with phase, ready count, estimated cost
- A `Lead` and `Teammates` section with each agent's pod status
- Recent `Events` from the operator at every phase transition

To see the actual file the team produced, exec into the writer pod and read it:

```bash
kubectl exec -n dev-agents hello-team-writer -- cat /workspace/output/overview.md
```

## 7. Clean up

Delete the team and the namespace:

```bash
kubectl delete agentteam hello-team -n dev-agents
kubectl delete namespace dev-agents
```

The operator will tear down all the team's pods, PVCs, and per-agent ServiceAccounts via owner references. To uninstall kagents itself:

```bash
helm uninstall kagents -n claude-teams-system
kubectl delete namespace claude-teams-system
```

To tear down the whole Kind cluster:

```bash
make kind-delete
```

## What you just did

A real Kubernetes operator just orchestrated two Claude Code instances communicating via a shared filesystem to produce real output, with K8s primitives doing the coordination work. RWX PVC for the mailbox, ServiceAccounts for per-agent identity, owner references for cleanup. No custom protocol, no orchestrator service, no daemon outside the cluster.

## Where to go next

- **[How-to guides](../how-to/index.md)**. Install on a real cloud, expose the dashboard, set budget alerts
- **[Reference](../reference/index.md)**. Every CRD field and Helm value documented
- **[Explanation](../explanation/index.md)**. How the file-based mailbox protocol actually works under the hood

## Common errors

??? warning "`PVCs stuck in Pending`"
    The operator requires a ReadWriteMany-capable StorageClass. On a Kind cluster, `make kind-create` sets one up under the alias `nfs`. If you're using your own cluster, check `kubectl get sc`. There must be one named `nfs` (or you need to pass `--set storage.storageClassName=<your-sc>` when installing the chart).

??? warning "`Pod stuck in CrashLoopBackOff`"
    Check the agent pod logs: `kubectl logs -n dev-agents hello-team-writer`. The most common cause is a missing or invalid Anthropic API key. Re-create the Secret with `kubectl create secret generic anthropic-api-key --namespace dev-agents --from-literal=ANTHROPIC_API_KEY=... --dry-run=client -o yaml | kubectl apply -f -`.

??? warning "`AgentTeam stuck in Initializing`"
    The init Job may have failed. Inspect: `kubectl get jobs -n dev-agents` and `kubectl logs -n dev-agents job/<init-job-name>`. Most often this is a permission issue with the StorageClass.
