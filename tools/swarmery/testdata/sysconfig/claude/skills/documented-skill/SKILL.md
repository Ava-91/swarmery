---
name: documented-skill
description: Fixture skill whose usage guide deliberately omits the Worked example subsection.
docs:
  status: reviewed
  source_sha: 7031e8347e4e
  updated: 2026-08-06
---

# Purpose

Fixture skill for the docs contract: the guide below is complete except for
`## Worked example`, so the coverage gate must fail it on exactly one required
subsection — and its fenced bash block carries the false-heading trap from
`docs/system-docs-format.md` §5.1.

# How to use

## What it does
Renders the deployment command for an order-processing service so you can read the exact
invocation before anything runs.

## When to use it
- You are about to deploy and want the literal command in front of you first.
- You are reviewing a change and need to know what the pipeline will run.

## When not to use it
- You want the deploy to actually happen — this skill only prints the command.
- You need the rollback command instead.

## How to invoke

```bash
# deploy the thing
deploy --service orders --env <envAlias>
```

Run the printed command yourself; the skill never executes it.

## Inputs
- `service` — the service to render the command for — required.
- `env` — the environment alias — optional, defaults to the local one.

## What you get back
A fenced command block in the final message. Nothing is written to disk and nothing is
executed on your behalf.

## Related
- `documented-agent` — prefer it when you want the whole task driven, not just the command.
