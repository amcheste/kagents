#!/usr/bin/env bash
# hack/generate-install-yaml.sh
#
# Produces a single `install.yaml` that `kubectl apply -f` can consume end-to-
# end to deploy the operator. Bundles:
#   1. The claude-teams-system namespace
#   2. All CRDs (AgentTeam, AgentTeamTemplate, AgentTeamRun)
#   3. RBAC (ClusterRole + ClusterRoleBinding)
#   4. The operator Deployment, with image + --agent-image pinned to $TAG
#
# Used by .github/workflows/release.yml to attach install.yaml to each GitHub
# Release so users can `kubectl apply -f <release-url>/install.yaml` without
# needing Helm.
#
# Usage:
#   TAG=v0.2.0 hack/generate-install-yaml.sh > install.yaml
#
# TAG is required — unpinned manifests would pull :latest and defeat the
# purpose of a release artifact.

set -euo pipefail

if [[ -z "${TAG:-}" ]]; then
  echo "ERROR: TAG must be set (e.g. TAG=v0.2.0)" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

cat "${REPO_ROOT}/config/manager/namespace.yaml"
echo "---"

for f in "${REPO_ROOT}"/config/crd/bases/*.yaml; do
  cat "$f"
  echo "---"
done

for f in "${REPO_ROOT}"/config/rbac/*.yaml; do
  cat "$f"
  echo "---"
done

# Pin the operator image to the release tag, and add --agent-image so agent
# pods use the matching runner version (the controller's hardcoded default is
# :latest, which would drift from the release). awk keeps this portable across
# GNU/BSD sed differences — this script runs in both Ubuntu CI and local dev.
awk -v tag="${TAG}" '
  /ghcr.io\/amcheste\/kagents:latest/ {
    sub(/:latest/, ":" tag)
  }
  /- --metrics-bind-address=:8080/ {
    print
    match($0, /^[[:space:]]*/)
    indent = substr($0, 1, RLENGTH)
    print indent "- --agent-image=ghcr.io/amcheste/claude-code-runner:" tag
    next
  }
  { print }
' "${REPO_ROOT}/config/manager/manager.yaml"
