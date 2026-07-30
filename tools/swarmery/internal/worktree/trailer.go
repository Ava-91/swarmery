package worktree

import (
	"fmt"
	"strings"
)

// trailerKey is the git commit trailer that attributes a commit to a board
// task. Adapted from Fusion's `Fusion-Task-Id:` (DESIGN.md §4.3) — deterministic
// task↔commit attribution replacing cwd/time heuristics, and the basis of
// foreign-commit (contamination) detection.
const trailerKey = "Swarm-Task-Id"

// Trailer renders the commit trailer line for a task id, e.g.
// Trailer("T-abc123") == "Swarm-Task-Id: T-abc123". Dispatched sessions are
// instructed to append this to every commit.
func Trailer(taskID string) string {
	return trailerKey + ": " + taskID
}

// CommitsForTask returns the SHAs of commits carrying this task's trailer,
// across all refs. It greps the exact trailer line (git log --grep is a regex,
// so the taskID is escaped). Newest first (git log default order).
func (m *Manager) CommitsForTask(repoRoot, taskID string) ([]string, error) {
	grep := "^" + regexpEscape(Trailer(taskID)) + "$"
	out, err := m.Git.Run(repoRoot, "log", "--all", "--format=%H", "--grep", grep, "-E")
	if err != nil {
		return nil, err
	}
	var shas []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			shas = append(shas, line)
		}
	}
	return shas, nil
}

// TreeHash returns the git tree object id of a worktree's HEAD commit
// (`git -C <worktree> rev-parse HEAD^{tree}`) — the content fingerprint used by
// the auto-verification cache (fusion phase 6) to skip re-grading an unchanged
// tree. It is the TREE, not the commit, so two commits with identical content
// (e.g. an amend that only touched the message) share a cache entry. Run against
// the WORKTREE path (not the repo root) so it reflects the task branch's tip.
func (m *Manager) TreeHash(worktreePath string) (string, error) {
	out, err := m.Git.Run(worktreePath, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// DiffFileCount reports how many files differ between the worktree's HEAD and base.
//
// `diff --name-only base...HEAD` (three dots) compares against the MERGE BASE, not
// against base's current tip: a two-dot range would count every file the base branch
// moved on since the work forked, inflating the number with changes this work never
// made. That inflation is exactly what would trip a scope bound on an innocent
// branch.
//
// An empty or unresolvable base is not treated as "everything": it returns an error
// so the caller can skip its bound rather than refuse work on a number it cannot
// trust.
func (m *Manager) DiffFileCount(worktreePath, base string) (int, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return 0, fmt.Errorf("worktree: diff file count: no base ref given")
	}
	out, err := m.Git.Run(worktreePath, "diff", "--name-only", base+"...HEAD")
	if err != nil {
		return 0, fmt.Errorf("worktree: diff file count vs %s: %w", base, err)
	}
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n, nil
}

// regexpEscape escapes the ERE metacharacters that can appear in a task id or
// the fixed trailer text, so `git log --grep` matches the literal line. Task
// ids are "T-"+base36 in practice, but the fixed "Swarm-Task-Id:" contains a
// literal '-' (harmless) and the escape keeps the grep correct if the id
// convention ever widens.
func regexpEscape(s string) string {
	const meta = `\.+*?()|[]{}^$`
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(meta, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
