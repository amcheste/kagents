#!/bin/bash
set -euo pipefail

# entrypoint.sh — Starts Claude Code as a team lead or teammate inside a K8s pod.

echo "[kagents] Starting Claude Code agent"
echo "  Team:    ${CLAUDE_CODE_TEAM_NAME:-unset}"
echo "  Agent:   ${CLAUDE_CODE_AGENT_NAME:-unset}"
echo "  Model:   ${CLAUDE_MODEL:-sonnet}"
echo "  Role:    ${CLAUDE_CODE_ROLE:-teammate}"

# Validate required env vars
if [[ -z "${ANTHROPIC_API_KEY:-}" && -z "${CLAUDE_OAUTH_TOKEN:-}" ]]; then
    echo "[ERROR] Neither ANTHROPIC_API_KEY nor CLAUDE_OAUTH_TOKEN is set."
    exit 1
fi

if [[ -z "${AGENT_PROMPT:-}" ]]; then
    echo "[ERROR] AGENT_PROMPT is not set."
    exit 1
fi

# --- Set up ~/.claude/ ---
# The team-state PVC is mounted at /var/claude-state and holds the shared
# teams/ and tasks/ directories used by Agent Teams coordination protocol.
# We symlink those into ~/.claude/ so Claude Code finds them at the expected paths.
mkdir -p ~/.claude
mkdir -p /var/claude-state/teams /var/claude-state/tasks

ln -sfn /var/claude-state/teams ~/.claude/teams
ln -sfn /var/claude-state/tasks ~/.claude/tasks

# --- Install Skills ---
# Skills ConfigMaps are mounted at /var/claude-skills/{name}/.
# Copy them into ~/.claude/skills/{name}/ so Claude Code can load them.
if [[ -d /var/claude-skills ]]; then
    for skill_dir in /var/claude-skills/*/; do
        [[ -d "$skill_dir" ]] || continue
        skill_name=$(basename "$skill_dir")
        dest=~/.claude/skills/"$skill_name"
        mkdir -p "$dest"
        cp -r "$skill_dir"/. "$dest"/
        echo "[kagents] Installed skill: $skill_name"
    done
fi

# --- Install MCP Config ---
# The per-agent MCP ConfigMap is mounted at /var/claude-mcp/mcp.json.
# Copy it to ~/.mcp.json so Claude Code picks up the MCP server configuration.
if [[ -f /var/claude-mcp/mcp.json ]]; then
    cp /var/claude-mcp/mcp.json ~/.mcp.json
    echo "[kagents] Installed MCP config"
fi

# --- Resolve model flag ---
MODEL_FLAG=""
case "${CLAUDE_MODEL:-sonnet}" in
    opus)   MODEL_FLAG="--model opus" ;;
    sonnet) MODEL_FLAG="--model sonnet" ;;
    *)      MODEL_FLAG="--model ${CLAUDE_MODEL}" ;;
esac

# --- Resolve permission mode ---
PERMISSION_FLAG=""
case "${CLAUDE_PERMISSION_MODE:-auto-accept}" in
    auto-accept) PERMISSION_FLAG="--dangerously-skip-permissions" ;;
    plan)        PERMISSION_FLAG="--permission-mode plan" ;;
    default)     PERMISSION_FLAG="" ;;
esac

# --- Navigate to workspace ---
if [[ -n "${WORKTREE_PATH:-}" ]]; then
    cd "/workspace/${WORKTREE_PATH}"
elif [[ -d /workspace ]]; then
    cd /workspace
fi

# Log context
if git log --oneline -3 2>/dev/null; then
    echo ""
else
    echo "[kagents] (no git repo in workspace)"
fi

# --- Launch Claude Code ---
echo "[kagents] Launching Claude Code..."
exec claude ${MODEL_FLAG} ${PERMISSION_FLAG} \
    --print \
    -p "${AGENT_PROMPT}"
