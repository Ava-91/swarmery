#!/bin/bash
# Read-before-write guard — PreToolUse hook on Edit|Write.
#
# This is a RECOVERY hook, not a denial. Editing a file you have not read is
# already refused by the harness with a bare error, which produced 23 + 5 cases
# in one retro window and the top error group for one agent. The bare error
# tells the agent what it did wrong and nothing about how to proceed, so it
# guesses.
#
# So this hook blocks the first attempt and hands back the file's contents on
# stderr — only stderr reaches the model — and lets the immediate retry through.
# The agent spends one turn instead of several, and it spends it holding the
# file it needed.
#
# ORDERING IS LOAD-BEARING. This hook is registered AFTER
# protect-sensitive-files.sh in the Edit|Write array: hooks run in array order
# and the first non-zero exit wins. A protected path must be refused as
# protected, not answered with its own contents — for a credential file the
# "recovery" would be a disclosure. Belt and braces, the echo is additionally
# suppressed here for anything that hook protects (is_sensitive below); the two
# lists are deliberately kept in step.
set -uo pipefail

# Echo caps. A file bigger than this floods the very context the hook exists to
# save, so it is truncated with an explicit note telling the agent how to get
# the rest.
MAX_LINES=400
MAX_BYTES=40960

input=$(cat)

file_path=$(printf '%s' "$input" | jq -r '.tool_input.file_path // .tool_input.path // empty' 2>/dev/null)
[ -z "$file_path" ] && exit 0

# Creating a new file has nothing to read; blocking there is pure friction.
[ -f "$file_path" ] || exit 0

session_id=$(printf '%s' "$input" | jq -r '.session_id // empty' 2>/dev/null)
# No session identity means no way to tell a first attempt from a retry. Fail
# OPEN: a hook that cannot decide must not block, or every edit in a session
# without an id becomes unrecoverable.
[ -z "$session_id" ] && exit 0

# Per-session marker directory. The path is hashed so any filename is safe, and
# the session id is sanitised because it lands in a directory name.
safe_session=$(printf '%s' "$session_id" | tr -c 'A-Za-z0-9_.-' '_')
if command -v shasum >/dev/null 2>&1; then
  path_key=$(printf '%s' "$file_path" | shasum -a 256 | cut -d' ' -f1)
elif command -v sha256sum >/dev/null 2>&1; then
  path_key=$(printf '%s' "$file_path" | sha256sum | cut -d' ' -f1)
else
  # No hasher available: fail open rather than invent a colliding key.
  exit 0
fi

marker_dir="${TMPDIR:-/tmp}/swarmery-rbw/${safe_session}"
marker="${marker_dir}/${path_key}"

# Already shown this file in this session — never block twice.
[ -f "$marker" ] && exit 0

mkdir -p "$marker_dir" 2>/dev/null || exit 0
: > "$marker" 2>/dev/null || exit 0

base_name=$(basename "$file_path")

# is_sensitive — paths whose CONTENTS must never be quoted back into a session.
# Kept in step with protect-sensitive-files.sh, which refuses to edit the same
# set. That hook runs first and should already have blocked these; this is the
# second lock on the same door, so a registration-order mistake cannot turn a
# recovery path into a disclosure path.
is_sensitive() {
  case "$base_name" in
    .env*|*.tfstate|*.tfstate.backup|*.populated.*|*.tfvars|\
    *.pem|*.key|*.p12|*.pfx|*.jks|*.keystore|\
    id_rsa*|id_ed25519*|id_ecdsa*|id_dsa*|\
    .npmrc|.netrc|_netrc|.pgpass|.htpasswd|\
    credentials|credentials.json|service-account*.json|kubeconfig|\
    .dockercfg|.docker.json|settings.local.json)
      return 0 ;;
  esac
  case "$file_path" in
    */.ssh/*|*/.aws/*|*/.gnupg/*) return 0 ;;
  esac
  return 1
}

echo "📖 READ FIRST: $file_path has not been read in this session." >&2
echo "" >&2

if is_sensitive; then
  echo "Its contents are NOT shown here: this path holds credential material," >&2
  echo "and quoting it into the session would be a disclosure, not a recovery." >&2
  echo "" >&2
  echo "This file is edited by a human, outside an agent session." >&2
  exit 2
fi

total_lines=$(wc -l < "$file_path" 2>/dev/null | tr -d ' ')
total_bytes=$(wc -c < "$file_path" 2>/dev/null | tr -d ' ')
: "${total_lines:=0}"
: "${total_bytes:=0}"

echo "Its current contents follow, so your next attempt has what it needs." >&2
echo "Re-issue the same edit — the retry is allowed." >&2
echo "" >&2
echo "───────── $file_path ─────────" >&2

# Truncate on whichever cap trips first.
truncated=0
if [ "$total_lines" -gt "$MAX_LINES" ] || [ "$total_bytes" -gt "$MAX_BYTES" ]; then
  truncated=1
fi

if [ "$truncated" -eq 1 ]; then
  head -n "$MAX_LINES" "$file_path" 2>/dev/null | head -c "$MAX_BYTES" >&2
  echo "" >&2
  echo "───────── TRUNCATED ─────────" >&2
  echo "Shown: the first $MAX_LINES lines / $MAX_BYTES bytes of $total_lines lines / $total_bytes bytes." >&2
  echo "Read the remainder with the Read tool and an offset before editing further." >&2
else
  cat "$file_path" >&2
  echo "" >&2
  echo "───────── end ($total_lines lines) ─────────" >&2
fi

exit 2
