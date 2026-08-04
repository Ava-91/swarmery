---
name: design-verify
description: "Measure an implemented screen against its design export with a headless pixel diff and report the regions, the number and the side-by-side image. NOT for choosing what to build (that's design-implement), NOT for resolving the design input (that's design-acquire), and NOT for behavioural UI checks without a reference design (that's browser-verification in core)."
version: "0.1.0"
owner: "swarmery-core"
---

# Purpose

Phase 6 of `design-implement`, and the only place the measurement procedure is written down.
Mechanical on purpose: six steps, run in order, every one of them producing something the
operator can see.

# 1. Get a live target

Check that the app answers on `design.devUrl`. If it does not, start `design.devCommand` in the
background, wait until the URL answers, and **record that this process has to be stopped** when
verification finishes — a dev server left running outlives the task and confuses the next one.

Never point verification at a production origin. The target is the local dev URL from
`design.devUrl` plus the route under test.

# 2. Run the diff

```bash
node "${CLAUDE_PLUGIN_ROOT}/scripts/screenshot-diff.mjs" \
  --design <design path> \
  --url <design.devUrl + route> \
  --viewport <authoring viewport> \
  --threshold <design.diff.threshold> \
  --pixel-tolerance <design.diff.pixelTolerance> \
  --out .design-verify/report/<slug>
```

The viewport is the authoring breakpoint carried over from Phase 2, not a convenient one. Add
`--full-page` when the screen is taller than the viewport and the part below the fold is in
scope.

Exit codes: `0` ok, `1` bad arguments or unreadable input, `2` runtime failure (the render
failed, or the URL was unreachable — check step 1 before blaming the diff), `3` the render
runtime is not prepared: run `ensure-runtime.mjs` once while online, see `design-implement`
Step 0.3.

Artefacts written to the output directory: `design.png`, `impl.png`, `diff.png`,
`side-by-side.png`, `report.json`.

# 3. Read `report.json` — regions before verdict

**`pass` is a global percentage, and a global percentage cannot see small geometric drift.**
Measured on this pack's own runtime: a real 2px element shift scored `diffPercent` 0.02 against
a `threshold` of 0.5 — that is `pass: true` on a screen that is visibly wrong.

So read `regions` and `sizeMismatch` on **every** run, whatever the verdict says. A non-empty
dominant region is a finding regardless of `pass`.

Procedure, unconditional:

1. Sort `regions` by `shareOfDiff`, descending.
2. For each region, take its bounding box (`x`, `y`, `width`, `height`) and locate it on
   `side-by-side.png`; name the concrete element it lands on. "Region at 240,880 covering 61%
   of the diff" is not yet a finding — "the summary row sits 2px low" is.
3. Report the mapped list to the operator with `diffPercent`, `threshold` and `pass`.

When `pass` is `false`, that list is the work queue. When `pass` is `true` and `regions` is
non-empty, that list is still reported as findings — closing on the verdict alone is how the
2px shift above ships.

Fix in the order given by `references/fidelity-checklist.md`, rule 7: container sizes and
spacing first, then typography, then colours and shadows.

# 4. `sizeMismatch` is a layout bug

A non-empty `sizeMismatch` means the implementation and the design do not even occupy the same
box. That is a layout defect — a container width, a padding, a missing constraint — and it is
fixed first. It is never a reason to normalise the two images to a common size: normalising
hides the defect and leaves every downstream region misaligned by the same amount, which then
gets chased element by element.

# 5. Run the project's own gates

Run `design.verify.lint` and `design.verify.typecheck`. Both must be green. A pixel-perfect
screen that fails typecheck is not delivered work, and neither command is optional because the
diff looks good.

# 6. Show the operator the image and the number

Report `side-by-side.png` and `diffPercent` (with `threshold` and `pass`), plus the mapped
region list from step 3 and the state of step 5.

**Declaring the work finished without those two artefacts is forbidden.** "Looks the same" is
not an output of this pack; a number and an image are. If the run was made in a degraded mode
(`degraded: screenshots` from `design-acquire`), say so in the same message and call the result
an approximation.

# Cleanup

Stop the dev server if step 1 started it. Leave the artefact directory in place — the operator
may want to look at `diff.png` after the summary.
