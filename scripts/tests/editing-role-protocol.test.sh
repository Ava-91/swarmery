#!/bin/bash
# Every editing role doc must state the read-before-write protocol.
#
# `plugins/core/hooks/read-before-write.sh` already ENFORCES the rule for every
# agent, via an Edit|Write matcher. This checks the other half: that an agent
# reading its own role doc can SEE the protocol, instead of discovering it by
# tripping over a refusal. A rule that lives only in a hook disappears the day
# the hook's matcher changes, and nothing fails.
#
# Editing roles are identified by a PROPERTY of the doc — frontmatter
# `permissionMode: acceptEdits`, the field that grants write access — not by a
# filename list. A list silently stops covering the next agent that gains write
# tools, which is the exact gap this check exists to close.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

python3 - "$ROOT" <<'PY'
import glob, os, re, sys

root = sys.argv[1]
os.chdir(root)

# Roles that edit despite not carrying `permissionMode: acceptEdits`. Listed
# here with the reason so the inclusion is a recorded decision.
ALSO_EDITING = {
    "plugins/core/agents/tech-lead.md":
        "runs permissionMode: default but owns the delegation brief every executor's edits are made from",
}

# Deliberate exemptions: an editing role that must NOT carry the protocol step,
# with the reason. Empty on purpose — every editing role today carries it.
# A stale entry (a file that no longer exists, is no longer an editing role, or
# does carry the step) fails this check, so an exemption cannot outlive its
# reason.
EXEMPT = {}

# The step is written in each doc's own local shape, so this keys on substance
# rather than on a heading: the hook is named, and writing from memory is
# refused. Both phrases are load-bearing content, not formatting.
REQUIRED = [
    (re.compile(r"read-before-write", re.I), "does not name the read-before-write protocol/hook"),
    (re.compile(r"from memory", re.I), "does not prohibit writing a file from memory"),
]

def frontmatter(path):
    parts = open(path, encoding="utf-8").read().split("---", 2)
    return parts[1] if len(parts) >= 3 else ""

editing = []
for f in sorted(glob.glob("plugins/*/agents/*.md")):
    if re.search(r"^permissionMode:\s*acceptEdits\s*$", frontmatter(f), re.M):
        editing.append(f)
for f in ALSO_EDITING:
    if f not in editing:
        editing.append(f)

problems = []

for f in sorted(editing):
    if f in EXEMPT:
        continue
    body = open(f, encoding="utf-8").read()
    for rx, why in REQUIRED:
        if not rx.search(body):
            problems.append(f"{f}: {why}")

# Stale-exemption sweep: an exemption is a decision about a real editing role
# that really lacks the step. Any other state means the reason has expired.
for f, reason in EXEMPT.items():
    if not os.path.exists(f):
        problems.append(f"{f}: exempted but the file no longer exists — drop the exemption")
        continue
    if f not in editing:
        problems.append(f"{f}: exempted but no longer an editing role — drop the exemption")
        continue
    if all(rx.search(open(f, encoding="utf-8").read()) for rx, _ in REQUIRED):
        problems.append(f"{f}: exempted but already carries the step — drop the exemption ({reason})")

# Same sweep for the inclusion list, so it cannot rot the other way.
for f, reason in ALSO_EDITING.items():
    if not os.path.exists(f):
        problems.append(f"{f}: listed as an editing role but the file no longer exists ({reason})")

print(f"editing-role-protocol: checked={len(editing)} exempt={len(EXEMPT)} problems={len(problems)}")
for p in problems:
    print("  ✗", p)
sys.exit(1 if problems else 0)
PY
