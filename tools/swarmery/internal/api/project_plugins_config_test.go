package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// ── GET /api/projects/{id}/plugins — declared project.json config ────────────
//
// The daemon never learns what any of these keys mean: the pack declares them
// and internal/pluginreq evaluates them. jira-pack is used as the fixture
// because it is the only real declaration that ships (plugins/jira-pack/
// requirements.json) — a domain word is legitimate in a test fixture, and
// nothing in the non-test daemon code names a pack.

const configPackManifest = `{
	"name": "swarmery",
	"metadata": {"version": "1.13.0"},
	"plugins": [
		{"name": "core", "source": "./plugins/core", "description": "the core plugin"},
		{"name": "jira-pack", "source": "./plugins/jira-pack", "description": "Jira pack"}
	]
}`

// jiraRequirements mirrors the shipped plugins/jira-pack/requirements.json.
const jiraRequirements = `{
  "version": 1,
  "projectConfig": [
    {
      "key": "jira",
      "title": "Jira tracker",
      "why": "/jira-fix runs with autonomy: auto.",
      "docs": "skills/jira-config/SKILL.md",
      "schema": {
        "type": "object",
        "properties": {
          "baseUrl":    { "type": "string" },
          "projectKey": { "type": "string" },
          "qaStatus":   { "type": "string" },
          "repro": {
            "type": "object",
            "properties": {"setup": {"type": "string"}, "test": {"type": "string"}},
            "required": ["test"]
          },
          "budget": {
            "type": "object",
            "properties": {"maxFiles": {"type": "integer"}}
          }
        },
        "required": ["baseUrl", "projectKey", "qaStatus", "repro"]
      }
    }
  ]
}`

// writePackRequirements drops a requirements.json into a pack's directory
// inside the seeded marketplace clone.
func writePackRequirements(t *testing.T, claudeDir, pack, body string) {
	t.Helper()
	dir := filepath.Join(claudeDir, "plugins", "marketplaces", "swarmery", "plugins", pack)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "requirements.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeProjectConfig writes <projectDir>/.claude/project.json.
func writeProjectConfig(t *testing.T, projectDir, body string) {
	t.Helper()
	claude := filepath.Join(projectDir, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "project.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// configTestServer seeds the two-pack catalog with jira-pack's declaration,
// enables both packs, and returns the server plus the project path.
func configTestServer(t *testing.T, requirements string) (srvURL, projectDir string) {
	t.Helper()
	srv, _ := projectsTestServer(t)
	claudeDir := seedPluginCatalog(t, configPackManifest)
	writePackRequirements(t, claudeDir, "jira-pack", requirements)
	path := projectPath(t, srv.URL, "1")
	writeProjectSettings(t, path, `{
		"enabledPlugins": {"core@swarmery": true, "jira-pack@swarmery": true}
	}`)
	return srv.URL, path
}

func pluginRowByName(t *testing.T, srvURL, projectID, name string) projectPluginDTO {
	t.Helper()
	for _, row := range getPluginsResponse(t, srvURL, projectID).Plugins {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("row %q not found", name)
	return projectPluginDTO{}
}

// The headline case: the pack is installed and on, but the project has no
// overlay at all, so every required leaf it declared is unfilled.
func TestProjectPluginsNeedsConfigWhenKeyAbsent(t *testing.T) {
	srvURL, _ := configTestServer(t, jiraRequirements)

	row := pluginRowByName(t, srvURL, "1", "jira-pack")
	if row.ConfigStatus != "needs-config" {
		t.Errorf("configStatus = %q, want needs-config", row.ConfigStatus)
	}
	want := []string{"baseUrl", "projectKey", "qaStatus", "repro.test"}
	if !reflect.DeepEqual(row.ConfigMissing, want) {
		t.Errorf("configMissing = %v, want %v", row.ConfigMissing, want)
	}
	// The modal's copy comes off the pack's own declaration, not the daemon.
	if row.ConfigKey != "jira" || row.ConfigTitle != "Jira tracker" ||
		row.ConfigDocs != "skills/jira-config/SKILL.md" || row.ConfigWhy == "" {
		t.Errorf("config metadata = %+v, want the declared key/title/docs/why", row)
	}
	// The schema travels verbatim so the client can render a form from it.
	var probe map[string]any
	if err := json.Unmarshal(row.ConfigSchema, &probe); err != nil {
		t.Fatalf("configSchema is not valid JSON: %v", err)
	}
	if _, ok := probe["properties"]; !ok {
		t.Errorf("configSchema = %s, want the declared fragment", row.ConfigSchema)
	}
	// Nothing to prefill from: the key is absent.
	if row.ConfigCurrent != nil {
		t.Errorf("configCurrent = %s, want absent", row.ConfigCurrent)
	}
	// The row that declares nothing stays clean.
	if core := pluginRowByName(t, srvURL, "1", "core"); core.ConfigStatus != "" {
		t.Errorf("core configStatus = %q, want empty (no requirements.json)", core.ConfigStatus)
	}
}

// A present but half-filled block is still needs-config, and configCurrent
// carries what is there so the form can prefill rather than blank the operator's
// existing values.
func TestProjectPluginsNeedsConfigWhenBlockIncomplete(t *testing.T) {
	srvURL, path := configTestServer(t, jiraRequirements)
	writeProjectConfig(t, path, `{"jira": {"baseUrl": "acme.example.net", "qaStatus": "", "repro": {"setup": "npm i"}}}`)

	row := pluginRowByName(t, srvURL, "1", "jira-pack")
	if row.ConfigStatus != "needs-config" {
		t.Errorf("configStatus = %q, want needs-config", row.ConfigStatus)
	}
	// baseUrl is filled; qaStatus is present-but-blank, which is unfilled;
	// repro exists but lacks the required leaf; repro.setup is optional and
	// must never appear even though it IS filled in.
	want := []string{"projectKey", "qaStatus", "repro.test"}
	if !reflect.DeepEqual(row.ConfigMissing, want) {
		t.Errorf("configMissing = %v, want %v", row.ConfigMissing, want)
	}
	var cur map[string]any
	if err := json.Unmarshal(row.ConfigCurrent, &cur); err != nil {
		t.Fatalf("configCurrent is not valid JSON: %v", err)
	}
	if cur["baseUrl"] != "acme.example.net" {
		t.Errorf("configCurrent = %s, want the operator's existing values for prefill", row.ConfigCurrent)
	}
}

// The same row, once the block is complete.
func TestProjectPluginsConfigOkWhenBlockComplete(t *testing.T) {
	srvURL, path := configTestServer(t, jiraRequirements)
	writeProjectConfig(t, path, `{"jira": {
		"baseUrl": "acme.example.net", "projectKey": "ABC", "qaStatus": "In QA",
		"repro": {"test": "make test"}
	}}`)

	row := pluginRowByName(t, srvURL, "1", "jira-pack")
	if row.ConfigStatus != "ok" {
		t.Errorf("configStatus = %q, want ok", row.ConfigStatus)
	}
	if len(row.ConfigMissing) != 0 {
		t.Errorf("configMissing = %v, want empty (budget and repro.setup are optional)", row.ConfigMissing)
	}
	// An ok row still carries the schema and current value: the operator can
	// open the modal to review or edit config that is already valid.
	if row.ConfigSchema == nil || row.ConfigCurrent == nil {
		t.Errorf("ok row lost its schema/current: %+v", row)
	}
}

// Drift outranks config. A pack that is not properly installed must be repaired
// first; asking the operator to fill in its config is noise in front of the
// real blocker.
func TestProjectPluginsDriftSuppressesConfig(t *testing.T) {
	for _, tc := range []struct {
		rule, wantStatus string
	}{
		{"plugin_enabled_not_installed", "missing"},
		{"plugin_version_behind", "behind"},
		{"plugin_cache_orphaned", "orphaned"},
	} {
		t.Run(tc.wantStatus, func(t *testing.T) {
			srv, db := projectsTestServer(t)
			claudeDir := seedPluginCatalog(t, configPackManifest)
			writePackRequirements(t, claudeDir, "jira-pack", jiraRequirements)
			path := projectPath(t, srv.URL, "1")
			writeProjectSettings(t, path, `{
				"enabledPlugins": {"core@swarmery": true, "jira-pack@swarmery": true}
			}`)
			seedFinding(t, db, pluginTarget("jira-pack@swarmery", path), tc.rule, "error", "drift", "")

			row := pluginRowByName(t, srv.URL, "1", "jira-pack")
			if row.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", row.Status, tc.wantStatus)
			}
			if row.ConfigStatus != "" || row.ConfigMissing != nil ||
				row.ConfigSchema != nil || row.ConfigCurrent != nil {
				t.Errorf("drifted row carries config fields: %+v", row)
			}
		})
	}
}

// A disabled pack has no say in this project, so the question is not asked —
// even though its declaration is right there and the project has no overlay.
func TestProjectPluginsDisabledPackHasNoConfigStatus(t *testing.T) {
	srv, _ := projectsTestServer(t)
	claudeDir := seedPluginCatalog(t, configPackManifest)
	writePackRequirements(t, claudeDir, "jira-pack", jiraRequirements)
	writeProjectSettings(t, projectPath(t, srv.URL, "1"), `{"enabledPlugins": {"core@swarmery": true}}`)

	row := pluginRowByName(t, srv.URL, "1", "jira-pack")
	if row.Enabled {
		t.Fatal("fixture enabled the pack; it must be off for this case")
	}
	if row.ConfigStatus != "" || row.ConfigSchema != nil {
		t.Errorf("disabled row carries config fields: %+v", row)
	}
}

// The normal case for most packs: no declaration, no config fields, no
// kilobyte of schema on every row of the list.
func TestProjectPluginsPackWithoutRequirementsHasNoConfigFields(t *testing.T) {
	srv, _ := projectsTestServer(t)
	seedPluginCatalog(t, configPackManifest) // no requirements.json written at all
	writeProjectSettings(t, projectPath(t, srv.URL, "1"), `{
		"enabledPlugins": {"core@swarmery": true, "jira-pack@swarmery": true}
	}`)

	for _, row := range getPluginsResponse(t, srv.URL, "1").Plugins {
		if row.ConfigStatus != "" || row.ConfigKey != "" || row.ConfigMissing != nil ||
			row.ConfigSchema != nil || row.ConfigCurrent != nil {
			t.Errorf("row %q carries config fields with no declaration: %+v", row.Name, row)
		}
	}
}

// A pack shipping a broken declaration must not be able to break the plugins
// list — the operator's only view of what is installed has to render anyway.
func TestProjectPluginsBrokenRequirementsDoesNotBreakTheList(t *testing.T) {
	for name, body := range map[string]string{
		"invalid JSON":        `{not json`,
		"unsupported version": `{"version": 99, "projectConfig": [{"key": "jira"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			srvURL, _ := configTestServer(t, body)

			resp := getPluginsResponse(t, srvURL, "1")
			if len(resp.Plugins) != 2 {
				t.Fatalf("plugins len = %d, want 2 — the whole list must still render (%+v)", len(resp.Plugins), resp.Plugins)
			}
			if resp.MarketplaceVersion != "1.13.0" {
				t.Errorf("marketplaceVersion = %q, want the catalog to be intact", resp.MarketplaceVersion)
			}
			row := pluginRowByName(t, srvURL, "1", "jira-pack")
			if row.Status != "ok" {
				t.Errorf("status = %q, want ok — a broken declaration is not drift", row.Status)
			}
			if row.ConfigStatus != "" || row.ConfigSchema != nil {
				t.Errorf("broken declaration produced config fields: %+v", row)
			}
		})
	}
}

// An unreadable/absent overlay is a correct input, not a failure: the daemon
// cannot see the config, so it reports everything as unfilled rather than
// claiming the config is satisfied.
func TestProjectPluginsMalformedProjectJSONReadsAsUnfilled(t *testing.T) {
	srvURL, path := configTestServer(t, jiraRequirements)
	writeProjectConfig(t, path, `{not json`)

	row := pluginRowByName(t, srvURL, "1", "jira-pack")
	if row.ConfigStatus != "needs-config" {
		t.Errorf("configStatus = %q, want needs-config", row.ConfigStatus)
	}
	if len(row.ConfigMissing) != 4 {
		t.Errorf("configMissing = %v, want all four required leaves", row.ConfigMissing)
	}
}

// A hand-written source must not walk the reader out of the marketplace root —
// the same guard marketplace.PluginVersion applies, proved by planting a
// readable declaration exactly where the escape would land.
func TestProjectPluginsRefusesParentTraversalInSource(t *testing.T) {
	srv, _ := projectsTestServer(t)
	claudeDir := seedPluginCatalog(t, `{
		"name": "swarmery",
		"metadata": {"version": "1.13.0"},
		"plugins": [{"name": "escaped", "source": "../../../escape", "description": "hand-edited"}]
	}`)
	escape := filepath.Join(claudeDir, "escape")
	if err := os.MkdirAll(escape, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(escape, "requirements.json"), []byte(jiraRequirements), 0o644); err != nil {
		t.Fatal(err)
	}
	writeProjectSettings(t, projectPath(t, srv.URL, "1"), `{"enabledPlugins": {"escaped@swarmery": true}}`)

	row := pluginRowByName(t, srv.URL, "1", "escaped")
	if row.ConfigStatus != "" || row.ConfigSchema != nil {
		t.Errorf("traversal was followed: %+v", row)
	}
}
