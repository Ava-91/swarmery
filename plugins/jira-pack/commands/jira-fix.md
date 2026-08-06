---
description: Thin entry point for `/jira-fix <url|KEY> [--dry-run] [--repo <path>]` — parses and validates the argument shape only, then hands control to @jira-task-runner. No run logic lives here.
allowed-tools:
  - Bash
docs:
  status: reviewed
  source_sha: d86bd17e01da
  updated: 2026-08-06
---

# /jira-fix — drive a tracker ticket end-to-end

## Usage

```
/jira-fix <url|KEY> [--dry-run] [--repo <path>]
```

Examples:

- `/jira-fix <PROJECT-KEY>-123`
- `/jira-fix https://<jira-base-url>/browse/<PROJECT-KEY>-123 --dry-run`
- `/jira-fix <PROJECT-KEY>-123 --repo ../other-checkout`

This command is invoked the same way from an interactive session and from the
`ai-prompt` step of a scheduled routine (`tools/swarmery/internal/routines/doc.go:3`
— the prompt arrives on the headless session's stdin), which is why a stable
command entry point exists at all rather than only an agent that has to be
addressed by name.

## What it does — and does not do

This is a **thin proxy**. It parses `$ARGUMENTS`, validates the *shape* of the
ticket reference and the flags, and delegates everything else — config
resolution, access preflight, the board card, triage, writeback — to
`@jira-task-runner` (`plugins/jira-pack/agents/jira-task-runner.md`). No run
logic is duplicated here. That includes the ticket's **class** (`defect` vs
`change`): this command never inspects the ticket, so it cannot know which one
it is, and the flags it forwards are identical either way.

In particular, this command does **not**:
- check that a `--repo` path exists or is a git repository — that is
  `jira-config`'s job, inside the agent it hands off to;
- validate `.claude/project.json`'s `jira` block — same skill, same reason;
- resolve Jira access, read the ticket, run a reproduction, classify, comment,
  or transition — all of that is `@jira-task-runner`'s job downstream.

If argument parsing fails, stop and report a usage error. Do not invoke the
agent on an unparseable reference — an agent run that immediately fails inside
`jira-config`/`jira-access-preflight` on a malformed key is a wasted turn this
command's own regex check exists to avoid.

## Argument parsing

1. **Ticket reference** (first positional argument) — exactly one of:
   - a bare key: `<PROJECT-KEY>-<number>`, e.g. `ABC-123`, matching
     `^[A-Z][A-Z0-9]*-[0-9]+$`;
   - a browse URL: `https://<jira-base-url>/browse/<KEY>` — extract `<KEY>` from
     the path segment following `/browse/` and validate that extracted key
     against the same regex.

   Neither form matches → print a usage error and stop; do not call the agent.

2. **`--dry-run`** — boolean flag, no value. Forwarded to `@jira-task-runner`
   verbatim. This is the flag that keeps every write call (Jira comment,
   transition, board-card POST/PATCH, and — downstream, in Phase 7 — git push
   and PR creation) out of the run; see that agent's own dry-run behavior for
   what each delegated skill prints instead.

3. **`--repo <path>`** — optional value flag. Forwarded verbatim, unresolved
   and unchecked, to `@jira-task-runner` → `jira-config`, which is the only
   place that decides whether the path exists and is a git repository.

Only a lightweight shape check needs Bash (regex only — no filesystem or
network calls belong in this command):

```bash
echo "$TICKET_REF" | grep -qE '^[A-Z][A-Z0-9]*-[0-9]+$' || echo "not a bare key — try extracting from a /browse/ URL instead"
```

## Delegation

Once the reference and flags parse cleanly, hand off to `@jira-task-runner`
with: the resolved ticket key (and the original URL, if that was the form
given), the `--dry-run` flag state, and the `--repo` value if provided.
`@jira-task-runner` re-derives everything else itself, starting from
`jira-config`, and never trusts this command's parsing for anything beyond
"here is a key-shaped string and here are the flags."

## Related

- `plugins/jira-pack/agents/jira-task-runner.md` — everything past argument
  parsing: config → access preflight → board card → triage (class decision +
  its mandatory evidence step + classification) → writeback.
- `plugins/jira-pack/skills/jira-config/SKILL.md` — validates
  `.claude/project.json`'s `jira` block and resolves the working repo
  (including `--repo` existence and git-ness).
- `plugins/core/commands/new-feature-branch.md` — the command-format precedent
  this file follows (frontmatter `description` + `allowed-tools`, prose body).

# How to use

## What it does

This command is the front door for driving a tracker ticket from a link to a finished, commented, transitioned ticket. It does one job itself: it reads the ticket reference and flags you typed, checks their shape, and stops with a usage error if they are malformed. Everything real — config, access checks, triage, the fix, the writeback — happens in the agent it hands off to.

## When to use it

- You have a ticket key or a browse URL and you want the whole run driven end-to-end.
- You want to see what a run would do without any writes landing on the ticket or board.
- The ticket lives in a checkout other than the one you are sitting in.
- A scheduled routine needs a stable entry point to feed a ticket into on a headless session.

## When not to use it

- You only want to read ticket data or list what is assigned to you — use the `jira-tasks` skill.
- You already know the ticket needs a plan, not a fix attempt — the `jira-escalation` skill covers that path.
- You want to address the runner directly with your own extra context — call `@jira-pack:jira-task-runner` by name instead.

## How to invoke

```
/jira-fix <url|KEY> [--dry-run] [--repo <path>]
```

Type it in an interactive session, or feed the same line to a headless session's stdin. The invocation is identical either way.

## Inputs

- **ticket reference** — a bare key like `ABC-123`, or a browse URL ending in `/browse/<KEY>` — required.
- **`--dry-run`** — boolean flag, no value. Keeps every write (comment, transition, board card, push, PR) out of the run — optional.
- **`--repo <path>`** — path to the checkout to work in. Forwarded unchecked; the config skill decides whether it exists — optional.

## What you get back

If the reference or a flag does not parse, you get a usage error and nothing runs. If it parses, control passes to `@jira-pack:jira-task-runner` with the resolved key, the dry-run state, and the repo value, and the rest of the output — access report, triage verdict, evidence, ticket comment — comes from that agent.

## Worked example

```
/jira-fix https://<jira-base-url>/browse/ABC-123 --dry-run
```

The key `ABC-123` is extracted from the path after `/browse/` and matched against the key pattern. It passes, so the command hands `ABC-123` plus the dry-run flag to `@jira-pack:jira-task-runner`. You end up with a full triage — access check, class decision, evidence — and printed request bodies where the writes would have gone.

## Related

- `@jira-pack:jira-task-runner` — the agent behind this command; address it directly when you want to pass extra context.
- `jira-config` — the skill that validates the tracker block in project config and resolves the working repo.
- `jira-triage` — the skill that classifies the ticket and picks one of five verdicts.
