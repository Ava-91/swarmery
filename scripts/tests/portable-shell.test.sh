#!/bin/bash
# Portability scanner: shell in this repo must run on Linux, not only on macOS.
#
# WHY THIS EXISTS. Two suites in this repo were green on macOS for months and
# had never run in CI. The day they did, both failed for the same reason — and
# then a brand-new hook, written the same week, failed for it a third time. The
# bug is not obvious enough to catch by review:
#
#   `stat` is TWO different tools wearing one name. BSD/macOS spells mtime
#   `-f %m`; GNU spells it `-c %Y`. The trap is that GNU's `-f` is NOT an error —
#   it means "show filesystem status", prints a multi-line `File: …` block, and
#   EXITS 0. So the natural-looking `stat -f %m x || stat -c %Y x` never reaches
#   its fallback on Linux and hands that block to whatever consumes it: into
#   `$(( … ))` it becomes an unbound-variable crash under `set -u`; into a
#   numeric guard it silently disables the check.
#
# A comment explaining this in three files would go stale. This fails the build
# instead. Same for the `shasum` (BSD) / `sha256sum` (GNU) split, and for
# `date -v` (BSD) / `date -d` (GNU).
#
# The rule in each case is the same: never let ONE spelling's exit code decide.
# Validate the OUTPUT, or try both.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1

pass=0
fail=0
ok() { pass=$((pass + 1)); }
bad() { fail=$((fail + 1)); printf '  ✗ %s\n' "$1"; }

# files — every shell script this repo ships or tests with.
files=()
while IFS= read -r f; do files+=("$f"); done < <(find plugins scripts -name '*.sh' | sort)

# code_of <file> — the file with comment-only lines removed. A comment that
# EXPLAINS the trap (this suite's own header does) is not the trap, and scanning
# raw text would make documenting the hazard a build failure.
code_of() { sed -E 's/^[[:space:]]*#.*$//' "$1"; }

# ── stat ──────────────────────────────────────────────────────────
# A file using `stat -f` must also use `stat -c`, and must not rely on `-f`
# failing: the `||` chain with `-f` FIRST is the exact broken shape.
for f in "${files[@]}"; do
  code_of "$f" | grep -q 'stat -f' || continue
  if ! code_of "$f" | grep -q 'stat -c'; then
    bad "$f uses BSD 'stat -f' with no GNU 'stat -c' form — it reads every mtime as garbage on Linux"
    continue
  fi
  # `stat -f … || stat -c …` on one line: the fallback is unreachable on Linux,
  # because GNU's -f exits 0.
  if code_of "$f" | grep -qE 'stat -f[^|]*\|\|[[:space:]]*stat -c'; then
    bad "$f falls back from 'stat -f' to 'stat -c' by exit code — GNU's -f exits 0, so the fallback never runs"
    continue
  fi
  ok
done

# ── shasum / sha256sum ────────────────────────────────────────────
for f in "${files[@]}"; do
  code_of "$f" | grep -q 'shasum' || continue
  if code_of "$f" | grep -q 'sha256sum'; then ok
  else bad "$f uses BSD 'shasum' with no GNU 'sha256sum' form"; fi
done

# ── date -v / date -d ─────────────────────────────────────────────
for f in "${files[@]}"; do
  code_of "$f" | grep -qE 'date -v' || continue
  if code_of "$f" | grep -qE 'date -d'; then ok
  else bad "$f uses BSD 'date -v' with no GNU 'date -d' form"; fi
done

# ── the shapes actually behave ────────────────────────────────────
# Not just "both spellings are present" — the real check. This runs whichever
# form the CURRENT machine has and asserts the result is a number, so the suite
# proves the invariant on Linux (in CI) and on macOS (locally) with one body.
probe="$(mktemp)"
trap 'rm -f "$probe"' EXIT
printf 'x' > "$probe"

mtime="$(stat -c %Y "$probe" 2>/dev/null)"
case "$mtime" in ''|*[!0-9]*) mtime="$(stat -f %m "$probe" 2>/dev/null)" ;; esac
case "$mtime" in
  ''|*[!0-9]*) bad "neither 'stat -c %Y' nor 'stat -f %m' yields a number on this machine — the helpers cannot work" ;;
  *) ok ;;
esac

# And the trap itself, asserted where it exists: on GNU, `stat -f` must be
# recognised as NOT an mtime. On BSD it is one, and the check is skipped.
if stat -c %Y "$probe" >/dev/null 2>&1; then
  wrong="$(stat -f %m "$probe" 2>/dev/null)"
  case "$wrong" in
    ''|*[!0-9]*) ok ;;  # correctly unusable — which is exactly why exit codes cannot be trusted
    *) bad "expected GNU 'stat -f %m' to yield non-numeric output; got '$wrong'" ;;
  esac
fi

printf 'portable-shell: %d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
