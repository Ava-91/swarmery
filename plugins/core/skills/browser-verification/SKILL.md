---
name: browser-verification
description: "Verify UI behavior in a live browser via Playwright MCP (navigate, snapshot, screenshots, console/network) against localdev or staging. NOT for full domain E2E flows and never against production."
version: "1.0.0"
owner: "swarmery-core"
color: cyan
docs:
  status: reviewed
  source_sha: 0ddbb7f8ca8e
  updated: 2026-08-06
---

# Purpose

Canonical procedure for verifying UI behavior in a real browser through the Playwright MCP tools (`mcp__plugin_playwright_playwright__browser_*`). Extracted from duplicated agent sections — @tech-lead, @ui-developer, @verification-agent, and @code-reviewer reference this skill and keep only their role-specific invariants.

# Guardrails (never violate)

- Confirm a live target first: `browser_navigate`, then verify the response — never assume a URL is up.
- Snapshot before acting — never act on assumed DOM state.
- Use throwaway/seed data; never mutate real records.
- `browser_run_code_unsafe` / `browser_evaluate` run arbitrary JS in the page — authorized local/staging targets only, **never a production origin**.
- Always `browser_close` when finished to release the browser session.
- A browser check confirms behavior; it does not replace the automated test suite or the quality gate. Read-only verifiers keep findings supplementary and warning-level — they never flip a deterministic PASS/FAIL verdict.

# Resources

- Read `resources/verification-procedure.md` when running a verification — target discovery, the core loop, the observation-only variant, domain E2E routing, inputs, and related skills.

# How to use

## What it does

Gives you one repeatable procedure for checking UI behavior in a real browser: navigate to the page, take an accessibility snapshot, drive the flow, and capture screenshots, console errors, and network failures as evidence. It also fixes the safety rules — snapshot before acting, seed data only, never production.

## When to use it

- You changed a component or page and want to see it render and behave live.
- A bug only shows up at runtime — hydration errors, failed API calls, broken responsive layout — that build and type-check never catch.
- You need visual evidence (screenshot, console log, network trace) for a review or verification report.
- You are a read-only verifier and want a supplementary warning-level signal alongside the deterministic test verdict.

NOT for full domain E2E lifecycle flows (use the domain pack's E2E skill), pixel-diff comparison against a design (use the design pack's verification skill), or anything pointed at production.

## How to invoke

```
Skill(skill: "core:browser-verification")
```

Invoke it before you open the browser tools, then follow the core loop in `resources/verification-procedure.md`.

## Worked example

Request: "Check that the line-items table on orders/line-items renders and sorting works at http://localhost:3000."

What happens: navigate → snapshot the accessibility tree → click the sort header using the ref the snapshot returned → snapshot again → screenshot → read console messages and network requests → close the browser. You end up with a before/after screenshot pair, a clean console (or the exact hydration error), and confirmation the sort request returned 200.
