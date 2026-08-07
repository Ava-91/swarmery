---
description: Deep-dive diagnostic on a failed GitLab pipeline — fetches failed job traces, pattern-matches common errors, proposes fixes
allowed-tools:
  - Bash
docs:
  status: reviewed
  source_sha: 4f4e4db6fe21
  updated: 2026-08-06
---

# /ci-diagnose — GitLab pipeline failure forensics

Given a failed pipeline ID (or auto-detect the most recent failed
pipeline on main), produce a diagnostic report: which jobs failed, the
full trace of each, pattern-matched root-cause hypothesis, and specific
recovery commands.

## Usage

```
/ci-diagnose                  # auto-find most recent failed pipeline on main
/ci-diagnose <pipeline-id>    # inspect a specific pipeline
/ci-diagnose -R <repo>        # non-default repo
```

## What it does

1. **Pipeline overview** — `glab ci get` to map job states.
2. **Failed-job traces** — `glab ci trace <job>` for each job with state
   `failed`. Tail of each (last ~100 lines is usually enough).
3. **Pattern match against known failure modes** (if the project keeps a
   curated failure-modes taxonomy in its docs, consult it first):

| Pattern in log | Root-cause hypothesis | Recovery |
|---|---|---|
| `[ERROR] Required secret '<app>-*' not found in namespace '<namespace>'` | Missing bootstrap secret | Run the project's bootstrap-secrets script/command |
| `Save error occurred: can't get a valid version for dependency` | Chart.yaml / Chart.lock drift | `bash scripts/check-chart-sync.sh` in the chart repo; `helm dependency update .` |
| `ERROR: remote payload exited 1` without other context | Remote deploy wrapper swallowed stderr — look at stdout in the remote-output capture | SSH to the VM and re-run the failing script manually; check the preceding `[ERROR]` lines |
| `helm rollback` failure or `--atomic` rollback triggered | Pod readiness probe failing; likely env-var missing or config broken | Check deployment env block; check pod logs |
| `FAILED_PRECONDITION: Secret Version is in DESTROYED state` | GCP Secret Manager destroyed-version | Repopulate the secret version via the project's bootstrap-secrets flow |
| `Too many authentication failures` | SSH agent crowded with keys | Ensure `IdentitiesOnly=yes -o IdentityAgent=none` in the SSH call (the project's SSH wrapper should set this) |
| `yaml Errors:` non-empty in pipeline metadata | CI YAML parse error | Open `.gitlab-ci.yml` / `ci/includes/*.yml`; `glab ci lint` locally |
| `IMAGE_DIGEST missing or malformed` | publish-metadata artefact didn't propagate | Retry build+publish; check `build.env` artefact |
| `[ERROR] Drift check` from rollback | Cluster-vs-versions drift (live digest ≠ version-pinning repo) | Run `helm rollback <release> -n <namespace>` on the VM first, then retry the staging rollback job |

4. **Output** — compact structured report:

```
=== Pipeline #<ID> on <ref> — <state> ===
Failed jobs: <list>

Job <name>:
  Exit code: <N>
  Last failure signal: <grep-matched pattern>
  Hypothesis: <mapped root cause>
  Recovery: <specific command>

Overall diagnosis: <one-line summary>
Suggested next action: <command to run>
```

## Implementation

Wraps:
- `glab ci list` (find most recent failed pipeline on main)
- `glab ci get --pipeline-id <ID>`
- `glab ci trace <job-name>`
- Regex scan against the pattern table above

For structured output, consider calling the `ci-incident-responder`
agent when multiple hypotheses need weighing.

## When to use

- Before retrying a failed pipeline (don't retry blindly)
- When the CI log shows only `remote payload exited 1` with no context
- When post-merge chain fails on main and you need to understand why fast

## Related

- `/env-check` — broader environment configuration snapshot
- `@ci-incident-responder` — multi-hypothesis agent
- `troubleshooting` skill — fuller treatment of the patterns above

# How to use

## What it does

When a pipeline fails, this command does the log-reading for you. It finds the failed pipeline (or takes an ID you give it), pulls the trace of every failed job, matches the output against a table of known failure signatures, and hands you a root-cause hypothesis plus the exact command to run next. You get a diagnosis instead of a wall of log output.

## When to use it

- A pipeline just went red and you are about to hit retry — read this first so you know whether a retry can possibly help.
- The trace ends in a bare `remote payload exited 1` with no surrounding context.
- A post-merge run on the default branch broke and you need the cause in minutes, not after scrolling traces.
- You want a written diagnosis you can paste into an incident thread.

## When not to use it

- You want a picture of environment configuration rather than one failed run — use `/env-check`.
- Several competing hypotheses need weighing against each other — hand it to the `ci-incident-responder` agent.
- You are learning the failure patterns themselves rather than triaging one run — read the `troubleshooting` skill.

## How to invoke

```
/ci-diagnose                  # most recent failed pipeline on the default branch
/ci-diagnose <pipeline-id>    # a specific pipeline
/ci-diagnose -R <repo>        # a repository other than the default
```

Run it with no arguments and it finds the newest failed pipeline itself.

## Inputs

- `<pipeline-id>` — the numeric pipeline to inspect — optional; auto-detected when omitted.
- `-R <repo>` — target a repository other than the current one — optional.
- A working `glab` CLI with access to the project — required.

## What you get back

A compact report in the transcript: the pipeline number, ref and state; the list of failed jobs; and for each job its exit code, the log line that matched a known pattern, the mapped root cause, and a recovery command. It closes with a one-line overall diagnosis and a suggested next action. Nothing is written to disk and nothing in the pipeline is changed — the command only reads.

## Worked example

```
/ci-diagnose 48213

=== Pipeline #48213 on main — failed ===
Failed jobs: deploy-staging

Job deploy-staging:
  Exit code: 1
  Last failure signal: Save error occurred: can't get a valid version for dependency
  Hypothesis: Chart.yaml / Chart.lock drift
  Recovery: bash scripts/check-chart-sync.sh in the chart repo

Overall diagnosis: chart dependencies drifted; a retry will fail the same way.
Suggested next action: helm dependency update .
```

You end up knowing that retrying is pointless and which repository to fix.

## Related

- `/env-check` — prefer it for a broad environment configuration snapshot.
- `@core:ci-incident-responder` — prefer it when one run has several plausible causes.
- `troubleshooting` skill — fuller treatment of the failure patterns behind this command.
