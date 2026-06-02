package controller

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// findInitContainer returns the first init container whose name starts
// with the given prefix, or nil. The init-container set on a Pod is
// order-dependent (k8s runs them sequentially) but tests should index
// by name rather than position so future ordering changes don't break
// every assertion.
func findInitContainer(pod *corev1.Pod, prefix string) *corev1.Container {
	for i := range pod.Spec.InitContainers {
		if strings.HasPrefix(pod.Spec.InitContainers[i].Name, prefix) {
			return &pod.Spec.InitContainers[i]
		}
	}
	return nil
}

// findVolume returns the first Volume by name, or nil.
func findVolume(pod *corev1.Pod, name string) *corev1.Volume {
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == name {
			return &pod.Spec.Volumes[i]
		}
	}
	return nil
}

func TestBuildAgentPod_OCISkill_ProducesInitContainerAndEmptyDir(t *testing.T) {
	t.Parallel()
	team := minimalTeam("oci-skills")
	r := newReconciler(team)
	skills := []claudev1alpha1.SkillSpec{
		{Name: "web-research", Source: claudev1alpha1.SkillSource{OCI: "ghcr.io/acme/skills/web-research:v1"}},
	}

	pod := r.buildAgentPod(team, "worker", "sonnet", "do work", "auto-accept", false,
		corev1.ResourceRequirements{}, nil, skills, nil, nil)

	// EmptyDir holds the pulled artifact between the init container and
	// the main container — read-only on the main side, RW on init.
	v := findVolume(pod, "skill-web-research")
	require.NotNil(t, v, "skill-web-research volume must exist")
	assert.NotNil(t, v.EmptyDir, "OCI skill volume must be emptyDir")
	assert.Nil(t, v.ConfigMap, "OCI skill volume must not be a ConfigMap projection")

	// Init container runs `oras pull` into /skill-out, which is the
	// pod-side view of the emptyDir.
	ic := findInitContainer(pod, "pull-skill-web-research")
	require.NotNil(t, ic, "pull-skill init container must exist")
	assert.Equal(t, defaultSkillPullerImage, ic.Image)
	assert.Contains(t, ic.Command, "oras")
	assert.Contains(t, ic.Command, "ghcr.io/acme/skills/web-research:v1")

	// No registry creds configured → no DOCKER_CONFIG and no auth volume.
	for _, e := range ic.Env {
		assert.NotEqual(t, "DOCKER_CONFIG", e.Name, "no auth secret = no DOCKER_CONFIG")
	}
	assert.Nil(t, findVolume(pod, "skill-auth"), "no auth secret = no skill-auth volume")
}

func TestBuildAgentPod_OCISkill_PrivateRegistryWiresAuth(t *testing.T) {
	t.Parallel()
	team := minimalTeam("oci-private")
	team.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "ghcr-creds"}}
	r := newReconciler(team)
	skills := []claudev1alpha1.SkillSpec{
		{Name: "internal", Source: claudev1alpha1.SkillSource{OCI: "ghcr.io/acme-internal/skills/secret:v3"}},
	}

	pod := r.buildAgentPod(team, "worker", "sonnet", "do work", "auto-accept", false,
		corev1.ResourceRequirements{}, nil, skills, nil, nil)

	// Pod-level imagePullSecrets propagated so the kubelet can pull
	// both the puller image and any private agent images.
	require.Len(t, pod.Spec.ImagePullSecrets, 1)
	assert.Equal(t, "ghcr-creds", pod.Spec.ImagePullSecrets[0].Name)

	// Auth volume projects .dockerconfigjson → config.json.
	authVol := findVolume(pod, "skill-auth")
	require.NotNil(t, authVol)
	require.NotNil(t, authVol.Secret)
	assert.Equal(t, "ghcr-creds", authVol.Secret.SecretName)
	require.Len(t, authVol.Secret.Items, 1)
	assert.Equal(t, ".dockerconfigjson", authVol.Secret.Items[0].Key)
	assert.Equal(t, "config.json", authVol.Secret.Items[0].Path)

	// Init container picks up DOCKER_CONFIG so ORAS finds creds.
	ic := findInitContainer(pod, "pull-skill-internal")
	require.NotNil(t, ic)
	var dockerConfig string
	for _, e := range ic.Env {
		if e.Name == "DOCKER_CONFIG" {
			dockerConfig = e.Value
		}
	}
	assert.Equal(t, "/auth/.docker", dockerConfig)
	var mountedAuth bool
	for _, m := range ic.VolumeMounts {
		if m.Name == "skill-auth" {
			mountedAuth = true
			assert.Equal(t, "/auth/.docker", m.MountPath)
			assert.True(t, m.ReadOnly)
		}
	}
	assert.True(t, mountedAuth, "skill-auth must be mounted into the puller init container")
}

func TestBuildAgentPod_ConfigMapSkill_StillProjectsDirectly(t *testing.T) {
	t.Parallel()
	team := minimalTeam("cm-skills")
	r := newReconciler(team)
	skills := []claudev1alpha1.SkillSpec{
		{Name: "report-writing", Source: claudev1alpha1.SkillSource{ConfigMap: "report-skill"}},
	}

	pod := r.buildAgentPod(team, "worker", "sonnet", "do work", "auto-accept", false,
		corev1.ResourceRequirements{}, nil, skills, nil, nil)

	v := findVolume(pod, "skill-report-writing")
	require.NotNil(t, v)
	require.NotNil(t, v.ConfigMap, "ConfigMap skill must use a ConfigMap volume")
	assert.Equal(t, "report-skill", v.ConfigMap.Name)

	assert.Nil(t, findInitContainer(pod, "pull-skill-"), "ConfigMap skills should not produce a puller init container")
}

func TestBuildAgentPod_MixedSkills(t *testing.T) {
	t.Parallel()
	team := minimalTeam("mixed-skills")
	r := newReconciler(team)
	skills := []claudev1alpha1.SkillSpec{
		{Name: "config-skill", Source: claudev1alpha1.SkillSource{ConfigMap: "cm"}},
		{Name: "oci-skill", Source: claudev1alpha1.SkillSource{OCI: "ghcr.io/x/y:1"}},
	}

	pod := r.buildAgentPod(team, "worker", "sonnet", "do work", "auto-accept", false,
		corev1.ResourceRequirements{}, nil, skills, nil, nil)

	require.NotNil(t, findVolume(pod, "skill-config-skill"))
	require.NotNil(t, findVolume(pod, "skill-oci-skill"))
	require.NotNil(t, findInitContainer(pod, "pull-skill-oci-skill"))
	assert.Nil(t, findInitContainer(pod, "pull-skill-config-skill"))
}

func TestBuildAgentPod_OCISkill_PullerImageOverride(t *testing.T) {
	t.Parallel()
	team := minimalTeam("override-image")
	r := newReconciler(team)
	r.SkillPullerImage = "my-mirror.example.com/oras:v1.2.0"
	skills := []claudev1alpha1.SkillSpec{
		{Name: "x", Source: claudev1alpha1.SkillSource{OCI: "ghcr.io/x/x:1"}},
	}
	pod := r.buildAgentPod(team, "worker", "sonnet", "do work", "auto-accept", false,
		corev1.ResourceRequirements{}, nil, skills, nil, nil)
	ic := findInitContainer(pod, "pull-skill-x")
	require.NotNil(t, ic)
	assert.Equal(t, "my-mirror.example.com/oras:v1.2.0", ic.Image)
}
