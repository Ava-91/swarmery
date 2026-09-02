# docker-build — build procedure and output contract

## Required environment

- Tools/libraries: `docker` (with buildx plugin), the cloud provider's CLI (for registry auth)
- Environment variable: `IMAGE_REGISTRY` — the container registry URL (e.g., `<region>-docker.pkg.dev/<project-id>/<repository>`; region from `project.json → cloud.region`). Do not hardcode the cloud project ID.

## Inputs

- `service: string` — which service to build: the main app (`<mainApp>`) or the device service (`<device>`)
- `tag: string` — image tag (git short hash for dev, semver for release)
- `push: boolean` — whether to push to the registry after building (default: false)

## Outputs

**Format:** Build result inlined in agent response.

**Length budget:** Build result max 10 lines. If build fails, include last 20 lines of build output.

**Output template:**

```
## Build Result
Build: {service}
Image: $IMAGE_REGISTRY/{image-name}:{tag}
Platform: {architecture}
Pushed: {yes|no}
Digest: {sha256:... if pushed, N/A if local only}

### Confidence: {HIGH|MEDIUM|LOW} -- {rationale}
```

## Procedure (Checkpoint: after each step)

1. **Authenticate with the cloud registry** — Ensure Docker is configured for the registry.
   ```bash
   # Local dev auth only -- CI pipelines use WIF (see gcp-cicd-auth skill)
   gcloud auth configure-docker <region>-docker.pkg.dev
   ```
   Checkpoint: `docker login` succeeds for the registry host.

2. **Set up buildx** — Ensure a buildx builder exists for the target platform.
   ```bash
   docker buildx create --name multiarch-builder --use 2>/dev/null || docker buildx use multiarch-builder
   docker buildx inspect --bootstrap
   ```
   Checkpoint: Builder is active and supports the target platform.

3. **Determine build parameters** — Based on the service:

   | Service | Image name | Platform | Dockerfile | Build context |
   |---------|-----------|----------|------------|---------------|
   | `<device>` (edge, on RPi) | `<device-image>` | `linux/arm64` | `Dockerfile.optimized` | `<device>/` |
   | `<mainApp>` (web) | `<mainApp>` | `linux/amd64` | `docker/Dockerfile` | `apps/<mainApp>/` |

4. **Build the image** — Run the build command. Do NOT pass `NEXT_PUBLIC_*` variables as `--build-arg`.

   **Device service**:
   ```bash
   cd <device>
   COMMIT_HASH=$(git rev-parse --short HEAD)

   docker buildx build \
     --platform linux/arm64 \
     -t $IMAGE_REGISTRY/<device-image>:$COMMIT_HASH \
     -f Dockerfile.optimized .
   ```

   **Main app**:
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

   The main-app image must be environment-agnostic per 12-factor. Client-visible configuration (like a maps API key) is injected at runtime via the `window.__ENV__` bridge pattern: the server renders `<script>window.__ENV__={GOOGLE_MAPS_API_KEY: process.env.GOOGLE_MAPS_API_KEY}</script>` and client code reads from `window.__ENV__`.

   Checkpoint: Build exits 0, image is tagged locally.

5. **Push (if requested)** — Append `--push` to the build command, or run `docker push`.

   **Side effect**: `--push` writes to the shared container registry immediately. This is visible to all environments and is not easily reversible without explicit image deletion.

   ```bash
   docker buildx build \
     --platform linux/arm64 \
     -t $IMAGE_REGISTRY/<device-image>:$COMMIT_HASH \
     -f Dockerfile.optimized . --push
   ```

   Checkpoint: Push succeeds; verify with `gcloud artifacts docker images describe $IMAGE_REGISTRY/<image>:$TAG`.

6. **Verify pushed image** — Confirm the image exists in the registry.
   ```bash
   gcloud artifacts docker images describe $IMAGE_REGISTRY/<image-name>:<tag>
   ```
   Checkpoint: Command returns image metadata including digest.

## Self-check before returning

- [ ] No `NEXT_PUBLIC_*` variables were passed as `--build-arg` (violates 12-factor; use `window.__ENV__` runtime bridge)
- [ ] Registry URL used the `$IMAGE_REGISTRY` variable, not a hardcoded cloud project ID
- [ ] Image tag is a git short hash or semver, never `latest`
- [ ] `--push` side effect was explicitly communicated to the operator before execution
- [ ] If pushed, image existence was verified via `gcloud artifacts docker images describe`
- [ ] Build platform matches the target deployment (ARM64 for RPi/edge, AMD64 for cloud/VM)
- [ ] Base image tag in Dockerfile is pinned to a specific digest or dated tag, not `latest` — if not, flag it
- [ ] Output matches the build result template format
- [ ] Confidence label attached — label LOW when build succeeds but push was not verified
