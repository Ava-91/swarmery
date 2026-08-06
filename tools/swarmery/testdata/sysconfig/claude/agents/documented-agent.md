---
name: documented-agent
description: Fixture agent carrying a complete usage guide — all eight subsections, reviewed provenance.
model: claude-fable-5
docs:
  status: reviewed
  source_sha: bf1f17459cf5
  updated: 2026-08-06
---

# Role

Fixture agent for the docs contract (see `docs/system-docs-format.md`): the body is
deliberately short, and everything the docs scanner cares about lives in the
`# How to use` block appended at the end.

# Boundaries

- Fixture boundary — keeps the agent_no_boundaries lint rule quiet.

# How to use

## What it does
Turns a batch of order line items into a priced order, so you never have to sum line
totals by hand or re-derive the total when one line changes.

## When to use it
- A caller sent line items and you need one priced order back.
- A line item changed and the order total has to be recomputed.
- You want the per-line price breakdown, not only the final number.

## When not to use it
- You only need to validate the payload shape — reach for the contract validator.
- You are persisting the order — this agent never writes to the database.

## How to invoke

```
@documented-agent price the order in orders/line-items/1042
```

Pass the order path; everything else is read from the order document itself.

## Inputs
- `order_path` — path to the order document — required.
- `currency` — ISO 4217 code used for rounding — optional, defaults to the order's own.

## What you get back
A priced order written back to the same path, plus a final message of the shape
`PRICED | lines: N | total: X`. No other side effects.

## Worked example

```
> @documented-agent price the order in orders/line-items/1042
reads 3 line items, applies the order's currency, writes the priced order back
PRICED | lines: 3 | total: 148.20
```

You end up with the same document, now carrying a total and a per-line breakdown.

## Related
- `stale-docs-agent` — the same shape with only the required subsections documented.
- `documented-skill` — prefer it when you want the command without an agent turn.
