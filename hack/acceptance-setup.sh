#!/usr/bin/env bash
# hack/acceptance-setup.sh
#
# Creates a Kind cluster, builds the operator image, loads it, installs CRDs,
# and deploys the operator configured for acceptance testing:
#   --agent-image=busybox:latest      (no API key needed; containers exit 0)
#   --skip-init-script                (no real git clone; init Job exits 0)
#
# Usage:
#   bash hack/acceptance-setup.sh
#
# Requires: kind, kubectl, docker, go (for make manifests)

set -euo pipefail

# Extend PATH to pick up Docker Desktop and Homebrew binaries.
export PATH="/opt/homebrew/bin:/usr/local/bin:/Applications/Docker.app/Contents/Resources/bin:${PATH}"

CLUSTER_NAME="${KIND_CLUSTER_NAME:-claude-teams}"
IMG="${IMG:-ghcr.io/amcheste/kagents:acceptance}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

log() { echo "==> $*"; }

log "Building operator image ${IMG}"
docker build -t "${IMG}" -f "${REPO_ROOT}/docker/Dockerfile.operator" "${REPO_ROOT}"

log "Creating Kind cluster '${CLUSTER_NAME}' (skips if already exists)"
if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  log "Cluster '${CLUSTER_NAME}' already exists — reusing"
else
  kind create cluster --name "${CLUSTER_NAME}" --wait 120s
fi

log "Loading operator image into Kind"
kind load docker-image "${IMG}" --name "${CLUSTER_NAME}"

log "Generating CRD manifests"
cd "${REPO_ROOT}" && make manifests --no-print-directory

log "Installing CRDs"
kubectl apply -f "${REPO_ROOT}/config/crd/bases/"

# Kind uses local-path-provisioner which only exposes a 'standard' StorageClass.
# Create an alias StorageClass named 'nfs' that uses the same provisioner so that
# the operator's default team-state and repo PVCs (which request 'nfs') can bind.
# On a single-node Kind cluster every pod shares one node, so hostPath volumes
# are mountable by multiple pods simultaneously — the RWX behaviour we need.
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

log "Deploying operator (acceptance mode: busybox agent image + skip-init-script)"
# Build a patched Deployment manifest with the acceptance flags.
kubectl apply -f "${REPO_ROOT}/config/manager/manager.yaml"
kubectl set image deployment/controller-manager \
  manager="${IMG}" \
  -n claude-teams-system

kubectl patch deployment controller-manager \
  -n claude-teams-system \
  --type=json \
  -p='[
    {"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--agent-image=busybox:latest"},
    {"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--init-image=busybox:latest"},
    {"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--skip-init-script"},
    {"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--pvc-access-mode=ReadWriteOnce"},
    {"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--agent-command=sh,-c,sleep 20 && exit 0"}
  ]'

log "Waiting for operator to be ready"
kubectl rollout status deployment/controller-manager \
  -n claude-teams-system \
  --timeout=120s

log ""
log "Acceptance environment is ready."
log "  Cluster : ${CLUSTER_NAME}"
log "  Image   : ${IMG}"
log "  Flags   : --agent-image=busybox:latest --skip-init-script"
log ""
log "Run acceptance tests:"
log "  make test-acceptance"
log ""
log "Tear down when done:"
log "  make acceptance-down"
