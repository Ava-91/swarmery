---
name: browser-verification
description: "Verify UI behavior in a live browser via Playwright MCP (navigate, snapshot, screenshots, console/network) against localdev or staging. NOT for full domain E2E flows and never against production."
version: "1.0.0"
owner: "swarmery-core"
color: cyan
docs:
  status: generated
  source_sha: 0ddbb7f8ca8e
  updated: 2026-08-06
---

# Purpose

Canonical procedure for verifying UI behavior in a real browser through the Playwright MCP tools (`mcp__plugin_playwright_playwright__browser_*`). Extracted 2026-06-10 from duplicated sections in @tech-lead, @react-specialist, @verification-agent, and @quality-checker — those agents now reference this skill and keep only their role-specific invariants.

# Step 0 — confirm a live target

The main app's dev server (project.json -> `mainApp`) typically runs at `http://localhost:3000` (`npm run dev`); a locally deployed cluster stack has its own ingress hostname (e.g., `https://d16.local`); post-deploy checks use the staging environment's URL (project.json -> `cloud.envAlias`). Never assume a URL is up — `browser_navigate` first, then verify the response.

# Core loop (interactive verification)

1. `browser_navigate` to the page under test.
2. `browser_snapshot` — capture the accessibility tree and act on the element refs it returns (more reliable than guessing CSS selectors; prefer `data-testid`).
3. Drive the flow as needed: `browser_click`, `browser_type`, `browser_fill_form`, `browser_select_option`, `browser_press_key`, `browser_hover`.
4. Capture evidence: `browser_take_screenshot` (visual state), `browser_console_messages` (runtime/hydration errors the build won't catch), `browser_network_requests` (failed/slow calls). Use `browser_resize` to check responsive breakpoints.

# Observation-only variant (report-only agents)

Read-only verifiers (@verification-agent, @quality-checker) restrict themselves to navigate + snapshot + screenshot + console/network capture, with at most the minimal `browser_click`/`browser_type` required to reach the state under test. Browser findings are supplementary, warning-level signal — they never flip a deterministic PASS/FAIL verdict.

# Guardrails (apply to every agent)

- Snapshot before acting — never act on assumed DOM state.
- Use throwaway/seed data; never mutate real records.
- `browser_run_code_unsafe` / `browser_evaluate` run arbitrary JS in the page — authorized local/staging targets only, **never a production origin**.
- Always `browser_close` when finished to release the browser session.
- A browser check confirms behavior; it does not replace the automated test suite or the Phase 5 quality gate.

# Domain E2E flows

For driving a full domain lifecycle flow through the UI (create/start/verify an entity end-to-end), do NOT improvise with the core loop — load the domain pack's E2E skill if the project ships one (canonical wizard + state-machine transitions + cleanup). Default target localdev only.

# How to use

## What it does

This skill gives you one repeatable procedure for checking UI behavior in a real browser through the Playwright MCP tools. Instead of guessing whether a change actually renders and works, you navigate to the page, take an accessibility snapshot, drive the flow, and capture screenshots, console errors, and network failures as evidence. It also fixes the safety rules — snapshot before acting, seed data only, never a production origin.

## When to use it

- You changed a component or page and want to see it render and behave in a live browser.
- A bug only shows up at runtime — hydration errors, failed API calls, broken responsive layout — that the build and type-check never catch.
- You need visual evidence (screenshot, console log, network trace) attached to a review or a verification report.
- You are a read-only verifier and want a supplementary, warning-level signal alongside the deterministic test verdict.

## When not to use it

- Driving a full domain lifecycle end-to-end through the UI — load the domain pack's E2E skill instead, which ships the canonical wizard and cleanup steps.
- Replacing the automated test suite or the quality gate — a browser check confirms behavior, it does not substitute for tests.
- Anything pointed at a production origin — there is no safe variant of this skill against real data.
- Comparing a screen against a design reference pixel by pixel — use the design pack's verification skill.

## How to invoke

```
Skill(skill: "core:browser-verification")
```

Invoke it before you open the browser tools, then follow the core loop it lays out.

## Inputs

- Target URL — the page under test, on a local dev server or a staging environment — required. Confirm it is actually up by navigating first; never assume.
- Flow to drive — the clicks, typing, and form fills needed to reach the state you care about — optional for a plain render check.
- Mode — interactive (full drive) or observation-only (navigate, snapshot, capture) — optional, defaults to interactive.

## What you get back

A worked verification sequence plus the evidence you captured: screenshots of the visual state, console messages showing runtime and hydration errors, and the network request list with failed or slow calls. Nothing is written to the repository. The browser session stays open until you close it, so close it when you finish.

## Worked example

```
Skill(skill: "core:browser-verification")

Request: "Check that the line-items table on orders/line-items renders and
sorting works at http://localhost:3000."

What happens: navigate to the page → snapshot the accessibility tree →
click the sort header using the ref the snapshot returned → snapshot again →
screenshot → read console messages and network requests → close the browser.

You end up with: a before/after screenshot pair, a clean console (or the
exact hydration error), and confirmation that the sort request returned 200.
```

## Related

- The design pack's `design-verify` skill — prefer it when you have a design export and need a measured pixel diff rather than a behavioral check.
- A domain pack's E2E flow skill — prefer it for full create-to-cleanup lifecycle runs through a wizard.
- `core:testing` — prefer it when the check belongs in the automated suite rather than in a one-off browser session.
