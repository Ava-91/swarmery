// Package repopath answers ONE question: which git repository root should a run
// execute in?
//
// projects.path is the project ROOT, and for a multi-repo project that root is an
// umbrella directory holding N checkouts with no .git of its own. Handing it to git
// is what made every plan and phase run in such a project die during admission with
// "fatal: not a git repository (or any parent up to mount point …)" — before a
// worktree was ever acquired, so nothing about the failure named the real cause
// (2026-07-30, task 48 / project Skygor).
//
// The plan format already names the repo per phase: the README sequencing table's
// `Repo` column and each phase doc's `| **Repo** | … |` header row, both stored raw
// in epic_phases.repo. This package turns those cells — plus project.json's
// mainApp/repos — into an absolute, validated repo root, or explains what it tried.
//
// It reads the filesystem and nothing else: no DB, no git subprocess (running git
// to find out whether git works is how we got the unusable error message), no
// config. Callers decide the priority order of the cells they pass.
package repopath

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrNoRepoRoot: no candidate passed validation. Always wrapped with the candidates
// that were tried, so the API can say WHAT was checked instead of echoing git's
// stderr at the user.
var ErrNoRepoRoot = errors.New("no git repository to run in")

// backtickRe pulls the `wrapped` fragments out of a declared Repo cell.
var backtickRe = regexp.MustCompile("`([^`]+)`")

// placeholder cells that declare nothing.
func isPlaceholder(tok string) bool {
	switch strings.ToLower(strings.TrimSpace(tok)) {
	case "", "—", "-", "–", "n/a", "na", "none", "tbd":
		return true
	}
	return false
}

// Tokens splits a declared Repo cell into candidate tokens, most specific first.
// It handles every shape the plan format actually produces:
//
//	"`sk-next` (`/Volumes/Work/Skygor/sk-next`)" → ["/Volumes/Work/Skygor/sk-next", "sk-next"]
//	"sk-next (+ helm)"                           → ["sk-next"]
//	"`sk-next` (+ Helm in `sk-k8s-next` / `dk-infrastructure`)"
//	                                             → ["sk-next", "sk-k8s-next", "dk-infrastructure"]
//
// Absolute paths sort first because they are the least ambiguous thing a doc can
// say; the relative order of everything else is preserved. Pure.
func Tokens(cell string) []string {
	var abs, rel []string
	seen := map[string]bool{}
	add := func(tok string) {
		tok = strings.TrimSpace(strings.Trim(strings.TrimSpace(tok), "*"))
		if isPlaceholder(tok) || seen[tok] {
			return
		}
		seen[tok] = true
		if filepath.IsAbs(tok) {
			abs = append(abs, tok)
			return
		}
		rel = append(rel, tok)
	}

	if m := backtickRe.FindAllStringSubmatch(cell, -1); len(m) > 0 {
		for _, g := range m {
			add(g[1])
		}
		return append(abs, rel...)
	}

	// No backticks: take the text up to the first separator that starts a
	// parenthetical or a list ("sk-next (+ helm)", "sk-next, sk-controlbox").
	// "/" is NOT a separator here — a declared repo may legitimately be a nested
	// path ("tools/swarmery"), and cutting at the slash would silently resolve the
	// run to the wrong directory rather than failing.
	head := cell
	if i := strings.IndexAny(head, "(,·|"); i >= 0 {
		head = head[:i]
	}
	add(head)
	return append(abs, rel...)
}

// Primary is the cell's declared repo NAME — Tokens' first non-absolute token, or
// the basename of its first absolute one. "" when the cell declares nothing.
//
// It is the identity planrun compares across phases to decide whether a plan spans
// repos: comparing raw cells would call "`sk-next`" and "sk-next (+ helm)" two
// different repositories and refuse a plan that lives in exactly one. Pure.
func Primary(cell string) string {
	for _, tok := range Tokens(cell) {
		if !filepath.IsAbs(tok) {
			return tok
		}
	}
	if toks := Tokens(cell); len(toks) > 0 {
		return filepath.Base(toks[0])
	}
	return ""
}

// Resolve picks the git repository root a run executes in.
//
// projectPath is projects.path; cells are declared Repo cells in priority order
// (phase doc header, then plan README row, then project.json-derived hints — the
// caller owns that order, this package does not read the DB or any config).
//
// A candidate is accepted only when it exists, holds a .git entry, and resolves
// INSIDE projectPath (or equals it). Rejected candidates fall through to the next
// one, and projectPath itself is always the final candidate — so a single-repo
// project resolves exactly as it did before this package existed, even when a doc
// declares a repo that is not on disk. That fallback is the backward-compatibility
// guarantee, not a nicety: every other project in the registry depends on it.
func Resolve(projectPath string, cells ...string) (string, error) {
	if strings.TrimSpace(projectPath) == "" {
		return "", fmt.Errorf("%w: no project path", ErrNoRepoRoot)
	}
	var tried []string
	try := func(cand string) (string, bool) {
		for _, t := range tried {
			if t == cand {
				return "", false // already rejected — do not re-stat or re-report it
			}
		}
		tried = append(tried, cand)
		if real, ok := accept(projectPath, cand); ok {
			return real, true
		}
		return "", false
	}

	for _, cell := range cells {
		for _, tok := range Tokens(cell) {
			cand := tok
			if !filepath.IsAbs(cand) {
				cand = filepath.Join(projectPath, cand)
			}
			if real, ok := try(cand); ok {
				return real, nil
			}
		}
	}
	if real, ok := try(projectPath); ok {
		return real, nil
	}
	return "", fmt.Errorf("%w: %s is not a git repository and no declared repo resolved (tried: %s)",
		ErrNoRepoRoot, projectPath, strings.Join(tried, ", "))
}

// accept validates one candidate: it must exist, carry a .git entry, and live
// inside projectPath. Returns the symlink-resolved path.
//
// EvalSymlinks runs BEFORE the containment check on purpose. A string-prefix test
// on the declared path would pass for a symlink that sits inside the project and
// points anywhere on the disk, and the cell it came from is untrusted text out of a
// markdown file — that is the one input that must not be able to place a worktree
// outside the project.
func accept(projectPath, cand string) (string, bool) {
	realProject, err := filepath.EvalSymlinks(projectPath)
	if err != nil {
		return "", false
	}
	real, err := filepath.EvalSymlinks(cand)
	if err != nil {
		return "", false
	}
	// A .git DIRECTORY is a normal checkout; a .git FILE is a linked worktree or a
	// submodule. Both are repositories git can run in, so both are accepted.
	if _, err := os.Stat(filepath.Join(real, ".git")); err != nil {
		return "", false
	}
	rel, err := filepath.Rel(realProject, real)
	if err != nil {
		return "", false
	}
	if rel != "." && (rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel)) {
		return "", false
	}
	return real, true
}

// overlayProject is the subset of a consumer's project.json this package reads.
type overlayProject struct {
	MainApp string   `json:"mainApp"`
	Repos   []string `json:"repos"`
}

// FileHints reads a project.json (a workspace's overlay/project.json or a
// checkout's .claude/project.json) and returns its declared repo hints in priority
// order: mainApp first, then repos[] when it holds exactly ONE entry.
//
// A longer repos[] is deliberately ignored: it lists what agents may search, not
// where a run belongs, and picking one of seven would be a guess presented as a
// decision. Missing or unparseable file ⇒ nil, no error — a hint source is
// advisory, and a broken overlay must not block a run whose phase doc already says
// the answer.
func FileHints(jsonPath string) []string {
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil
	}
	var p overlayProject
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil
	}
	var out []string
	if !isPlaceholder(p.MainApp) {
		out = append(out, strings.TrimSpace(p.MainApp))
	}
	if len(p.Repos) == 1 && !isPlaceholder(p.Repos[0]) {
		out = append(out, strings.TrimSpace(p.Repos[0]))
	}
	return out
}

// SameDir reports whether two paths name the same directory, comparing them
// AFTER symlink resolution.
//
// filepath.Clean is not enough and the difference is not cosmetic: Resolve returns
// an EvalSymlinks'd path while projects.path is stored raw, so on macOS (/var →
// /private/var, and any project reached through a symlinked mount) a single-repo
// project would compare as "run root ≠ project root" and get treated as multi-repo
// — inheriting settings it should not and being told, wrongly, that its worktree is
// a checkout inside the project. Falls back to Clean for paths that cannot be
// resolved, which is the best available answer, not a guess about equality.
func SameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	resolve := func(p string) string {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return real
		}
		return filepath.Clean(p)
	}
	return resolve(a) == resolve(b)
}

// InheritedSettings names the project settings file a run should be handed on
// the command line, or "" when it needs none.
//
// A worktree is never the project directory: Claude Code discovers
// .claude/settings.json by walking up from cwd, and a worktree lives under
// ~/.swarmery/worktrees/…, so it inherits nothing from the project. That is
// invisible while a project keeps its .claude/ committed INSIDE the repo the
// worktree is cut from — the checkout carries it. It stops being invisible the
// moment the run root is a sub-repo: project Skygor declares core@swarmery in
// /Volumes/Work/Skygor/.claude/settings.json, the run happens in a checkout of
// sk-next, and the plan run died with "--agent 'tech-lead' not found" because the
// plugin that ships that agent was never enabled for the session (2026-07-30).
//
// Rules, in order:
//   - repoRoot == projectPath ⇒ "". The run IS a checkout of the project repo;
//     whatever it carries is what the project chose to commit, and lending it a
//     second copy would change behaviour for every existing project.
//   - the worktree already has .claude/settings.json ⇒ "". The repo made its own
//     statement, and it is the more specific one — same precedence rule the phase
//     doc gets over the plan README.
//   - otherwise the project's settings file, when it exists.
func InheritedSettings(projectPath, repoRoot, worktreePath string) string {
	if projectPath == "" || repoRoot == "" || SameDir(projectPath, repoRoot) {
		return ""
	}
	if worktreePath != "" {
		if _, err := os.Stat(filepath.Join(worktreePath, ".claude", "settings.json")); err == nil {
			return ""
		}
	}
	settings := filepath.Join(projectPath, ".claude", "settings.json")
	if _, err := os.Stat(settings); err != nil {
		return ""
	}
	return settings
}
