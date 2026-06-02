# Authoring and Publishing Skills

Skills are the unit of expertise a Claude Code agent loads at startup.
They're a directory mounted at `~/.claude/skills/{name}/` containing
instructions, examples, and templates. kagents supports two
distribution mechanisms:

- **ConfigMap** — skill files live inside the cluster as a ConfigMap.
  Simplest, no network. Good for skills authored alongside the team CR.
- **OCI** — skill files are packaged as an OCI artifact in a registry
  (ghcr.io, ECR, GCR, internal Harbor, etc.) and pulled by an init
  container at pod startup. Versioned, signed, shared across clusters.

This page covers the OCI path. ConfigMap skills are just key/value
files — see the API reference for `SkillSource.configMap`.

## Anatomy of a skill

A skill is a directory. Anthropic's Claude Code reads `SKILL.md` at
load time and treats anything else in the directory as supplementary
material it can reference.

```
my-skill/
├── SKILL.md           # required: the skill's instructions
├── examples/          # optional: example inputs and expected outputs
│   ├── input-1.md
│   └── output-1.md
└── templates/         # optional: output formats the agent fills in
    └── report.tmpl
```

`SKILL.md` is markdown. It typically describes what the skill does,
when the agent should reach for it, and any conventions the agent
should follow.

## Publishing as an OCI artifact

[ORAS](https://oras.land) is the CLI that pushes arbitrary directories
to an OCI-compatible registry. The kagents operator uses the same tool
to pull them back at pod startup.

```bash
# From the skill's directory:
oras push ghcr.io/myorg/skills/my-skill:v1 \
  --artifact-type application/vnd.kagents.skill.v1 \
  .
```

A few notes on the command above:

- `--artifact-type` is conventional. The operator does not enforce it
  today; future versions may use it to validate that an OCI reference
  points at a skill rather than a random container image.
- Tag immutably (`:v1`, `:v1.0.3`, or `@sha256:...`) — `:latest`
  works but means every pod re-pulls in case the content moved.
- Pin to a digest (`@sha256:...`) in production for byte-for-byte
  reproducibility and to let the registry short-circuit identical
  pulls cheaply.

## Referencing a skill from an AgentTeam

```yaml
teammates:
  - name: researcher
    skills:
      - name: my-skill
        source:
          oci: ghcr.io/myorg/skills/my-skill:v1
```

The operator pulls `ghcr.io/myorg/skills/my-skill:v1` into a per-skill
emptyDir, mounts it at `/var/claude-skills/my-skill/`, and the runner
entrypoint copies it to `~/.claude/skills/my-skill/` before launching
Claude Code.

## Private registries

For skills in a private registry, point the team at a
`kubernetes.io/dockerconfigjson` Secret:

```bash
kubectl create secret docker-registry ghcr-creds \
  --docker-server=ghcr.io \
  --docker-username=$GHCR_USER \
  --docker-password=$GHCR_PAT
```

```yaml
spec:
  imagePullSecrets:
    - name: ghcr-creds
  teammates:
    - name: researcher
      skills:
        - name: internal-skill
          source:
            oci: ghcr.io/internal/skills/private:v1
```

The operator does two things with the secret:

1. **Propagates it to the pod** so the kubelet can pull the runner
   image and the ORAS puller image from a private registry.
2. **Mounts its `.dockerconfigjson` into the puller init container**
   so `oras pull` sees credentials via `$DOCKER_CONFIG`.

Multi-registry deployments: combine all credentials into a single
dockerconfigjson Secret. The operator only mounts the first listed
secret into the init container, so consolidating keeps things
predictable.

## Re-pull semantics

The skill-puller init container runs once per pod start. There is no
shared cache between pods. Two practical consequences:

- **Pin to digests** in production. The registry can cheaply skip
  identical content; mutable tags force a fresh pull each time.
- **Skill artifacts should be small**. They're text + examples; if
  yours is hundreds of MB, something is off — most skills measure in
  kilobytes.

## Air-gapped clusters

The default puller image is `ghcr.io/oras-project/oras:v1.2.0`. In
air-gapped or registry-restricted environments, mirror it to an
internal registry and tell the operator via the
`--skill-puller-image` flag:

```yaml
# Helm values.yaml
manager:
  skillPullerImage: registry.internal/oras:v1.2.0
```

## Skill content patterns

Some patterns we've found useful:

- **State the agent's persona up front.** "You are a financial
  analyst." Clear framing improves output quality.
- **Show, don't just tell.** A worked example in `examples/` is worth
  ten paragraphs of "you should follow this structure."
- **Templates beat prose for output shape.** If the agent should
  produce a structured artifact, ship a `templates/report.tmpl` with
  placeholders and reference it from `SKILL.md`.

See `config/samples/oci-skills-team.yaml` for a working team that uses
both public and private OCI skills.
