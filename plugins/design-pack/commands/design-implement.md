---
description: Thin entry point for `/design-implement <export-path|url> [--route <path>] [--viewport WxH] [--from-screenshots] [--dry-run]` — parses and validates the argument shape only, then hands control to the design-implement skill. No run logic lives here.
allowed-tools:
  - Bash
---

# /design-implement — turn a design handoff into an implemented screen

## Usage

```
/design-implement <export-path|url> [--route <path>] [--viewport WxH] [--from-screenshots] [--dry-run]
```

Examples:

- `/design-implement ./handoff/checkout.html --route /checkout`
- `/design-implement ./handoff/checkout.zip --route /checkout --viewport 1440x900`
- `/design-implement https://<design-host>/share/<id> --route /checkout --dry-run`
- `/design-implement ./handoff/shots/ --route /checkout --from-screenshots`

This command is invoked the same way from an interactive session and from the
`ai-prompt` step of a scheduled routine, where the prompt arrives on a headless
session's stdin and nobody is there to address an agent by name. That is the
whole reason a command exists alongside `@design-implementer`: a stable entry
point that does not depend on an agent being named correctly in free text (the
same rationale as `plugins/jira-pack/commands/jira-fix.md:21-25`).

## What it does — and does not do

This is a **thin proxy**. It parses `$ARGUMENTS`, validates the *shape* of the
positional argument and the flags, and delegates everything else to the
`design-implement` skill (`plugins/design-pack/skills/design-implement/SKILL.md`),
which owns all of Phases 0-6: prerequisites, acquire, ground truth, recon, plan,
implement, verify.

In particular, this command does **not**:
- read, unzip, or parse the export — the skill's Phase 1 (`design-acquire`) does;
- check that `.claude/project.json` carries a `design` block, or that its
  `tokensFile` / `componentsRoot` / `routesRoot` paths exist — the skill's
  Phase 0 does, and prints the paste-ready fragment when they are missing;
- start `design.devCommand`, screenshot anything, compute a diff, or decide
  what to change — the skill's Phases 3-6 and `@design-implementer` own that.

If an explanation of **how** to diff, which region to fix first, or what counts
as a passing screen ever appears in this file, it leaked out of the skill and
belongs back there.

Argument parsing failure → print the usage error and stop. Never start the
skill on an unparseable argument.

## Argument parsing

1. **`<export-path|url>`** (first positional, required) — exactly one of:
   - a filesystem path ending in `.html` or `.zip`, or a directory — and it
     must exist;
   - an `http(s)` URL that parses.

   Missing or empty → print usage and stop.

2. **`--route <path>`** — optional value flag; must start with `/` (`^/`).
   Forwarded verbatim: whether that route exists under `design.routesRoot` is
   the skill's question, not this command's.

3. **`--viewport <WxH>`** — optional value flag; must match
   `^[0-9]{3,5}x[0-9]{3,5}$`. Omitted → the skill takes the authoring viewport
   from the export.

4. **`--from-screenshots`** — boolean flag declaring the degraded mode where
   only screenshots exist. **Incompatible** with a `.html` or `.zip` positional
   argument: markup means the run has ground truth, and the degraded mode would
   discard it. That combination → error naming both arguments, and stop.

5. **`--dry-run`** — boolean flag. It means *stop at the plan*, not *do
   nothing*: Phases 1-3 (acquire, ground truth, recon) still run, because they
   are what produces the Phase 4 plan and its approved file list. The run
   prints that plan and stops **before** `@design-implementer` is invoked — so
   no project file is written and no fix is attempted.

Only regex-level shape checks need Bash — no network calls and no reads of the
export belong here:

```bash
echo "$VIEWPORT" | grep -qE '^[0-9]{3,5}x[0-9]{3,5}$' || echo "usage: --viewport WxH, e.g. 1440x900"
```

## Delegation

Once the argument and flags parse cleanly, hand control to the
`design-implement` skill with: the positional argument exactly as given,
`--route`, `--viewport`, and the two flag states. The skill re-derives
everything else from the `design` block of `.claude/project.json` and never
trusts this command's parsing beyond "here is an export-shaped argument and
here are the flags."

## Related

- `plugins/design-pack/skills/design-implement/SKILL.md` — the six phases; the
  operator approves the Phase 4 plan before any project file is written.
- `plugins/design-pack/agents/design-implementer.md` — the executor the skill
  invokes for Phases 5-6, with its autonomy contract and eight STOP triggers.
- `plugins/jira-pack/commands/jira-fix.md` — the thin-proxy command format this
  file follows.
