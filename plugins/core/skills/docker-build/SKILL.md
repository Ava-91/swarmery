---
name: docker-build
description: "Use this skill when building or pushing Docker images for the project's services (the main app, the device service) to the cloud container registry. Don't use it for deploys (use deployment) or Dockerfile template editing without a build (use code-quality)."
version: "1.0.0"
owner: "swarmery-core"
docs:
  status: reviewed
  source_sha: bcd522a1fbe6
  updated: 2026-08-06
---

# Purpose

Build and push Docker images for the project's services to the cloud container registry (`.claude/project.json` → `cloud.provider`): multi-arch builds (ARM64 edge, AMD64 web), tagging conventions, registry authentication. Image creation only — rollout belongs to the project's infra pack skills. Placeholders `<mainApp>` and `<device>` come from project.json.

# Rules (never violate)

- Never pass `NEXT_PUBLIC_*` as `--build-arg` — client config comes through the `window.__ENV__` runtime bridge (12-factor).
- Registry URLs use `$IMAGE_REGISTRY` — never a hardcoded cloud project ID.
- Tags are a git short hash (dev) or semver (release) — never `latest`; flag base images pinned to `:latest`.
- A push writes to the shared registry immediately and irreversibly — confirm tag and target first; never push to production from a local machine (CI only).
- Stop and ask when tests have not passed for the code being built, or on a platform mismatch.
- Verify every push via the registry describe command; attach a confidence label (LOW if unverified).

# Resources

- Read `resources/build-procedure.md` when building — the procedure with commands, per-service build parameters, output template, and self-check.
- Read `resources/troubleshooting-and-examples.md` when diagnosing — DO-NOT patterns, escalation conditions, two full examples, failure modes.

# How to use

## What it does

Builds Docker images for your services and, when asked, pushes them to your cloud container registry. It handles what is easy to get wrong: multi-architecture builds through buildx, tag conventions, registry authentication, and the 12-factor rule keeping client config out of the image. It produces the image; rollout is somebody else's job.

## When to use it

- An image for the main app or a device/edge service, built from the current commit.
- A built image pushed to the registry and verified by digest.
- Setting up buildx for ARM64 edge builds, or diagnosing a build failure.

Not for rollouts or config templates (the project's infra pack skills), Dockerfile review without a build (`code-quality`), or production pushes from a local machine (escalate).

## How to invoke

```
Skill(skill: "core:docker-build")
```

Say which `service` to build, the `tag`, and whether to `push` (default false); `IMAGE_REGISTRY` must be set in the environment.

## Worked example

```
Skill(skill: "core:docker-build")
Build and push the device service image for the current commit.
```

The skill authenticates against the registry, brings up a multi-arch buildx builder, builds `linux/arm64` from `Dockerfile.optimized`, pushes with the git short hash as tag, then confirms the image exists — returning a build result block with image, platform, digest, and `Confidence: HIGH -- push verified`.

## Related

- The project's infra pack skills — rollout; `code-standards` — the build-once/deploy-anywhere rule; `gcp-cicd-auth` — pipeline auth instead of local login.
