#!/usr/bin/env bash
# SessionStart hook — warn when a session is running under an account other than
# the one this project is bound to.
#
# SKELETON. The comparison and the warning land in a later phase; today this is
# a deliberate no-op so the hook wiring can ship, be reviewed, and be proven
# harmless on its own.
#
# Two rules it already obeys, and must keep obeying once it does something:
#
#   - FAIL-OPEN. A hook that can fail is a hook that can block a session, so
#     there is no `set -e` here and the exit code is unconditionally 0.
#   - DRAIN STDIN. Claude Code writes the hook payload to stdin; leaving it
#     unread risks the writer blocking on a full pipe buffer.
set -uo pipefail

cat >/dev/null 2>&1 || true

exit 0
