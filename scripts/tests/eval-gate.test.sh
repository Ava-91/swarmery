#!/usr/bin/env bash
# Test scripts/eval-gate.sh classification: hard fail blocks, soft warns.
set -euo pipefail
here="$(cd "$(dirname "$0")/../.." && pwd)"
gate="$here/scripts/eval-gate.sh"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT

# Fixture A: a failing deterministic (contains) assert => hard fail (exit 1).
cat > "$tmp/hard.json" <<'JSON'
{"results":{"results":[
  {"gradingResult":{"componentResults":[
    {"pass":false,"assertion":{"type":"contains"}},
    {"pass":true,"assertion":{"type":"llm-rubric"}}
  ]}}
]}}
JSON

# Fixture B: only a failing llm-rubric => soft (exit 0 + warning).
cat > "$tmp/soft.json" <<'JSON'
{"results":{"results":[
  {"gradingResult":{"componentResults":[
    {"pass":true,"assertion":{"type":"contains"}},
    {"pass":false,"assertion":{"type":"llm-rubric"}}
  ]}}
]}}
JSON

# Fixture C: all pass => exit 0.
cat > "$tmp/pass.json" <<'JSON'
{"results":{"results":[
  {"gradingResult":{"componentResults":[
    {"pass":true,"assertion":{"type":"contains"}}
  ]}}
]}}
JSON

set +e
bash "$gate" "$tmp/hard.json"; hard=$?
bash "$gate" "$tmp/soft.json"; soft=$?
bash "$gate" "$tmp/pass.json"; pass=$?
bash "$gate" "$tmp/missing.json"; missing=$?
set -e

fail=0
[ "$hard" -eq 1 ]    || { echo "FAIL: hard exit=$hard want 1"; fail=1; }
[ "$soft" -eq 0 ]    || { echo "FAIL: soft exit=$soft want 0"; fail=1; }
[ "$pass" -eq 0 ]    || { echo "FAIL: pass exit=$pass want 0"; fail=1; }
[ "$missing" -eq 2 ] || { echo "FAIL: missing exit=$missing want 2"; fail=1; }
[ "$fail" -eq 0 ] && echo "eval-gate.test.sh: OK"
exit "$fail"
