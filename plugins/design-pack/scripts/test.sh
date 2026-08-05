#!/bin/bash
# Local smoke test for the design pack verification scripts.
#
# Framework-free, in the style of the repository's other behavioural tests: each
# case runs a real script against the fixtures and asserts on the artifacts it
# produced, with pass/fail counters and one line per check.
#
# This test needs the pinned browser, so it is NOT wired into CI. Run it locally:
#   bash plugins/design-pack/scripts/test.sh
# With DESIGN_PACK_TEST_OFFLINE=1 it skips cleanly when the cache is not prepared,
# which is how a gate can call it without ever downloading anything.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FIXTURES="${SCRIPT_DIR}/fixtures"
DESIGN="${FIXTURES}/design-fixture.html"
MUTATED="${FIXTURES}/design-fixture-mutated.html"
CACHE="${SWARMERY_DESIGN_CACHE:-${XDG_CACHE_HOME:-${HOME}/.cache}/swarmery-design-pack}"

pass=0
fail=0

ok() {
  pass=$((pass + 1))
  printf '  ✓ %s\n' "$1"
}

bad() {
  fail=$((fail + 1))
  printf '  ✗ %s\n' "$1"
  if [ -n "${2:-}" ]; then printf '      %s\n' "$2"; fi
}

# ── 1. runtime gate ───────────────────────────────────────────────
# No cache and an explicit offline request is a clean skip, not a failure.
if [ ! -f "${CACHE}/runtime.json" ] && [ "${DESIGN_PACK_TEST_OFFLINE:-0}" = "1" ]; then
  echo "SKIP: runtime not prepared"
  exit 0
fi

WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

echo "design-pack verification scripts:"

desc="runtime cache is prepared"
if node "${SCRIPT_DIR}/ensure-runtime.mjs" --quiet >"${WORK}/runtime.log" 2>&1; then
  ok "${desc}"
else
  bad "${desc}" "$(tail -n 3 "${WORK}/runtime.log")"
  printf 'design-pack scripts: %d passed, %d failed\n' "${pass}" "${fail}"
  exit 1
fi

# ── 2. token inventory ────────────────────────────────────────────
desc="extract-computed-styles inventories the fixture verbatim"
if node "${SCRIPT_DIR}/extract-computed-styles.mjs" --input "${DESIGN}" --out "${WORK}/tokens" \
    >"${WORK}/extract.log" 2>&1; then
  if detail="$(node -e '
const fs = require("fs");
const dir = process.argv[1];
const t = JSON.parse(fs.readFileSync(dir + "/tokens.json", "utf8"));
const md = fs.readFileSync(dir + "/tokens.md", "utf8");
const some = (a, f) => Array.isArray(a) && a.some(f);
const miss = [];
if (!some(t.typography, (x) => x.fontSize === "13px")) miss.push("typography 13px");
if (!some(t.spacing, (x) => x.value === "18px" && x.property === "gap" && x.onFourPxGrid === false)) miss.push("gap 18px off-grid");
if (!some(t.radii, (x) => x.value === "12px")) miss.push("radius 12px");
if (!some(t.colors, (x) => /^#[0-9a-f]{6,8}$/.test(x.hex) && String(x.oklch).startsWith("oklch("))) miss.push("colors hex+oklch");
if (!some(t.borders, (x) => String(x.value).includes("1px solid"))) miss.push("borders");
if (!some(t.shadows, (x) => String(x.value).length > 0)) miss.push("shadows");
if (!(t.authoredBreakpoints || []).includes(768)) miss.push("breakpoint 768");
if (!(t.fontFamiliesRequired || []).length) miss.push("fontFamiliesRequired");
if (!t.source || !t.viewport || !t.elementsScanned) miss.push("header fields");
if (!md.includes("13px") || !md.includes("18px")) miss.push("tokens.md misses fixture values");
if (miss.length) { console.error(miss.join("; ")); process.exit(1); }
' "${WORK}/tokens" 2>&1)"; then
    ok "${desc}"
  else
    bad "${desc}" "${detail}"
  fi
else
  bad "${desc}" "$(tail -n 3 "${WORK}/extract.log")"
fi

# ── 3. design against itself ──────────────────────────────────────
desc="screenshot-diff reports zero difference for design against itself"
if node "${SCRIPT_DIR}/screenshot-diff.mjs" --design "${DESIGN}" --url "file://${DESIGN}" \
    --out "${WORK}/self" >"${WORK}/self.log" 2>&1; then
  if detail="$(node -e '
const fs = require("fs");
const t = JSON.parse(fs.readFileSync(process.argv[1] + "/report.json", "utf8"));
const miss = [];
if (t.diffPercent !== 0) miss.push("diffPercent=" + t.diffPercent);
if (t.pass !== true) miss.push("pass=" + t.pass);
if (t.sizeMismatch !== null) miss.push("unexpected sizeMismatch");
if (typeof t.diffPixels !== "number" || typeof t.totalPixels !== "number") miss.push("pixel counts");
if (typeof t.threshold !== "number" || !t.url || !t.design || !t.viewport) miss.push("header fields");
if (!Array.isArray(t.regions)) miss.push("regions is not an array");
for (const key of ["design", "impl", "diff", "sideBySide"]) {
  if (!t.artifacts || !t.artifacts[key]) miss.push("artifacts." + key + " missing from report");
  else if (!fs.existsSync(t.artifacts[key])) miss.push(key + " image not written");
}
if (miss.length) { console.error(miss.join("; ")); process.exit(1); }
' "${WORK}/self" 2>&1)"; then
    ok "${desc}"
  else
    bad "${desc}" "${detail}"
  fi
else
  bad "${desc}" "$(tail -n 3 "${WORK}/self.log")"
fi

# ── 4. design against the 2px mutation ────────────────────────────
desc="screenshot-diff localises the 2px mutation in one dominant region"
if node "${SCRIPT_DIR}/screenshot-diff.mjs" --design "${DESIGN}" --url "file://${MUTATED}" \
    --out "${WORK}/mutated" >"${WORK}/mutated.log" 2>&1; then
  if detail="$(node -e '
const fs = require("fs");
const t = JSON.parse(fs.readFileSync(process.argv[1] + "/report.json", "utf8"));
const miss = [];
if (!(t.diffPercent > 0)) miss.push("diffPercent=" + t.diffPercent);
if (!(t.diffPixels > 0)) miss.push("diffPixels=" + t.diffPixels);
if (!Array.isArray(t.regions) || t.regions.length < 1) { miss.push("no regions"); }
else {
  const r = t.regions[0];
  if (!(r.shareOfDiff >= 0.5)) miss.push("top region holds only " + r.shareOfDiff + " of the diff");
  if (!(r.x >= 38 && r.x <= 44)) miss.push("region x=" + r.x + " does not cover the moved edge at 40..42");
  if (!(r.width <= 8)) miss.push("region width=" + r.width);
  if (!(r.y >= 30 && r.y <= 60)) miss.push("region y=" + r.y);
  if (!(r.height >= 100)) miss.push("region height=" + r.height);
}
if (miss.length) { console.error(miss.join("; ")); process.exit(1); }
' "${WORK}/mutated" 2>&1)"; then
    ok "${desc}"
  else
    bad "${desc}" "${detail}"
  fi
else
  bad "${desc}" "$(tail -n 3 "${WORK}/mutated.log")"
fi

# ── 5. remote origin guard ────────────────────────────────────────
desc="screenshot-diff refuses a non-loopback URL without --allow-remote"
if node "${SCRIPT_DIR}/screenshot-diff.mjs" --design "${DESIGN}" --url "https://example.com" \
    --out "${WORK}/remote" >"${WORK}/remote.log" 2>&1; then
  bad "${desc}" "the script exited 0"
else
  code=$?
  if [ "${code}" -ne 0 ] && grep -qi "loopback" "${WORK}/remote.log"; then
    ok "${desc}"
  else
    bad "${desc}" "exit ${code}: $(tail -n 2 "${WORK}/remote.log")"
  fi
fi

printf 'design-pack scripts: %d passed, %d failed\n' "${pass}" "${fail}"
[ "${fail}" -eq 0 ]
