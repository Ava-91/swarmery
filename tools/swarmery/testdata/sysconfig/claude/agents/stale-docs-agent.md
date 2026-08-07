---
name: stale-docs-agent
description: Fixture agent with a generated, deliberately stale usage guide — required subsections only.
model: claude-haiku-4-5
docs:
  status: generated
  source_sha: 000000000000
  updated: 2026-08-06
---

# Role

Fixture agent whose guide was machine-generated and never reviewed. Its `source_sha` does
not match the body (`docs/system-docs-format.md` §4), so the staleness check must report
this item as stale rather than as a parse error.

# Boundaries

- Fixture boundary — keeps the agent_no_boundaries lint rule quiet.

# How to use

## What it does
Reconciles a shipped order against its line items and reports the differences, so a
mismatch is caught before the invoice goes out.

## When to use it
- A shipment closed and you want the order reconciled against it.
- An invoice is disputed and you need the line-level difference.

## How to invoke

```
@stale-docs-agent reconcile orders/line-items/1042
```

Pass the order path; the shipment is resolved from the order document.

## Worked example

```
> @stale-docs-agent reconcile orders/line-items/1042
compares 3 ordered lines against 2 shipped lines
RECONCILED | lines: 3 | mismatched: 1
```

You end up with a report naming the one line that did not ship as ordered.
