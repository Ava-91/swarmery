package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// seedFinding inserts one config_lint_findings row. resolvedAt "" leaves it
// active.
func seedFinding(t *testing.T, db *sql.DB, target, rule, severity, message, resolvedAt string) {
	t.Helper()
	var resolved any
	if resolvedAt != "" {
		resolved = resolvedAt
	}
	execSQL(t, db,
		`INSERT INTO config_lint_findings (target, rule, severity, message, detected_at, resolved_at)
		 VALUES (?, ?, ?, ?, '2026-07-28T00:00:00Z', ?)`,
		target, rule, severity, message, resolved)
}

// pluginTarget mirrors plugindrift.Target — spelled out here so a change to the
// wire format fails these tests loudly instead of silently agreeing with itself.
func pluginTarget(id, projectPath string) string { return "plugin:" + id + "|" + projectPath }

// ── GET /api/projects/{id}/plugins — drift status ────────────────────────────

func TestProjectPluginsStatusMissing(t *testing.T) {
	srv, db := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	path := projectPath(t, srv.URL, "1")
	writeProjectSettings(t, path, `{"enabledPlugins": {"core@swarmery": true}}`)
	seedFinding(t, db, pluginTarget("core@swarmery", path), "plugin_enabled_not_installed",
		"error", "enabled here, but installed only for /Volumes/Work/Skygor/scripts", "")

	row := pluginRow(t, srv.URL, "1", "core")
	if row.Status != "missing" {
		t.Errorf("status = %q, want missing", row.Status)
	}
	if !strings.Contains(row.Detail, "/Volumes/Work/Skygor/scripts") {
		t.Errorf("detail = %q, want the foreign project path", row.Detail)
	}
}

// A finding for another project must never leak into this one — the whole point
// of encoding the project path into the target.
func TestProjectPluginsStatusIgnoresOtherProjects(t *testing.T) {
	srv, db := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	path := projectPath(t, srv.URL, "1")
	writeProjectSettings(t, path, `{"enabledPlugins": {"core@swarmery": true}}`)
	seedFinding(t, db, pluginTarget("core@swarmery", "/some/other/project"),
		"plugin_enabled_not_installed", "error", "not mine", "")

	if row := pluginRow(t, srv.URL, "1", "core"); row.Status != "ok" {
		t.Errorf("status = %q (detail %q), want ok — another project's finding leaked", row.Status, row.Detail)
	}
}

func TestProjectPluginsStatusIgnoresResolvedFindings(t *testing.T) {
	srv, db := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	path := projectPath(t, srv.URL, "1")
	writeProjectSettings(t, path, `{"enabledPlugins": {"core@swarmery": true}}`)
	seedFinding(t, db, pluginTarget("core@swarmery", path), "plugin_enabled_not_installed",
		"error", "fixed already", "2026-07-28T10:00:00Z")

	if row := pluginRow(t, srv.URL, "1", "core"); row.Status != "ok" {
		t.Errorf("status = %q, want ok — a resolved finding must not count", row.Status)
	}
}

func TestProjectPluginsStatusUnknownWhenDisabled(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	writeProjectSettings(t, projectPath(t, srv.URL, "1"), `{"enabledPlugins": {"core@swarmery": true}}`)

	row := pluginRow(t, srv.URL, "1", "uav-pack")
	if row.Status != "unknown" {
		t.Errorf("status = %q, want unknown for a disabled plugin", row.Status)
	}
	if row.Detail != "" {
		t.Errorf("detail = %q, want empty", row.Detail)
	}
}

// missing outranks behind: the reader must see the fatal problem, not the
// cosmetic one, when a plugin has both.
func TestProjectPluginsStatusMissingOutranksBehind(t *testing.T) {
	srv, db := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	path := projectPath(t, srv.URL, "1")
	writeProjectSettings(t, path, `{"enabledPlugins": {"core@swarmery": true}}`)
	target := pluginTarget("core@swarmery", path)
	seedFinding(t, db, target, "plugin_version_behind", "warn", "installed 1.2.0, marketplace has 2.7.0", "")
	seedFinding(t, db, target, "plugin_enabled_not_installed", "error", "not installed here", "")

	if row := pluginRow(t, srv.URL, "1", "core"); row.Status != "missing" {
		t.Errorf("status = %q, want missing to outrank behind", row.Status)
	}
}

func TestProjectPluginsStatusNoteDoesNotDowngradeOK(t *testing.T) {
	srv, db := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	path := projectPath(t, srv.URL, "1")
	writeProjectSettings(t, path, `{"enabledPlugins": {"core@swarmery": true}}`)
	seedFinding(t, db, pluginTarget("core@swarmery", path), "plugin_note", "info", "some CLI note", "")

	if row := pluginRow(t, srv.URL, "1", "core"); row.Status != "ok" {
		t.Errorf("status = %q, want ok — plugin_note is not a row status", row.Status)
	}
}

func pluginRow(t *testing.T, srvURL, projectID, name string) projectPluginDTO {
	t.Helper()
	for _, p := range getPluginsResponse(t, srvURL, projectID).Plugins {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no %q row in the plugins response", name)
	return projectPluginDTO{}
}

// ── GET /api/health — pluginDrift ────────────────────────────────────────────

func TestHealthPluginDriftZeroWhenClean(t *testing.T) {
	srv, _ := projectsTestServer(t)
	var resp healthDTO
	getJSON(t, srv.URL+"/api/health", &resp)
	if resp.PluginDrift.Error != 0 || resp.PluginDrift.Warn != 0 {
		t.Errorf("pluginDrift = %+v, want zeroes", resp.PluginDrift)
	}
}

func TestHealthPluginDriftCounts(t *testing.T) {
	srv, db := projectsTestServer(t)
	seedFinding(t, db, "plugin:a@m|/p1", "plugin_enabled_not_installed", "error", "m", "")
	seedFinding(t, db, "plugin:b@m|/p1", "plugin_enabled_not_installed", "error", "m", "")
	seedFinding(t, db, "plugin:c@m|/p1", "plugin_version_behind", "warn", "m", "")
	seedFinding(t, db, "plugin:d@m|/p1", "plugin_note", "info", "m", "")                               // counted in neither
	seedFinding(t, db, "plugin:e@m|/p1", "plugin_cache_orphaned", "warn", "m", "2026-07-28T10:00:00Z") // resolved
	seedFinding(t, db, "agent:12", "agent_dead", "error", "not a plugin rule", "")                     // excluded

	var resp healthDTO
	getJSON(t, srv.URL+"/api/health", &resp)
	if resp.PluginDrift.Error != 2 {
		t.Errorf("pluginDrift.error = %d, want 2", resp.PluginDrift.Error)
	}
	if resp.PluginDrift.Warn != 1 {
		t.Errorf("pluginDrift.warn = %d, want 1", resp.PluginDrift.Warn)
	}
}

// ── POST /api/projects/{id}/plugins/{name}/repair ────────────────────────────

// repairSpy records every CLI invocation so the fence tests can assert that no
// process was spawned at all.
//
// `plugin list --json` is answered from `installed` and deliberately NOT
// recorded: it is the read the handler performs to find out where the plugin
// lives, whereas calls/dirs assert what the repair actually DID. Counting the
// probe would make every existing "ran exactly once" assertion say "twice"
// without telling the reader anything.
type repairSpy struct {
	calls [][]string
	dirs  []string
	out   []byte
	err   error
	// installed is the `plugin list --json` body; "" ⇒ an empty list. Setting
	// listErr instead simulates a CLI that cannot answer the question at all.
	installed string
	listErr   error
	// onRun simulates a side effect of the real CLI (a user-scope install
	// writing enabledPlugins into the user settings.json).
	onRun func()
}

func (s *repairSpy) Run(_ context.Context, dir string, args ...string) ([]byte, error) {
	if len(args) >= 2 && args[0] == "plugin" && args[1] == "list" {
		if s.listErr != nil {
			return nil, s.listErr
		}
		if s.installed == "" {
			return []byte("[]"), nil
		}
		return []byte(s.installed), nil
	}
	s.calls = append(s.calls, args)
	s.dirs = append(s.dirs, dir)
	if s.onRun != nil {
		s.onRun()
	}
	return s.out, s.err
}

// installedAt builds a one-entry `plugin list --json` body.
func installedAt(id, scope, version, projectPath string) string {
	e := map[string]any{"id": id, "scope": scope, "version": version, "enabled": true}
	if projectPath != "" {
		e["projectPath"] = projectPath
	}
	raw, err := json.Marshal([]any{e})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func attachRepairer(t *testing.T, s *repairSpy) {
	t.Helper()
	AttachPluginRepairer(s)
	t.Cleanup(func() { pluginRepairer = nil })
}

func TestRepairPluginInstallsWhenMissing(t *testing.T) {
	srv, db := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	path := projectPath(t, srv.URL, "1")
	seedFinding(t, db, pluginTarget("core@swarmery", path), "plugin_enabled_not_installed", "error", "gone", "")
	spy := &repairSpy{out: []byte("installed core@swarmery")}
	attachRepairer(t, spy)

	out := doJSON(t, "POST", srv.URL+"/api/projects/1/plugins/core@swarmery/repair", nil, 200)
	if out["action"] != "install" {
		t.Errorf("action = %v, want install", out["action"])
	}
	if out["restart"] != true {
		t.Errorf("restart = %v, want true", out["restart"])
	}
	if len(spy.calls) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(spy.calls))
	}
	want := []string{"plugin", "install", "core@swarmery", "--scope", "project"}
	if strings.Join(spy.calls[0], " ") != strings.Join(want, " ") {
		t.Errorf("args = %v, want %v", spy.calls[0], want)
	}
	if spy.dirs[0] != path {
		t.Errorf("cmd dir = %q, want the project path %q", spy.dirs[0], path)
	}
}

func TestRepairPluginUpdatesWhenBehind(t *testing.T) {
	srv, db := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	path := projectPath(t, srv.URL, "1")
	seedFinding(t, db, pluginTarget("core@swarmery", path), "plugin_version_behind", "warn", "old", "")
	spy := &repairSpy{
		out:       []byte("updated"),
		installed: installedAt("core@swarmery", "project", "1.0.0", path),
	}
	attachRepairer(t, spy)

	out := doJSON(t, "POST", srv.URL+"/api/projects/1/plugins/core@swarmery/repair", nil, 200)
	if out["action"] != "update" {
		t.Fatalf("action = %v, want update", out["action"])
	}
	want := []string{"plugin", "update", "core@swarmery", "--scope", "project"}
	if strings.Join(spy.calls[0], " ") != strings.Join(want, " ") {
		t.Errorf("args = %v, want %v", spy.calls[0], want)
	}
}

// The reported failure: the pack is behind at USER scope, and repair asked for
// `plugin update --scope project` anyway — which the CLI rejects outright with
// "Plugin X is not installed at scope project". The behind copy is the one that
// must be updated, wherever it lives.
func TestRepairPluginUpdatesBehindUserScopeInstallAtUserScope(t *testing.T) {
	srv, db := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	path := projectPath(t, srv.URL, "1")
	seedFinding(t, db, pluginTarget("core@swarmery", path), "plugin_version_behind", "warn",
		"installed 0.1.0, marketplace has 0.2.0 — run update to pick it up", "")
	spy := &repairSpy{
		out:       []byte("updated"),
		installed: installedAt("core@swarmery", "user", "0.1.0", ""),
	}
	attachRepairer(t, spy)

	out := doJSON(t, "POST", srv.URL+"/api/projects/1/plugins/core@swarmery/repair", nil, 200)
	if out["action"] != "update" || out["scope"] != "user" {
		t.Fatalf("action/scope = %v/%v, want update/user", out["action"], out["scope"])
	}
	if len(spy.calls) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(spy.calls))
	}
	if slices.Contains(spy.calls[0], "project") {
		t.Errorf("args = %v, want no project scope — nothing is installed there to update", spy.calls[0])
	}
	want := []string{"plugin", "update", "core@swarmery"}
	if strings.Join(spy.calls[0], " ") != strings.Join(want, " ") {
		t.Errorf("args = %v, want %v", spy.calls[0], want)
	}
}

// `local` is the CLI's project-local scope (.claude/settings.local.json). It is
// available to the project, so it can be behind — and it is not `project`.
func TestRepairPluginUpdatesBehindLocalScopeInstallAtLocalScope(t *testing.T) {
	srv, db := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	path := projectPath(t, srv.URL, "1")
	seedFinding(t, db, pluginTarget("core@swarmery", path), "plugin_version_behind", "warn", "old", "")
	spy := &repairSpy{
		out:       []byte("updated"),
		installed: installedAt("core@swarmery", "local", "0.1.0", path),
	}
	attachRepairer(t, spy)

	out := doJSON(t, "POST", srv.URL+"/api/projects/1/plugins/core@swarmery/repair", nil, 200)
	if out["scope"] != "local" {
		t.Fatalf("scope = %v, want local", out["scope"])
	}
	want := []string{"plugin", "update", "core@swarmery", "--scope", "local"}
	if strings.Join(spy.calls[0], " ") != strings.Join(want, " ") {
		t.Errorf("args = %v, want %v", spy.calls[0], want)
	}
}

// A "behind" row whose install has since been removed must not run an update
// against a copy that is not there — the finding is stale, the repair is an
// install. This is the state /Volumes/Work/english-grammar was left in.
func TestRepairPluginInstallsWhenBehindFindingIsStale(t *testing.T) {
	srv, db := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	path := projectPath(t, srv.URL, "1")
	seedFinding(t, db, pluginTarget("core@swarmery", path), "plugin_version_behind", "warn", "old", "")
	spy := &repairSpy{out: []byte("installed")} // nothing installed anywhere
	attachRepairer(t, spy)

	out := doJSON(t, "POST", srv.URL+"/api/projects/1/plugins/core@swarmery/repair", nil, 200)
	if out["action"] != "install" {
		t.Fatalf("action = %v, want install — the behind finding is stale", out["action"])
	}
	want := []string{"plugin", "install", "core@swarmery", "--scope", "project"}
	if strings.Join(spy.calls[0], " ") != strings.Join(want, " ") {
		t.Errorf("args = %v, want %v", spy.calls[0], want)
	}
}

// A CLI that cannot answer "where is it installed" must not block the repair:
// the handler falls back to the finding-derived action at project scope, which
// is what it did before the lookup existed.
func TestRepairPluginFallsBackToProjectScopeWhenListFails(t *testing.T) {
	srv, db := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	path := projectPath(t, srv.URL, "1")
	seedFinding(t, db, pluginTarget("core@swarmery", path), "plugin_version_behind", "warn", "old", "")
	spy := &repairSpy{out: []byte("updated"), listErr: errors.New("exit status 1")}
	attachRepairer(t, spy)

	out := doJSON(t, "POST", srv.URL+"/api/projects/1/plugins/core@swarmery/repair", nil, 200)
	if out["scope"] != "project" {
		t.Fatalf("scope = %v, want project", out["scope"])
	}
	want := []string{"plugin", "update", "core@swarmery", "--scope", "project"}
	if strings.Join(spy.calls[0], " ") != strings.Join(want, " ") {
		t.Errorf("args = %v, want %v", spy.calls[0], want)
	}
}

func TestRepairPluginRunnerErrorIs502(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	spy := &repairSpy{out: []byte("partial output"), err: errors.New("exit status 1")}
	attachRepairer(t, spy)

	out := doJSON(t, "POST", srv.URL+"/api/projects/1/plugins/core@swarmery/repair", nil, 502)
	body, _ := out["output"].(string)
	if !strings.Contains(body, "exit status 1") {
		t.Errorf("output = %q, want the CLI error text", body)
	}
}

func TestRepairPluginRejectsIDWithoutMarketplace(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	spy := &repairSpy{}
	attachRepairer(t, spy)

	doJSON(t, "POST", srv.URL+"/api/projects/1/plugins/core/repair", nil, 400)
	if len(spy.calls) != 0 {
		t.Errorf("runner ran %d times on a malformed id, want 0", len(spy.calls))
	}
}

// The fence must reject BEFORE spawning anything — a 403 that still shelled out
// would be a fence in name only.
func TestRepairPluginNoRootsForbiddenWithoutSpawning(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	spy := &repairSpy{}
	attachRepairer(t, spy)
	AttachOnboard(OnboardConfig{})
	t.Cleanup(func() { AttachOnboard(OnboardConfig{}) })

	doJSON(t, "POST", srv.URL+"/api/projects/1/plugins/core@swarmery/repair", nil, 403)
	if len(spy.calls) != 0 {
		t.Fatalf("runner ran %d times behind a closed fence, want 0", len(spy.calls))
	}
}

func TestRepairPluginOutsideRootsForbiddenWithoutSpawning(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	spy := &repairSpy{}
	attachRepairer(t, spy)

	// Project 2's path is not under the harness's onboarding root.
	doJSON(t, "POST", srv.URL+"/api/projects/2/plugins/core@swarmery/repair", nil, 403)
	if len(spy.calls) != 0 {
		t.Fatalf("runner ran %d times for an out-of-root project, want 0", len(spy.calls))
	}
}

func TestRepairPluginUnavailableWithoutRepairer(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	pluginRepairer = nil

	out := doJSON(t, "POST", srv.URL+"/api/projects/1/plugins/core@swarmery/repair", nil, 503)
	if msg, _ := out["error"].(string); !strings.Contains(msg, "claude CLI") {
		t.Errorf("error = %q, want the unresolved-CLI message", msg)
	}
}

// ── the symlinked-overlay fallback ───────────────────────────────────────────
//
// The multi-repo consumer overlay pattern (CLAUDE.md / EXTENDING.md): .claude
// is itself a symlink into a shared agents/ repo. The claude CLI refuses to
// write project-scope settings through such a directory (SymlinkWriteRefusedError,
// observed against a real consumer with `.claude -> agents`), so repair falls
// back to a user-scope install — which resolves the drift, because
// plugindrift.resolveFor counts a Scope=="user" entry as available to every
// project — and then reverts the global enable the CLI sets as a side effect.

// seedOverlayProject registers project 4 with a symlinked .claude and a seeded
// enabled-but-not-installed finding, and points the user-settings anchor at a
// temp dir. Returns the project dir and the user settings.json path.
func seedOverlayProject(t *testing.T, srvURL string, db *sql.DB, globalSettings string) (string, string) {
	t.Helper()
	root := filepath.Dir(projectPath(t, srvURL, "1")) // project 1's onboarding root
	projDir := filepath.Join(root, "overlay-consumer")
	agentsDir := filepath.Join(projDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "settings.json"),
		[]byte(`{"enabledPlugins": {"core@swarmery": true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("agents", filepath.Join(projDir, ".claude")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	execSQL(t, db, `INSERT INTO projects (id, path, slug, name, first_seen, archived)
		VALUES (4, ?, 'overlay', 'Overlay', '2026-07-29T00:00:00Z', 0)`, projDir)
	seedFinding(t, db, pluginTarget("core@swarmery", projDir), "plugin_enabled_not_installed", "error", "gone", "")

	claudeDir := t.TempDir()
	userSettings := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(userSettings, []byte(globalSettings), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := pluginsClaudeDir
	pluginsClaudeDir = claudeDir
	t.Cleanup(func() { pluginsClaudeDir = prev })
	return projDir, userSettings
}

func globalEnabled(t *testing.T, path, key string) (any, bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	ep, _ := d["enabledPlugins"].(map[string]any)
	v, ok := ep[key]
	return v, ok
}

func TestRepairPluginFallsBackToUserScopeOnSymlinkedClaudeDir(t *testing.T) {
	srv, db := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	_, userSettings := seedOverlayProject(t, srv.URL, db, `{"enabledPlugins":{"lsp-pack@swarmery":true}}`)

	spy := &repairSpy{out: []byte("installed core@swarmery")}
	// the real CLI's side effect at user scope
	spy.onRun = func() {
		if err := os.WriteFile(userSettings,
			[]byte(`{"enabledPlugins":{"lsp-pack@swarmery":true,"core@swarmery":true}}`), 0o644); err != nil {
			t.Error(err)
		}
	}
	attachRepairer(t, spy)

	out := doJSON(t, "POST", srv.URL+"/api/projects/4/plugins/core@swarmery/repair", nil, 200)
	if out["scope"] != "user" {
		t.Errorf("scope = %v, want user", out["scope"])
	}
	if len(spy.calls) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(spy.calls))
	}
	// No --scope project: that is the whole point — it cannot succeed here.
	want := []string{"plugin", "install", "core@swarmery"}
	if strings.Join(spy.calls[0], " ") != strings.Join(want, " ") {
		t.Errorf("args = %v, want %v", spy.calls[0], want)
	}
	if spy.dirs[0] != "" {
		t.Errorf("cmd dir = %q, want empty — a user-scope install must not run in the symlinked project", spy.dirs[0])
	}
	// The global enable the install caused must be gone again.
	if _, present := globalEnabled(t, userSettings, "core@swarmery"); present {
		t.Error("core@swarmery is still globally enabled — repairing one consumer turned the pack on for every project")
	}
	if v, present := globalEnabled(t, userSettings, "lsp-pack@swarmery"); !present || v != true {
		t.Error("a foreign global enable was clobbered by the revert")
	}
	if w, _ := out["warning"].(string); w != "" {
		t.Errorf("warning = %q, want none on a clean run", w)
	}
}

// The revert must run even when the CLI fails — a partial install can still
// have written the key.
func TestRepairPluginUserScopeRevertsAfterCLIFailure(t *testing.T) {
	srv, db := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	_, userSettings := seedOverlayProject(t, srv.URL, db, `{"enabledPlugins":{}}`)

	spy := &repairSpy{out: []byte("partial"), err: errors.New("exit status 1")}
	spy.onRun = func() {
		if err := os.WriteFile(userSettings,
			[]byte(`{"enabledPlugins":{"core@swarmery":true}}`), 0o644); err != nil {
			t.Error(err)
		}
	}
	attachRepairer(t, spy)

	doJSON(t, "POST", srv.URL+"/api/projects/4/plugins/core@swarmery/repair", nil, 502)
	if _, present := globalEnabled(t, userSettings, "core@swarmery"); present {
		t.Error("a failed repair left the pack globally enabled")
	}
}

// An untrustworthy snapshot must stop the install BEFORE it runs: a global
// enable swarmery cannot revert is worse than an unrepaired plugin.
func TestRepairPluginRefusesUserScopeWhenGlobalSettingsMalformed(t *testing.T) {
	srv, db := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	seedOverlayProject(t, srv.URL, db, `{"enabledPlugins": [broken`)

	spy := &repairSpy{out: []byte("installed")}
	attachRepairer(t, spy)

	out := doJSON(t, "POST", srv.URL+"/api/projects/4/plugins/core@swarmery/repair", nil, 409)
	if len(spy.calls) != 0 {
		t.Fatalf("runner ran %d times without a trustworthy snapshot, want 0", len(spy.calls))
	}
	if body, _ := out["output"].(string); !strings.Contains(body, "refused") {
		t.Errorf("output = %q, want it to say the fallback was refused", body)
	}
}

// A repair must force a drift pass BEFORE reading the status back, and do it
// synchronously — otherwise the response (and the list the UI refetches) still
// carries the pre-repair status, and a working repair renders as a dead button.
func TestRepairPluginRefreshesDriftBeforeReportingStatus(t *testing.T) {
	srv, db := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	path := projectPath(t, srv.URL, "1")
	seedFinding(t, db, pluginTarget("core@swarmery", path), "plugin_enabled_not_installed", "error", "gone", "")
	attachRepairer(t, &repairSpy{out: []byte("ok")})

	refreshed := false
	AttachDriftRefresher(func() {
		refreshed = true
		// what a real pass would do once the plugin is installed
		execSQL(t, db, `UPDATE config_lint_findings SET resolved_at = '2026-07-29T00:00:00Z'
			WHERE rule = 'plugin_enabled_not_installed'`)
	})
	t.Cleanup(func() { driftRefresher = nil })

	out := doJSON(t, "POST", srv.URL+"/api/projects/1/plugins/core@swarmery/repair", nil, 200)
	if !refreshed {
		t.Fatal("repair did not force a drift pass")
	}
	if out["status"] != "ok" {
		t.Errorf("status = %v, want ok — the refresh must land BEFORE the status is read", out["status"])
	}
}

// The ordinary (non-symlinked) path must keep reporting project scope.
func TestRepairPluginReportsProjectScopeNormally(t *testing.T) {
	srv, db := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	path := projectPath(t, srv.URL, "1")
	seedFinding(t, db, pluginTarget("core@swarmery", path), "plugin_enabled_not_installed", "error", "gone", "")
	attachRepairer(t, &repairSpy{out: []byte("ok")})

	out := doJSON(t, "POST", srv.URL+"/api/projects/1/plugins/core@swarmery/repair", nil, 200)
	if out["scope"] != "project" {
		t.Errorf("scope = %v, want project", out["scope"])
	}
}

func TestRepairPluginUnknownProject(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)
	attachRepairer(t, &repairSpy{})

	doJSON(t, "POST", srv.URL+"/api/projects/9999/plugins/core@swarmery/repair", nil, 404)
}

// The React repair button builds "<name>@<marketplace>" from this field rather
// than hard-coding the marketplace name.
func TestProjectPluginsReportsMarketplaceName(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalog(t, threePackManifest)

	if got := getPluginsResponse(t, srv.URL, "1").MarketplaceName; got != "swarmery" {
		t.Errorf("marketplaceName = %q, want swarmery", got)
	}
}

// ── GET /api/system/insights — pluginDrift ───────────────────────────────────

func TestSystemInsightsPluginDriftResolvesProjects(t *testing.T) {
	srv, db := projectsTestServer(t)
	p1 := projectPath(t, srv.URL, "1")
	execSQL(t, db, `INSERT INTO projects (id, path, slug, name, first_seen, archived)
		VALUES (77, '/Volumes/Work/other', 'other', 'Other', '2026-07-10T00:00:00Z', 0)`)

	seedFinding(t, db, pluginTarget("core@swarmery", p1), "plugin_enabled_not_installed",
		"error", "missing here", "")
	seedFinding(t, db, pluginTarget("web-pack@swarmery", "/Volumes/Work/other"),
		"plugin_version_behind", "warn", "behind", "")
	seedFinding(t, db, "plugin:detector", "plugin_detector_unavailable",
		"error", "claude binary not found", "")
	seedFinding(t, db, pluginTarget("uav-pack@swarmery", p1), "plugin_cache_orphaned",
		"warn", "gone", "2026-07-28T10:00:00Z") // resolved — must not appear

	var resp systemInsightsDTO
	getJSON(t, srv.URL+"/api/system/insights", &resp)

	byID := map[string]pluginDriftDTO{}
	for _, d := range resp.PluginDrift {
		byID[d.PluginID] = d
	}
	if len(byID) != 3 {
		t.Fatalf("pluginDrift has %d rows, want 3: %+v", len(resp.PluginDrift), resp.PluginDrift)
	}

	core := byID["core@swarmery"]
	if core.ProjectSlug == nil || *core.ProjectSlug == "" {
		t.Errorf("core row must resolve a project slug, got %+v", core)
	}
	if core.ProjectPath != p1 {
		t.Errorf("core projectPath = %q, want %q", core.ProjectPath, p1)
	}

	if web := byID["web-pack@swarmery"]; web.ProjectSlug == nil || *web.ProjectSlug != "other" {
		t.Errorf("web-pack must resolve to the 'other' project, got %+v", web)
	}

	// Machine-wide blindness has no project — it must render, unlinked.
	det := byID["detector"]
	if det.Rule != "plugin_detector_unavailable" {
		t.Fatalf("no plugin:detector row: %+v", resp.PluginDrift)
	}
	if det.ProjectSlug != nil {
		t.Errorf("plugin:detector must carry a null projectSlug, got %v", *det.ProjectSlug)
	}
	if det.ProjectPath != "" {
		t.Errorf("plugin:detector projectPath = %q, want empty", det.ProjectPath)
	}
}

func TestSystemInsightsPluginDriftEmptyWhenClean(t *testing.T) {
	srv, _ := projectsTestServer(t)
	var resp systemInsightsDTO
	getJSON(t, srv.URL+"/api/system/insights", &resp)
	if len(resp.PluginDrift) != 0 {
		t.Errorf("pluginDrift = %+v, want empty", resp.PluginDrift)
	}
}
