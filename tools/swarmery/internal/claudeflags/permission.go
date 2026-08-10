// Package claudeflags builds the CLI flags the daemon's headless `claude`
// spawns share. It exists for one of them: --permission-mode.
//
// A headless run has no interactive approver. Every tool call that would have
// opened a permission prompt is therefore auto-DENIED, and — this is the part
// that hurt — the process still exits 0. The run is recorded as a clean success
// that landed nothing: no files written, no commits, no checkboxes ticked, and
// the only account of what happened lives in a reply nobody stores.
//
// Observed 2026-08-10 on a phase run (project handwrytten, phase-1 of an
// approved plan): every Write/Edit refused with "requested permissions to
// write … you haven't granted it yet", every Bash command outside the
// project's allow list refused as well, exit 0, worktree removed with zero
// diff.
//
// Measured on Claude Code (macOS, `claude -p`, a project settings.json
// carrying allow + deny rules), asking one run to Write a file, `touch` a file,
// `git commit`, and `node -e`:
//
//	mode                Write   Bash touch   Bash git commit   Bash node -e
//	(flag omitted)      deny    deny         deny              deny
//	acceptEdits         ok      ok           deny              deny
//	dontAsk             deny    deny         deny              deny
//	auto                deny    deny         deny              deny
//	bypassPermissions   ok      ok           ok                ok
//
// Only bypassPermissions lets a run do what an execution contract actually
// asks for: edit files, run the verification command, commit. Hence the
// default here.
//
// Why that default is not reckless at THESE spawn sites, specifically:
//
//   - The run happens in a throwaway git worktree on a swarm/ branch, and its
//     prompt forbids push, PR and merge. The blast radius is a directory the
//     daemon deletes when the run ends.
//   - permissions.deny still applies. Verified, not assumed: with
//     `Read(./.env)` denied in the project settings, a bypassPermissions run
//     asked to read .env came back DENIED. bypassPermissions skips the ASK,
//     not the deny list.
//
// Operators who disagree can pin any mode per spawn site, or omit the flag
// entirely, via the env knobs below — the previous behaviour is one env var
// away.
package claudeflags

import (
	"log"
	"os"
	"strings"
)

// ModeEnv is the cross-site override, consulted when a spawn site's own knob is
// unset.
const ModeEnv = "SWARMERY_PERMISSION_MODE"

// DefaultMode is what a headless run gets when nothing is configured. See the
// package doc for the measurement behind this choice.
const DefaultMode = "bypassPermissions"

// OmitMode is the escape hatch: setting a knob to this value passes NO
// --permission-mode flag at all, restoring pre-fix behaviour (which denies
// every prompting tool call — kept reachable deliberately, so an operator
// debugging a permission question can reproduce the old shape exactly).
const OmitMode = "off"

// validModes mirrors `claude --help`'s choices for --permission-mode. An
// unknown value must not reach the CLI: `claude` rejects it and the spawn dies
// before the run starts, turning an operator typo into a dead phase.
var validModes = map[string]string{
	"acceptedits":       "acceptEdits",
	"acceptEdits":       "acceptEdits",
	"auto":              "auto",
	"bypasspermissions": "bypassPermissions",
	"bypassPermissions": "bypassPermissions",
	"manual":            "manual",
	"dontask":           "dontAsk",
	"dontAsk":           "dontAsk",
	"plan":              "plan",
}

// PermissionModeArgs returns the `--permission-mode <mode>` pair to append to a
// headless spawn's argv, or nil when the flag must be omitted.
//
// siteEnv names the spawn site's own knob (e.g.
// SWARMERY_PHASERUN_PERMISSION_MODE) and wins over ModeEnv, which wins over
// DefaultMode. An empty siteEnv means "this site has no knob of its own".
func PermissionModeArgs(siteEnv string) []string {
	mode := resolveMode(siteEnv)
	if mode == "" {
		return nil
	}
	return []string{"--permission-mode", mode}
}

// resolveMode applies the precedence and validates. "" means omit the flag.
func resolveMode(siteEnv string) string {
	raw, from := "", ""
	if siteEnv != "" {
		if v := strings.TrimSpace(os.Getenv(siteEnv)); v != "" {
			raw, from = v, siteEnv
		}
	}
	if raw == "" {
		if v := strings.TrimSpace(os.Getenv(ModeEnv)); v != "" {
			raw, from = v, ModeEnv
		}
	}
	if raw == "" {
		return DefaultMode
	}
	switch strings.ToLower(raw) {
	case OmitMode, "none", "default":
		// "default" is spelled out here rather than passed through: it is NOT one
		// of the CLI's choices, but it is the word an operator reaches for when
		// they mean "whatever claude does on its own", which is exactly omission.
		return ""
	}
	if canonical, ok := validModes[raw]; ok {
		return canonical
	}
	if canonical, ok := validModes[strings.ToLower(raw)]; ok {
		return canonical
	}
	log.Printf("warning: claudeflags: ignoring invalid %s=%q; using %s (valid: acceptEdits, auto, bypassPermissions, manual, dontAsk, plan, or %q to omit the flag)",
		from, raw, DefaultMode, OmitMode)
	return DefaultMode
}
