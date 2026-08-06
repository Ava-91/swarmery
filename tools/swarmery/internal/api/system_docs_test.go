package api

// phase 2: system docs tab — the `docs` object on the agents/skills detail and
// command hub endpoints (docs/system-docs-format.md).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

const docsFixtureRoot = "../../testdata/sysconfig"

// docsSecret is planted inside a guide body: the markdown is served redacted
// exactly like the item body, so it must never reach the wire.
const docsSecret = "sk-ant-guidesecret999"

func readDocsFixture(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(docsFixtureRoot, rel))
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	return string(raw)
}

// sysDocsServer seeds a world with exactly the cases the guide surface has: a
// fully documented agent, an undocumented agent, a partially documented skill,
// and two commands — one whose file exists on disk, one whose file has
// vanished.
func sysDocsServer(t *testing.T) *httptest.Server {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "docs.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}

	documented := readDocsFixture(t, "claude/agents/documented-agent.md")
	// Plant a secret in the guide body to prove docs.markdown is redacted.
	documented = strings.Replace(documented,
		"## What it does\n",
		"## What it does\nANTHROPIC_API_KEY="+docsSecret+"\n", 1)
	skill := readDocsFixture(t, "claude/skills/documented-skill/SKILL.md")
	undocumented := "---\nname: plain-agent\n---\n\n# Role\n\nNo usage guide at all.\n"

	exec(`INSERT INTO agents (id, name, scope, project_id, file_path, model, description,
	                          current_version_id, origin, plugin_name)
	      VALUES (1, 'documented-agent', 'global', NULL, '/u/.claude/agents/documented-agent.md',
	              'claude-fable-5', 'Documented fixture', 1, 'local', NULL),
	             (2, 'plain-agent', 'global', NULL, '/u/.claude/agents/plain-agent.md',
	              NULL, 'Undocumented fixture', 2, 'local', NULL)`)
	exec(`INSERT INTO agent_versions (id, agent_id, content_hash, content, created_at, change_note) VALUES
	      (1, 1, 'doc-hash', ?, '2026-08-01T00:00:00Z', 'initial'),
	      (2, 2, 'plain-hash', ?, '2026-08-01T00:00:00Z', 'initial')`, documented, undocumented)

	exec(`INSERT INTO skills (id, name, scope, project_id, dir_path, description,
	                          current_version_id, origin, plugin_name)
	      VALUES (1, 'documented-skill', 'global', NULL, '/u/.claude/skills/documented-skill',
	              'Partially documented fixture', 1, 'local', NULL)`)
	exec(`INSERT INTO skill_versions (id, skill_id, content_hash, content, created_at, change_note)
	      VALUES (1, 1, 'skill-doc-hash', ?, '2026-08-01T00:00:00Z', 'initial')`, skill)

	// Command 1 exists on disk with a guide; command 2 points at a path that
	// is gone — the degrade-to-empty case.
	cmdDir := t.TempDir()
	cmdPath := filepath.Join(cmdDir, "deploy.md")
	writeFile(t, cmdPath, readDocsFixture(t, "claude/agents/stale-docs-agent.md"))
	exec(`INSERT INTO commands (id, name, scope, project_id, file_path, description,
	                            origin, plugin_name, content_hash)
	      VALUES (1, 'deploy', 'global', NULL, ?, 'Deploy the app', 'local', NULL, 'cmd-hash'),
	             (2, 'vanished', 'global', NULL, ?, 'Gone', 'local', NULL, 'gone-hash')`,
		cmdPath, filepath.Join(cmdDir, "does-not-exist.md"))

	h, err := NewServer(db, false)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// okBody returns the raw response body, asserting HTTP 200. Raw text is the
// only way to tell `[]` from `null` — both decode to a zero-length Go slice.
func okBody(t *testing.T, url string) string {
	t.Helper()
	status, body := getBody(t, url)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", url, status)
	}
	return body
}

// docsWire mirrors systemDocsDTO for assertions.
type docsWire struct {
	Present   bool     `json:"present"`
	Duplicate bool     `json:"duplicate"`
	Markdown  string   `json:"markdown"`
	Sections  []string `json:"sections"`
	Missing   []string `json:"missing"`
	Status    string   `json:"status"`
	Stale     bool     `json:"stale"`
}

func TestSystemAgentDetailServesDocs(t *testing.T) {
	srv := sysDocsServer(t)

	var got struct {
		Docs docsWire `json:"docs"`
	}
	getJSON(t, srv.URL+"/api/system/agents/1", &got)
	d := got.Docs

	if !d.Present || d.Duplicate {
		t.Fatalf("present=%v duplicate=%v, want true/false", d.Present, d.Duplicate)
	}
	if d.Status != "reviewed" {
		t.Errorf("status = %q, want reviewed", d.Status)
	}
	if len(d.Sections) != 8 {
		t.Errorf("sections = %q, want all 8", d.Sections)
	}
	if len(d.Missing) != 0 {
		t.Errorf("missing = %q, want none", d.Missing)
	}
	// The planted line sits INSIDE the guide, and §4 deletes the guide before
	// fingerprinting — so editing it must NOT mark the item stale. This is the
	// property that keeps the generator from invalidating its own output.
	if d.Stale {
		t.Error("stale = true after an edit confined to the guide block (§4 excludes it)")
	}
	if d.Markdown == "" {
		t.Error("markdown is empty")
	}

	// Redaction: the guide goes through redact() like the body does.
	body := okBody(t, srv.URL+"/api/system/agents/1")
	if strings.Contains(body, docsSecret) {
		t.Errorf("response leaked the secret planted in the guide body")
	}
}

func TestSystemAgentDetailUndocumentedServesEmptyArrays(t *testing.T) {
	srv := sysDocsServer(t)

	var got struct {
		Docs docsWire `json:"docs"`
	}
	getJSON(t, srv.URL+"/api/system/agents/2", &got)
	if got.Docs.Present {
		t.Errorf("present = true for an item with no guide, want false")
	}
	if got.Docs.Status != "" || got.Docs.Stale {
		t.Errorf("status=%q stale=%v, want empty/false", got.Docs.Status, got.Docs.Stale)
	}

	// The wire contract: [] and never null, so the client can iterate blindly.
	body := okBody(t, srv.URL+"/api/system/agents/2")
	if !strings.Contains(body, `"sections":[]`) {
		t.Errorf("sections did not serialize as [] — body: %s", docsSlice(t, body))
	}
	if !strings.Contains(body, `"missing":[]`) {
		t.Errorf("missing did not serialize as [] — body: %s", docsSlice(t, body))
	}
	if strings.Contains(body, `"sections":null`) || strings.Contains(body, `"missing":null`) {
		t.Errorf("guide arrays serialized as null — body: %s", docsSlice(t, body))
	}
}

func TestSystemSkillDetailServesPartialDocs(t *testing.T) {
	srv := sysDocsServer(t)

	var got struct {
		Docs docsWire `json:"docs"`
	}
	getJSON(t, srv.URL+"/api/system/skills/1", &got)
	if !got.Docs.Present {
		t.Fatal("present = false, want true")
	}
	// The fence trap survives the whole round trip: a fence-blind parse loses
	// the subsections after `# deploy the thing` and reports more than one gap.
	if len(got.Docs.Sections) != 7 {
		t.Errorf("sections = %q, want 7", got.Docs.Sections)
	}
	if want := []string{"Worked example"}; len(got.Docs.Missing) != 1 || got.Docs.Missing[0] != want[0] {
		t.Errorf("missing = %q, want %q", got.Docs.Missing, want)
	}
}

func TestCommandHubServesDocs(t *testing.T) {
	srv := sysDocsServer(t)

	var got struct {
		Docs docsWire `json:"docs"`
	}
	getJSON(t, srv.URL+"/api/system/commands/1/hub", &got)
	if !got.Docs.Present {
		t.Fatal("present = false for a command whose file carries a guide")
	}
	if got.Docs.Status != "generated" {
		t.Errorf("status = %q, want generated", got.Docs.Status)
	}
	if len(got.Docs.Missing) != 0 {
		t.Errorf("missing = %q, want none", got.Docs.Missing)
	}
	if !got.Docs.Stale {
		t.Error("stale = false, want true (the fixture records 000000000000)")
	}
}

func TestCommandHubMissingFileServesAbsentDocs(t *testing.T) {
	srv := sysDocsServer(t)

	// getJSON asserts HTTP 200: a vanished file degrades, never fails.
	var got struct {
		Docs docsWire `json:"docs"`
	}
	getJSON(t, srv.URL+"/api/system/commands/2/hub", &got)
	if got.Docs.Present {
		t.Errorf("present = true for a command whose file is gone, want false")
	}
	body := okBody(t, srv.URL+"/api/system/commands/2/hub")
	if !strings.Contains(body, `"sections":[]`) || !strings.Contains(body, `"missing":[]`) {
		t.Errorf("guide arrays did not serialize as [] — body: %s", docsSlice(t, body))
	}
}

// docsSlice extracts the "docs" object from a response for readable failures.
func docsSlice(t *testing.T, body string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return body
	}
	return string(m["docs"])
}
