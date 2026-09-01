#!/usr/bin/env bash
# validate-agent-refs.test.sh — behavioral tests for the reference-integrity gate.
#
# Each seeded defect class must fail with a pointed PROBLEM line; a clean
# fixture tree and the live repo must pass. Runs the validator against a temp
# corpus via AGENT_REFS_ROOT.
set -euo pipefail

SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/validate-agent-refs.sh"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

pass=0
fail=0

# make_agent <root> <plugin> <name> <frontmatter-extra...>
make_agent() {
  local root="$1" plugin="$2" name="$3"; shift 3
  mkdir -p "$root/plugins/$plugin/agents"
  {
    echo '---'
    echo "name: $name"
    echo "description: test agent"
    for line in "$@"; do echo "$line"; done
    echo '---'
    echo 'body'
  } > "$root/plugins/$plugin/agents/$name.md"
}

make_skill() {
  local root="$1" plugin="$2" name="$3"
  mkdir -p "$root/plugins/$plugin/skills/$name"
  printf -- '---\nname: %s\ndescription: test skill\n---\nbody\n' "$name" \
    > "$root/plugins/$plugin/skills/$name/SKILL.md"
}

expect_pass() {
  local label="$1" root="$2"
  if out=$(AGENT_REFS_ROOT="$root" bash "$SCRIPT" 2>&1); then
    echo "ok   $label"; pass=$((pass+1))
  else
    echo "FAIL $label — expected pass, got:"; echo "$out" | sed 's/^/     /'; fail=$((fail+1))
  fi
}

expect_fail() {
  local label="$1" root="$2" needle="$3"
  if out=$(AGENT_REFS_ROOT="$root" bash "$SCRIPT" 2>&1); then
    echo "FAIL $label — expected failure, validator passed"; fail=$((fail+1))
  elif ! grep -qF "$needle" <<<"$out"; then
    echo "FAIL $label — failed but without expected message '$needle':"; echo "$out" | sed 's/^/     /'; fail=$((fail+1))
  else
    echo "ok   $label"; pass=$((pass+1))
  fi
}

# 1. clean fixture passes
R="$TMP/clean"; make_skill "$R" core good-skill
make_agent "$R" core alpha 'model: sonnet' 'skills:' '  - good-skill'
expect_pass "clean fixture" "$R"

# 2. dead skills ref fails
R="$TMP/deadskill"
make_agent "$R" core alpha 'model: sonnet' 'skills:' '  - ghost-skill'
expect_fail "dead skills ref" "$R" 'skills entry "ghost-skill"'

# 3. pack→core skill ref passes; pack→nowhere fails
R="$TMP/layered"; make_skill "$R" core shared-skill
make_agent "$R" some-pack beta 'model: haiku' 'skills:' '  - shared-skill'
expect_pass "pack resolves core skill" "$R"
make_agent "$R" some-pack gamma 'model: haiku' 'skills:' '  - phantom'
expect_fail "pack dead skill ref" "$R" 'skills entry "phantom"'

# 4. non-alias model fails
R="$TMP/badmodel"
make_agent "$R" core alpha 'model: claude-sonnet-9'
expect_fail "pinned model in frontmatter" "$R" 'not an alias'

# 5. unknown frontmatter key fails (the permissionMode class)
R="$TMP/badkey"
make_agent "$R" core alpha 'model: sonnet' 'permissionMode: acceptEdits'
expect_fail "ignored frontmatter key" "$R" 'frontmatter key "permissionMode"'

# 6. dead ${CLAUDE_PLUGIN_ROOT} path fails
R="$TMP/deadpath"
make_agent "$R" core alpha 'model: sonnet'
# shellcheck disable=SC2016
echo 'see ${CLAUDE_PLUGIN_ROOT}/templates/nothing.md' >> "$R/plugins/core/agents/alpha.md"
expect_fail "dead plugin-root path" "$R" 'templates/nothing.md'

# 7. retired model id in a body fails
R="$TMP/retired"
make_agent "$R" core alpha 'model: sonnet'
echo 'tier table: sonnet-4-6 fleet default' >> "$R/plugins/core/agents/alpha.md"
expect_fail "retired model id in body" "$R" 'retired model id'

# 8. pinned current id in an agent body fails
R="$TMP/pinnedbody"
make_agent "$R" core alpha 'model: sonnet'
echo 'Model: claude-sonnet-5 for this work' >> "$R/plugins/core/agents/alpha.md"
expect_fail "pinned model id in agent body" "$R" 'pinned model id'

# 9. stale CROSS_PLUGIN_ALLOW entry fails
R="$TMP/staleallow"
make_agent "$R" core alpha 'model: sonnet'
if out=$(AGENT_REFS_ROOT="$R" bash -c '
  src="$1"
  sed "s/^CROSS_PLUGIN_ALLOW=\"\"/CROSS_PLUGIN_ALLOW=\"core:never-used\"/" "$src" | bash
' _ "$SCRIPT" 2>&1); then
  echo "FAIL stale allow entry — expected failure"; fail=$((fail+1))
elif grep -qF 'stale CROSS_PLUGIN_ALLOW entry' <<<"$out"; then
  echo "ok   stale allow entry"; pass=$((pass+1))
else
  echo "FAIL stale allow entry — wrong message:"; echo "$out" | sed 's/^/     /'; fail=$((fail+1))
fi

# 10. the live repo passes
expect_pass "live repo" "$REPO_ROOT"

echo "passed=$pass failed=$fail"
[ "$fail" -eq 0 ]
