#!/bin/bash
# architecture-freshness.sh — PreToolUse hook on the Agent tool.
#
# A research-shaped subagent must not go exploring against a stale architecture
# map. It refuses the FIRST such spawn per session with the exact remedy, and
# never a second one.
#
# WHY. Two projects carried maps 15 and 28 days stale in one retro window. In the
# same window `main` fired the search-loop detector 255 times and the
# `general-purpose` agent accounted for $836.77 of cost. These are one fact seen
# twice: the map exists so an agent can navigate directly instead of searching
# broadly — the session-start digest says so in its own footer — and when the map
# is stale the agent cannot trust it, falls back to broad search, and burns tokens
# rediscovering structure the map was supposed to hand over. A 28-day-old map on a
# repo committing at this rate is not a map, it is a historical document.
#
# The advisor's R7 rule already computes all of this and its detail string
# promises "the freshness gate makes it an incremental refresh" — referring to a
# gate that did not exist. This is that gate.
#
# WHY IT BLOCKS ONCE AND ONLY ONCE. The phase's own instruction is to prefer
# refresh-then-proceed, and to degrade rather than trap when the refresh is
# expensive or can fail. Refreshing the map is a MODEL task (the skill fans out
# subagents to re-describe modules), so a hook cannot perform it — the choice is
# between refusing and warning. A pure warning gates nothing; a refusal on every
# spawn could deadlock a session whose refresh keeps failing. So: the first
# research-shaped spawn in a session is refused with an instruction the agent can
# actually act on (`/architecture-map`, incremental from the stored commit), and
# every subsequent spawn proceeds regardless of what the agent decided. The gate
# costs at most one turn per session and cannot trap a run.
#
# SCOPE. Research-shaped runs only (AD-6). Gating every run would stall the fleet
# whenever any map ages, and most work does not need the map at all.
#
# Kill switch: SWARMERY_MAP_FRESHNESS=0.
# Contract: exit 2 refuses the spawn with the reason on stderr (only stderr
# reaches the model); every other path exits 0 silently.

# STALE_DAYS must not disagree with the advisor's R7StaleDays
# (tools/swarmery/internal/advisor/rules.go) — a gate that fires on a map the
# advisor still calls fresh is a gate the operator learns to distrust.
# scripts/tests/architecture-freshness.test.sh pins the two together.
STALE_DAYS="${SWARMERY_MAP_STALE_DAYS:-7}"

# Drain stdin FIRST, before any early exit. A hook that exits without reading
# its payload leaves the writer holding a closed pipe: the caller takes SIGPIPE
# and, under `pipefail`, that failure becomes the pipeline's exit status. The
# kill switch is supposed to be invisible, so it must not be able to fail the
# very command it is standing aside from.
input=$(cat)

[ "${SWARMERY_MAP_FRESHNESS:-1}" = "0" ] && exit 0

command -v jq >/dev/null 2>&1 || exit 0
command -v node >/dev/null 2>&1 || exit 0

agent_type=$(printf '%s' "$input" | jq -r '.tool_input.subagent_type // .tool_input.type // empty' 2>/dev/null)
description=$(printf '%s' "$input" | jq -r '.tool_input.description // empty' 2>/dev/null)
session_id=$(printf '%s' "$input" | jq -r '.session_id // empty' 2>/dev/null)

# ── what counts as research-shaped ────────────────────────────────
# Explicit, and deliberately narrow. These are the agent types whose whole job is
# to navigate a codebase they do not already hold in context — the ones that pay
# for a stale map in broad search. An implementer working from a plan is NOT
# research-shaped: it has been told where to go.
#
# The name is matched with any plugin prefix stripped (`core:context-gatherer` →
# `context-gatherer`), because the same agent is spawned under both spellings.
is_research_agent() {
  case "${1##*:}" in
    general-purpose|general|explore|Explore|\
    context-gatherer|tech-researcher|code-auditor|downstream-analyzer|\
    architecture-designer|silent-failure-hunter)
      return 0 ;;
  esac
  return 1
}

# A spawn whose TYPE is unrecognised (a project-local agent, the catch-all) is
# judged by what it was asked to do. Only exploration verbs count; an unknown
# agent asked to "implement" or "fix" is left alone.
looks_like_research() {
  printf '%s' "$1" | grep -Eqi '(^|[[:space:]])(explore|investigate|survey|audit|map|locate|trace|find (out|where|every|all)|search (for|across)|where (is|are|does)|which files|understand)([[:space:]]|$)'
}

if ! is_research_agent "$agent_type"; then
  looks_like_research "$description" || exit 0
fi

# ── is the map stale? ─────────────────────────────────────────────
PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"
MAP="${PROJECT_DIR}/architecture-out/architecture-map.json"

# No map at all is NOT this hook's problem. Demanding one before any exploration
# would refuse the first session in every repo that has never built one, which is
# a worse failure than a stale map: it blocks work that has no remedy to hand.
[ -f "$MAP" ] || exit 0

analyzed=$(MAP="$MAP" node -e '
  try {
    const v = JSON.parse(require("fs").readFileSync(process.env.MAP, "utf8")).analyzedAtCommit;
    process.stdout.write(typeof v === "string" && v.length >= 7 ? v : "");
  } catch (e) { process.stdout.write(""); }
' 2>/dev/null)
[ -n "$analyzed" ] || exit 0

head_sha=$(git -C "$PROJECT_DIR" rev-parse HEAD 2>/dev/null)
[ -n "$head_sha" ] || exit 0
# Same first test the advisor makes: a map analyzed at HEAD is current whatever
# its mtime says.
[ "$head_sha" = "$analyzed" ] && exit 0

# Age in whole days from the map's mtime — the advisor's own measure.
#
# `stat` is two tools with one name, and the difference is a trap rather than an
# inconvenience: BSD/macOS spells mtime `-f %m`, GNU spells it `-c %Y`, and GNU's
# `-f` is NOT an error — it means "filesystem status" and prints a multi-line
# `File: …` block with exit code 0. So `stat -f %m || stat -c %Y` never reaches
# its fallback on Linux and yields that block. Neither form is trusted by its
# exit code here: whichever produces digits wins.
now=$(date +%s)
mtime=$(stat -c %Y "$MAP" 2>/dev/null)
[[ "$mtime" =~ ^[0-9]+$ ]] || mtime=$(stat -f %m "$MAP" 2>/dev/null)
[[ "$mtime" =~ ^[0-9]+$ ]] || exit 0
age_days=$(( (now - mtime) / 86400 ))
[ "$age_days" -ge "$STALE_DAYS" ] || exit 0

# ── refuse, once ──────────────────────────────────────────────────
# The marker is what makes this a one-turn cost rather than a trap. Written
# BEFORE the refusal, so even a session that never refreshes proceeds on its next
# attempt. No session id means no way to tell a first spawn from a tenth, and a
# gate that cannot tell must not refuse at all.
[ -n "$session_id" ] || exit 0
safe_session=$(printf '%s' "$session_id" | tr -c 'A-Za-z0-9_.-' '_')
marker="${TMPDIR:-/tmp}/swarmery-map-freshness/${safe_session}"
[ -f "$marker" ] && exit 0
mkdir -p "$(dirname "$marker")" 2>/dev/null || exit 0
: > "$marker" 2>/dev/null || exit 0

printf '🗺️  STALE ARCHITECTURE MAP — refresh before exploring.\n' >&2
printf '\n' >&2
printf '  map analyzed at: %s (%d days old)\n' "${analyzed:0:7}" "$age_days" >&2
printf '  repo HEAD:       %s\n' "${head_sha:0:7}" >&2
printf '\n' >&2
printf 'You are about to spend a research agent navigating this repo from a map that no\n' >&2
printf 'longer describes it. That is how broad search happens: the map is not trusted, the\n' >&2
printf 'agent greps instead, and rediscovering structure costs far more than refreshing it.\n' >&2
printf '\n' >&2
printf 'Run /architecture-map first. It is INCREMENTAL from the stored commit — it diffs\n' >&2
printf '%s..HEAD and re-describes only the modules those changes touched.\n' "${analyzed:0:7}" >&2
printf '\n' >&2
printf 'Then re-issue this spawn. This gate refuses ONCE per session: if you judge the\n' >&2
printf 'refresh not worth it, or it fails, simply spawn again and it will proceed.\n' >&2
exit 2
