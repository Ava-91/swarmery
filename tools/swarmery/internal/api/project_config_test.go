package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── PUT /api/projects/{id}/config/{key} ──────────────────────────────────────
//
// This is the only endpoint that writes a project's .claude/project.json — a
// file the operator hand-edits and agent-work.sh reads. Every refusal below is
// therefore asserted twice: the status code, AND the file still being byte-for-
// byte what it was. A fence that answers 403 after writing is not a fence.
//
// jira-pack is the fixture because it is the only real declaration that ships.
// A domain word is legitimate in a test fixture; the rule it must not cross is
// into non-test daemon code, and nothing in project_config.go names a pack.

// closedRequirements is the shipped plugins/jira-pack/requirements.json,
// additionalProperties:false included — the construct that turns an operator's
// typo into a rejection instead of a silently stored field.
const closedRequirements = `{
  "version": 1,
  "projectConfig": [
    {
      "key": "jira",
      "title": "Jira tracker",
      "why": "/jira-fix runs with autonomy: auto.",
      "docs": "skills/jira-config/SKILL.md",
      "schema": {
        "type": "object",
        "additionalProperties": false,
        "properties": {
          "baseUrl":    { "type": "string" },
          "projectKey": { "type": "string" },
          "qaStatus":   { "type": "string" },
          "repro": {
            "type": "object",
            "additionalProperties": false,
            "properties": {
              "setup": { "type": "string" },
              "test":  { "type": "string" }
            },
            "required": ["test"]
          },
          "budget": {
            "type": "object",
            "additionalProperties": false,
            "properties": {
              "maxFiles":    { "type": "integer", "minimum": 1 },
              "maxAttempts": { "type": "integer", "minimum": 1 }
            }
          }
        },
        "required": ["baseUrl", "projectKey", "qaStatus", "repro"]
      }
    }
  ]
}`

// overlayTenKeys is a live-shaped overlay: ten top-level keys the write must not
// disturb, in the order and formatting a human left them in.
const overlayTenKeys = `{
  "$schema": "../_schema/project.schema.json",
  "name": "example",
  "displayName": "Example Project",
  "codePath": "/path/to/your/project",
  "mainApp": "web-app",
  "apps": [
    "web-app",
    "marketing-site"
  ],
  "repos": [
    "apps/web-app",
    "infrastructure"
  ],
  "cloud": {
    "provider": "gcp",
    "region": "your-region"
  },
  "stack": {
    "web": "Next.js + TypeScript",
    "db": "PostgreSQL + an ORM"
  },
  "enabledPacks": [
    "iot-pack",
    "web-pack"
  ]
}
`

// validBlock satisfies closedRequirements.
func validBlock() map[string]any {
	return map[string]any{
		"baseUrl":    "acme.example.net",
		"projectKey": "ABC",
		"qaStatus":   "In QA",
		"repro":      map[string]any{"test": "make test"},
	}
}

func configURL(srvURL, projectID, key string) string {
	return srvURL + "/api/projects/" + projectID + "/config/" + key
}

func projectJSONPath(projectDir string) string {
	return filepath.Join(projectDir, ".claude", "project.json")
}

// assertOverlayUntouched is the second half of every refusal case.
func assertOverlayUntouched(t *testing.T, projectDir, want string) {
	t.Helper()
	if got := readDisk(t, projectJSONPath(projectDir)); got != want {
		t.Errorf("project.json was modified by a refused request:\n%s\n--- want ---\n%s", got, want)
	}
	if _, err := os.Stat(projectJSONPath(projectDir) + ".bak"); err == nil {
		t.Error("a .bak was written for a request that was refused")
	}
}

// configWriteServer seeds the catalog + a live overlay and returns both.
func configWriteServer(t *testing.T) (srvURL, projectDir string) {
	t.Helper()
	srvURL, projectDir = configTestServer(t, closedRequirements)
	writeProjectConfig(t, projectDir, overlayTenKeys)
	return srvURL, projectDir
}

// ── the fence, copied from putProjectPlugin ──────────────────────────────────

func TestPutProjectConfigNoFence(t *testing.T) {
	srvURL, projectDir := configWriteServer(t)
	// Drop the onboarding roots the harness wired: the fence must reject.
	AttachOnboard(OnboardConfig{})
	t.Cleanup(func() { AttachOnboard(OnboardConfig{}) })

	out := doJSON(t, http.MethodPut, configURL(srvURL, "1", "jira"),
		map[string]any{"value": validBlock()}, http.StatusForbidden)
	if msg, _ := out["error"].(string); !strings.Contains(msg, "SWARMERY_ONBOARD_ROOTS") {
		t.Errorf("error = %q, want the SWARMERY_ONBOARD_ROOTS fence message", msg)
	}
	assertOverlayUntouched(t, projectDir, overlayTenKeys)
}

// The route's own fence, ahead of everything the handler does: a page on
// another origin must not be able to rewrite an overlay through a daemon that
// is listening on the operator's loopback.
func TestPutProjectConfigCrossOrigin(t *testing.T) {
	srvURL, projectDir := configWriteServer(t)

	req, err := http.NewRequest(http.MethodPut, configURL(srvURL, "1", "jira"),
		strings.NewReader(`{"value":{"baseUrl":"a","projectKey":"B","qaStatus":"QA","repro":{"test":"t"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin PUT = %d, want 403", resp.StatusCode)
	}
	assertOverlayUntouched(t, projectDir, overlayTenKeys)
}

func TestPutProjectConfigBadID(t *testing.T) {
	srvURL, projectDir := configWriteServer(t)
	doJSON(t, http.MethodPut, configURL(srvURL, "abc", "jira"),
		map[string]any{"value": validBlock()}, http.StatusBadRequest)
	assertOverlayUntouched(t, projectDir, overlayTenKeys)
}

func TestPutProjectConfigUnknownProject(t *testing.T) {
	srvURL, projectDir := configWriteServer(t)
	doJSON(t, http.MethodPut, configURL(srvURL, "999", "jira"),
		map[string]any{"value": validBlock()}, http.StatusNotFound)
	assertOverlayUntouched(t, projectDir, overlayTenKeys)
}

// The path fence: a project outside SWARMERY_ONBOARD_ROOTS is refused, and its
// overlay — which really is on disk, so the assertion means something — is
// untouched.
func TestPutProjectConfigOutsideRoots(t *testing.T) {
	srv, db := projectsTestServer(t)
	seedCatalogWithRequirements(t, closedRequirements)

	// A real project with a real overlay, deliberately NOT under the onboard
	// root — so "the file is untouched" is an assertion about a file that exists.
	outside := t.TempDir()
	writeProjectConfig(t, outside, overlayTenKeys)
	execSQL(t, db, `INSERT INTO projects (id, path, slug, name, first_seen, last_activity, archived)
		VALUES (7, ?, 'outside', 'Outside', '2026-07-10T00:00:00Z', '2026-07-14T00:00:00Z', 0)`, outside)

	doJSON(t, http.MethodPut, configURL(srv.URL, "7", "jira"),
		map[string]any{"value": validBlock()}, http.StatusForbidden)
	assertOverlayUntouched(t, outside, overlayTenKeys)
}

// ── this endpoint's own checks ───────────────────────────────────────────────

// Without this check the route is an arbitrary writer: any key, any project.json.
func TestPutProjectConfigUndeclaredKeyIsRefused(t *testing.T) {
	srvURL, projectDir := configWriteServer(t)

	out := doJSON(t, http.MethodPut, configURL(srvURL, "1", "commitScopes"),
		map[string]any{"value": map[string]any{"a": "b"}}, http.StatusNotFound)
	if msg, _ := out["error"].(string); msg != "unknown config key: commitScopes" {
		t.Errorf("error = %q, want \"unknown config key: commitScopes\"", msg)
	}
	assertOverlayUntouched(t, projectDir, overlayTenKeys)
}

// A pack whose declaration is malformed declares nothing, so it cannot
// authorise a write either.
func TestPutProjectConfigBrokenDeclarationAuthorisesNothing(t *testing.T) {
	srvURL, projectDir := configTestServer(t, `{"version": 99, "projectConfig": [{"key": "jira"}]}`)
	writeProjectConfig(t, projectDir, overlayTenKeys)

	doJSON(t, http.MethodPut, configURL(srvURL, "1", "jira"),
		map[string]any{"value": validBlock()}, http.StatusNotFound)
	assertOverlayUntouched(t, projectDir, overlayTenKeys)
}

func TestPutProjectConfigNoMarketplace(t *testing.T) {
	srv, _ := projectsTestServer(t)
	projectDir := projectPath(t, srv.URL, "1")
	writeProjectConfig(t, projectDir, overlayTenKeys)
	AttachPluginCatalog(t.TempDir()) // a config root with no marketplace clone
	t.Cleanup(func() { AttachPluginCatalog("") })

	out := doJSON(t, http.MethodPut, configURL(srv.URL, "1", "jira"),
		map[string]any{"value": validBlock()}, http.StatusNotFound)
	if msg, _ := out["error"].(string); !strings.Contains(msg, "marketplace is not installed") {
		t.Errorf("error = %q, want the marketplace-not-installed message", msg)
	}
	assertOverlayUntouched(t, projectDir, overlayTenKeys)
}

func TestPutProjectConfigRejectsBadBodies(t *testing.T) {
	for name, body := range map[string]any{
		"no value field":     map[string]any{},
		"value is a string":  map[string]any{"value": "acme.example.net"},
		"value is a list":    map[string]any{"value": []any{1, 2}},
		"value is null":      map[string]any{"value": nil},
		"value is a number":  map[string]any{"value": 7},
		"value is a boolean": map[string]any{"value": true},
	} {
		t.Run(name, func(t *testing.T) {
			srvURL, projectDir := configWriteServer(t)
			doJSON(t, http.MethodPut, configURL(srvURL, "1", "jira"), body, http.StatusBadRequest)
			assertOverlayUntouched(t, projectDir, overlayTenKeys)
		})
	}
}

// ── schema validation: 422, and nothing on disk ──────────────────────────────

func TestPutProjectConfigMissingRequiredFieldIsRejected(t *testing.T) {
	srvURL, projectDir := configWriteServer(t)

	out := doJSON(t, http.MethodPut, configURL(srvURL, "1", "jira"), map[string]any{
		"value": map[string]any{"baseUrl": "acme.example.net", "repro": map[string]any{}},
	}, http.StatusUnprocessableEntity)

	problems := problemStrings(t, out)
	want := []string{"projectKey is required", "qaStatus is required", "repro.test is required"}
	for _, w := range want {
		if !containsString(problems, w) {
			t.Errorf("problems = %v, want it to include %q", problems, w)
		}
	}
	assertOverlayUntouched(t, projectDir, overlayTenKeys)
}

// The typo case additionalProperties:false exists for: without it this write
// would be stored in silence and the pack would go on reading a default it was
// never told about.
func TestPutProjectConfigTypoInFieldNameIsRejected(t *testing.T) {
	srvURL, projectDir := configWriteServer(t)

	out := doJSON(t, http.MethodPut, configURL(srvURL, "1", "jira"), map[string]any{
		"value": map[string]any{
			"baseUrl": "acme.example.net", "projectKey": "ABC", "qastatus": "In QA",
			"repro": map[string]any{"test": "make test"},
		},
	}, http.StatusUnprocessableEntity)

	if !containsString(problemStrings(t, out), "unknown field: qastatus") {
		t.Errorf("problems = %v, want \"unknown field: qastatus\"", problemStrings(t, out))
	}
	assertOverlayUntouched(t, projectDir, overlayTenKeys)
}

func TestPutProjectConfigTypeAndMinimumAreEnforced(t *testing.T) {
	srvURL, projectDir := configWriteServer(t)

	value := validBlock()
	value["budget"] = map[string]any{"maxFiles": 0}
	out := doJSON(t, http.MethodPut, configURL(srvURL, "1", "jira"),
		map[string]any{"value": value}, http.StatusUnprocessableEntity)
	if !containsString(problemStrings(t, out), "budget.maxFiles must be at least 1") {
		t.Errorf("problems = %v, want the minimum violation", problemStrings(t, out))
	}

	value["budget"] = map[string]any{"maxFiles": "five"}
	out = doJSON(t, http.MethodPut, configURL(srvURL, "1", "jira"),
		map[string]any{"value": value}, http.StatusUnprocessableEntity)
	if !containsString(problemStrings(t, out), "budget.maxFiles must be an integer") {
		t.Errorf("problems = %v, want the type violation", problemStrings(t, out))
	}
	assertOverlayUntouched(t, projectDir, overlayTenKeys)
}

// ── overlay preconditions ────────────────────────────────────────────────────

// The daemon does not create an overlay on empty ground: a project.json built
// from one pack's block would be missing everything else the toolchain reads.
func TestPutProjectConfigWithoutProjectJSON(t *testing.T) {
	srvURL, projectDir := configTestServer(t, closedRequirements)

	out := doJSON(t, http.MethodPut, configURL(srvURL, "1", "jira"),
		map[string]any{"value": validBlock()}, http.StatusConflict)
	if msg, _ := out["error"].(string); !strings.Contains(msg, "attach the project first") {
		t.Errorf("error = %q, want the attach-the-project-first message", msg)
	}
	if _, err := os.Stat(projectJSONPath(projectDir)); !os.IsNotExist(err) {
		t.Error("project.json was created; the daemon must not bootstrap an overlay")
	}
}

func TestPutProjectConfigMalformedProjectJSON(t *testing.T) {
	srvURL, projectDir := configTestServer(t, closedRequirements)
	const broken = `{oops`
	writeProjectConfig(t, projectDir, broken)

	doJSON(t, http.MethodPut, configURL(srvURL, "1", "jira"),
		map[string]any{"value": validBlock()}, http.StatusConflict)
	assertOverlayUntouched(t, projectDir, broken)
}

// ── the write itself ─────────────────────────────────────────────────────────

// The headline case: one key in, every other key exactly where it was, byte for
// byte, plus a backup holding the previous contents.
func TestPutProjectConfigWritesOneKeyAndPreservesTheRest(t *testing.T) {
	srvURL, projectDir := configWriteServer(t)

	out := doJSON(t, http.MethodPut, configURL(srvURL, "1", "jira"),
		map[string]any{"value": validBlock()}, http.StatusOK)
	if out["key"] != "jira" || out["written"] != true || out["changed"] != true {
		t.Errorf("body = %v, want key=jira written=true changed=true", out)
	}
	if out["backup"] != ".claude/project.json.bak" {
		t.Errorf("backup = %v, want .claude/project.json.bak", out["backup"])
	}

	want := `{
  "$schema": "../_schema/project.schema.json",
  "name": "example",
  "displayName": "Example Project",
  "codePath": "/path/to/your/project",
  "mainApp": "web-app",
  "apps": [
    "web-app",
    "marketing-site"
  ],
  "repos": [
    "apps/web-app",
    "infrastructure"
  ],
  "cloud": {
    "provider": "gcp",
    "region": "your-region"
  },
  "stack": {
    "web": "Next.js + TypeScript",
    "db": "PostgreSQL + an ORM"
  },
  "enabledPacks": [
    "iot-pack",
    "web-pack"
  ],
  "jira": {
    "baseUrl": "acme.example.net",
    "projectKey": "ABC",
    "qaStatus": "In QA",
    "repro": {
      "test": "make test"
    }
  }
}
`
	if got := readDisk(t, projectJSONPath(projectDir)); got != want {
		t.Errorf("project.json =\n%s\n--- want ---\n%s", got, want)
	}
	if got := readDisk(t, projectJSONPath(projectDir)+".bak"); got != overlayTenKeys {
		t.Errorf(".bak =\n%s\n--- want the pre-write contents ---\n%s", got, overlayTenKeys)
	}

	// The whole point of the write: the row the modal opened from now reads ok.
	row := pluginRowByName(t, srvURL, "1", "jira-pack")
	if row.ConfigStatus != "ok" {
		t.Errorf("configStatus after a successful write = %q, want ok (missing: %v)",
			row.ConfigStatus, row.ConfigMissing)
	}
}

// A second write replaces the block IN PLACE — a key that migrates to the end of
// the file on every save makes the overlay's history unreviewable.
func TestPutProjectConfigRewriteKeepsKeyPosition(t *testing.T) {
	srvURL, projectDir := configWriteServer(t)
	doJSON(t, http.MethodPut, configURL(srvURL, "1", "jira"),
		map[string]any{"value": validBlock()}, http.StatusOK)

	updated := validBlock()
	updated["qaStatus"] = "Ready for QA"
	doJSON(t, http.MethodPut, configURL(srvURL, "1", "jira"),
		map[string]any{"value": updated}, http.StatusOK)

	body := readDisk(t, projectJSONPath(projectDir))
	if !strings.Contains(body, `"qaStatus": "Ready for QA"`) {
		t.Errorf("second write did not land:\n%s", body)
	}
	if strings.Count(body, `"jira"`) != 1 {
		t.Errorf("jira block appears %d times, want 1:\n%s", strings.Count(body, `"jira"`), body)
	}
	if strings.Index(body, `"jira"`) < strings.Index(body, `"enabledPacks"`) {
		t.Errorf("the block moved above enabledPacks instead of keeping its position:\n%s", body)
	}
	// Every foreign key is still there after two round trips.
	for _, key := range []string{"$schema", "displayName", "codePath", "mainApp", "apps", "repos", "cloud", "stack", "enabledPacks"} {
		if !strings.Contains(body, `"`+key+`"`) {
			t.Errorf("lost foreign key %q:\n%s", key, body)
		}
	}
}

// Re-saving the same value is honest about having changed nothing.
func TestPutProjectConfigIdenticalValueReportsUnchanged(t *testing.T) {
	srvURL, _ := configWriteServer(t)
	doJSON(t, http.MethodPut, configURL(srvURL, "1", "jira"),
		map[string]any{"value": validBlock()}, http.StatusOK)

	out := doJSON(t, http.MethodPut, configURL(srvURL, "1", "jira"),
		map[string]any{"value": validBlock()}, http.StatusOK)
	if out["changed"] != false {
		t.Errorf("changed = %v on a re-save of the same value, want false", out["changed"])
	}
}

// The multi-repo consumer overlay: <project>/.claude is a symlink into a shared
// agents repo. The write must reach the target file, and the rename must not
// cross a filesystem boundary getting there.
func TestPutProjectConfigThroughSymlinkedOverlay(t *testing.T) {
	srv, db := projectsTestServer(t)
	seedCatalogWithRequirements(t, closedRequirements)

	root := filepath.Dir(projectPath(t, srv.URL, "1"))
	shared := filepath.Join(root, "shared-agents")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "project.json"), []byte(overlayTenKeys), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "settings.json"),
		[]byte(`{"enabledPlugins": {"core@swarmery": true, "jira-pack@swarmery": true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "linked")
	if err := os.MkdirAll(linked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(linked, ".claude")); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, `INSERT INTO projects (id, path, slug, name, first_seen, last_activity, archived)
		VALUES (8, ?, 'linked', 'Linked', '2026-07-10T00:00:00Z', '2026-07-14T00:00:00Z', 0)`, linked)

	doJSON(t, http.MethodPut, configURL(srv.URL, "8", "jira"),
		map[string]any{"value": validBlock()}, http.StatusOK)

	body := readDisk(t, filepath.Join(shared, "project.json"))
	if !strings.Contains(body, "acme.example.net") {
		t.Errorf("the symlink target was not written:\n%s", body)
	}
	if !strings.Contains(body, `"$schema"`) {
		t.Errorf("the symlink target lost its foreign keys:\n%s", body)
	}
	if _, err := os.Stat(filepath.Join(shared, "project.json.bak")); err != nil {
		t.Errorf("backup not written beside the target: %v", err)
	}
	// The .claude symlink is still a symlink, not a directory the rename made.
	fi, err := os.Lstat(filepath.Join(linked, ".claude"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error(".claude is no longer a symlink")
	}
}

// The other half of the same pattern, and the shape the RESPONSE gets wrong most
// easily: .claude is a real directory and project.json inside it is the symlink.
// The .bak then lands beside the target and nothing under the project dir names
// it, so a fixed ".claude/project.json.bak" in the body would point an operator
// at a file that does not exist. The recovery path has to open.
func TestPutProjectConfigThroughSymlinkedProjectJSON(t *testing.T) {
	srv, db := projectsTestServer(t)
	seedCatalogWithRequirements(t, closedRequirements)

	root := filepath.Dir(projectPath(t, srv.URL, "1"))
	shared := filepath.Join(root, "shared-file-agents")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(shared, "project.json")
	if err := os.WriteFile(target, []byte(overlayTenKeys), 0o644); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "linked-file")
	claude := filepath.Join(linked, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(claude, "project.json")); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, `INSERT INTO projects (id, path, slug, name, first_seen, last_activity, archived)
		VALUES (9, ?, 'linked-file', 'Linked File', '2026-07-10T00:00:00Z', '2026-07-14T00:00:00Z', 0)`, linked)

	out := doJSON(t, http.MethodPut, configURL(srv.URL, "9", "jira"),
		map[string]any{"value": validBlock()}, http.StatusOK)

	if body := readDisk(t, target); !strings.Contains(body, "acme.example.net") {
		t.Errorf("the symlink target was not written:\n%s", body)
	}
	// The symlink survives as a symlink — a rename over it would have replaced it
	// with a regular file and detached the shared repo.
	fi, err := os.Lstat(filepath.Join(claude, "project.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("project.json is no longer a symlink")
	}
	// The premise of the assertion below: the friendly path really is absent here.
	if _, serr := os.Stat(filepath.Join(claude, "project.json.bak")); !os.IsNotExist(serr) {
		t.Fatalf("stat .claude/project.json.bak = %v, want it absent for a symlinked FILE", serr)
	}
	reported, ok := out["backup"].(string)
	if !ok || reported == "" {
		t.Fatalf("body carries no backup path: %v", out)
	}
	path := reported
	if !filepath.IsAbs(path) {
		path = filepath.Join(linked, reported)
	}
	if got := readDisk(t, path); got != overlayTenKeys {
		t.Errorf("reported backup %q holds:\n%s\n--- want the pre-write contents ---\n%s",
			reported, got, overlayTenKeys)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

// seedCatalogWithRequirements seeds the two-pack catalog plus a declaration,
// for cases that build their own project rather than using configTestServer.
func seedCatalogWithRequirements(t *testing.T, requirements string) {
	t.Helper()
	claudeDir := seedPluginCatalog(t, configPackManifest)
	writePackRequirements(t, claudeDir, "jira-pack", requirements)
}

func problemStrings(t *testing.T, out map[string]any) []string {
	t.Helper()
	raw, ok := out["problems"].([]any)
	if !ok {
		t.Fatalf("body has no problems array: %v", out)
	}
	got := make([]string, 0, len(raw))
	for _, p := range raw {
		s, ok := p.(string)
		if !ok {
			t.Fatalf("problem is not a string: %v", p)
		}
		got = append(got, s)
	}
	return got
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
