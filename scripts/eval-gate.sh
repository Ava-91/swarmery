#!/usr/bin/env bash
# Hybrid eval gate (Verification Contour v2, Pipeline B).
# Usage: eval-gate.sh <promptfoo-results.json>
# Exit: 0 = ok (deterministic asserts passed; soft failures only warn),
#       1 = a deterministic (non-llm-rubric) assert failed,
#       2 = results file missing or unparseable (fail loud).
set -euo pipefail

results="${1:?usage: eval-gate.sh <results.json>}"
if [ ! -f "$results" ] || ! jq -e . "$results" >/dev/null 2>&1; then
  echo "::error::eval-gate: results file missing or invalid: $results"
  exit 2
fi

# Count failing components split by assertion class.
hard=$(jq '[.results.results[].gradingResult.componentResults[]?
            | select(.pass == false and .assertion.type != "llm-rubric")] | length' "$results")
soft=$(jq '[.results.results[].gradingResult.componentResults[]?
            | select(.pass == false and .assertion.type == "llm-rubric")] | length' "$results")

if [ "${soft:-0}" -gt 0 ]; then
  echo "::warning::eval-gate: ${soft} soft (llm-rubric) assertion(s) failed — not blocking."
fi

if [ "${hard:-0}" -gt 0 ]; then
  echo "::error::eval-gate: ${hard} deterministic assertion(s) failed — blocking merge."
  exit 1
fi

echo "eval-gate: deterministic asserts green (soft failures: ${soft:-0})."
exit 0
