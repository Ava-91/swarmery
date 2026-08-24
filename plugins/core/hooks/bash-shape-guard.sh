#!/bin/bash
# Bash command-shape guard — PreToolUse hook on the Bash tool.
#
# Refuses three malformed command shapes BEFORE the auto-mode classifier
# refuses them with no guidance. All three were measured, not guessed: a retro
# window produced 39 "this bash command contains multiple operations" blocks,
# 18 unterminated-heredoc EOF errors, and 420 minutes of wall-clock spent
# waiting on refusals that were predictable from the command's shape alone.
#
# The point is not to forbid more than the classifier does. It is to say NO
# earlier, in one line, with the alternative — instead of leaving the agent to
# discover the refusal after the wait.
#
# Protocol (house style, see protect-sensitive-files.sh): read the hook payload
# from stdin, extract with jq, print the reason to STDERR because only stderr
# reaches the model, exit 2 to block and 0 to allow.
#
# WARN-MODE BURN-IN (gate-hardening rule 1, added 2026-08-24): every rule logs
# its decision to stderr AND to a durable per-rule counter, and exits 0 until
# the flip. Each message carries its rule id ([heredoc] / [multi-mutation] /
# [sleep-before-read]) so hits can be counted per rule before the flip, and the
# tests assert the DECISION rather than the exit code, so they keep passing
# across it.
#
# THE GATE IS docs/GATE-HARDENING.md, NOT THE DATE BELOW. A date cannot answer
# "how many times did this rule fire, and how many of those were wrong?" —
# which is the only question the flip depends on. Fill the per-rule rows of that
# document from `scripts/guard-hits.sh` (it reads the log this hook writes),
# review the false positives, and only then raise a rule's exit code.
# ENFORCE_FROM is the *deadline for the review*, not an auto-enforce trigger:
# nothing in this hook reads it to decide anything.
BLOCK_EXIT=0          # ← 0 = warn; raise to 2 only per docs/GATE-HARDENING.md
ENFORCE_FROM="2026-10-01"   # review deadline; see docs/GATE-HARDENING.md

# Burn-in log: one JSON record per decision. Truncation cap on the command
# text, because this file is appended to on every hit and a single command can
# be arbitrarily large.
LOG_BASENAME="bash-shape-guard.jsonl"
LOG_CMD_MAX=200

set -uo pipefail

input=$(cat)
command_text=$(printf '%s' "$input" | jq -r '.tool_input.command // empty' 2>/dev/null)

# Nothing to judge — a payload without a command is not this hook's business.
[ -z "$command_text" ] && exit 0

# Identity for the burn-in record. Both are optional: a payload without them
# still gets counted, it just cannot be attributed.
session_id=$(printf '%s' "$input" | jq -r '.session_id // empty' 2>/dev/null)
hook_cwd=$(printf '%s' "$input" | jq -r '.cwd // empty' 2>/dev/null)

# guard_log_file — where the burn-in log lives, resolved at runtime. A hook
# that hard-codes a path stops working the moment it is installed anywhere
# else, so: explicit override (tests, operators) → the project's workspace
# metrics dir (house style, same resolution as pre-commit-test-gate.sh) →
# $CLAUDE_PROJECT_DIR → a temp dir. The write always has somewhere to land.
guard_log_file() {
  if [ -n "${BASH_SHAPE_GUARD_LOG:-}" ]; then
    printf '%s' "$BASH_SHAPE_GUARD_LOG"
  elif [ -n "${AGENT_PROJECT:-}" ]; then
    printf '%s/%s/workspace/metrics/%s' \
      "${AGENT_WORKSPACE_ROOT:-$HOME/swarmery-workspace}" "$AGENT_PROJECT" "$LOG_BASENAME"
  elif [ -n "${CLAUDE_PROJECT_DIR:-}" ]; then
    printf '%s/.claude-workspace/metrics/%s' "${CLAUDE_PROJECT_DIR%/}" "$LOG_BASENAME"
  else
    printf '%s/swarmery-guard/%s' "${TMPDIR:-/tmp}" "$LOG_BASENAME"
  fi
}

# log_decision <rule-id> <warn|block> — append one record, and never let that
# append change what the hook does. Telemetry is strictly secondary to the
# allow-or-block contract: a read-only directory, a missing jq, a full disk all
# have to leave the exit code and the stderr text byte-identical.
#
# jq -c is what guarantees one decision = one line: it escapes the newlines and
# quotes a command can legitimately contain, so no command shape can forge a
# second record.
log_decision() {
  local rule="$1" decision="$2" logfile dir
  logfile=$(guard_log_file)
  dir=$(dirname "$logfile")
  mkdir -p "$dir" 2>/dev/null || return 0
  jq -cn \
    --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --arg rule "$rule" \
    --arg decision "$decision" \
    --arg session "$session_id" \
    --arg cwd "$hook_cwd" \
    --arg cmd "${command_text:0:$LOG_CMD_MAX}" \
    '{ts:$ts,hook:"bash-shape-guard",rule:$rule,decision:$decision,session:$session,cwd:$cwd,cmd:$cmd}' \
    >> "$logfile" 2>/dev/null || true
  return 0
}

# refuse <rule-id> <headline> [extra lines…] — emit the decision and leave.
# In warn mode this still exits 0; the text is identical either way so the
# burn-in log shows exactly what enforcement would have said.
refuse() {
  local rule="$1"; shift
  if [ "$BLOCK_EXIT" -eq 0 ]; then
    log_decision "$rule" "warn"
    printf '⚠️  WARN (enforce from %s): [%s] %s\n' "$ENFORCE_FROM" "$rule" "$1" >&2
  else
    log_decision "$rule" "block"
    printf '🚫 BLOCKED: [%s] %s\n' "$rule" "$1" >&2
  fi
  shift
  local line
  for line in "$@"; do
    printf '%s\n' "$line" >&2
  done
  exit "$BLOCK_EXIT"
}

# ── rule: heredoc ─────────────────────────────────────────────────
# Any inline heredoc operator with a delimiter word: <<EOF, <<'EOF', <<"EOF",
# <<-EOF. `<<<` (herestring) is NOT a heredoc and is left alone.
if printf '%s' "$command_text" | grep -Eq '<<-?[[:space:]]*([A-Za-z_][A-Za-z0-9_]*|'\''[^'\'']+'\''|"[^"]+")'; then
  if ! printf '%s' "$command_text" | grep -q '<<<'; then
    refuse "heredoc" \
      "this command writes file content through an inline heredoc." \
      "Inline heredocs produced 18 unterminated-EOF errors in the last retro window:" \
      "the delimiter has to survive shell quoting, tool-call escaping and the model's" \
      "own line wrapping, and any one of them breaks it." \
      "" \
      "Use the Write tool for new file content, or Edit for a change to an existing file." \
      "If the content genuinely must be produced by a program, redirect that program's" \
      "output in its own call."
  fi
fi

# ── segmentation ──────────────────────────────────────────────────
# Split on the shell's sequencing operators only. A pipe is NOT a separator:
# a pipe chain is one operation for this guard's purposes, and treating it as
# many is exactly the false positive that gets a guard switched off.
mapfile -t segments < <(printf '%s' "$command_text" | sed -E 's/(\|\||&&|;)/\n/g')

# trim <string> — strip leading/trailing whitespace.
trim() { printf '%s' "$1" | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//'; }

# is_mutating <segment> — does this segment change something on disk or in git?
# A leading `cd <dir>` prefix is stripped first: `cd tools/swarmery && make test`
# is canonical in this repo's CLAUDE.md and must stay allowed.
is_mutating() {
  local seg
  seg=$(trim "$1")
  seg=$(printf '%s' "$seg" | sed -E 's/^cd[[:space:]]+[^[:space:]]+[[:space:]]*//')
  [ -z "$seg" ] && return 1

  # A redirection INTO a file is a write, whatever the command is. `2>&1` and
  # `>/dev/null` are not: they discard or merge, they do not create artefacts.
  if printf '%s' "$seg" | grep -Eq '>>?[[:space:]]*[^&[:space:]]' &&
     ! printf '%s' "$seg" | grep -Eq '>>?[[:space:]]*/dev/null'; then
    return 0
  fi

  case "$seg" in
    # git subcommands that write
    "git commit"*|"git add"*|"git push"*|"git checkout"*|"git reset"*|\
    "git merge"*|"git rebase"*|"git tag"*|"git stash"*|"git worktree add"*|\
    "git worktree remove"*|"git branch -d"*|"git branch -D"*)
      return 0 ;;
    # filesystem mutations
    "rm "*|"rm"|"mv "*|"cp "*|"mkdir "*|"touch "*|"chmod "*|"chown "*|"ln "*|\
    "rmdir "*|"truncate "*)
      return 0 ;;
    # in-place editors and writers
    "tee "*|"sed -i"*|"perl -i"*)
      return 0 ;;
    # package/toolchain installs
    "npm install"*|"npm ci"*|"npm i "*|"yarn add"*|"pnpm install"*|\
    "pip install"*|"pip3 install"*|"go install"*|"make install"*|\
    "brew install"*|"cargo install"*)
      return 0 ;;
  esac
  return 1
}

# ── rule: multi-mutation ──────────────────────────────────────────
mutating_segments=()
for seg in "${segments[@]}"; do
  trimmed=$(trim "$seg")
  [ -z "$trimmed" ] && continue
  if is_mutating "$trimmed"; then
    mutating_segments+=("$trimmed")
  fi
done

if [ "${#mutating_segments[@]}" -ge 2 ]; then
  details=("One operation per call." "" "The mutating segments in this command:")
  for seg in "${mutating_segments[@]}"; do
    details+=("  • $seg")
  done
  details+=(
    ""
    "This is the shape the auto-mode classifier refuses with \"this bash command"
    "contains multiple operations\" — 39 times in the last retro window, and the"
    "wait before each refusal is what cost 420 minutes."
    ""
    "Issue them as separate calls. Read-only chains and pipes are fine as one call;"
    "it is chaining two CHANGES that has to be split, so a failure halfway through"
    "leaves a state you can name."
  )
  refuse "multi-mutation" \
    "this command chains ${#mutating_segments[@]} mutating operations." \
    "${details[@]}"
fi

# ── rule: sleep-before-read ───────────────────────────────────────
# A bare `sleep N` segment followed later in the same command by a read of a
# file or log. Waiting inside the command burns wall-clock the operator pays
# for and hides the wait from every timeout that could bound it.
saw_sleep=0
for seg in "${segments[@]}"; do
  trimmed=$(trim "$seg")
  [ -z "$trimmed" ] && continue
  if [ "$saw_sleep" -eq 1 ] &&
     printf '%s' "$trimmed" | grep -Eq '^(cat|tail|head|less|more|grep|rg)[[:space:]]'; then
    refuse "sleep-before-read" \
      "this command sleeps and then reads in the same call." \
      "Make the read a separate call." \
      "" \
      "Sleeping inside a command burns wall-clock that no timeout can bound and that" \
      "the retro measured at 420 minutes of waiting. If you are waiting for something" \
      "to finish, poll it in its own call, or wait on the thing itself rather than on" \
      "a fixed number of seconds."
  fi
  if printf '%s' "$trimmed" | grep -Eq '^sleep[[:space:]]+[0-9.]+$'; then
    saw_sleep=1
  fi
done

exit 0
