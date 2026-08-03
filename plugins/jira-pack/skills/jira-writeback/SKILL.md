---
name: jira-writeback
description: "Post the run's verdict comment (rendered from a plugins/jira-pack/templates/ template) and, when the verdict allows it, transition the ticket to jira.qaStatus by exact match. Idempotent via a hidden marker; the comment always precedes the transition attempt. NOT for classifying the ticket (that's jira-triage) and NOT for the board card (that's swarmery-board-card)."
version: "0.1.0"
owner: "swarmery-core"
---

# Purpose

The only skill in `jira-pack` that calls `addCommentToJiraIssue` or
`transitionJiraIssue`. It takes `jira-triage`'s verdict and evidence bundle,
renders the matching template from `plugins/jira-pack/templates/`, posts it,
and — only when the caller says the verdict allows it — attempts the QA
transition. It never decides the verdict itself and never touches the board
card.

# Inputs (from the caller, `@jira-task-runner`)

- `verdict` — one of `already-fixed`, `cannot-reproduce`, `needs-info`,
  `needs-fix` (Phase 7) — never called for `too-large`, which Phase 7's
  `jira-escalation` handles on its own.
- The evidence bundle from `jira-triage` (command, exit code, output
  fragment, commit/test reference, or the specific questions for
  `needs-info`) — used to fill the chosen template's placeholders.
- `attemptTransition: yes | no` — `no` only for `needs-info`; `yes` for every
  other verdict this skill is called with.
- `cloudId`, `issueIdOrKey`, and the pinned Atlassian MCP tool prefix, all
  resolved by `jira-access-preflight`.
- A run tag (the board card's `externalId`, or a timestamp-less tag if the
  board is unavailable) for the idempotency marker.

# Step 1 — resolve the transition, before composing the comment

Call `getTransitionsForJiraIssue` (a **read**, always allowed, even in
`--dry-run`, even when `attemptTransition: no` — see the note at the end of
this section). Search for a transition whose target status is an **exact**
match against `jira.qaStatus` from the config: case-insensitive, leading/
trailing whitespace trimmed, and nothing fuzzier than that. The config exists
precisely so this skill never has to guess which status is "the QA one" —
a partial or fuzzy match would silently pick the wrong transition on a
workflow with several similarly-named statuses.

- **Found** → note the transition `id`; the comment will not need an extra
  line, and Step 4 will actually call `transitionJiraIssue`.
- **Not found** → the comment (composed in Step 2) gains a line naming every
  available transition, and Step 4 never fires — the ticket's status is left
  exactly as it was.

This read happens **first**, ahead of composing the comment body, because its
result decides whether the comment needs that extra "status not changed"
line. Running it first does not violate the comment-before-transition
ordering below — that ordering is about the two **write** calls
(`addCommentToJiraIssue`, `transitionJiraIssue`); a read is free to happen
whenever it is needed to make the write correct.

When `attemptTransition: no` (the `needs-info` path): still call
`getTransitionsForJiraIssue` only if the caller wants the dry-run/for-the-record
answer; in a real (non-dry-run) `needs-info` run this read is not needed at
all, since no transition of any kind will ever be attempted regardless of
what it would return — skip it to avoid an unnecessary call.

# Step 2 — compose the comment

Render the template matching the verdict (see
[Templates](#templates-plugins-jira-pack-templates)) with the evidence bundle,
plus — only if Step 1 found no match — the "status not changed" line:

```
Status not changed: no transition to "<jira.qaStatus>" exists in the current
workflow.
Available transitions: <comma-separated list of transition names>.
```

Every rendered comment ends with a hidden idempotency marker as its **literal
last line**:

```
<!-- swarmery:jira-task-runner run=<external_id or run tag> -->
```

**Language**: the same language the ticket is written in (read off
`summary`/`description`); English if that is ambiguous.

# Step 3 — idempotency check

Before posting, scan the ticket's existing comments (already fetched during
`jira-triage`'s Step 1 read — no extra call needed) for the **most recent**
comment carrying this agent's marker for this ticket. Skip the write —
recording `comment: skipped (identical verdict already posted)` — only when
**both** are true:

- a marker for this ticket is present in that most-recent agent comment, and
- that comment's rendered verdict is the **same** verdict this run just
  produced.

A marker alone is not sufficient grounds to skip: a ticket that was
`needs-info` last run and is `cannot-reproduce` this run (more information
became available, or the environment got fixed) must still get a fresh
comment — only a truly identical repeat write is what idempotency guards
against.

# Step 4 — post, in order: comment, then transition

1. **Write** the comment via `addCommentToJiraIssue`
   (`contentFormat: "markdown"`, `cloudId`, `issueIdOrKey`, `commentBody`) —
   unless Step 3 said to skip.
2. **Only then**, if `attemptTransition: yes` and Step 1 found a match, call
   `transitionJiraIssue` with `transition: {id}`.

**Why this order, specifically**: if the transition call fails after the
comment succeeded, the ticket already carries the explanation of what the run
found — a human reading it is not left guessing. The reverse order (transition
first) would risk leaving the ticket moved to a new status with no comment
explaining why, which is a worse failure mode than a transition that simply
didn't happen yet.

# Dry-run

No call to `addCommentToJiraIssue` or `transitionJiraIssue` — those are the
only two calls this mode suppresses. `getTransitionsForJiraIssue` (Step 1) and
the idempotency scan (Step 3, over already-fetched comments) still run, so the
dry-run's printed answer is honest rather than a guess. Print, instead of the
two write calls:

```
DRY-RUN jira comment <KEY>
<full rendered comment text, including the hidden marker line>
DRY-RUN jira transition <KEY> → "<jira.qaStatus>" (transition id=<id> | NOT AVAILABLE)
```

If Step 3 would have skipped the write (identical verdict already posted),
print that instead of the `DRY-RUN jira comment` line:
`would skip: identical verdict already posted`.

# Templates (`plugins/jira-pack/templates/`)

| Verdict | Template |
|---|---|
| `already-fixed` | `comment-already-fixed.md` |
| `cannot-reproduce` | `comment-cannot-reproduce.md` |
| `needs-info` | `comment-needs-info.md` |
| `needs-fix` (Phase 7) | `comment-fix-summary.md` |
| `too-large` (Phase 7, posted by `jira-escalation`, not this skill) | `comment-too-large.md` |

Every template ships with placeholders only — no real ticket keys, hostnames,
or team names (`docs/NEUTRALITY.md`).

# Placeholders / neutrality

Any example this skill's own report prints uses only `<jira-base-url>`-style
hosts, `<KEY>`, and `<PROJECT-KEY>` — `scripts/scan-flavor.sh` must stay
`✓ clean` for `plugins/**`.

# Self-check before returning

- [ ] `getTransitionsForJiraIssue` ran before the comment body was finalized,
      whenever a transition might be attempted
- [ ] The rendered comment ends with the hidden marker as its literal last
      line
- [ ] The idempotency check compared **both** marker presence and verdict
      match — never skipped on marker presence alone
- [ ] `addCommentToJiraIssue` was called (or explicitly skipped, with a
      recorded reason) before any `transitionJiraIssue` call in the same run
- [ ] `needs-info` calls never resulted in a `transitionJiraIssue` call
- [ ] Dry-run made zero calls to `addCommentToJiraIssue`/`transitionJiraIssue`

# Common mistakes to avoid

- Calling `transitionJiraIssue` before `addCommentToJiraIssue` "to save a
  round trip" — never; the ordering exists specifically for the failure case.
- Fuzzy-matching `jira.qaStatus` ("QA" matching "QA Review") — exact,
  case-insensitive, trimmed, nothing looser.
- Skipping a comment merely because *any* marker is present, without checking
  the verdict it recorded matches this run's verdict.
- Posting the "status not changed" line even when a matching transition was
  found (it belongs only in the not-found branch).

# Related

- `plugins/jira-pack/skills/jira-triage/SKILL.md` — produces the verdict and
  evidence bundle this skill renders and posts.
- `plugins/jira-pack/skills/jira-config/SKILL.md` — source of `jira.qaStatus`.
- `plugins/jira-pack/skills/jira-access-preflight/SKILL.md` — source of the
  pinned Atlassian MCP tool prefix and `cloudId`.
- `plugins/jira-pack/skills/swarmery-board-card/SKILL.md` — moves the board
  card after this skill completes; this skill never calls the board API
  itself.
- `plugins/jira-pack/templates/` — the five comment templates this skill
  renders from.
