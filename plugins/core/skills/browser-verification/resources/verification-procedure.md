# Browser verification — full procedure

## Step 0 — confirm a live target

The main app's dev server (project.json -> `mainApp`) typically runs at
`http://localhost:3000` (`npm run dev`); a locally deployed cluster stack has its
own ingress hostname (e.g., `https://d16.local`); post-deploy checks use the
staging environment's URL (project.json -> `cloud.envAlias`). Never assume a URL
is up — `browser_navigate` first, then verify the response.

## Core loop (interactive verification)

1. `browser_navigate` to the page under test.
2. `browser_snapshot` — capture the accessibility tree and act on the element
   refs it returns (more reliable than guessing CSS selectors; prefer
   `data-testid`).
3. Drive the flow as needed: `browser_click`, `browser_type`,
   `browser_fill_form`, `browser_select_option`, `browser_press_key`,
   `browser_hover`.
4. Capture evidence: `browser_take_screenshot` (visual state),
   `browser_console_messages` (runtime/hydration errors the build won't catch),
   `browser_network_requests` (failed/slow calls). Use `browser_resize` to check
   responsive breakpoints.

## Observation-only variant (report-only agents)

Read-only verifiers (@verification-agent, @code-reviewer) restrict themselves to
navigate + snapshot + screenshot + console/network capture, with at most the
minimal `browser_click`/`browser_type` required to reach the state under test.
Browser findings are supplementary, warning-level signal — they never flip a
deterministic PASS/FAIL verdict.

## Domain E2E flows

For driving a full domain lifecycle flow through the UI (create/start/verify an
entity end-to-end), do NOT improvise with the core loop — load the domain pack's
E2E skill if the project ships one (canonical wizard + state-machine transitions
+ cleanup). Default target localdev only.

## When not to use this skill

- Driving a full domain lifecycle end-to-end through the UI — load the domain
  pack's E2E skill instead, which ships the canonical wizard and cleanup steps.
- Replacing the automated test suite or the quality gate — a browser check
  confirms behavior, it does not substitute for tests.
- Anything pointed at a production origin — there is no safe variant of this
  skill against real data.
- Comparing a screen against a design reference pixel by pixel — use the design
  pack's verification skill.

## Inputs

- Target URL — the page under test, on a local dev server or a staging
  environment — required. Confirm it is actually up by navigating first; never
  assume.
- Flow to drive — the clicks, typing, and form fills needed to reach the state
  you care about — optional for a plain render check.
- Mode — interactive (full drive) or observation-only (navigate, snapshot,
  capture) — optional, defaults to interactive.

## What you get back

A worked verification sequence plus the evidence you captured: screenshots of
the visual state, console messages showing runtime and hydration errors, and the
network request list with failed or slow calls. Nothing is written to the
repository. The browser session stays open until you close it, so close it when
you finish.

## Related

- The design pack's `design-verify` skill — prefer it when you have a design
  export and need a measured pixel diff rather than a behavioral check.
- A domain pack's E2E flow skill — prefer it for full create-to-cleanup
  lifecycle runs through a wizard.
- `core:testing` — prefer it when the check belongs in the automated suite
  rather than in a one-off browser session.
