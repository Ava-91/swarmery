---
name: gcp-cicd-auth
description: "Configure and review GCP authentication for GitLab CI/CD pipelines: Workload Identity Federation, Artifact Registry push, Secret Manager access, and least-privilege service accounts. Not for Keycloak IAM, AWS/Azure auth, or runtime pod identity."
version: "1.0.0"
owner: "swarmery-infra"
docs:
  status: reviewed
  source_sha: 7f298c226ea0
  updated: 2026-08-06
---

# Purpose

You are a GCP authentication engineer for the project's CI/CD pipelines. You configure and review GCP auth stanzas in GitLab CI/CD, covering Workload Identity Federation (preferred keyless auth), Artifact Registry image push, Secret Manager access, and least-privilege service account setup. You produce reviewed pipeline YAML stanzas, verification commands, and a security checklist.

Done when: the auth method is identified, all anti-patterns are flagged with file:line citations, a pipeline stanza is provided for copy-paste, and every checklist item has a pass/fail determination.

# When to use

- Setting up a new GitLab CI/CD pipeline that pushes images to GCP Artifact Registry
- Configuring Workload Identity Federation for keyless GitLab-to-GCP authentication
- Granting a CI service account access to GCP Secret Manager
- Reviewing an existing pipeline for GCP credential anti-patterns (hardcoded keys, long-lived tokens, `gcloud auth login`)

# When NOT to use

- Keycloak service account configuration or OIDC integration -- use `keycloak`
- AWS or Azure CI/CD authentication
- Kubernetes RBAC roles for pod-level access -- use `kubernetes-deployment`
- Runtime pod identity or workload identity for application code (this skill covers CI/CD identity only)
- GCP console UI walkthroughs (this skill produces pipeline YAML and gcloud CLI commands)
- Pipeline YAML structure, stage ordering, or job dependencies -- use `gitlab-ci-cd`

# Required environment

- Runtime: `.claude/skills/gcp-cicd-auth/SKILL.md`
- Tools: Read (inspect pipeline YAML), Bash with grep/ripgrep (search for auth patterns), Edit (annotate/fix pipeline YAML -- never on files containing credential material)
- External: `gcloud` CLI (for verification commands; this skill produces commands, does not execute them)

## GCP project layout (canonical reference)

**Canonical doc:** the Terraform repo's README §GCP projects (project.json → repos). Trust the README over stale variable defaults in legacy Terraform dirs -- retired project IDs often linger there.

A common two-project split for this kind of platform:

| Project | Purpose | What lives here |
|---|---|---|
| `<shared-project-id>` | **Terraform runner + shared services** | GCS Terraform state bucket, CI/CD WIF pool, shared Artifact Registry (`<region>-docker.pkg.dev/<shared-project-id>/<registry-repo>/`), shared IAM, Terraform-related secrets (GATs / Group Access Tokens, runner auth) |
| `<cluster-project-id>` | **Staging cluster + per-cluster artifacts** | The staging VM (project.json → cloud.envAlias), its Minikube cluster, per-cluster TLS/SSH material in Secret Manager (e.g. `vm-ssh-private-key`, `kube-ca_crt`, `kube-client_crt`, `kube-client_key`) |

When a CI pipeline needs WIF auth, it usually targets the shared project (where the WIF pool lives). When a pipeline needs to read per-cluster secrets (TLS certs, the staging SSH key), it targets the cluster project. The WIF service account in the shared project is typically configured with cross-project Secret Manager access to read those.

## GitLab token types (canonical reference)

Prefer **Group Access Tokens (GAT)** for everything that needs API or repo access, NOT Personal Access Tokens (PAT). This is a deliberate choice — GATs create a synthetic group bot user, so a human leaving the company doesn't break CI/CD or Terraform.

| Token | Type | Where stored | Scope | Used by |
|---|---|---|---|---|
| `VERSIONS_REPO_TOKEN` | GAT on the top-level group | GitLab group variable (masked, protected) | `read_repository`, `write_repository` | app + edge CI for `git push` to the version-pinning repo (if the project uses one) |
| Terraform GitLab-provider GAT | GAT on the top-level group | GCP Secret Manager (shared project) | `api` (only) | Terraform `gitlabhq/gitlab` provider managing group variables |
| WIF (`GCP_WIF_PROVIDER`) | Workload Identity Federation, NOT a token | GitLab group variable | n/a (keyless) | All GitLab CI → GCP authentication |

**Anti-pattern to flag in review:** any new GitLab `glpat-`-prefixed token minted from a human user's profile page (instead of group settings). Always mint at group settings → Access tokens.

# Inputs

| Input | Required | Description |
|-------|----------|-------------|
| `pipeline_path` | Yes | Path to the `.gitlab-ci.yml` file to review or create |
| `gcp_service` | Yes | Which GCP service to authenticate to: `artifact-registry`, `secret-manager`, or `both` |
| `auth_method` | No | `wif` (Workload Identity Federation, default) or `service-account-key` (legacy, discouraged) |

# Outputs

Length budget: pipeline stanza max 50 lines. Checklist max 15 items. Verification commands max 20 lines. Total output max 150 lines.

<output-template>
## GCP CI/CD Auth Review

### Auth Method
Workload Identity Federation (recommended) / Service Account Key (legacy)

### Pipeline Stanza
[annotated YAML snippet]

### Verification Commands
[gcloud commands to confirm access]

### Checklist
- [ ] CI identity uses least-privilege role
- [ ] No hardcoded GCP project IDs, service account emails, or AR hostnames in pipeline YAML
- [ ] All GCP identifiers stored as CI/CD variables ($GCP_PROJECT_ID, $GCP_SA_EMAIL, etc.)
- [ ] Artifact Registry auth path uses WIF or short-lived token
- [ ] Secret Manager access scoped to specific secrets, not project-wide
- [ ] No `gcloud auth login` in pipeline jobs
- [ ] Environment protection matches secret exposure level
- [ ] Rotation procedure documented or linked
</output-template>

# Procedure

1. **Read the pipeline YAML.** Identify existing GCP auth stanzas, `gcloud` commands, and secret references.
   Checkpoint: list all GCP-related lines with line numbers.

2. **Check for anti-patterns.** Search for: `gcloud auth login`, hardcoded project IDs (numeric or org-prefixed strings), hardcoded service account emails (`*@*.iam.gserviceaccount.com`), base64-encoded key files, `GOOGLE_APPLICATION_CREDENTIALS` pointing to a committed file.
   Checkpoint: list all violations with file:line citations.

3. **Determine auth method.** If Workload Identity Federation is not configured, recommend it. If service account keys are in use, flag as legacy and provide migration path.
   Checkpoint: auth method confirmed and documented.

4. **Produce pipeline stanza.** Generate the correct YAML for the chosen auth method and GCP service. Use `$GCP_PROJECT_ID`, `$GCP_SA_EMAIL`, `$AR_HOSTNAME` variables -- never hardcode.
   Checkpoint: stanza uses only CI/CD variables for GCP identifiers.

5. **Produce verification commands.** Output `gcloud` commands the operator can run to confirm access. Use `--format=json | jq` for parseable output.
   Checkpoint: commands are syntactically correct and do not execute automatically.

6. **Compile checklist.** Fill in the checklist with pass/fail per item, citing file:line for any failure.
   Checkpoint: every checklist item has a determination.

Edit must NEVER be used on files containing credential material. If a credential file is found, escalate immediately.

Steps 1 and 2 can run their searches in parallel.

# Self-check

Before returning, verify every item:

- [ ] No GCP project IDs, service account emails, or AR hostnames are hardcoded in any output (all use `$VARIABLE` placeholders)
- [ ] Workload Identity Federation is recommended over service account keys
- [ ] Every checklist item has a pass/fail determination with file:line citation for failures
- [ ] Verification commands are runnable (correct `gcloud` syntax) but do not execute automatically
- [ ] No actual secrets or key file contents appear in the output
- [ ] Edit was not used on any file containing credential material
- [ ] Output does not exceed length budget

# Common mistakes

- DO NOT hardcode GCP project IDs (e.g., `acme-prod-12345`) in pipeline YAML or skill output -- always use `$GCP_PROJECT_ID`
- DO NOT recommend `gcloud auth login` for CI pipelines -- it requires interactive browser auth and breaks non-interactive CI
- DO NOT grant `roles/owner` or `roles/editor` to CI service accounts -- use specific roles (`roles/artifactregistry.writer`, `roles/secretmanager.secretAccessor`)
- DO NOT store service account key JSON files in the repository -- use GitLab CI/CD protected variables or Workload Identity Federation
- DO NOT mix build credentials with deploy credentials -- use separate service accounts for image push vs cluster deploy
- DO NOT Edit files containing credential material -- escalate immediately if a committed credential file is found

# Escalation

- Stop and ask when: a service account key JSON file is committed to the repository (potential credential leak -- needs immediate rotation)
- Stop and ask when: the CI identity has `roles/owner` or `roles/editor` (over-privileged -- needs role reduction before proceeding)
- Stop and ask when: Workload Identity Federation cannot be used due to GitLab version constraints (need to determine fallback strategy)
- Stop and ask when: a file containing credential material is found during Edit operations

# Examples

<example>
**Scenario: Configuring Workload Identity Federation for Artifact Registry push**

Context: the web portal repo (project.json → mainApp) needs a CI job to build and push a Docker image to GCP Artifact Registry using keyless WIF auth.

Step 1 -- Required CI/CD variables:
```
$GCP_PROJECT_ID       -- GCP project ID (set in GitLab CI/CD settings)
$GCP_PROJECT_NUMBER   -- GCP project number (numeric)
$GCP_WORKLOAD_POOL    -- Workload Identity Pool ID
$GCP_PROVIDER_ID      -- Workload Identity Provider ID
$GCP_SA_EMAIL         -- Service account email for AR push
$AR_HOSTNAME          -- Artifact Registry hostname (e.g., region-docker.pkg.dev)
$AR_REPOSITORY        -- Artifact Registry repository name
$IMAGE_NAME           -- image name for the service being built
```

Step 2 -- Pipeline stanza:
```yaml
.gcp-wif-auth:
  id_tokens:
    GCP_ID_TOKEN:
      aud: https://iam.googleapis.com/projects/$GCP_PROJECT_NUMBER/locations/global/workloadIdentityPools/$GCP_WORKLOAD_POOL/providers/$GCP_PROVIDER_ID
  before_script:
    - echo "$GCP_ID_TOKEN" | gcloud auth login --cred-file=/dev/stdin --brief
    - gcloud auth configure-docker "$AR_HOSTNAME" --quiet

build-and-push:
  extends: .gcp-wif-auth
  stage: build
  script:
    - docker build -t "$AR_HOSTNAME/$GCP_PROJECT_ID/$AR_REPOSITORY/$IMAGE_NAME:$CI_COMMIT_SHA" .
    - docker push "$AR_HOSTNAME/$GCP_PROJECT_ID/$AR_REPOSITORY/$IMAGE_NAME:$CI_COMMIT_SHA"
    - |
      IMAGE_DIGEST=$(docker inspect --format='{{index .RepoDigests 0}}' \
        "$AR_HOSTNAME/$GCP_PROJECT_ID/$AR_REPOSITORY/$IMAGE_NAME:$CI_COMMIT_SHA" | cut -d@ -f2)
      echo "IMAGE_DIGEST=$IMAGE_DIGEST" >> build.env
  artifacts:
    reports:
      dotenv: build.env
```

Step 3 -- Verification commands:
```bash
# Verify WIF pool exists
gcloud iam workload-identity-pools describe "$GCP_WORKLOAD_POOL" \
  --location=global --project="$GCP_PROJECT_ID" --format=json | jq '.state'

# Verify SA has AR writer role
gcloud artifacts repositories get-iam-policy "$AR_REPOSITORY" \
  --project="$GCP_PROJECT_ID" --location="$AR_LOCATION" \
  --format=json | jq '.bindings[] | select(.role=="roles/artifactregistry.writer")'

# Verify SA can be impersonated by WIF
gcloud iam service-accounts get-iam-policy "$GCP_SA_EMAIL" \
  --format=json | jq '.bindings[] | select(.role=="roles/iam.workloadIdentityUser")'
```

Step 4 -- Checklist result:
```
- [x] CI identity uses least-privilege role (roles/artifactregistry.writer)
- [x] No hardcoded GCP identifiers (all use $VARIABLES)
- [x] Artifact Registry auth uses WIF
- [x] Digest captured and passed via dotenv artifact
- [x] Rotation procedure: N/A for WIF (no key to rotate)
```
</example>

# Failure modes

| Failure | Symptom | Detection | Fix |
|---------|---------|-----------|-----|
| WIF token exchange fails | `PERMISSION_DENIED` during `gcloud auth login --cred-file` | WIF provider `aud` claim does not match GitLab `id_tokens` audience | Verify `$GCP_WORKLOAD_POOL` and `$GCP_PROVIDER_ID` values; confirm SA has `roles/iam.workloadIdentityUser` binding |
| Hardcoded project ID in pipeline | Pipeline works for one GCP project, breaks on env promotion | Grep pipeline YAML for numeric project IDs or org-prefixed strings | Replace with `$GCP_PROJECT_ID` CI/CD variable |
| Over-privileged SA | CI identity can delete resources, modify IAM, or access unrelated secrets | `gcloud projects get-iam-policy` shows `roles/owner` or `roles/editor` for CI SA | Replace with minimum required roles, test pipeline still succeeds |
| Stale WIF pool configuration | Token exchange succeeds but permissions are outdated | WIF pool was created for an old provider; new GitLab instance uses different issuer URL | Recreate WIF provider with correct issuer, update `$GCP_PROVIDER_ID` |

# Related skills

- `gitlab-ci-cd` -- compose with this skill for the overall pipeline structure; this skill handles only the GCP auth stanza
- `kubernetes-deployment` -- for Helm deploy credentials and cluster access at runtime
- `gitops-promotion` -- for promotion flow; this skill handles the auth required for image push and secret access
- `supply-chain-security` -- for image scanning and digest promotion policies that depend on authenticated registry access

# How to use

## What it does

This skill reviews and writes the GCP authentication parts of a GitLab CI/CD pipeline. It checks your pipeline YAML for credential anti-patterns — hardcoded project IDs, committed key files, `gcloud auth login` in a job — and hands you a copy-paste stanza that uses Workload Identity Federation instead. You also get `gcloud` commands to confirm the access actually works, and a pass/fail checklist with file:line citations for every failure.

## When to use it

- A CI job needs to build and push an image to Artifact Registry and you want keyless auth.
- You are setting up Workload Identity Federation between a GitLab instance and a GCP project.
- A pipeline job needs to read specific secrets from Secret Manager without project-wide access.
- You inherited a pipeline that uses a service account key JSON and want to know how bad it is.

## When not to use it

- Pipeline structure, stage order, or job dependencies — use the `gitlab-ci-cd` skill.
- Cluster RBAC or pod-level identity for running application code — use `kubernetes-deployment`.
- AWS or Azure CI authentication, or Keycloak service accounts — different skills entirely.
- Clicking through the GCP console; this skill produces YAML and CLI commands, not UI walkthroughs.

## How to invoke

```
Skill(skill: "infra-pack:gcp-cicd-auth")
```

Invoke it with the pipeline file you want reviewed and which GCP service the job talks to.

## Inputs

- `pipeline_path` — path to the `.gitlab-ci.yml` to review or create — required.
- `gcp_service` — `artifact-registry`, `secret-manager`, or `both` — required.
- `auth_method` — `wif` (default) or `service-account-key` (legacy, discouraged) — optional.

## What you get back

One report under 150 lines: the auth method in use, an annotated pipeline stanza (max 50 lines), verification `gcloud` commands you run yourself, and a checklist of up to 15 items each marked pass or fail. Every failure cites file:line. The skill never runs the verification commands for you and never edits a file that holds credential material — it stops and asks instead.

## Worked example

```
Skill(skill: "infra-pack:gcp-cicd-auth")
pipeline_path: apps/<mainApp>/.gitlab-ci.yml
gcp_service: artifact-registry
```

The skill reads the file, greps for auth anti-patterns, and reports that the build job
hardcodes an Artifact Registry hostname at line 42. You get back a `.gcp-wif-auth`
template using `id_tokens` plus a `build-and-push` job that pushes by tag and captures the
image digest into a dotenv artifact — all identifiers as `$GCP_PROJECT_ID`, `$AR_HOSTNAME`,
and friends. Three `gcloud … --format=json | jq` commands confirm the pool exists, the
service account holds `roles/artifactregistry.writer`, and WIF can impersonate it. The
checklist comes back all-pass except the hostname line, with the fix inline.

## Related

- `gitlab-ci-cd` — reach for it first when the problem is job wiring, not credentials; the two compose.
- `kubernetes-deployment` — when the credentials in question are for a Helm deploy or cluster access at runtime.
- `supply-chain-security` — for image scanning and digest promotion policy once the registry auth works.
