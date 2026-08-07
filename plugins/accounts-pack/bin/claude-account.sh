#!/usr/bin/env bash
# Run `claude` under the account bound to this project.
#
# Thin by design: every decision lives in `swarmery account`, so this wrapper
# and the daemon can never disagree about which account a project uses. If you
# find yourself adding an `if` here, it belongs in the CLI.
#
# Usage: claude-account.sh [claude args ...]
#        CLAUDE_PROJECT_DIR=/path/to/project claude-account.sh
#
# The project is CLAUDE_PROJECT_DIR when set (Claude Code exports it), else the
# current directory. It must be the project ROOT: the binding lives in
# <project>/.claude/settings.local.json and is not searched for in parent
# directories.
set -euo pipefail

if ! command -v swarmery >/dev/null 2>&1; then
  # Honest degradation beats a silent default: without the CLI we cannot know
  # the binding, so say so and run the default account rather than pretending
  # this session is running under the account the operator chose.
  echo "accounts-pack: swarmery CLI not found — running under the default account" >&2
  exec claude "$@"
fi

exec swarmery account exec --path "${CLAUDE_PROJECT_DIR:-$PWD}" -- claude "$@"
