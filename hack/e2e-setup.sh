#!/usr/bin/env bash
# hack/e2e-setup.sh
#
# Creates a Kind cluster, builds the operator image and the claude-code-runner
# image, loads both into Kind, installs CRDs, and deploys the operator with NO
# agent-image override — so agent pods start the real Claude Code runner and
# call the live Anthropic API.
#
# The ANTHROPIC_API_KEY env var is required — the E2E test suite reads it to
# populate per-test Secrets that the AgentTeam references.
#
# Usage:
#   export ANTHROPIC_API_KEY=sk-ant-...
#   bash hack/e2e-setup.sh
#
# Requires: kind, kubectl, docker, go (for make manifests)

set -euo pipefail

# Extend PATH to pick up Docker Desktop and Homebrew binaries.
export PATH="/opt/homebrew/bin:/usr/local/bin:/Applications/Docker.app/Contents/Resources/bin:${PATH}"

if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
  echo "ERROR: ANTHROPIC_API_KEY is not set — the E2E suite hits the real API."
  echo "       export ANTHROPIC_API_KEY=sk-ant-... and re-run."
  exit 1
fi

CLUSTER_NAME="${KIND_CLUSTER_NAME:-claude-teams-e2e}"
OPERATOR_IMG="${OPERATOR_IMG:-ghcr.io/amcheste/kagents:e2e}"
RUNNER_IMG="${RUNNER_IMG:-ghcr.io/amcheste/claude-code-runner:e2e}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

log() { echo "==> $*"; }

log "Building operator image ${OPERATOR_IMG}"
docker build -t "${OPERATOR_IMG}" -f "${REPO_ROOT}/docker/Dockerfile.operator" "${REPO_ROOT}"

log "Building claude-code-runner image ${RUNNER_IMG}"
docker build -t "${RUNNER_IMG}" -f "${REPO_ROOT}/docker/Dockerfile.claude-code" "${REPO_ROOT}"

log "Creating Kind cluster '${CLUSTER_NAME}' (skips if already exists)"
if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  log "Cluster '${CLUSTER_NAME}' already exists — reusing"
else
  kind create cluster --name "${CLUSTER_NAME}" --wait 120s
fi

log "Loading images into Kind"
kind load docker-image "${OPERATOR_IMG}" --name "${CLUSTER_NAME}"
kind load docker-image "${RUNNER_IMG}" --name "${CLUSTER_NAME}"

# Pre-pull busybox for the test's verifier pod so specs don't pause on image pull.
log "Pre-pulling busybox for verifier pods"
docker pull busybox:latest
kind load docker-image busybox:latest --name "${CLUSTER_NAME}"

log "Generating CRD manifests"
cd "${REPO_ROOT}" && make manifests --no-print-directory

log "Installing CRDs"
kubectl apply -f "${REPO_ROOT}/config/crd/bases/"

# Same alias pattern as acceptance-setup.sh: the operator's default PVCs request
# StorageClass 'nfs', and a single-node Kind doesn't have one. Alias 'nfs' to
# local-path-provisioner so PVCs bind.
log "Creating 'nfs' StorageClass alias for Kind (backed by local-path-provisioner)"
kubectl apply -f - <<'YAML'
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: nfs
provisioner: rancher.io/local-path
reclaimPolicy: Delete
volumeBindingMode: WaitForFirstConsumer
YAML

log "Deploying RBAC"
kubectl apply -f "${REPO_ROOT}/config/rbac/"

log "Creating operator namespace"
kubectl apply -f "${REPO_ROOT}/config/manager/namespace.yaml"

log "Deploying operator (E2E mode: real runner image, no agent-command override)"
kubectl apply -f "${REPO_ROOT}/config/manager/manager.yaml"
kubectl set image deployment/controller-manager \
  manager="${OPERATOR_IMG}" \
  -n claude-teams-system

# Point the operator at the real runner image we just built. Because Kind is
# single-node we still need --pvc-access-mode=ReadWriteOnce so the default RWX
# request doesn't block PVC binding on local-path.
kubectl patch deployment controller-manager \
  -n claude-teams-system \
  --type=json \
  -p='[
    {"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--agent-image='"${RUNNER_IMG}"'"},
    {"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--pvc-access-mode=ReadWriteOnce"}
  ]'

log "Waiting for operator to be ready"
kubectl rollout status deployment/controller-manager \
  -n claude-teams-system \
  --timeout=120s

log ""
log "E2E environment is ready."
log "  Cluster   : ${CLUSTER_NAME}"
log "  Operator  : ${OPERATOR_IMG}"
log "  Runner    : ${RUNNER_IMG}"
log ""
log "Run E2E tests (reads ANTHROPIC_API_KEY from env):"
log "  make test-e2e"
log ""
log "Tear down when done:"
log "  make e2e-down"
