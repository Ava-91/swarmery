package api

// Settings overlays — the one place this package asks "which DECLARED settings
// files also apply to this project" before deriving managed/packs from a repo's
// .claude/settings.json.
//
// Background in internal/settingsoverlay: a project whose plugin set is injected
// by an external launcher at CLI precedence (`claude --settings <file>`) keeps
// enabledPlugins out of the repo, so repo-only detection reported managed:false
// for a project running the full plugin set in every session.
//
// Not to be confused with AttachOverlaysDir (system.go), which is the
// marketplace's overlays/ directory of consumer project.json examples. Different
// thing entirely; same English word.

import (
	"strings"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/projectscan"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/settingsoverlay"
)

// settingsOverlays is attached once at startup (nil in tests that never wire
// one). Mirrors AttachOnboard (onboard.go:41) / AttachProjectsRoots.
var settingsOverlays *settingsoverlay.Reader

// AttachSettingsOverlays points the plugin-state readers at an overlay
// descriptor. Passing nil (or never calling it) leaves every derivation
// repo-only, which is the pre-overlay behaviour.
func AttachSettingsOverlays(r *settingsoverlay.Reader) { settingsOverlays = r }

// overlaysFor resolves the overlays covering projectPath. Never errors: the
// Reader degrades a missing/malformed descriptor to an empty list, and a nil
// Reader is "not configured".
func overlaysFor(projectPath string) []projectscan.SettingsOverlay {
	return settingsOverlays.For(projectPath)
}

// overlayPacks names the swarmery plugins an overlay list switches ON, with the
// "@swarmery" suffix stripped ("core" included). It exists so a surface can tell
// an overlay-provided plugin from a repo-provided one WITHOUT re-reading files —
// the two are not interchangeable everywhere (see projectPlugins, where drift is
// only ever evaluated against the repo's own settings).
func overlayPacks(overlays []projectscan.SettingsOverlay) map[string]bool {
	out := map[string]bool{}
	for _, ov := range overlays {
		for key, on := range ov.Settings.EnabledPlugins {
			if !on || !strings.HasSuffix(key, "@"+pluginMarketplace) {
				continue
			}
			out[strings.TrimSuffix(key, "@"+pluginMarketplace)] = true
		}
	}
	return out
}
