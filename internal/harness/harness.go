// Package harness is the seam between the kagents operator and the
// underlying agent-team runtime. The operator's core — PVC layout, pod
// lifecycle, RBAC, scheduling, output routing, delivery — is harness-neutral.
// Anything that depends on the agent runtime's coordination protocol (which
// env vars the runner needs, what image runs, which model identifiers are
// valid) lives behind an implementation of [Harness].
//
// Today there is exactly one implementation: [ClaudeCode], which adapts
// Anthropic's native Claude Code Agent Teams protocol. The interface
// intentionally stays thin and the registry intentionally stays static —
// no plugin SPI, no dynamic loading, no second adapter — until a real
// second harness exists.
package harness

import (
	corev1 "k8s.io/api/core/v1"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// AgentRole identifies which slot in a team a pod plays. Adapters may use
// this to vary pod decoration (lead vs. teammate).
type AgentRole string

const (
	RoleLead     AgentRole = "lead"
	RoleTeammate AgentRole = "teammate"
)

// DefaultHarness is the name used when an AgentTeam omits spec.harness.
const DefaultHarness = "claude-code"

// Harness is the operator's seam to a particular agent-team runtime.
// Implementations are stateless and safe for concurrent use.
type Harness interface {
	// Name returns the spec.harness enum value this adapter handles.
	Name() string

	// DefaultImage returns the default runner image for this harness.
	// The operator overrides this when --agent-image is set on the flag line.
	DefaultImage() string

	// ProtocolEnv returns the env vars that activate this harness's
	// coordination protocol on a runner pod (e.g. for Claude Code, the
	// CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1 flag plus the team / agent /
	// role identity vars its entrypoint reads). The operator merges these
	// with its own generic env vars (model, prompt, auth) when building
	// each agent pod.
	//
	// The interface intentionally stays narrow to the env-var
	// contribution rather than a broader PodSpec mutation hook —
	// expanding to volume mounts or command args is deferred until a
	// real second harness needs it (the YAGNI guardrail in the design doc).
	ProtocolEnv(role AgentRole, agentName string, team *claudev1alpha1.AgentTeam) []corev1.EnvVar

	// Models returns the set of valid model identifiers for this harness.
	// The operator surfaces this to CRD validation; the runner image
	// interprets the specific values.
	Models() []string
}
