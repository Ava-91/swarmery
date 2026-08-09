package worktree

// `git worktree add` only ever materializes files that are committed to git.
// A project onboarded into swarmery gets its .claude/settings.json (where
// enabledPlugins lives) written by onboarding or hand-edited, and it is
// entirely normal for that file to sit untracked (`git status` shows `??`) —
// nothing in the onboarding flow requires a commit. Every fresh worktree cut
// for that project then starts with NO .claude/ at all: zero plugins, zero
// project.json, and a headless `--agent <pack>:<agent>` run fails with
// Claude Code's built-in zero-plugin agent list and no hint the cause was an
// untracked file back in the source checkout (issue #192).
//
// syncUntrackedConfig closes that gap for every Acquire caller (dispatch,
// planrun, phaserun — verify reuses whatever worktree dispatch already
// acquired) by lending the source checkout's copies of the files git left
// behind, whenever the worktree does not already have its own.

import (
	"log"
	"os"
	"path/filepath"
)

// configFilesToSync are relative to a checkout root. settings.local.json is
// gitignored by convention in virtually every project (it holds per-machine
// overrides), so it is copied unconditionally here — there is no "commit it
// instead" fix available for that one the way there is for settings.json.
var configFilesToSync = []string{
	filepath.Join(".claude", "settings.json"),
	filepath.Join(".claude", "settings.local.json"),
	filepath.Join(".claude", "project.json"),
}

// syncUntrackedConfig copies each of configFilesToSync from repoRoot into
// worktreePath when the source checkout has it and the fresh worktree does
// not. Best-effort: a copy failure is logged, never returned — this runs at
// the tail of Acquire, and a permissions hiccup on a convenience file must
// not fail the whole acquisition (a caller can still lend settings explicitly
// via repopath.InheritedSettings / --settings, which this does not replace).
func syncUntrackedConfig(repoRoot, worktreePath string) {
	for _, rel := range configFilesToSync {
		src := filepath.Join(repoRoot, rel)
		dst := filepath.Join(worktreePath, rel)
		copied, err := copyMissing(src, dst)
		if err != nil {
			log.Printf("warning: worktree: copy %s into %s: %v", rel, worktreePath, err)
			continue
		}
		if copied {
			log.Printf("worktree: %s is untracked in %s — copied it into %s so the worktree keeps the project's plugin config",
				rel, repoRoot, worktreePath)
		}
	}
}

// copyMissing copies src to dst when src exists as a regular file and dst
// does not exist yet. copied=false with err=nil covers every "nothing to do"
// case: no source (the project genuinely has no such file), a non-regular
// source (symlink/dir — not ours to reinterpret), or a dst that already
// exists (git materialized it because it IS tracked — the worktree's own
// copy is the more specific answer and is left untouched).
func copyMissing(src, dst string) (copied bool, err error) {
	srcInfo, err := os.Lstat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !srcInfo.Mode().IsRegular() {
		return false, nil
	}
	if _, err := os.Lstat(dst); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(dst, data, srcInfo.Mode().Perm()); err != nil {
		return false, err
	}
	return true, nil
}
