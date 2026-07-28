// Package marketplace reads the plugin catalog from the marketplace clone
// Claude Code keeps under <claudeDir>/plugins/marketplaces/<name>/.claude-plugin/
// marketplace.json. The clone is refreshed by Claude Code itself on marketplace
// update, so the catalog matches what is actually installable on this machine —
// unlike the plugin cache, which only holds packs already installed.
package marketplace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	// Plugins preserves manifest order (core first by convention).
	Plugins []Plugin
}

type manifest struct {
	Metadata struct {
		Version string `json:"version"`
	} `json:"metadata"`
	Plugins []Plugin `json:"plugins"`
}

// Read parses the marketplace manifest for the named marketplace. A missing
// clone surfaces as fs.ErrNotExist (unwrapped ReadFile error) so callers can
// distinguish "marketplace not installed" from a parse failure.
func Read(claudeDir, name string) (*Catalog, error) {
	path := filepath.Join(claudeDir, "plugins", "marketplaces", name, ".claude-plugin", "marketplace.json")
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
	return &Catalog{Version: m.Metadata.Version, Plugins: m.Plugins}, nil
}

// PluginVersion reads the version of one catalogued pack from its own
// plugin.json inside the marketplace clone. Returns "" (no error) when the
// pack has no readable manifest — a catalogue entry whose source is missing
// is a stale-clone symptom, not a detector failure.
func PluginVersion(claudeDir, marketplaceName string, p Plugin) string {
	if p.Source == "" {
		return ""
	}
	path := filepath.Join(claudeDir, "plugins", "marketplaces", marketplaceName,
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
