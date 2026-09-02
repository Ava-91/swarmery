# docker-build — mistakes, escalation, examples, and failure modes

## Common mistakes to avoid (DO NOT patterns)

- DO NOT pass `NEXT_PUBLIC_*` as `--build-arg` — this bakes environment-specific values into the image at `next build` time, making the image usable only in one environment. Use the `window.__ENV__` runtime bridge pattern instead: server injects `<script>window.__ENV__={...}</script>`, client reads from `window.__ENV__`
- DO NOT hardcode the cloud project ID in registry URLs — use the `$IMAGE_REGISTRY` environment variable. The project ID may differ between contexts (e.g., a shared dev project vs. a numbered production project)
- DO NOT use the `latest` tag — use git short hash for development or semver for releases
- DO NOT push to the shared registry without confirming the tag and target image — pushes are visible to all environments
- DO NOT push to the production registry from a local dev machine — use CI/CD pipeline
- DO NOT forget to set up buildx for cross-platform builds — `docker build` alone does not support `--platform`
- DO NOT use `gcloud auth login` in CI pipelines — CI uses Workload Identity Federation (see `gcp-cicd-auth` skill). The `gcloud auth configure-docker` command in the build procedure is for local dev auth only

## Escalation (stop-and-ask conditions)

- Stop and ask when: operator wants to push to the production registry from a local machine
- Stop and ask when: build fails with a platform mismatch (e.g., trying to build ARM64 on x86 without QEMU)
- Stop and ask when: `NEXT_PUBLIC_*` build-arg is requested (explain the 12-factor violation and suggest the runtime bridge)
- Stop and ask when: tests have not passed for the code being built
- Stop and ask when: base image in Dockerfile uses `:latest` tag — flag for pinning

## Example: build and push the device-service image after a telemetry fix

```bash
# Step 1: Authenticate (local dev only -- CI uses WIF)
gcloud auth configure-docker <region>-docker.pkg.dev

# Step 2: Set up buildx
docker buildx create --name multiarch-builder --use 2>/dev/null || docker buildx use multiarch-builder
docker buildx inspect --bootstrap

# Step 3: Build and push
cd <device>
COMMIT_HASH=$(git rev-parse --short HEAD)  # e.g., a1b2c3d

docker buildx build \
  --platform linux/arm64 \
  -t $IMAGE_REGISTRY/<device-image>:$COMMIT_HASH \
  -f Dockerfile.optimized . --push

# Step 4: Verify
gcloud artifacts docker images describe \
  $IMAGE_REGISTRY/<device-image>:$COMMIT_HASH
# Image: <device-image>:a1b2c3d
# Digest: sha256:abc123...
# Platform: linux/arm64
```

Output:
```
## Build Result
Build: <device>
Image: $IMAGE_REGISTRY/<device-image>:a1b2c3d
Platform: linux/arm64
Pushed: yes
Digest: sha256:abc123def456...

### Confidence: HIGH -- push verified via gcloud artifacts describe
```

## Example: build the main-app image (no push, local testing)

```bash
cd apps/<mainApp>
COMMIT_HASH=$(git rev-parse --short HEAD)

# No --build-arg for NEXT_PUBLIC_* variables.
# Client config is injected at runtime via window.__ENV__ bridge.
docker buildx build \
  --platform linux/amd64 \
  -t $IMAGE_REGISTRY/<mainApp>:$COMMIT_HASH \
  -f docker/Dockerfile .
```

Output:
```
## Build Result
Build: <mainApp>
Image: $IMAGE_REGISTRY/<mainApp>:e4f5g6h
Platform: linux/amd64
Pushed: no
Digest: N/A

### Confidence: HIGH -- build succeeded locally
```

## Failure modes (symptom -> detection -> action)

- **ImagePullBackOff after push**: symptom: runtime pod cannot pull the image -> detect: pod description shows authentication error -> fix: re-authenticate with `gcloud auth configure-docker`, regenerate the docker pull secret via the infrastructure repo's `./files/dockerSecret.sh`
- **buildx not available**: symptom: `docker buildx build` returns "buildx not found" -> detect: error message on command execution -> fix: install buildx plugin or create builder with `docker buildx create --name multiarch-builder --use`
- **ARM64 build slow on x86**: symptom: build takes 10x longer than expected -> detect: build platform is `linux/arm64` but host is x86 -> fix: QEMU emulation is expected to be slow; for faster builds, build on the device directly or use CI runners with native ARM64
- **NEXT_PUBLIC baked into image**: symptom: client-side config shows wrong API key in non-dev environment -> detect: `docker history` shows `NEXT_PUBLIC_*` in build args -> fix: remove `--build-arg`, implement `window.__ENV__` runtime bridge per code-standards
- **Stale base image**: symptom: vulnerability scanner flags known CVEs in base layer -> detect: `docker inspect` shows base image last pulled months ago -> fix: pin base image to a recent digest, rebuild

## Related skills (compose vs defer)

- The project's infra pack skills — **defer**: after docker-build produces the image, they handle rolling it out to the runtime environment
- `staging operations` (the project's `<envAlias>-operations` skill) — **compose**: staging operations consume images from the registry for staging deploys
- `code-standards` — **compose**: code-standards defines the 12-factor build-once/deploy-anywhere rule that governs how the main-app image must be built
- `gcp-cicd-auth` — **compose**: for CI pipeline auth via Workload Identity Federation (not `gcloud auth login`)
