// Package hookcfg manages the Claude Code hook entries that wire projects to
// the swarmery approvals channel: `swarmery hooks install|uninstall|status`.
//
// Placement is per-project `.claude/settings.local.json` (D2 — the
// not-shared/gitignored tier; hook commands carry a machine-local binary
// path). Surgery rules:
//
//   - read-modify-write via map[string]any, mutating ONLY the event arrays
//     listed in managedEvents — every foreign key and foreign hook survives;
//   - our entries are recognized by the "swarmery hook" command substring;
//   - unparseable JSON aborts WITHOUT writing;
//   - the original file is copied to .bak before the first write;
//   - idempotent: a second install produces no diff;
//   - uninstall removes ONLY swarmery entries (and the empty containers the
//     removal leaves behind).
//
// The user-level ~/.claude/settings.json tier is deliberately NOT supported
// in this iteration (no --user flag).
package hookcfg

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/approvals"
)

// marker identifies swarmery-managed hook command entries.
const marker = "swarmery hook"

// hookTimeoutMargin is the slack the installed "timeout" carries over the
// approval window. Claude Code KILLS the hook process at its timeout, while the
// shim's own expiry is a clean silent fail-open — so the installed value must
// clear the window, never trail it.
const hookTimeoutMargin = 10 * time.Second

// hookTimeout is the installed PermissionRequest per-hook "timeout" (seconds):
// the approval window + margin, so Claude Code never kills the shim mid-poll
// (frozen: docs/hooks-protocol.md §Timing, spike E6). DERIVED, not a literal:
// a hard-coded value silently truncates the poll the moment the window changes,
// which leaves the CLI prompting locally while the daemon still holds the
// request as answerable from the dashboard.
func hookTimeout() int {
	return int((approvalWindow() + hookTimeoutMargin).Seconds())
}

// approvalWindow mirrors the daemon's own resolution of the window
// (SWARMERY_APPROVAL_TIMEOUT → approvals.DefaultTimeout, see cmd/swarmery's
// envApprovalTimeout). The installer and the daemon MUST agree on it: the
// installed timeout exists only to cover the window the daemon will hold.
func approvalWindow() time.Duration {
	if v := os.Getenv("SWARMERY_APPROVAL_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return approvals.DefaultTimeout
}

// managedEvent binds a Claude Code hook event to the shim verb that serves it
// and to the entry shape Install writes. This table is the single source of
// truth: install, uninstall and status all iterate it, so an event added here
// reaches all three at once.
type managedEvent struct {
	event   string // Claude Code hook event name
	verb    string // `swarmery hook <verb>`
	matcher string // "" ⇒ no matcher key, i.e. every invocation
	timeout int    // 0 ⇒ no timeout key; the shim self-limits
}

// The approvals channel (PermissionRequest polls for up to approval_timeout,
// hence the long per-hook timeout; Stop is fire-and-forget) plus the
// plugin-drift warning.
//
// SessionStart must ride on the daemon's own hook rather than on core's
// SessionStart hooks: the drift it warns about can BE a missing core, and a
// plugin that is not installed ships no hooks to warn with.
//
// A function, not a var: the PermissionRequest timeout is resolved from the
// approval window at call time, so an install and a status check in the same
// process still read the same env the daemon does.
func managedEvents() []managedEvent {
	return []managedEvent{
		{event: "PermissionRequest", verb: "permission-request", matcher: "*", timeout: hookTimeout()},
		{event: "Stop", verb: "stop"},
		{event: "SessionStart", verb: "session-start"},
	}
}

// System bundles the environment the hooks manager operates on; tests use a
// temp Home and an in-memory Out.
type System struct {
	Home string
	Out  io.Writer
}

// InstalledBin returns the path baked into hook commands: the launchd-managed
// daemon binary (~/.swarmery/bin/swarmery), NOT the invoking binary — hook
// entries must survive rebuilds of a dev checkout.
func (s *System) InstalledBin() string {
	return filepath.Join(s.Home, ".swarmery", "bin", "swarmery")
}

// SettingsPath returns <project>/.claude/settings.local.json.
func SettingsPath(project string) string {
	return filepath.Join(project, ".claude", "settings.local.json")
}

// command renders one hook command line. A non-zero port is baked in as an
// env prefix so hooked projects can target a non-default daemon port.
func (s *System) command(event string, port int) string {
	cmd := s.InstalledBin() + " hook " + event
	if port > 0 {
		cmd = fmt.Sprintf("SWARMERY_PORT=%d %s", port, cmd)
	}
	return cmd
}

// ── install ──────────────────────────────────────────────────────────────────

// Install writes (or refreshes) the swarmery hook entries (managedEvents) into
// the project's settings.local.json. Idempotent: when the file already
// contains exactly the desired entries nothing is written.
func (s *System) Install(project string, port int) error {
	path := SettingsPath(project)
	raw, root, existed, err := readSettings(path)
	if err != nil {
		return err
	}

	// Refresh semantics: strip any stale swarmery entries first, then append
	// the current ones — a changed binary path or timeout self-heals.
	stripOurs(root)
	hooks := ensureMap(root, "hooks")
	for _, me := range managedEvents() {
		entry := map[string]any{"type": "command", "command": s.command(me.verb, port)}
		if me.timeout > 0 {
			entry["timeout"] = me.timeout
		}
		group := map[string]any{"hooks": []any{entry}}
		if me.matcher != "" {
			group["matcher"] = me.matcher
		}
		hooks[me.event] = append(sliceOf(hooks[me.event]), group)
	}

	changed, err := writeSettings(path, raw, root, existed)
	if err != nil {
		return err
	}
	if changed {
		fmt.Fprintf(s.Out, "%s: hooks installed (%s)\n", project, path)
	} else {
		fmt.Fprintf(s.Out, "%s: already installed — no changes\n", project)
	}
	return nil
}

// Uninstall removes ONLY swarmery hook entries; foreign hooks and every other
// setting survive. A file that never contained swarmery entries is untouched.
func (s *System) Uninstall(project string) error {
	path := SettingsPath(project)
	raw, root, existed, err := readSettings(path)
	if err != nil {
		return err
	}
	if !existed {
		fmt.Fprintf(s.Out, "%s: not installed (no %s)\n", project, path)
		return nil
	}
	// A file without swarmery entries is left completely untouched — not
	// even re-formatted.
	if !stripOurs(root) {
		fmt.Fprintf(s.Out, "%s: not installed — no changes\n", project)
		return nil
	}

	if _, err := writeSettings(path, raw, root, existed); err != nil {
		return err
	}
	fmt.Fprintf(s.Out, "%s: swarmery hooks removed\n", project)
	return nil
}

// ── status ───────────────────────────────────────────────────────────────────

// State classifies one project's hook installation.
type State string

const (
	StateInstalled    State = "installed"
	StateStale        State = "stale" // present but binary path/shape drifted
	StateNotInstalled State = "not installed"
	StateBroken       State = "broken json"
)

// Inspect reports the installation state of one project.
func (s *System) Inspect(project string, port int) State {
	raw, err := os.ReadFile(SettingsPath(project))
	if os.IsNotExist(err) {
		return StateNotInstalled
	}
	if err != nil {
		return StateBroken
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return StateBroken
	}
	events := managedEvents()
	found := map[string]bool{}
	current := 0
	for _, me := range events {
		hooks, _ := root["hooks"].(map[string]any)
		for _, g := range sliceOf(hooks[me.event]) {
			group, _ := g.(map[string]any)
			for _, h := range sliceOf(group["hooks"]) {
				entry, _ := h.(map[string]any)
				cmd, _ := entry["command"].(string)
				if !strings.Contains(cmd, marker) {
					continue
				}
				found[me.event] = true
				// The timeout counts as much as the command: an entry whose
				// timeout trails the approval window gets the shim KILLED
				// mid-poll, so it must read stale and be refreshed, not pass as
				// installed.
				if cmd == s.command(me.verb, port) && timeoutOf(entry) == me.timeout {
					current++
				}
			}
		}
	}
	switch {
	case len(found) == 0:
		return StateNotInstalled
	// An install predating a newly managed event lands here as stale, which is
	// exactly right: it needs a refresh to gain the missing hook.
	case len(found) == len(events) && current == len(events):
		return StateInstalled
	default:
		return StateStale
	}
}

// timeoutOf reads an entry's "timeout" in the shape JSON gives it (a float64),
// returning 0 for an absent key — which is also managedEvent's "no timeout"
// value, so the two compare directly.
func timeoutOf(entry map[string]any) int {
	t, ok := entry["timeout"].(float64)
	if !ok {
		return 0
	}
	return int(t)
}

// Status prints a project → state table.
func (s *System) Status(projects []string, port int) error {
	for _, p := range projects {
		fmt.Fprintf(s.Out, "%-14s %s\n", s.Inspect(p, port), p)
	}
	return nil
}

// ── settings surgery helpers ─────────────────────────────────────────────────

// readSettings loads and parses the settings file. A missing file yields an
// empty root; a parse failure aborts (never write over a file we cannot read).
func readSettings(path string) (raw []byte, root map[string]any, existed bool, err error) {
	raw, err = os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, map[string]any{}, false, nil
	}
	if err != nil {
		return nil, nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, nil, true, fmt.Errorf(
			"%s is not valid JSON (%v) — aborting without writing; fix or remove the file and retry", path, err)
	}
	return raw, root, true, nil
}

// writeSettings marshals root (2-space indent, trailing newline) and writes
// it if it differs from the original bytes. The original is preserved as
// .bak before the FIRST swarmery write.
func writeSettings(path string, raw []byte, root map[string]any, existed bool) (changed bool, err error) {
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return false, err
	}
	out = append(out, '\n')
	if existed && bytes.Equal(out, raw) {
		return false, nil
	}
	if existed {
		bak := path + ".bak"
		if _, err := os.Stat(bak); os.IsNotExist(err) {
			if err := os.WriteFile(bak, raw, 0o644); err != nil {
				return false, fmt.Errorf("write backup %s: %w", bak, err)
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

// stripOurs removes every hook command entry containing the swarmery marker,
// dropping matcher groups / event arrays / the hooks object itself when the
// removal leaves them empty. Foreign entries are never touched. Reports
// whether anything was removed.
func stripOurs(root map[string]any) bool {
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		return false
	}
	removed := false
	for _, me := range managedEvents() {
		groups := sliceOf(hooks[me.event])
		if groups == nil {
			continue
		}
		var keptGroups []any
		for _, g := range groups {
			group, ok := g.(map[string]any)
			if !ok {
				keptGroups = append(keptGroups, g)
				continue
			}
			var keptHooks []any
			for _, h := range sliceOf(group["hooks"]) {
				entry, ok := h.(map[string]any)
				cmd, _ := entry["command"].(string)
				if ok && strings.Contains(cmd, marker) {
					removed = true
					continue // ours — drop
				}
				keptHooks = append(keptHooks, h)
			}
			if len(keptHooks) == 0 {
				continue // group existed only for our entries — drop it
			}
			group["hooks"] = keptHooks
			keptGroups = append(keptGroups, group)
		}
		if len(keptGroups) == 0 {
			delete(hooks, me.event)
		} else {
			hooks[me.event] = keptGroups
		}
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	}
	return removed
}

func ensureMap(root map[string]any, key string) map[string]any {
	if m, ok := root[key].(map[string]any); ok {
		return m
	}
	m := map[string]any{}
	root[key] = m
	return m
}

func sliceOf(v any) []any {
	s, _ := v.([]any)
	return s
}

// ProjectsFromDB lists the non-archived project paths known to the daemon DB
// (for --all).
func ProjectsFromDB(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT path FROM projects WHERE archived = 0 ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		if strings.HasPrefix(p, "/") { // skip the '(unknown)' placeholder rows
			out = append(out, p)
		}
	}
	return out, rows.Err()
}
