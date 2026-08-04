package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/settingsoverlay"
)

// attachOverlayDescriptor points the api package's overlay reader at path and
// restores the previous reader on cleanup. path may not exist — that is one of
// the degradation cases under test.
func attachOverlayDescriptor(t *testing.T, path string) {
	t.Helper()
	prev := settingsOverlays
	AttachSettingsOverlays(settingsoverlay.New(path))
	t.Cleanup(func() { settingsOverlays = prev })
}

// writeOverlay writes an overlay settings file plus a descriptor naming it for
// roots, wires the reader at the descriptor, and returns the descriptor path.
// An empty settingsBody writes no settings file at all — the dangling
// settingsPath case.
func writeOverlay(t *testing.T, name, settingsBody string, roots ...string) string {
	t.Helper()
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	if settingsBody != "" {
		if err := os.WriteFile(settings, []byte(settingsBody), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	desc := filepath.Join(dir, "overlays.json")
	body, err := json.Marshal(map[string]any{"overlays": []map[string]any{
		{"name": name, "settingsPath": settings, "roots": roots},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(desc, body, 0o644); err != nil {
		t.Fatal(err)
	}
	attachOverlayDescriptor(t, desc)
	return desc
}

// TestProjectPluginsOverlayEnablesUnmanagedRepo is the whole point of the
// feature: a repo that enables nothing, whose plugin set is injected by a
// launcher at CLI precedence, must still report as managed.
func TestProjectPluginsOverlayEnablesUnmanagedRepo(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	path := projectPath(t, srv.URL, "1")
	writeProjectSettings(t, path, `{}`)
	writeOverlay(t, "acme", `{
		"enabledPlugins": {"core@swarmery": true, "uav-pack@swarmery": true},
		"extraKnownMarketplaces": {"swarmery": {"source": {"repo": "owner/swarmery"}}}
	}`, filepath.Dir(path))

	resp := getPluginsResponse(t, srv.URL, "1")
	if len(resp.OverlaySources) != 1 || resp.OverlaySources[0] != "acme" {
		t.Errorf("overlaySources = %v, want [acme]", resp.OverlaySources)
	}
	// Overlay-provided plugins are enabled but "unknown": plugin drift is only
	// ever scanned from the repo's own settings.json, so nothing checked them.
	want := []projectPluginDTO{
		{Name: "core", Description: "the core plugin", Enabled: true, Locked: true,
			Status: "unknown", Detail: overlayStatusDetail},
		{Name: "uav-pack", Description: "UAV domain pack", Enabled: true,
			Status: "unknown", Detail: overlayStatusDetail},
		{Name: "lsp-pack", Description: "LSP pack", Enabled: false, Status: "unknown"},
	}
	assertPluginRows(t, resp.Plugins, want)
}

// TestProjectPluginsOverlayWithNoRepoSettingsFile covers the same path when the
// project has no .claude/settings.json at all — ReadPluginState used to return
// a nil state there, which would have rendered every row off.
func TestProjectPluginsOverlayWithNoRepoSettingsFile(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	path := projectPath(t, srv.URL, "1")
	if err := os.Remove(filepath.Join(path, ".claude", "settings.json")); err != nil {
		t.Fatal(err)
	}
	writeOverlay(t, "acme", `{"enabledPlugins": {"core@swarmery": true}}`, filepath.Dir(path))

	resp := getPluginsResponse(t, srv.URL, "1")
	if !resp.Plugins[0].Enabled {
		t.Errorf("core enabled = false with an overlay and no repo settings, want true")
	}
	if len(resp.OverlaySources) != 1 {
		t.Errorf("overlaySources = %v, want [acme]", resp.OverlaySources)
	}
}

// TestProjectPluginsOverlayWinsOnKeyConflict pins the merge precedence: the
// overlay rides at CLI precedence in a real session, so it outranks the repo on
// a shared key — including turning something OFF.
func TestProjectPluginsOverlayWinsOnKeyConflict(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	path := projectPath(t, srv.URL, "1")
	writeProjectSettings(t, path, `{
		"enabledPlugins": {"core@swarmery": true, "lsp-pack@swarmery": true}
	}`)
	writeOverlay(t, "acme", `{
		"enabledPlugins": {"lsp-pack@swarmery": false, "uav-pack@swarmery": true}
	}`, filepath.Dir(path))

	resp := getPluginsResponse(t, srv.URL, "1")
	want := []projectPluginDTO{
		// Repo-enabled and untouched by the overlay: real drift status.
		{Name: "core", Description: "the core plugin", Enabled: true, Locked: true, Status: "ok"},
		// Overlay-only enable.
		{Name: "uav-pack", Description: "UAV domain pack", Enabled: true,
			Status: "unknown", Detail: overlayStatusDetail},
		// Repo said true, overlay said false — overlay wins.
		{Name: "lsp-pack", Description: "LSP pack", Enabled: false, Status: "unknown"},
	}
	assertPluginRows(t, resp.Plugins, want)
}

// TestProjectPluginsOutsideOverlayRootsIsRepoOnly — a declared overlay must not
// leak into projects it does not cover.
func TestProjectPluginsOutsideOverlayRootsIsRepoOnly(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	writeOverlay(t, "acme", `{"enabledPlugins": {"uav-pack@swarmery": true}}`, t.TempDir())

	resp := getPluginsResponse(t, srv.URL, "1")
	if len(resp.OverlaySources) != 0 {
		t.Errorf("overlaySources = %v, want none for an uncovered project", resp.OverlaySources)
	}
	// Seeded repo settings enable core + iot-pack; uav-pack must stay off.
	for _, p := range resp.Plugins {
		if p.Name == "uav-pack" && p.Enabled {
			t.Error("uav-pack enabled by an overlay whose roots do not cover this project")
		}
	}
	if !resp.Plugins[0].Enabled {
		t.Error("core enabled = false, want the repo's own settings to still apply")
	}
}

// TestProjectPluginsDegradesOnBrokenDescriptor is the hard requirement: every
// way the descriptor can rot must answer 200 with the repo-only view.
func TestProjectPluginsDegradesOnBrokenDescriptor(t *testing.T) {
	cases := map[string]func(t *testing.T, projectParent string){
		"missing descriptor": func(t *testing.T, _ string) {
			attachOverlayDescriptor(t, filepath.Join(t.TempDir(), "absent.json"))
		},
		"malformed descriptor": func(t *testing.T, _ string) {
			p := filepath.Join(t.TempDir(), "overlays.json")
			if err := os.WriteFile(p, []byte(`{"overlays": [{ nope`), 0o644); err != nil {
				t.Fatal(err)
			}
			attachOverlayDescriptor(t, p)
		},
		"dangling settingsPath": func(t *testing.T, parent string) {
			writeOverlay(t, "acme", "", parent) // descriptor names a file that is not there
		},
	}
	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			srv, _ := projectsTestServer(t)
			seedPluginCatalog(t, threePackManifest)
			wire(t, filepath.Dir(projectPath(t, srv.URL, "1")))

			resp := getPluginsResponse(t, srv.URL, "1")
			if len(resp.OverlaySources) != 0 {
				t.Errorf("overlaySources = %v, want none", resp.OverlaySources)
			}
			// Repo-only view survives intact: seeded settings enable core.
			if !resp.Plugins[0].Enabled || resp.Plugins[0].Status != "ok" {
				t.Errorf("core row = %+v, want the repo-only enabled/ok view", resp.Plugins[0])
			}
		})
	}
}

// TestProjectsListReportsOverlayState covers the OTHER managed-derivation site:
// the project list DTO (handlers.go), which is what the dashboard reads.
func TestProjectsListReportsOverlayState(t *testing.T) {
	srv, _ := projectsTestServer(t)
	path := projectPath(t, srv.URL, "1")
	writeProjectSettings(t, path, `{}`)
	writeOverlay(t, "acme", `{
		"enabledPlugins": {"core@swarmery": true, "iot-pack@swarmery": true}
	}`, filepath.Dir(path))

	for _, p := range getProjectsList(t, srv.URL+"/api/projects") {
		if p.ID != 1 {
			continue
		}
		if p.Plugin == nil {
			t.Fatal("project 1 plugin state is nil, want the overlay-derived state")
		}
		if !p.Plugin.Managed {
			t.Error("managed = false, want true from the overlay")
		}
		if len(p.Plugin.Packs) != 1 || p.Plugin.Packs[0] != "iot-pack" {
			t.Errorf("packs = %v, want [iot-pack]", p.Plugin.Packs)
		}
		if len(p.Plugin.OverlaySources) != 1 || p.Plugin.OverlaySources[0] != "acme" {
			t.Errorf("overlaySources = %v, want [acme]", p.Plugin.OverlaySources)
		}
		return
	}
	t.Fatal("project 1 missing from the list")
}
