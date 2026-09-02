# Commit Message Procedure

## When to use

- Completing an implementation task and staging files for commit
- Squashing commits and need a new summary message for the squashed result
- Reviewing a commit message for project convention compliance

## When NOT to use

- Git tag messages (use release conventions, not commit conventions)
- Merge commit messages (use MR description as the merge message)
- Changelog entries (commit format != changelog format)
- Automated version bump commits in a version-pinning repo (use the promotion convention from the project's infra pack skills)
- Reviewing git history or reading `git log` (no message generation needed)

## Required environment

- Runtime: `.claude/skills/git-commit/SKILL.md`
- Tools: none (rule-based message generation from staged diff context)
- Companion file: `.claude/skills/git-commit/examples/commit-examples.md` -- contains worked examples. NOTE: that file may contain deprecated scopes (`be`, `fe`, `helm`) from a legacy stack; always use current scopes from the project's `.claude/project.json` -> `commitScopes`.

## Inputs

- `diff: string` -- the staged diff or description of changes
- `repo: string` -- which repo the commit targets (determines scope)

## Outputs

- Format: a commit message string in conventional commit format
- Length budget: subject line max 72 characters

```
<type>(<scope>): <subject>

[optional body: blank line, then bullet list of changes]

[optional footer: BREAKING CHANGE:, Closes #N]
```

## Procedure

1. **Read the diff** -- Identify the primary change: new feature, bug fix, refactor, test, docs, CI, build, or chore.
   **Checkpoint:** Single `type` selected.

2. **Select scope** -- Map the changed files to the project's scope list (`.claude/project.json` -> `commitScopes`; illustrative defaults in `format-reference.md`). If files span multiple repos, generate one commit message per repo.
   **Checkpoint:** Scope matches the repo.

3. **Security gate** -- Check whether any staged files are likely to contain secrets: `.env`, `*.populated.yaml`, `credentials.json`, `*.key`, `*.pem`, `*secret*`. If any match: REFUSE to generate a commit message. Instruct the user to unstage those files first. Do not ask for confirmation -- the answer is always no.
   **Checkpoint:** No secret files staged.

4. **Write subject** -- Imperative mood, lowercase first word, no trailing period. Describe the user-visible change, not the file touched. Max 72 characters.
   **Checkpoint:** Subject reads as "this commit will [subject]".

5. **Add body** (if the change is non-trivial) -- Blank line after subject, then bullet list of specific changes. Each bullet starts with `-`.

6. **Add footer** (if applicable) -- `BREAKING CHANGE: <description>` for breaking changes. `Closes #N` for issue references.

7. **Verify** -- Confirm type, scope, subject, body, and footer all follow the rules in `format-reference.md`.
   **Checkpoint:** All rules pass.

## Self-check before returning

- [ ] Type correctly reflects the nature of the change (feat = new capability, fix = bug fix, refactor = no behavior change)
- [ ] Scope matches the project's current scope list (no deprecated scopes)
- [ ] Subject is imperative mood, lowercase, no period, under 72 characters
- [ ] Subject describes the user-visible change, not just the file name
- [ ] Body bullets (if present) each start with `-` and describe a specific change
- [ ] `BREAKING CHANGE:` footer present if the change breaks existing behavior
- [ ] No secret files staged (`.env`, `*.populated.yaml`, `credentials.json`, key/pem files)

## What to surface to the user

- The generated commit message
- Reasoning for type and scope selection (one sentence)
- Warning if staged files include potential secrets (and refusal to generate)

## Escalation

- **Secret files detected:** Refuse to generate a commit message. Instruct the user to unstage `.env`, `*.populated.yaml`, credential, and key files first. No exceptions.
- **Ambiguous type between `fix` and `refactor`:** Ask the user: "did user-visible behavior change?" If yes, use `fix`. If no, use `refactor`.
- **Changes span more than 2 repos:** Confirm the user wants separate commits per repo.
