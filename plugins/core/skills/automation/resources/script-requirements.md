# automation — procedure, safety requirements, and checks

## Required environment

- Runtime: `.claude/skills/automation/SKILL.md`
- Tools: Read, Write, Bash
- File system assumptions:
  - `kubectl` (or the runtime's equivalent CLI — `.claude/project.json` -> `cloud.runtime`) is configured with the appropriate context
  - The target namespace is passed as a parameter (never hardcoded)
  - Chaos experiments require explicit `ALLOW_CHAOS=true` environment variable
- Canonical script storage path: `devops/scripts/<runbook-name>.sh` (or `.py`). All produced scripts are saved here and referenced in a pull request for review before cluster execution.

## Inputs

- `runbook_name: string` — name of the runbook to automate (e.g., "restart-device-gateway", "flush-redis")
- `target_namespace: string` — target namespace (e.g., "app", "edge")
- `target_deployment: string` — deployment name (e.g., "device-gateway", "web-app")
- `automation_level: "script" | "self-healing" | "chaos"` — what to produce

## Outputs

- Format: executable shell script (`.sh`) or Python module (`.py`) with inline documentation
- Length budget: scripts under 100 lines; self-healing modules under 200 lines
- Storage: saved to `devops/scripts/<runbook-name>.sh` (or `.py`) using the Write tool

## Procedure

1. **Review the manual runbook** — Read the existing documentation or user description of the manual steps using the Read tool.
   **Checkpoint:** Manual steps are understood and can be listed sequentially.

2. **Confidence gate** — If the runbook steps are ambiguous, contradictory, or incomplete, STOP and ask the user for clarification before proceeding. Do not write or execute scripts against a live cluster without at least one human confirmation step.
   **Checkpoint:** Runbook steps are unambiguous and complete.

3. **Identify safety requirements** — For each step, determine:
   - Is it destructive (deletes data, restarts services, modifies state)?
   - Does it require confirmation?
   - Can it be rolled back?
   - Is a dry-run possible?
   **Checkpoint:** Every destructive step has a safety gate identified.

4. **Parameterize all environment-specific values** — Replace hardcoded namespaces, deployment names, hostnames, and credentials with script parameters or environment variables.
   **Checkpoint:** Zero hardcoded cluster-specific values remain.

5. **Write the automated script** — Use the Write tool to save the script to `devops/scripts/<runbook-name>.sh` (or `.py`). Apply these mandatory rules:
   - Every destructive kubectl command must be preceded by a `--dry-run=client` step
   - Every script must accept `--dry-run` flag that skips all destructive operations
   - Every script must log what it does with timestamps
   - Every script must use `set -euo pipefail` (bash) or equivalent error handling
   - Chaos experiments must check `ALLOW_CHAOS=true` before executing
   - Chaos experiments must check that the target namespace is NOT a production namespace
   - All parameters must have defaults documented in usage text
   **Checkpoint:** Script saved to canonical path.

6. **Add rollback procedure** — Document or script the rollback for each destructive step.
   **Checkpoint:** Rollback is either scripted or documented with exact commands.

7. **Test with dry-run** — Use the Bash tool to execute the script with `--dry-run` flag and verify output. Include the dry-run output in your response.
   **Checkpoint:** Dry-run completes without errors and shows what would happen.

8. **Final acceptance check** — Script is parameterized, has safety gates, has rollback, and dry-run passes.
   **Checkpoint:** All self-check items pass.

## Self-check before returning

- [ ] Zero hardcoded namespaces — all namespace values come from parameters or environment variables
- [ ] Zero hardcoded deployment names — all deployment names come from parameters
- [ ] Every destructive command has a preceding `--dry-run=client` step or equivalent
- [ ] Script accepts a `--dry-run` flag that prevents all destructive operations
- [ ] Chaos experiments check `ALLOW_CHAOS=true` environment variable before executing
- [ ] Chaos experiments reject production namespaces (any namespace containing "prod")
- [ ] Script uses `set -euo pipefail` or equivalent error handling
- [ ] Every operation is logged with a timestamp
- [ ] Rollback procedure is documented or scripted for every destructive step
- [ ] Script is saved to `devops/scripts/` using the Write tool

## Common mistakes to avoid

- DO NOT hardcode `-n app` or any namespace — always use a `$NAMESPACE` variable with the namespace passed as a required parameter
- DO NOT hardcode deployment names like `deployment/device-gateway` — accept them as parameters
- DO NOT run chaos experiments without an environment guard that blocks production
- DO NOT use `kubectl delete pod` without a preceding dry-run and confirmation prompt
- DO NOT use `asyncio.sleep()` without a cancellation mechanism in self-healing code — use `asyncio.wait_for()` with a timeout
- DO NOT reference non-existent Python modules (e.g., `import disconnect_device`) — only use modules that exist in the repository
- DO NOT schedule recurring chaos experiments (cron-based Chaos Mesh) without explicit human approval and a documented kill switch
- DO NOT write scripts by echoing content through Bash — always use the Write tool to create script files

## What to surface to the user

- The complete script with inline comments explaining each step
- Dry-run output showing what the script would do
- Rollback procedure (scripted or documented)
- Estimated toil reduction (time saved per execution * frequency)
- Any assumptions about cluster state or prerequisites

## Escalation

- Stop and ask when: the runbook involves deleting PersistentVolumeClaims or StatefulSet data
- Stop and ask when: the chaos experiment targets a namespace that might be production (any namespace not explicitly marked as non-prod)
- Stop and ask when: the self-healing controller would automatically restart a deployment more than 3 times in 10 minutes (restart loop risk)
- Refuse and explain when: asked to automate a runbook that includes credential rotation without a secrets manager integration
- Refuse and explain when: asked to run chaos experiments against a production cluster without explicit written confirmation

## Failure modes

| Failure | Detect | Fix |
|---------|--------|-----|
| Script fails because kubectl context is wrong | "error: context not found" in output | Add `kubectl config current-context` check at script start |
| Rollout restart hangs because new pod fails health checks | `kubectl rollout status` times out | Script catches the timeout and suggests `kubectl rollout undo` |
| Chaos experiment accidentally targets production | Namespace contains "prod" | Environment guard blocks execution and exits with error |

## Toil measurement

When automating a runbook, document the toil reduction:

```
Manual time per execution: X minutes
Automated time per execution: Y minutes
Frequency: Z times per week
Weekly savings: (X - Y) * Z minutes
```

## Related skills

- `code-standards` — defer to for script quality and style conventions
- `api-integration` — compose when a runbook step involves calling the main app's API endpoints
- `code-quality` — defer to for complexity and maintainability analysis of automation scripts
- The project's infra pack skills — defer to for CI/CD pipelines, deployment manifests, and infrastructure templates
