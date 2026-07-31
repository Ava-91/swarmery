// Package projectscan is the read-only counterpart to internal/onboard: given a
// consumer project's directory it reports whether the swarmery plugin is enabled
// (its .claude/settings.json) and enumerates the project-LOCAL agent/skill/
// command/hook files under .claude/. It never writes and never errors the whole
// listing on a single unreadable project — a missing or malformed settings.json
// simply yields a nil PluginState so the caller renders the project as
// telemetry-only rather than failing the request.
//
// Scope boundary: Components resolves ONLY the files a project ships locally.
// Components provided by the enabled plugins (core@swarmery + packs) live in the
// plugin cache (~/.claude/plugins/cache) and are deliberately NOT enumerated
// here — that is a later stretch step. The enabled packs are still reported by
// name via PluginState.Packs.
package projectscan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PluginState is the swarmery-plugin view of a project, derived from its
// .claude/settings.json. The zero value is meaningful: Managed=false with empty
// packs describes a project whose settings.json exists but does not enable
// swarmery.
type PluginState struct {
	// Managed reports enabledPlugins["core@swarmery"] === true.
	Managed bool `json:"managed"`
	// Packs are the other "<pack>@swarmery" entries enabled alongside core,
	// with the "@swarmery" suffix stripped, sorted for a stable response.
	Packs []string `json:"packs"`
	// Marketplace is extraKnownMarketplaces.swarmery.source.repo, "" when absent.
	Marketplace string `json:"marketplace"`
	// UnderOnboardRoot reports whether the project path is under one of the
	// daemon's onboarding roots — i.e. whether the (fenced) detach endpoint may
	// operate on it. Purely advisory for the UI; the write path re-checks.
	UnderOnboardRoot bool `json:"underOnboardRoot"`
	// OverlaySources names the declared settings overlays that contributed to
	// this state (internal/settingsoverlay). Empty/omitted for the ordinary case
	// where the repo's own settings.json is the whole story — its presence is
	// what tells a reader that managed/packs describe more than this repo.
	OverlaySources []string `json:"overlaySources,omitempty"`
}

// marketplaceSuffix is the "@<marketplace>" tag every swarmery plugin key
// carries in enabledPlugins (e.g. "core@swarmery").
const marketplaceSuffix = "@swarmery"

// Settings is the subset of a Claude Code settings.json that plugin state is
// derived from. It is exported because settings no longer come only from the
// repo: internal/settingsoverlay feeds DECLARED out-of-repo files through this
// same parser, and two copies of a file format is how they drift apart.
type Settings struct {
	// EnabledPlugins is the raw enabledPlugins map ("<name>@<marketplace>" →
	// on/off). Explicit false entries are kept: an overlay layered on top has to
	// be able to turn something OFF, which a presence-only set cannot express.
	EnabledPlugins map[string]bool
	// Marketplaces maps extraKnownMarketplaces.<name> → source.repo.
	Marketplaces map[string]string
}

// settingsShape is the on-disk JSON shape behind Settings.
type settingsShape struct {
	EnabledPlugins         map[string]bool `json:"enabledPlugins"`
	ExtraKnownMarketplaces map[string]struct {
		Source struct {
			Repo string `json:"repo"`
		} `json:"source"`
	} `json:"extraKnownMarketplaces"`
}

// ReadSettings parses one settings.json. Unlike the Read* helpers below it
// REPORTS failure (missing file, malformed JSON) — the callers decide how
// tolerant to be, and the overlay reader needs to tell "not declared" from
// "declared but gone" to log the latter once.
func ReadSettings(path string) (Settings, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Settings{}, err
	}
	var s settingsShape
	if err := json.Unmarshal(raw, &s); err != nil {
		return Settings{}, err
	}
	out := Settings{EnabledPlugins: s.EnabledPlugins}
	if len(s.ExtraKnownMarketplaces) > 0 {
		out.Marketplaces = make(map[string]string, len(s.ExtraKnownMarketplaces))
		for name, mp := range s.ExtraKnownMarketplaces {
			out.Marketplaces[name] = mp.Source.Repo
		}
	}
	return out, nil
}

// SettingsOverlay is one declared settings file that also applies to a project,
// already read. Name is the operator's label, echoed back as provenance.
type SettingsOverlay struct {
	Name     string
	Settings Settings
}

// ReadPluginState reads <projectPath>/.claude/settings.json and reports the
// swarmery plugin state. A missing or malformed file returns (nil, nil): the
// project is simply not swarmery-managed as far as we can tell, which must not
// fail the list. roots is the daemon's onboarding allow-list, used only to
// compute UnderOnboardRoot (pass nil to skip that hint).
//
// overlays are declared settings files that also apply to this project
// (internal/settingsoverlay resolves them). They are folded ON TOP of the repo's
// settings because that is where they sit in a real session: a launcher passing
// `claude --settings <file>` outranks the repo's .claude/settings.json, so on a
// key conflict the overlay wins, and later overlays win over earlier ones. With
// no overlays this is byte-for-byte the previous behaviour.
func ReadPluginState(projectPath string, roots []string, overlays ...SettingsOverlay) (*PluginState, error) {
	repo, rerr := ReadSettings(filepath.Join(projectPath, ".claude", "settings.json"))
	merged, sources := mergeSettings(repo, overlays)
	if rerr != nil && len(sources) == 0 {
		// Nothing readable in the repo AND nothing declared for it: not managed
		// as far as we can tell. Not an error — see the package doc.
		return nil, nil //nolint:nilerr // absent settings = not managed
	}

	st := &PluginState{
		UnderOnboardRoot: underAnyRoot(projectPath, roots),
		OverlaySources:   sources,
	}
	for key, on := range merged.EnabledPlugins {
		if !on || !strings.HasSuffix(key, marketplaceSuffix) {
			continue
		}
		name := strings.TrimSuffix(key, marketplaceSuffix)
		if name == "core" {
			st.Managed = true
			continue
		}
		st.Packs = append(st.Packs, name)
	}
	sort.Strings(st.Packs)
	st.Marketplace = merged.Marketplaces["swarmery"]
	return st, nil
}

// mergeSettings folds the overlays over the repo's settings, key by key, and
// returns the names of the overlays that actually supplied something.
//
// "Contributed" is deliberately narrow: an overlay that covers the project but
// declares no enabledPlugins and no marketplaces changed nothing, and naming it
// in overlaySources would be provenance for an effect that never happened.
func mergeSettings(repo Settings, overlays []SettingsOverlay) (Settings, []string) {
	out := Settings{
		EnabledPlugins: make(map[string]bool, len(repo.EnabledPlugins)),
		Marketplaces:   make(map[string]string, len(repo.Marketplaces)),
	}
	for k, v := range repo.EnabledPlugins {
		out.EnabledPlugins[k] = v
	}
	for k, v := range repo.Marketplaces {
		out.Marketplaces[k] = v
	}
	var sources []string
	for _, ov := range overlays {
		if len(ov.Settings.EnabledPlugins) == 0 && len(ov.Settings.Marketplaces) == 0 {
			continue
		}
		for k, v := range ov.Settings.EnabledPlugins {
			out.EnabledPlugins[k] = v
		}
		for k, v := range ov.Settings.Marketplaces {
			out.Marketplaces[k] = v
		}
		sources = append(sources, ov.Name)
	}
	return out, sources
}

// ReadEnabledPlugins returns every plugin id ("<name>@<marketplace>") that
// <projectPath>/.claude/settings.json switches on, across all marketplaces.
// A missing or malformed file returns (nil, nil) — same tolerance as
// ReadPluginState: an unreadable project must never fail a scan.
//
// Repo-only ON PURPOSE, unlike ReadPluginState: its one caller is the plugin
// drift detector, whose question ("is what this project enables actually
// installed for it?") and whose repair action (`claude plugin install --scope
// project`, which WRITES the repo's settings.json) are both repo-scoped. See
// internal/plugindrift/ticker.go loadProjects.
func ReadEnabledPlugins(projectPath string) ([]string, error) {
	s, err := ReadSettings(filepath.Join(projectPath, ".claude", "settings.json"))
	if err != nil {
		return nil, nil //nolint:nilerr // absent/malformed settings = nothing enabled here
	}
	ids := make([]string, 0, len(s.EnabledPlugins))
	for key, on := range s.EnabledPlugins {
		if on && strings.Contains(key, "@") {
			ids = append(ids, key)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// Component is one project-local registry entry (agent, skill, command or hook).
type Component struct {
	Name string `json:"name"`
	// Source is always "local" here; plugin-provided components (a later step)
	// will carry "core@swarmery" / "<pack>@swarmery".
	Source string `json:"source"`
}

// ComponentCounts is the at-a-glance tally rendered on the list/detail header.
type ComponentCounts struct {
	Agents   int `json:"agents"`
	Skills   int `json:"skills"`
	Commands int `json:"commands"`
	Hooks    int `json:"hooks"`
}

// Components is the project-local registry inventory.
type Components struct {
	Agents   []Component     `json:"agents"`
	Skills   []Component     `json:"skills"`
	Commands []Component     `json:"commands"`
	Hooks    []Component     `json:"hooks"`
	Counts   ComponentCounts `json:"counts"`
}

// Components enumerates the project-local .claude/{agents,skills,commands,hooks}
// entries. Agents/commands are *.md files; skills are directories (each holds a
// SKILL.md); hooks are the files under .claude/hooks/. Missing directories yield
// empty slices, never an error — the returned Components is always non-nil.
func ReadComponents(projectPath string) (*Components, error) {
	claudeDir := filepath.Join(projectPath, ".claude")
	c := &Components{
		Agents:   markdownComponents(filepath.Join(claudeDir, "agents")),
		Skills:   dirComponents(filepath.Join(claudeDir, "skills")),
		Commands: markdownComponents(filepath.Join(claudeDir, "commands")),
		Hooks:    fileComponents(filepath.Join(claudeDir, "hooks")),
	}
	c.Counts = ComponentCounts{
		Agents:   len(c.Agents),
		Skills:   len(c.Skills),
		Commands: len(c.Commands),
		Hooks:    len(c.Hooks),
	}
	return c, nil
}

// markdownComponents lists *.md files in dir, named without the extension.
func markdownComponents(dir string) []Component {
	return scanDir(dir, func(e os.DirEntry) (string, bool) {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || strings.HasPrefix(name, ".") {
			return "", false
		}
		return strings.TrimSuffix(name, ".md"), true
	})
}

// dirComponents lists subdirectories of dir (skills: one dir per skill).
func dirComponents(dir string) []Component {
	return scanDir(dir, func(e os.DirEntry) (string, bool) {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			return "", false
		}
		return e.Name(), true
	})
}

// fileComponents lists regular files in dir (hooks: scripts / hooks.json).
func fileComponents(dir string) []Component {
	return scanDir(dir, func(e os.DirEntry) (string, bool) {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			return "", false
		}
		return e.Name(), true
	})
}

// scanDir reads dir and returns a sorted []Component for entries pick accepts.
// A missing/unreadable dir yields an empty slice (never nil), so JSON renders
// `[]` not `null`.
func scanDir(dir string, pick func(os.DirEntry) (string, bool)) []Component {
	out := []Component{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if name, ok := pick(e); ok {
			out = append(out, Component{Name: name, Source: "local"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// underAnyRoot reports whether path is one of roots or nested inside it. It is a
// lexical check on cleaned absolute paths — enough for the advisory UI hint; the
// detach write path performs the symlink-safe fence (api.resolveUnderRoots).
func underAnyRoot(path string, roots []string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	abs = filepath.Clean(abs)
	for _, root := range roots {
		rootAbs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(filepath.Clean(rootAbs), abs)
		if err != nil {
			continue
		}
		if rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel)) {
			return true
		}
	}
	return false
}
