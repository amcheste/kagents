package harness

import (
	corev1 "k8s.io/api/core/v1"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// claudeCodeRunnerImage is the default runner image for the claude-code
// harness. Overridable via the operator's --agent-image flag.
const claudeCodeRunnerImage = "ghcr.io/amcheste/claude-code-runner:latest"

// ClaudeCode is the [Harness] implementation for Anthropic's native Claude
// Code Agent Teams protocol (file-based mailboxes at ~/.claude/teams/ +
// shared task list at ~/.claude/tasks/, activated by the experimental
// CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1 env var).
//
// This is the first (and, today, only) harness adapter. The seam exists so
// a second adapter — for a different team-based agent runtime — can plug
// in behind the same operator API, without needing changes to the
// reconciler, PVC layout, scheduling, or delivery paths.
type ClaudeCode struct{}

// Name implements [Harness].
func (ClaudeCode) Name() string { return DefaultHarness }

// DefaultImage implements [Harness].
func (ClaudeCode) DefaultImage() string { return claudeCodeRunnerImage }

// ProtocolEnv implements [Harness] by returning the Claude Code Agent Teams
// protocol-activation env vars. The CLAUDE_CODE_* names are dictated by the
// runner image's entrypoint; renaming them to a generic AGENT_* prefix would
// require coordinated changes to the runner image and is deferred (see the
// "thin seam" guardrail in the design doc).
func (ClaudeCode) ProtocolEnv(role AgentRole, agentName string, team *claudev1alpha1.AgentTeam) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS", Value: "1"},
		{Name: "CLAUDE_CODE_TEAM_NAME", Value: team.Name},
		{Name: "CLAUDE_CODE_AGENT_NAME", Value: agentName},
		{Name: "CLAUDE_CODE_ROLE", Value: string(role)},
	}
}

// Models implements [Harness]. The valid model identifiers for Claude Code,
// matching the existing CRD validation enum. Budget rate handling stays in
// the budget package for now; if a future harness needs different rates,
// the rate table moves out of the budget package and into the adapter.
func (ClaudeCode) Models() []string {
	return []string{"opus", "sonnet", "haiku"}
}

// DefaultRegistry returns the static map of known harnesses, keyed by
// spec.harness value. Consumed by cmd/manager when wiring the reconciler.
// Kept deliberately static — no plugin SPI, no dynamic discovery — until
// a real second harness exists.
func DefaultRegistry() map[string]Harness {
	return map[string]Harness{
		DefaultHarness: ClaudeCode{},
	}
}
