---
name: jira-access-preflight
description: "Provider-agnostic Jira access verification -- the first unconditional step of every jira-pack run. Resolves the Atlassian MCP tools by NAME (never by channel prefix), pins the discovered prefix for the run, resolves cloudId, and smoke-reads the target ticket. Any failed step stops the run with a JIRA ACCESS: FAILED report before any write call is made. NOT for reading/writing ticket content itself (that's jira-tasks / the write tools) and NOT for validating the jira config block (that's jira-config)."
version: "0.1.0"
owner: "swarmery-core"
docs:
  status: generated
  source_sha: 6c6c80d7bc0f
  updated: 2026-08-06
---

# Purpose

Make "can this run actually reach Jira" the first unconditional check of any jira-pack flow,
before `jira-tasks` reads anything or `/jira-fix` writes anything. If access is broken, the run
must stop with a report that explains exactly what's missing and how to fix it -- never
proceed on a partial or guessed capability set, and never let a write tool fire when a read
tool already failed.

# Why the tool prefix can't be hardcoded

At least two channels can expose the same Atlassian MCP tools, under **different** prefixes,
with **identical** tool names:

- an officially-installed Atlassian MCP plugin, e.g. tools registered under a
  `mcp__plugin_atlassian_atlassian__*`-shaped prefix
- a claude.ai connector for Atlassian/Rovo, e.g. tools registered under a
  `mcp__claude_ai_Atlassian_Rovo__*`-shaped prefix

Those two are **illustrative examples of the shape**, not the two values this skill expects --
a host may register either one, both at once, neither, or a differently-named channel entirely,
and which channel (if any) is live changes between sessions and between machines. A skill that
hardcodes one prefix will report "no access" against a perfectly reachable Jira the moment the
*other* channel is the one that's live -- exactly the bug `plugins/core/skills/jira-tasks/SKILL.md`
carried in its own narrower read-only tool-loading example until Phase 8 de-hardcoded it to the
same by-name resolution this skill uses. This skill never assumes a specific prefix is present;
it resolves by tool name and treats whatever prefix comes back as the answer.

# When to use

Run this skill's four steps, in order, as the unconditional first action of any jira-pack
run -- before `jira-tasks` performs a read and before `/jira-fix` (Phase 6) attempts any write.
Every step must pass before the run is allowed to touch a real ticket. A step failing at any
point stops the run immediately with the `JIRA ACCESS: FAILED` report in
[Failure report](#failure-report) -- there is no partial-access mode.

# Step 1 -- resolve tools by name, then pin the prefix

Load the Atlassian MCP tool schemas via `ToolSearch`, searching by the tool's **name**, not by
guessing a prefix:

```
ToolSearch  query: "+jira getTransitionsForJiraIssue transitionJiraIssue addCommentToJiraIssue"
ToolSearch  query: "+atlassian getAccessibleAtlassianResources getJiraIssue"
```

The five tools this run cannot proceed without:

| Tool | Why it's required |
|------|--------------------|
| `getAccessibleAtlassianResources` | resolves `cloudId` (Step 3) and backs the priority rule (Step 2) |
| `getJiraIssue` | reads the ticket -- title, status, description, comments |
| `getTransitionsForJiraIssue` | finds the QA transition the run will move the ticket to |
| `transitionJiraIssue` | performs that move |
| `addCommentToJiraIssue` | posts the run's verdict comment |

Missing **any one** of the five is "no access," even when the other four resolve cleanly. A
run that can read a ticket but can't comment on it would reach the end of its work and silently
write nothing -- worse than failing loudly up front, because nobody would know to look.

From the ToolSearch results, take the full tool names returned and read off the shared prefix
(everything up to and including the final `__` before the method name -- e.g. the prefix in
`mcp__plugin_atlassian_atlassian__getJiraIssue` is `mcp__plugin_atlassian_atlassian__`; the
prefix in `mcp__claude_ai_Atlassian_Rovo__getJiraIssue` is `mcp__claude_ai_Atlassian_Rovo__`;
any other prefix ToolSearch resolves is read off the same way). **Pin that prefix for the rest
of the run.** Every subsequent tool call in Steps 2-4, and every call `jira-tasks` or `/jira-fix`
makes downstream, uses only that prefix. Never call a tool under a second prefix mid-run even
if both channels are online simultaneously -- a comment and a transition issued from two
different channels can land under two different accounts, which is a worse failure mode than
simply picking one and staying on it.

If ToolSearch resolves zero of the five names under any prefix, that is the access failure --
report it as "tool resolution" in Step 1 of the failure report, not as a generic error.

# Step 2 -- priority rule when two channels are both live

If tool names resolve under **two different prefixes** at once (both channels active this
session):

1. Call `getAccessibleAtlassianResources` once per candidate prefix.
2. Compare each response's resource URL against `jira.baseUrl` (from `jira-config`, see
   [Related](#related)). The prefix whose resource matches `jira.baseUrl` wins and is pinned.
3. If **both** match (or the comparison can't distinguish them), the officially-installed
   plugin channel wins over the claude.ai connector -- it's the project's explicit, declared
   configuration, not a personal claude.ai account, so it's the more predictable choice when
   both are equally valid.

The chosen provider prefix is always printed -- in this skill's own report, and threaded into
the board card's `prompt` field so a downstream reader can see which channel a run actually
used without re-deriving it.

# Step 3 -- resolve cloudId

Call `getAccessibleAtlassianResources` (no arguments) under the pinned prefix. Take the `id` of
the resource whose URL matches `jira.baseUrl`. If nothing matches, that is a distinct failure
from "Jira is unreachable" -- report it as "the token can see other sites, but not
`<jira-base-url>`" (naming the sites it *can* see), since the fix (re-scope the token / grant
access to this site) is different from "no access at all."

If any later call in Step 4 (or in the run downstream) 404s, re-resolve `cloudId` exactly once
-- a site migration or token re-scope can invalidate a previously-resolved id mid-run -- and
only give up after that single retry also 404s.

# Step 4 -- smoke-read the target ticket

Call `getJiraIssue` for the run's target ticket with
`fields: ["summary", "status", "description", "comment"]`. This is the last gate: success here
is the **only** condition under which the run is allowed to proceed past preflight. Any other
outcome (404, permission error, timeout) is a Step 4 failure in the report below.

# Failure report

Any failed step stops the run immediately -- this is a normal, expected outcome of preflight,
not an exceptional crash path. Print exactly this shape:

```
JIRA ACCESS: FAILED
Step that failed:      <tool resolution | priority rule | cloudId | ticket read>
What's unavailable:     <missing tool names / sites the token can see instead>
Consequence:            no comment, transition, branch, or PR was created.

How to enable access:
  Option A -- official Atlassian MCP plugin:
    1. Enable it in enabledPlugins ("atlassian@<marketplace>": true).
    2. Confirm .mcp.json points at the plugin's Atlassian MCP endpoint.
    3. Authorize once in an interactive Claude Code session (/mcp).
  Option B -- claude.ai Atlassian/Rovo connector:
    1. Enable the Atlassian/Rovo connector in claude.ai connector settings.
    2. Confirm it appears as an available MCP server for this session.
  Headless limitation: the OAuth flow needs an interactive session -- it cannot
  run inside a headless/non-interactive run. Authorize once, in an interactive
  Claude Code session (/mcp) or in claude.ai connector settings, then re-run
  /jira-fix.
```

Fill in the two blank-style lines (`Step that failed`, `What's unavailable`) with the specific
detail from whichever step failed -- e.g. "tool resolution" with the exact tool names that
never resolved under any prefix, or "cloudId" with the sites the token actually has access to.
The `Consequence` line is always the literal guarantee above: it is true precisely because
every write tool (`transitionJiraIssue`, `addCommentToJiraIssue`, and any branch/PR step
downstream) is gated behind all four preflight steps passing first.

See `references/setup.md` for the full enablement walkthrough of both channels, how to verify
access once enabled, the headless-OAuth limitation in more detail, and the common failure
signatures (stale `cloudId`, token missing project access, two channels with different
accounts).

# Related

- `plugins/jira-pack/skills/jira-config/SKILL.md` -- resolves and validates `jira.baseUrl` and
  the rest of the `jira` config block; this skill consumes `jira.baseUrl`, it does not
  re-validate the config block itself.
- `plugins/core/skills/jira-tasks/SKILL.md` -- read-only ticket queries once a run is past
  preflight; resolves tools by name the same way this skill does (Phase 8 de-hardcoded its
  tool-loading example, which used to pin one prefix -- exactly the failure mode this skill's
  own by-name resolution exists to avoid for jira-pack's autonomous run).
- `plugins/core/skills/troubleshooting/SKILL.md` -- the diagnostic-report style (structured
  failure block, explicit "what's missing" + "how to fix" shape) this skill's failure report
  follows.
- `references/setup.md` -- step-by-step enablement for both channels.

# How to use

## What it does

This skill checks that a run can actually reach your tracker before it touches a real ticket. It finds the Atlassian MCP tools by name instead of guessing which channel registered them, locks onto the prefix it found, resolves the site id, and reads the target ticket once. If any of that fails, the run stops with a report that names the broken step and how to fix it — no half-working run that reads a ticket but silently fails to comment on it.

## When to use it

- You are starting any jira-pack run and have not yet verified access this session.
- Two tracker channels may be registered at once (a plugin and a personal connector), and you need one of them pinned for the whole run.
- A run failed with a permissions or 404 error and you want to know whether the token can see the site at all.
- You are writing a flow that will comment on or transition a ticket, and you want every write gated behind a passing read.

## When not to use it

- You want to read ticket content — use the `jira-tasks` skill once preflight has passed.
- You want to check the `jira` config block for missing keys — use the `jira-config` skill.
- You want to post a verdict or move a ticket — use `jira-writeback`.

## How to invoke

```
Skill(skill: "jira-pack:jira-access-preflight")
```

Run it as the unconditional first action of a jira-pack run, before any read and long before any write.

## Inputs

- Target ticket key — the ticket the run is about — required for the smoke read in Step 4.
- `jira.baseUrl` — the site URL from the config block — required, used to pick the right channel and resolve the site id.

## What you get back

On success: a pinned tool prefix and a resolved site id that every later call in the run reuses, plus the ticket's summary, status, description, and comments. The chosen channel is printed so a reader can see which one the run used. On failure: a `JIRA ACCESS: FAILED` block naming the failed step, what is unavailable, the guarantee that nothing was written, and enablement steps for both channels.

## Worked example

```
Skill(skill: "jira-pack:jira-access-preflight")

→ resolves 5 tool names; one prefix found, pinned for the run
→ site id resolved from the resource whose URL matches jira.baseUrl
→ ticket read: summary, status, description, comments
→ preflight passed; the run continues to triage
```

If the fifth tool had not resolved, you would get the failure block instead and nothing downstream would run.

## Related

- `jira-config` — prefer it when the question is whether the config block itself is valid.
- `jira-tasks` — read-only ticket queries after preflight has passed.
- `troubleshooting` — the diagnostic-report style this skill's failure block follows.
