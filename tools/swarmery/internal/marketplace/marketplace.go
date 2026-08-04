// Package marketplace reads the plugin catalog from a marketplace's
// <root>/.claude-plugin/marketplace.json. The root is whatever Claude Code
// recorded for that marketplace in <claudeDir>/plugins/known_marketplaces.json:
// the clone under plugins/marketplaces/<name> for a github-source marketplace,
// or the operator's own checkout for a directory-source one. A github clone is
// refreshed by Claude Code itself on marketplace update, so the catalog matches
// what is actually installable on this machine — unlike the plugin cache, which
// only holds packs already installed.
package marketplace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Plugin is one catalog entry. The manifest carries no per-plugin version —
// pack versions live in each pack's own plugin.json, reachable via Source —
// so the catalog exposes only what the manifest guarantees.
type Plugin struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Source is the pack's path inside the clone (e.g. "./plugins/core").
	Source string `json:"source"`
}

// Catalog is the parsed marketplace manifest.
type Catalog struct {
	// Version is metadata.version (tracks the core plugin's version).
	Version string
	// Root is the directory the manifest was read from — the clone under
	// plugins/marketplaces/<name>, or the operator's own checkout for a
	// directory-source marketplace. Exposed because a pack's own files live at
	// filepath.Join(Root, plugin.Source) and callers outside this package need
	// to reach them (internal/pluginreq reads a pack's requirements.json there).
	// Without it, catalogRoot's two-branch resolution would have to be
	// reimplemented — and kept in sync — by every such caller.
	Root string
	// Plugins preserves manifest order (core first by convention).
	Plugins []Plugin
}

type manifest struct {
	Metadata struct {
		Version string `json:"version"`
	} `json:"metadata"`
	Plugins []Plugin `json:"plugins"`
}

// registryFile is <claudeDir>/plugins/known_marketplaces.json — Claude Code's
// record of every marketplace added on this machine, keyed by marketplace name.
const registryFile = "known_marketplaces.json"

// catalogRoot resolves the directory holding <root>/.claude-plugin/marketplace.json
// for the named marketplace.
//
// Claude Code installs a github-source marketplace as a clone under
// plugins/marketplaces/<name> but a directory-source one stays wherever the
// operator's checkout lives, with NOTHING under plugins/marketplaces. The
// registry's installLocation is the only field that covers both, so it wins when
// readable; every other case falls back to the legacy clone path, which for a
// github entry is byte-identical to installLocation anyway.
//
// Deliberately does NOT stat the resolved root. A stale registry entry must
// surface as fs.ErrNotExist from the subsequent ReadFile — the same signal
// callers already branch on — rather than silently falling back to a legacy
// clone that a re-add left behind, which would serve provably stale catalog data.
func catalogRoot(claudeDir, name string) string {
	legacy := filepath.Join(claudeDir, "plugins", "marketplaces", name)
	raw, err := os.ReadFile(filepath.Join(claudeDir, "plugins", registryFile))
	if err != nil {
		return legacy
	}
	var reg map[string]struct {
		InstallLocation string `json:"installLocation"`
	}
	if err := json.Unmarshal(raw, &reg); err != nil {
		return legacy
	}
	e, ok := reg[name]
	// A relative installLocation would resolve against the daemon's CWD —
	// nondeterministic and outside anything the operator sanctioned. Claude Code
	// always writes an absolute path, so rejecting is free.
	if !ok || !filepath.IsAbs(e.InstallLocation) {
		return legacy
	}
	return filepath.Clean(e.InstallLocation)
}

// Read parses the marketplace manifest for the named marketplace. A missing
// clone surfaces as fs.ErrNotExist (unwrapped ReadFile error) so callers can
// distinguish "marketplace not installed" from a parse failure.
func Read(claudeDir, name string) (*Catalog, error) {
	root := catalogRoot(claudeDir, name)
	path := filepath.Join(root, ".claude-plugin", "marketplace.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if m.Plugins == nil {
		// Guarantee [] over null once the catalog reaches a JSON response,
		// matching the projectscan convention.
		m.Plugins = []Plugin{}
	}
	return &Catalog{Version: m.Metadata.Version, Root: root, Plugins: m.Plugins}, nil
}

// PluginVersion reads the version of one catalogued pack from its own
// plugin.json inside the marketplace clone. Returns "" (no error) when the
// pack has no readable manifest — a catalogue entry whose source is missing
// is a stale-clone symptom, not a detector failure.
func PluginVersion(claudeDir, marketplaceName string, p Plugin) string {
	if p.Source == "" {
		return ""
	}
	// Source comes from a manifest that, for a directory-source marketplace, sits
	// in a working tree the operator edits. `..` never appears in a legitimate
	// entry, and refusing it keeps a hand-edited manifest from walking the reader
	// out of the marketplace root.
	if strings.Contains(filepath.ToSlash(p.Source), "..") {
		return ""
	}
	path := filepath.Join(catalogRoot(claudeDir, marketplaceName),
		filepath.Clean(p.Source), ".claude-plugin", "plugin.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	return m.Version
}
