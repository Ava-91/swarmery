package onboard

// Capture/restore of ONE enabledPlugins key in the user-level settings.json.
//
// Why this exists: for a consumer whose <project>/.claude is a symlinked
// overlay (the multi-repo pattern in CLAUDE.md / EXTENDING.md), a `--scope
// project` install cannot run at all — the claude CLI refuses to write settings
// through a symlinked directory (SymlinkWriteRefusedError). The only install
// the CLI will perform for such a project is the default `--scope user` one.
//
// That fallback works for drift purposes: plugindrift.resolveFor counts any
// Scope=="user" entry as available to EVERY project, so a user-scope install
// clears plugin_enabled_not_installed for the symlinked consumer. But the CLI
// also sets enabledPlugins["<id>"]=true in the USER settings.json as a side
// effect, which would silently switch the pack on for every other project on
// the machine.
//
// Verified against claude CLI 2.1.220: deleting that key afterwards leaves the
// entry in `plugin list --json` as scope=user, enabled=false — still installed,
// still drift-resolving, no longer globally enabled. resolveFor reads Scope and
// never reads Enabled, so the undo is genuine rather than cosmetic. That
// measurement is what makes this file correct; if a future CLI drops
// un-enabled plugins from `plugin list --json`, the fallback silently stops
// resolving drift and this comment is the place to start.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrGlobalSettingsUnreadable — the user settings.json exists but cannot be
// read or parsed. The caller must NOT proceed with a user-scope install: the
// global enable it causes could not be reverted afterwards.
var ErrGlobalSettingsUnreadable = errors.New("user settings.json unreadable")

// GlobalEnable is the pre-install state of one enabledPlugins key.
type GlobalEnable struct {
	path    string
	key     string
	present bool
	value   any
}

// CaptureGlobalEnable records the current value of enabledPlugins[pluginID] in
// <claudeDir>/settings.json so a later Restore can put it back verbatim.
//
// A missing settings.json is fine — the key is simply absent, and Restore will
// delete it from whatever the CLI creates. Anything else (unreadable file,
// malformed JSON, enabledPlugins of an unexpected shape) is an error, because
// a snapshot that cannot be trusted must stop the install rather than risk an
// irreversible global enable.
func CaptureGlobalEnable(claudeDir, pluginID string) (*GlobalEnable, error) {
	sPath := filepath.Join(claudeDir, "settings.json")
	g := &GlobalEnable{path: sPath, key: pluginID}

	raw, err := os.ReadFile(sPath)
	if os.IsNotExist(err) {
		return g, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGlobalSettingsUnreadable, err)
	}
	settings := map[string]any{}
	if uerr := json.Unmarshal(raw, &settings); uerr != nil {
		return nil, fmt.Errorf("%w: %v", ErrGlobalSettingsUnreadable, uerr)
	}
	ep, ok := settings["enabledPlugins"]
	if !ok {
		return g, nil
	}
	epMap, ok := ep.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: enabledPlugins has an unexpected shape", ErrGlobalSettingsUnreadable)
	}
	g.value, g.present = epMap[pluginID]
	return g, nil
}

// Restore puts enabledPlugins[key] back to its captured state: deleted when it
// was absent, set to the captured value when it was present.
//
// It deliberately re-reads settings.json instead of writing back the whole
// captured document. The daemon's own provision engine also shells out to
// user-scope `claude plugin install` (provision/service.go), so this file has
// concurrent writers; a narrow read-modify-write touching one key preserves
// whatever else landed in the meantime. The race window is not zero — a
// competing write between this read and this write is lost — but it is
// milliseconds wide and bounded to a single key, whereas restoring a whole
// snapshot would clobber every concurrent change unconditionally.
//
// A no-op (the key already holds the captured state) writes nothing, so the
// common case leaves no backup file and no mtime churn.
func (g *GlobalEnable) Restore() error {
	raw, err := os.ReadFile(g.path)
	if os.IsNotExist(err) {
		// Nothing was created; nothing to revert.
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrGlobalSettingsUnreadable, err)
	}
	settings := map[string]any{}
	if uerr := json.Unmarshal(raw, &settings); uerr != nil {
		return fmt.Errorf("%w: %v", ErrGlobalSettingsUnreadable, uerr)
	}

	epMap, _ := settings["enabledPlugins"].(map[string]any)
	if epMap == nil {
		if !g.present {
			return nil // absent before, absent now
		}
		epMap = map[string]any{}
		settings["enabledPlugins"] = epMap
	}

	cur, curPresent := epMap[g.key]
	switch {
	case g.present && curPresent && cur == g.value:
		return nil
	case !g.present && !curPresent:
		return nil
	case g.present:
		epMap[g.key] = g.value
	default:
		delete(epMap, g.key)
	}

	if err := os.WriteFile(g.path+".bak", raw, 0o644); err != nil {
		return fmt.Errorf("write backup %s.bak: %w", g.path, err)
	}
	return writeJSON(g.path, settings)
}
