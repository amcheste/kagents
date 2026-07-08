package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

func TestAgentServiceAccountName_MatchesPodName(t *testing.T) {
	// Sharing a name lets `kubectl get sa,pod -l team=X` pair them visually
	// and keeps cleanup logic simple (same label selector, same name).
	team := minimalTeam("sa-name")
	assert.Equal(t, "sa-name-lead", agentServiceAccountName(team, "lead"))
	assert.Equal(t, agentPodName(team, "worker"), agentServiceAccountName(team, "worker"))
}

func TestTeamPVCNames_BaselineOnly(t *testing.T) {
	team := minimalTeam("pvcs")
	assert.Equal(t, []string{"pvcs-team-state"}, teamPVCNames(team))
}

func TestTeamPVCNames_CodingModeIncludesRepo(t *testing.T) {
	team := withRepo(minimalTeam("pvcs-coding"))
	assert.Equal(t, []string{"pvcs-coding-team-state", "pvcs-coding-repo"}, teamPVCNames(team))
}

func TestTeamPVCNames_CoworkModeIncludesOutput(t *testing.T) {
	team := withWorkspace(minimalTeam("pvcs-cowork"))
	assert.Equal(t, []string{"pvcs-cowork-team-state", "pvcs-cowork-output"}, teamPVCNames(team))
}

func TestAgentPolicyRules_APIKeySecret(t *testing.T) {
	team := minimalTeam("policy")
	rules := agentPolicyRules(team)

	// Expect exactly one Secret rule with resourceNames restricted to the
	// team's apiKeySecret (my-secret per minimalTeam) and verbs=[get].
	var secretRule *rbacv1.PolicyRule
	for i, rule := range rules {
		for _, res := range rule.Resources {
			if res == "secrets" {
				secretRule = &rules[i]
			}
		}
	}
	require.NotNil(t, secretRule, "expected a secrets rule")
	assert.Equal(t, []string{"my-secret"}, secretRule.ResourceNames,
		"secret rule must restrict to the team's apiKeySecret name")
	assert.Equal(t, []string{"get"}, secretRule.Verbs)
}

func TestAgentPolicyRules_OmitsSecretsWhenAuthUnset(t *testing.T) {
	// An OAuth-less team with a cleared apiKeySecret has no secret rule —
	// the operator must not grant blanket secret access.
	team := minimalTeam("no-auth")
	team.Spec.Auth.APIKeySecret = ""
	rules := agentPolicyRules(team)
	for _, rule := range rules {
		for _, res := range rule.Resources {
			assert.NotEqual(t, "secrets", res, "no secret rule when auth is unset")
		}
	}
}

func TestAgentPolicyRules_PVCsScopedToTeamOnly(t *testing.T) {
	team := withRepo(minimalTeam("pvcs-scope"))
	rules := agentPolicyRules(team)
	var pvcRule *rbacv1.PolicyRule
	for i, rule := range rules {
		for _, res := range rule.Resources {
			if res == "persistentvolumeclaims" {
				pvcRule = &rules[i]
			}
		}
	}
	require.NotNil(t, pvcRule)
	assert.ElementsMatch(t,
		[]string{"pvcs-scope-team-state", "pvcs-scope-repo"},
		pvcRule.ResourceNames,
		"PVC rule must list only the team's own PVCs")
}

func TestEnsureAgentServiceAccount_CreatesSARoleAndBinding(t *testing.T) {
	team := withRepo(minimalTeam("rbac-create"))
	r := newReconciler(team)
	team = fetch(t, r, "rbac-create")
	ctx := context.Background()

	require.NoError(t, r.ensureAgentServiceAccount(ctx, team, "lead"))

	name := "rbac-create-lead"

	var sa corev1.ServiceAccount
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, &sa))

	var role rbacv1.Role
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, &role))
	// Owner reference back to the team so cascading delete cleans up the Role.
	require.Len(t, role.OwnerReferences, 1)
	assert.Equal(t, "rbac-create", role.OwnerReferences[0].Name)

	var rb rbacv1.RoleBinding
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, &rb))
	require.Len(t, rb.Subjects, 1)
	assert.Equal(t, "ServiceAccount", rb.Subjects[0].Kind)
	assert.Equal(t, name, rb.Subjects[0].Name)
	assert.Equal(t, "Role", rb.RoleRef.Kind)
	assert.Equal(t, name, rb.RoleRef.Name)
}

func TestEnsureAgentServiceAccount_Idempotent(t *testing.T) {
	team := minimalTeam("rbac-idem")
	r := newReconciler(team)
	team = fetch(t, r, "rbac-idem")
	ctx := context.Background()

	require.NoError(t, r.ensureAgentServiceAccount(ctx, team, "lead"))
	require.NoError(t, r.ensureAgentServiceAccount(ctx, team, "lead"),
		"second call must not error on pre-existing SA/Role/RoleBinding")
}

func TestEnsureAgentServiceAccount_UpdatesRoleWhenSecretChanges(t *testing.T) {
	// If the team rotates its apiKeySecret name, the Role must follow —
	// otherwise the pod would be granted access to a stale secret.
	team := minimalTeam("rbac-rotate")
	r := newReconciler(team)
	team = fetch(t, r, "rbac-rotate")
	ctx := context.Background()

	require.NoError(t, r.ensureAgentServiceAccount(ctx, team, "lead"))

	var role rbacv1.Role
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "rbac-rotate-lead", Namespace: "default"}, &role))
	// Sanity: original secret name is my-secret (minimalTeam default).
	var secretRule *rbacv1.PolicyRule
	for i, rule := range role.Rules {
		for _, res := range rule.Resources {
			if res == "secrets" {
				secretRule = &role.Rules[i]
			}
		}
	}
	require.NotNil(t, secretRule)
	assert.Equal(t, []string{"my-secret"}, secretRule.ResourceNames)

	// Rotate the secret and re-run.
	team.Spec.Auth.APIKeySecret = "rotated-secret"
	require.NoError(t, r.Update(ctx, team))
	team = fetch(t, r, "rbac-rotate")
	require.NoError(t, r.ensureAgentServiceAccount(ctx, team, "lead"))

	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "rbac-rotate-lead", Namespace: "default"}, &role))
	secretRule = nil
	for i, rule := range role.Rules {
		for _, res := range rule.Resources {
			if res == "secrets" {
				secretRule = &role.Rules[i]
			}
		}
	}
	require.NotNil(t, secretRule)
	assert.Equal(t, []string{"rotated-secret"}, secretRule.ResourceNames)
}

func TestEnsureAgentPod_SetsServiceAccountName(t *testing.T) {
	// End-to-end: after ensureAgentPod creates a pod, its spec points at the
	// per-agent ServiceAccount.
	team := minimalTeam("sa-pod")
	r := newReconciler(team)
	team = fetch(t, r, "sa-pod")
	ctx := context.Background()

	require.NoError(t, r.ensureAgentPod(ctx, team, "worker", "sonnet", "work",
		"auto-accept", false, corev1.ResourceRequirements{}, nil, nil, nil, nil))

	var pod corev1.Pod
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "sa-pod-worker", Namespace: "default"}, &pod))
	assert.Equal(t, "sa-pod-worker", pod.Spec.ServiceAccountName)

	// And the SA actually exists.
	var sa corev1.ServiceAccount
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "sa-pod-worker", Namespace: "default"}, &sa))
}

func TestEnsureAgentPod_CreatesDistinctSAPerAgent(t *testing.T) {
	// Each teammate gets its own SA — the whole point of the feature.
	team := minimalTeam("distinct-sa")
	r := newReconciler(team)
	team = fetch(t, r, "distinct-sa")
	ctx := context.Background()

	require.NoError(t, r.ensureAgentPod(ctx, team, "lead", "opus", "lead", "auto-accept", true, corev1.ResourceRequirements{}, nil, nil, nil, nil))
	require.NoError(t, r.ensureAgentPod(ctx, team, "worker", "sonnet", "work", "auto-accept", false, corev1.ResourceRequirements{}, nil, nil, nil, nil))

	var sa1, sa2 corev1.ServiceAccount
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "distinct-sa-lead", Namespace: "default"}, &sa1))
	require.NoError(t, r.Get(ctx, types.NamespacedName{Name: "distinct-sa-worker", Namespace: "default"}, &sa2))
	assert.NotEqual(t, sa1.Name, sa2.Name)
}

func TestEnsureAgentServiceAccount_OwnerRefSetForCascadingDelete(t *testing.T) {
	// Owner references back to the team so cascading delete cleans up every
	// per-agent RBAC object when the AgentTeam is deleted.
	team := minimalTeam("gc-rbac")
	r := newReconciler(team)
	team = fetch(t, r, "gc-rbac")
	ctx := context.Background()
	require.NoError(t, r.ensureAgentServiceAccount(ctx, team, "lead"))

	name := types.NamespacedName{Name: "gc-rbac-lead", Namespace: "default"}

	var sa corev1.ServiceAccount
	require.NoError(t, r.Get(ctx, name, &sa))
	require.Len(t, sa.OwnerReferences, 1)
	assert.Equal(t, "gc-rbac", sa.OwnerReferences[0].Name)

	var role rbacv1.Role
	require.NoError(t, r.Get(ctx, name, &role))
	require.Len(t, role.OwnerReferences, 1)
	assert.Equal(t, "gc-rbac", role.OwnerReferences[0].Name)

	var rb rbacv1.RoleBinding
	require.NoError(t, r.Get(ctx, name, &rb))
	require.Len(t, rb.OwnerReferences, 1)
	assert.Equal(t, "gc-rbac", rb.OwnerReferences[0].Name)
}

var _ = errors.IsNotFound
