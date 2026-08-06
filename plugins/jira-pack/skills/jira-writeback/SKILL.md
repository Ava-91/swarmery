---
name: jira-writeback
description: "Post the run's verdict comment (rendered from the plugins/jira-pack/templates/ template matching verdict + ticket class) and, when the verdict allows it, transition the ticket to jira.qaStatus by exact match. Idempotent via a hidden marker carrying verdict and class; the comment always precedes the transition attempt. NOT for classifying the ticket (that's jira-triage) and NOT for the board card (that's swarmery-board-card)."
version: "0.2.0"
owner: "swarmery-core"
docs:
  status: reviewed
  source_sha: abec8fdeeb3c
  updated: 2026-08-06
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
- `class` — `defect` or `change`, from `jira-triage`. It selects the template
  for `needs-fix` (see [Templates](#templates-plugins-jira-pack-templates)) and
  is recorded in the idempotency marker. A `needs-fix` call that arrives
  without a class is a caller bug: stop and report it rather than defaulting to
  `defect`, which would post a root-cause comment on a feature ticket.
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
last line**, and the marker carries this run's verdict so a later run can
decide whether to skip **without** re-deriving the verdict from the comment's
rendered prose:

```
<!-- swarmery:jira-task-runner run=<external_id or run tag> verdict=<already-fixed|cannot-reproduce|needs-fix|needs-info|too-large> class=<defect|change> -->
```

`class` is recorded alongside the verdict because the two together are what a
later run needs to decide whether anything changed: the same ticket can
legitimately move from `needs-info` (criteria weren't testable) to `needs-fix`
(they are now), and a marker carrying only the verdict cannot distinguish a
re-run of the same conclusion from a genuinely new one on a re-classified
ticket.

**Language**: the same language the ticket is written in (read off
`summary`/`description`); English if that is ambiguous.

# Step 3 — idempotency check

Before posting, scan the ticket's existing comments (already fetched during
`jira-triage`'s Step 1 read — no extra call needed) for markers matching this
agent's tag, `<!-- swarmery:jira-task-runner run=<tag> verdict=<slug> -->`. If
more than one such marker exists on the ticket, take the **most recent** one —
never an older one. The skip decision is read straight off that marker's
`verdict` field — never reconstructed by reading the surrounding comment
prose:

- **No marker with this run's tag** (first run, or the board card is new) →
  write.
- **Marker found, `verdict` differs from this run's verdict** — e.g. the most
  recent marker carries `verdict=needs-info` and this run just produced
  `cannot-reproduce` (more information became available, or the environment
  got fixed) → write; only a truly identical repeat write is what idempotency
  guards against.
- **Marker found, `verdict` matches this run's verdict** → skip the write,
  recording `comment: skipped (identical verdict already posted)`.

Use the exact verdict slugs `jira-triage` assigns: `already-fixed`,
`cannot-reproduce`, `needs-fix`, `needs-info`, `too-large` — never a
paraphrase, so the marker's `verdict` field always matches one of the five.

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

| Verdict | Class | Template |
|---|---|---|
| `already-fixed` | either | `comment-already-fixed.md` |
| `cannot-reproduce` | `defect` only (a `change` ticket can never carry this verdict — `jira-triage`) | `comment-cannot-reproduce.md` |
| `needs-info` | either | `comment-needs-info.md` |
| `needs-fix` | `defect` | `comment-fix-summary.md` |
| `needs-fix` | `change` | `comment-change-summary.md` |
| `too-large` (posted by `jira-escalation`, not this skill) | either | `comment-too-large.md` |

The `needs-fix` split is the whole reason `class` is an input: the defect
template opens with a root cause, which a change ticket does not have, and the
change template pairs each acceptance criterion with the test that now asserts
it, which a defect fix has no list for. Rendering the wrong one produces a
comment whose first line is a fabrication.

**A `cannot-reproduce` call for a `class: change` ticket is a caller bug** —
stop and report it instead of rendering the template. `jira-triage` cannot
produce that combination; if it arrives here, something upstream reclassified
or lost the class, and posting it would move unimplemented work to QA.

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
      line, carrying both `verdict=` and `class=`
- [ ] The template was selected by verdict **and** class — `needs-fix` on a
      `change` ticket rendered `comment-change-summary.md`, never the
      root-cause-shaped `comment-fix-summary.md`
- [ ] No `cannot-reproduce` comment was rendered for a `class: change` ticket
- [ ] The idempotency check read the verdict off the most recent marker's
      `verdict` field — never skipped on marker presence alone, and never
      guessed the prior verdict from the comment's rendered prose
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
  its `verdict` field matches this run's verdict — and never inferring that
  prior verdict from the comment's rendered text instead of the marker.
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

# How to use

## What it does

This skill is the only thing in the pack that writes to a ticket. You hand it a verdict and the evidence behind it, and it renders the matching comment template, posts it, and — when the verdict allows — moves the ticket to your configured QA status. It never decides the verdict itself, and it never touches the board card.

## When to use it

- A triage step has produced a verdict and evidence bundle, and the ticket now needs that result written back as a comment.
- The run finished successfully and the ticket should move to the configured QA status.
- You want a rerun on the same ticket to stay safe: an identical verdict posts nothing twice.
- You are checking a run end to end and want the dry-run output of exactly what would be posted.

## When not to use it

- To decide whether a ticket is already fixed, needs a fix, or needs more information — use `jira-triage`.
- To move the tracking card on the board after the comment lands — use `swarmery-board-card`.
- To post the over-budget plan comment on a `too-large` ticket — use `jira-escalation`.
- To find the QA status name or the connection details — use `jira-config` and `jira-access-preflight`.

## How to invoke

```
Skill(skill: "jira-pack:jira-writeback")
```

Call it once per run, after triage has finished. It is normally invoked by the task runner rather than typed by hand.

## Inputs

- `verdict` — one of `already-fixed`, `cannot-reproduce`, `needs-info`, `needs-fix` — required.
- `class` — `defect` or `change` — required; it picks which `needs-fix` template renders.
- Evidence bundle — the command, exit code, output fragment, commit or test reference, or the questions to ask — required.
- `attemptTransition` — `yes` or `no`; `no` only for `needs-info` — required.
- `cloudId`, ticket key, and the pinned tool prefix — required, all resolved by the preflight skill.
- Run tag — the identifier written into the hidden idempotency marker — required.

## What you get back

A comment on the ticket, written in the ticket's own language, ending with a hidden marker that records the verdict and class. If no transition matching your QA status exists, the comment names every transition that does exist and the status is left alone. The comment is always written before the transition is attempted, so a failed move never leaves an unexplained status change. In dry-run mode nothing is written — you get the full rendered comment and the transition decision printed instead.

## Worked example

```
Skill(skill: "jira-pack:jira-writeback")
verdict: cannot-reproduce, class: defect, attemptTransition: yes, key: <KEY>

→ reads available transitions, finds one matching the configured QA status
→ renders comment-cannot-reproduce.md with the repro command and exit code
→ no prior marker for this run tag, so it posts the comment
→ then transitions <KEY> to the QA status
```

You end up with one comment carrying the evidence and a hidden marker, and the ticket sitting in QA. Rerun it with the same verdict and it reports `comment: skipped (identical verdict already posted)`.

## Related

- `jira-triage` — produces the verdict and evidence this skill renders.
- `jira-escalation` — handles the `too-large` verdict, which this skill never receives.
- `swarmery-board-card` — moves the board card once this skill has finished.
- `jira-config` — supplies the QA status name matched here.
