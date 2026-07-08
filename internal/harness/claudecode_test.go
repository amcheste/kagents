package harness

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

func TestClaudeCode_Name(t *testing.T) {
	t.Parallel()
	assert.Equal(t, DefaultHarness, ClaudeCode{}.Name())
	assert.Equal(t, "claude-code", ClaudeCode{}.Name())
}

func TestClaudeCode_DefaultImage(t *testing.T) {
	t.Parallel()
	img := ClaudeCode{}.DefaultImage()
	// The exact tag is intentionally pinned to :latest today; the assertion
	// is that the image lives under the project's ghcr.io org and is named
	// claude-code-runner.
	assert.Contains(t, img, "ghcr.io/amcheste/claude-code-runner")
}

func TestClaudeCode_Models(t *testing.T) {
	t.Parallel()
	models := ClaudeCode{}.Models()
	// Order is part of the documented API since the slice flows through to
	// CRD validation enums; assert exactly.
	assert.Equal(t, []string{"opus", "sonnet", "haiku"}, models)
}

func TestClaudeCode_ProtocolEnv_TeammateRole(t *testing.T) {
	t.Parallel()

	team := &claudev1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "ns"},
	}

	env := ClaudeCode{}.ProtocolEnv(RoleTeammate, "worker-1", team)

	require.Len(t, env, 4, "expects the four CLAUDE_CODE_* protocol env vars")
	assert.Equal(t, "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS", env[0].Name)
	assert.Equal(t, "1", env[0].Value)
	assert.Equal(t, "CLAUDE_CODE_TEAM_NAME", env[1].Name)
	assert.Equal(t, "alpha", env[1].Value)
	assert.Equal(t, "CLAUDE_CODE_AGENT_NAME", env[2].Name)
	assert.Equal(t, "worker-1", env[2].Value)
	assert.Equal(t, "CLAUDE_CODE_ROLE", env[3].Name)
	assert.Equal(t, "teammate", env[3].Value)
}

func TestClaudeCode_ProtocolEnv_LeadRole(t *testing.T) {
	t.Parallel()

	team := &claudev1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "ns"},
	}

	env := ClaudeCode{}.ProtocolEnv(RoleLead, "lead", team)

	// CLAUDE_CODE_ROLE should carry the lead role verbatim.
	var roleEnv *corev1.EnvVar
	for i := range env {
		if env[i].Name == "CLAUDE_CODE_ROLE" {
			roleEnv = &env[i]
		}
	}
	require.NotNil(t, roleEnv)
	assert.Equal(t, "lead", roleEnv.Value)
}

func TestDefaultRegistry_ContainsClaudeCode(t *testing.T) {
	t.Parallel()
	reg := DefaultRegistry()
	h, ok := reg[DefaultHarness]
	require.True(t, ok, "registry should contain claude-code by default")
	_, isClaudeCode := h.(ClaudeCode)
	assert.True(t, isClaudeCode, "default harness should be ClaudeCode")
}
