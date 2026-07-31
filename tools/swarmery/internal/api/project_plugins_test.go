package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedPluginCatalog writes a marketplace.json fixture into a temp claudeDir
// (mirroring writeManifest in internal/marketplace/marketplace_test.go), points
// the project-plugins endpoints at it via AttachPluginCatalog, and restores the
// global on cleanup.
// It returns the claudeDir it created so a caller can drop a
// known_marketplaces.json beside the clone; existing callers ignore it.
func seedPluginCatalog(t *testing.T, body string) string {
	t.Helper()
	claudeDir := t.TempDir()
	dir := filepath.Join(claudeDir, "plugins", "marketplaces", "swarmery", ".claude-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "marketplace.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	AttachPluginCatalog(claudeDir)
	t.Cleanup(func() { AttachPluginCatalog("") })
	return claudeDir
}

const threePackManifest = `{
	"name": "swarmery",
	"metadata": {"version": "1.13.0"},
	"plugins": [
		{"name": "core", "source": "./plugins/core", "description": "the core plugin"},
		{"name": "uav-pack", "source": "./plugins/uav-pack", "description": "UAV domain pack"},
		{"name": "lsp-pack", "source": "./plugins/lsp-pack", "description": "LSP pack"}
	]
}`

func getPluginsResponse(t *testing.T, srvURL, projectID string) projectPluginsResponse {
	t.Helper()
	var resp projectPluginsResponse
	getJSON(t, srvURL+"/api/projects/"+projectID+"/plugins", &resp)
	return resp
}

func TestProjectPluginsMergesCatalogAndState(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	// Overwrite the seeded settings: enable core + lsp-pack only.
	writeProjectSettings(t, projectPath(t, srv.URL, "1"), `{
		"enabledPlugins": {"core@swarmery": true, "lsp-pack@swarmery": true}
	}`)

	resp := getPluginsResponse(t, srv.URL, "1")
	if resp.MarketplaceVersion != "1.13.0" {
		t.Errorf("marketplaceVersion = %q, want 1.13.0", resp.MarketplaceVersion)
	}
	// Status: enabled rows with no findings are "ok"; a disabled row is
	// "unknown" — nothing was checked for it, so nothing is claimed.
	want := []projectPluginDTO{
		{Name: "core", Description: "the core plugin", Enabled: true, Locked: true, Status: "ok"},
		{Name: "uav-pack", Description: "UAV domain pack", Enabled: false, Locked: false, Status: "unknown"},
		{Name: "lsp-pack", Description: "LSP pack", Enabled: true, Locked: false, Status: "ok"},
	}
	if len(resp.Plugins) != len(want) {
		t.Fatalf("plugins len = %d, want %d (%+v)", len(resp.Plugins), len(want), resp.Plugins)
	}
	for i, w := range want {
		if resp.Plugins[i] != w {
			t.Errorf("plugins[%d] = %+v, want %+v (manifest order)", i, resp.Plugins[i], w)
		}
	}
}

func TestProjectPluginsCanWriteFollowsFence(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	path := projectPath(t, srv.URL, "1")
	t.Cleanup(func() { AttachOnboard(OnboardConfig{}) })

	// No onboarding roots → the write fence would reject, canWrite=false.
	AttachOnboard(OnboardConfig{})
	if resp := getPluginsResponse(t, srv.URL, "1"); resp.CanWrite {
		t.Error("canWrite = true without onboarding roots, want false")
	}

	// Project path under an allowed root → canWrite=true.
	AttachOnboard(OnboardConfig{Roots: []string{filepath.Dir(path)}, WorkspaceRoot: t.TempDir()})
	if resp := getPluginsResponse(t, srv.URL, "1"); !resp.CanWrite {
		t.Error("canWrite = false with the project under an onboarding root, want true")
	}
}

func TestProjectPluginsStaleCloneKeepsEnabledPack(t *testing.T) {
	srv, _ := projectsTestServer(t)
	// Stale clone: manifest only knows core.
	seedPluginCatalog(t, `{
		"name": "swarmery",
		"metadata": {"version": "1.13.0"},
		"plugins": [{"name": "core", "source": "./plugins/core", "description": "the core plugin"}]
	}`)
	writeProjectSettings(t, projectPath(t, srv.URL, "1"), `{
		"enabledPlugins": {"core@swarmery": true, "web-pack@swarmery": true}
	}`)

	resp := getPluginsResponse(t, srv.URL, "1")
	if len(resp.Plugins) != 2 {
		t.Fatalf("plugins len = %d, want 2 (%+v)", len(resp.Plugins), resp.Plugins)
	}
	last := resp.Plugins[1]
	if last.Name != "web-pack" || !last.Enabled || last.Locked {
		t.Errorf("appended row = %+v, want web-pack enabled unlocked", last)
	}
	if last.Status != "missing" {
		t.Errorf("status = %q, want missing", last.Status)
	}
	if !strings.Contains(last.Detail, "missing from the local marketplace clone") {
		t.Errorf("detail = %q, want a stale-clone note", last.Detail)
	}
}

func TestProjectPluginsNoMarketplace(t *testing.T) {
	srv, _ := projectsTestServer(t)
	// A claudeDir with no marketplaces/ clone at all.
	AttachPluginCatalog(t.TempDir())
	t.Cleanup(func() { AttachPluginCatalog("") })

	out := doJSON(t, "GET", srv.URL+"/api/projects/1/plugins", nil, 404)
	msg, _ := out["error"].(string)
	if !strings.Contains(msg, "marketplace is not installed") {
		t.Errorf("error = %q, want the marketplace-not-installed message", msg)
	}
}

func TestProjectPluginsBadID(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)

	out := doJSON(t, "GET", srv.URL+"/api/projects/bad/plugins", nil, 400)
	if msg, _ := out["error"].(string); msg != "invalid project id" {
		t.Errorf("error = %q, want \"invalid project id\"", msg)
	}
}

func TestProjectPluginsUnknownProject(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)

	out := doJSON(t, "GET", srv.URL+"/api/projects/9999/plugins", nil, 404)
	if msg, _ := out["error"].(string); msg != "project not found" {
		t.Errorf("error = %q, want \"project not found\"", msg)
	}
}

// ── PUT /api/projects/{id}/plugins/{name} ────────────────────────────────────

func TestPutPluginNoFence(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	// Drop the onboarding roots the harness wired: the fence must reject.
	AttachOnboard(OnboardConfig{})
	t.Cleanup(func() { AttachOnboard(OnboardConfig{}) })

	out := doJSON(t, "PUT", srv.URL+"/api/projects/1/plugins/lsp-pack",
		map[string]any{"enabled": true}, 403)
	msg, _ := out["error"].(string)
	if !strings.Contains(msg, "SWARMERY_ONBOARD_ROOTS") {
		t.Errorf("error = %q, want the SWARMERY_ONBOARD_ROOTS fence message", msg)
	}
}

func TestPutPluginOutsideRoots(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)

	// Project 2's path (/tmp/telemetry-only-nonexistent) is NOT under the
	// onboarding root → the symlink-safe fence rejects it before any write
	// (mirrors TestDetachProjectOutsideRoots).
	doJSON(t, "PUT", srv.URL+"/api/projects/2/plugins/lsp-pack",
		map[string]any{"enabled": true}, 403)
}

func TestPutPluginNoSettings(t *testing.T) {
	srv, db := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)

	// A real directory under the onboarding root, but with no .claude/settings.json:
	// the fence admits it, then TogglePlugin's ErrNoSettings maps to 409.
	root := filepath.Dir(projectPath(t, srv.URL, "1"))
	barePath := filepath.Join(root, "bare")
	if err := os.MkdirAll(barePath, 0o755); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, `INSERT INTO projects (id, path, slug, name, first_seen, last_activity, archived)
		VALUES (4, ?, 'bare', 'Bare', '2026-07-10T00:00:00Z', '2026-07-14T00:00:00Z', 0)`, barePath)

	out := doJSON(t, "PUT", srv.URL+"/api/projects/4/plugins/lsp-pack",
		map[string]any{"enabled": true}, 409)
	msg, _ := out["error"].(string)
	if !strings.Contains(msg, "attach the project first") {
		t.Errorf("error = %q, want the attach-the-project-first message", msg)
	}
}

func TestPutPluginCoreLocked(t *testing.T) {
	srv, _ := projectsTestServer(t) // roots are wired by the harness
	seedPluginCatalog(t, threePackManifest)

	out := doJSON(t, "PUT", srv.URL+"/api/projects/1/plugins/core",
		map[string]any{"enabled": false}, 400)
	if msg, _ := out["error"].(string); msg != "core is managed via attach/detach" {
		t.Errorf("error = %q, want \"core is managed via attach/detach\"", msg)
	}
}

func TestPutPluginUnknownName(t *testing.T) {
	srv, _ := projectsTestServer(t)
	// Catalog knows core only: enabling anything else is a 404.
	seedPluginCatalog(t, `{
		"name": "swarmery",
		"metadata": {"version": "1.13.0"},
		"plugins": [{"name": "core", "source": "./plugins/core", "description": "the core plugin"}]
	}`)

	out := doJSON(t, "PUT", srv.URL+"/api/projects/1/plugins/nope",
		map[string]any{"enabled": true}, 404)
	if msg, _ := out["error"].(string); msg != "unknown plugin: nope" {
		t.Errorf("error = %q, want \"unknown plugin: nope\"", msg)
	}
}

func TestPutPluginEnableDisable(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	path := projectPath(t, srv.URL, "1")
	settings := filepath.Join(path, ".claude", "settings.json")

	// Enable: a real write with a backup.
	out := doJSON(t, "PUT", srv.URL+"/api/projects/1/plugins/lsp-pack",
		map[string]any{"enabled": true}, 200)
	if out["name"] != "lsp-pack" || out["enabled"] != true || out["changed"] != true {
		t.Errorf("enable body = %v, want name=lsp-pack enabled=true changed=true", out)
	}
	if out["backup"] != ".claude/settings.json.bak" {
		t.Errorf("backup = %v, want .claude/settings.json.bak", out["backup"])
	}
	if body := readDisk(t, settings); !strings.Contains(body, `"lsp-pack@swarmery": true`) {
		t.Errorf("settings.json missing lsp-pack@swarmery after enable:\n%s", body)
	}
	if _, err := os.Stat(settings + ".bak"); err != nil {
		t.Errorf("backup not written: %v", err)
	}

	// The GET view reflects the new state.
	resp := getPluginsResponse(t, srv.URL, "1")
	found := false
	for _, p := range resp.Plugins {
		if p.Name == "lsp-pack" {
			found = true
			if !p.Enabled {
				t.Error("GET shows lsp-pack disabled after a successful enable")
			}
		}
	}
	if !found {
		t.Fatalf("lsp-pack missing from GET response: %+v", resp.Plugins)
	}

	// Idempotent second enable: no write, no backup field.
	out = doJSON(t, "PUT", srv.URL+"/api/projects/1/plugins/lsp-pack",
		map[string]any{"enabled": true}, 200)
	if out["changed"] != false {
		t.Errorf("second enable changed = %v, want false", out["changed"])
	}
	if bak, ok := out["backup"]; ok && bak != "" {
		t.Errorf("no-op enable must omit backup, got %v", bak)
	}

	// Disable: the key is deleted, not set to false.
	out = doJSON(t, "PUT", srv.URL+"/api/projects/1/plugins/lsp-pack",
		map[string]any{"enabled": false}, 200)
	if out["enabled"] != false || out["changed"] != true {
		t.Errorf("disable body = %v, want enabled=false changed=true", out)
	}
	if body := readDisk(t, settings); strings.Contains(body, "lsp-pack@swarmery") {
		t.Errorf("settings.json still mentions lsp-pack@swarmery after disable:\n%s", body)
	}

	// Foreign-key survival: the enable+disable round-trip must not clobber the
	// keys the harness seeded — other enabledPlugins entries and top-level
	// settings outside enabledPlugins.
	body := readDisk(t, settings)
	for _, key := range []string{"extraKnownMarketplaces", "core@swarmery", "iot-pack@swarmery"} {
		if !strings.Contains(body, key) {
			t.Errorf("settings.json lost foreign key %q after enable+disable round-trip:\n%s", key, body)
		}
	}
}

func TestPutPluginMalformedSettings(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	path := projectPath(t, srv.URL, "1")
	settings := filepath.Join(path, ".claude", "settings.json")
	if err := os.WriteFile(settings, []byte(`{oops`), 0o644); err != nil {
		t.Fatal(err)
	}

	out := doJSON(t, "PUT", srv.URL+"/api/projects/1/plugins/lsp-pack",
		map[string]any{"enabled": true}, 409)
	msg, _ := out["error"].(string)
	if !strings.Contains(msg, "malformed") {
		t.Errorf("error = %q, want a malformed-settings message", msg)
	}
	if body := readDisk(t, settings); body != `{oops` {
		t.Errorf("malformed settings.json must never be overwritten, got %q", body)
	}
}

// ---------------------------------------------------------------------------
// Directory-source marketplaces. Every test above seeds the legacy clone layout
// with no known_marketplaces.json — a github-marketplace machine — so those
// staying green is the proof this endpoint did not change for them.
// ---------------------------------------------------------------------------

// registryEntry mirrors one value of <claudeDir>/plugins/known_marketplaces.json.
// Marshalled rather than hand-written so temp-dir paths are escaped by
// encoding/json instead of by string concatenation.
type registryEntry struct {
	Source struct {
		Source string `json:"source"`
		Path   string `json:"path,omitempty"`
		Repo   string `json:"repo,omitempty"`
	} `json:"source"`
	InstallLocation string `json:"installLocation"`
}

func directoryEntry(root string) registryEntry {
	var e registryEntry
	e.Source.Source = "directory"
	e.Source.Path = root
	e.InstallLocation = root
	return e
}

func githubEntry(repo, installLocation string) registryEntry {
	var e registryEntry
	e.Source.Source = "github"
	e.Source.Repo = repo
	e.InstallLocation = installLocation
	return e
}

func writeKnownMarketplaces(t *testing.T, claudeDir string, entries map[string]registryEntry) {
	t.Helper()
	dir := filepath.Join(claudeDir, "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "known_marketplaces.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedPluginCatalogDirectorySource reproduces the machine that produced the
// reported 404: the marketplace was added as a DIRECTORY source, so the manifest
// lives in the operator's checkout and <claudeDir>/plugins/marketplaces/ is never
// created at all. A second, unrelated registry entry proves lookup is by key.
func seedPluginCatalogDirectorySource(t *testing.T, body string) {
	t.Helper()
	claudeDir := t.TempDir()
	mktRoot := t.TempDir()
	dir := filepath.Join(mktRoot, ".claude-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "marketplace.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	writeKnownMarketplaces(t, claudeDir, map[string]registryEntry{
		"swarmery": directoryEntry(mktRoot),
		"nabu-org": githubEntry("nabu-org/packs", filepath.Join(claudeDir, "plugins", "marketplaces", "nabu-org")),
	})
	// Deliberately nothing under <claudeDir>/plugins/marketplaces/.
	if _, err := os.Stat(filepath.Join(claudeDir, "plugins", "marketplaces")); !os.IsNotExist(err) {
		t.Fatalf("stat plugins/marketplaces = %v, want it absent (that absence is the bug's precondition)", err)
	}
	AttachPluginCatalog(claudeDir)
	t.Cleanup(func() { AttachPluginCatalog("") })
}

// The regression test for the reported 404. Assertions are deliberately
// identical to TestProjectPluginsMergesCatalogAndState: a directory-source
// machine must produce the very same response a github one does.
func TestProjectPluginsResolvesDirectorySourceMarketplace(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalogDirectorySource(t, threePackManifest)
	writeProjectSettings(t, projectPath(t, srv.URL, "1"), `{
		"enabledPlugins": {"core@swarmery": true, "lsp-pack@swarmery": true}
	}`)

	resp := getPluginsResponse(t, srv.URL, "1")
	if resp.MarketplaceVersion != "1.13.0" {
		t.Errorf("marketplaceVersion = %q, want 1.13.0", resp.MarketplaceVersion)
	}
	want := []projectPluginDTO{
		{Name: "core", Description: "the core plugin", Enabled: true, Locked: true, Status: "ok"},
		{Name: "uav-pack", Description: "UAV domain pack", Enabled: false, Locked: false, Status: "unknown"},
		{Name: "lsp-pack", Description: "LSP pack", Enabled: true, Locked: false, Status: "ok"},
	}
	if len(resp.Plugins) != len(want) {
		t.Fatalf("plugins len = %d, want %d (%+v)", len(resp.Plugins), len(want), resp.Plugins)
	}
	for i, w := range want {
		if resp.Plugins[i] != w {
			t.Errorf("plugins[%d] = %+v, want %+v (manifest order)", i, resp.Plugins[i], w)
		}
	}
}

// A registry that does not mention our marketplace is not evidence against the
// legacy clone — it must not be able to break the github path.
func TestProjectPluginsRegistryWithoutEntryFallsBackToClone(t *testing.T) {
	srv, _ := projectsTestServer(t)
	claudeDir := seedPluginCatalog(t, threePackManifest)
	writeKnownMarketplaces(t, claudeDir, map[string]registryEntry{
		"nabu-org": githubEntry("nabu-org/packs", filepath.Join(claudeDir, "plugins", "marketplaces", "nabu-org")),
	})
	// Same settings normalisation as TestProjectPluginsMergesCatalogAndState, so
	// the seeded fixture cannot append rows for packs outside this manifest.
	writeProjectSettings(t, projectPath(t, srv.URL, "1"), `{
		"enabledPlugins": {"core@swarmery": true, "lsp-pack@swarmery": true}
	}`)

	resp := getPluginsResponse(t, srv.URL, "1")
	if resp.MarketplaceVersion != "1.13.0" {
		t.Errorf("marketplaceVersion = %q, want 1.13.0", resp.MarketplaceVersion)
	}
	want := []string{"core", "uav-pack", "lsp-pack"}
	if len(resp.Plugins) != len(want) {
		t.Fatalf("plugins len = %d, want %d (%+v)", len(resp.Plugins), len(want), resp.Plugins)
	}
	for i, name := range want {
		if resp.Plugins[i].Name != name {
			t.Errorf("plugins[%d].Name = %q, want %q (the legacy clone's manifest order)", i, resp.Plugins[i].Name, name)
		}
	}
}
