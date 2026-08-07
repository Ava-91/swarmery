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
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/sysscan"
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

	// The guide findings the linter (sysscan/lint.go) writes over exactly this
	// world — phase 3's coverage surface reads them and never re-parses a file:
	//   agent 1  complete + reviewed          → clean (its one row is RESOLVED,
	//                                           and a resolved row must not count)
	//   agent 2  no guide                     → docs_missing
	//   skill 1  guide without Worked example → docs_incomplete
	//   command 1 complete but generated+stale→ docs_stale + docs_unreviewed
	//   command 2 file gone                   → nothing: an unreadable file is
	//                                           skipped by the linter, not a
	//                                           violation (it degrades, like the
	//                                           detail endpoint above).
	// The incomplete message is assembled from the SAME marker constant the
	// linter writes, so this fixture cannot drift from the real message shape —
	// the trailing rune-floor parenthetical included, since parsing must survive it.
	incomplete := "/u/.claude/skills/documented-skill: usage guide is " +
		sysscan.DocsMissingSectionsMarker + "Worked example (each required subsection needs 40+ runes of body)"
	exec(`INSERT INTO config_lint_findings (target, rule, severity, message, detected_at, resolved_at) VALUES
	      ('agent:1', 'docs_missing', 'warn', 'was undocumented', '2026-08-01T00:00:00Z', '2026-08-02T00:00:00Z'),
	      ('agent:2', 'docs_missing', 'warn',
	       '/u/.claude/agents/plain-agent.md: no usage guide section', '2026-08-02T00:00:00Z', NULL),
	      ('skill:1', 'docs_incomplete', 'warn', ?, '2026-08-02T00:00:00Z', NULL),
	      ('command:1', 'docs_stale', 'info', 'deploy.md: the item changed since its guide was written',
	       '2026-08-02T00:00:00Z', NULL),
	      ('command:1', 'docs_unreviewed', 'info', 'deploy.md: docs.status is "generated"',
	       '2026-08-02T00:00:00Z', NULL)`, incomplete)

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

// ---- phase 3: coverage headline, list flag, insights list ------------------

func TestSystemSummaryDocsCoverage(t *testing.T) {
	srv := sysDocsServer(t)

	var s struct {
		Agents   int64 `json:"agents"`
		Skills   int64 `json:"skills"`
		Commands int64 `json:"commands"`
		Docs     struct {
			Total      int64 `json:"total"`
			Documented int64 `json:"documented"`
			Reviewed   int64 `json:"reviewed"`
		} `json:"docs"`
	}
	getJSON(t, srv.URL+"/api/system/summary", &s)

	if want := s.Agents + s.Skills + s.Commands; s.Docs.Total != want {
		t.Errorf("docs.total = %d, want %d (live agents + skills + commands)", s.Docs.Total, want)
	}
	// 5 items; agent 2 (missing) and skill 1 (incomplete) are undocumented; of
	// the 3 documented ones only command 1 is unreviewed. Agent 1's RESOLVED
	// docs_missing row must not count against it.
	if s.Docs.Total != 5 || s.Docs.Documented != 3 || s.Docs.Reviewed != 2 {
		t.Errorf("docs = %+v, want total=5 documented=3 reviewed=2", s.Docs)
	}
}

func TestSystemListsCarryDocumentedFlag(t *testing.T) {
	srv := sysDocsServer(t)

	for _, tc := range []struct {
		path string
		want map[string]bool // item name → documented
	}{
		{"/api/system/agents", map[string]bool{"documented-agent": true, "plain-agent": false}},
		{"/api/system/skills", map[string]bool{"documented-skill": false}},
	} {
		var items []struct {
			Name       string `json:"name"`
			Documented bool   `json:"documented"`
		}
		getJSON(t, srv.URL+tc.path, &items)
		got := map[string]bool{}
		for _, it := range items {
			got[it.Name] = it.Documented
		}
		for name, want := range tc.want {
			if got[name] != want {
				t.Errorf("GET %s: %s.documented = %v, want %v", tc.path, name, got[name], want)
			}
		}
	}
}

func TestSystemInsightsUndocumented(t *testing.T) {
	srv := sysDocsServer(t)

	var got struct {
		Undocumented []struct {
			Kind    string   `json:"kind"`
			ID      int64    `json:"id"`
			Name    string   `json:"name"`
			Scope   string   `json:"scope"`
			Path    string   `json:"path"`
			Rule    string   `json:"rule"`
			Missing []string `json:"missing"`
		} `json:"undocumented"`
	}
	getJSON(t, srv.URL+"/api/system/insights", &got)

	if len(got.Undocumented) != 2 {
		t.Fatalf("undocumented = %d entries, want 2 (plain-agent, documented-skill)", len(got.Undocumented))
	}
	byName := map[string]int{}
	for i, u := range got.Undocumented {
		byName[u.Name] = i
	}

	agent := got.Undocumented[byName["plain-agent"]]
	if agent.Kind != "agent" || agent.Scope != "global" || agent.Path == "" {
		t.Errorf("plain-agent entry = %+v, want kind/scope/path populated", agent)
	}
	// No guide at all → every required subsection is missing.
	if len(agent.Missing) != len(sysscan.RequiredDocSections) {
		t.Errorf("plain-agent missing = %q, want all %d required subsections",
			agent.Missing, len(sysscan.RequiredDocSections))
	}

	skill := got.Undocumented[byName["documented-skill"]]
	if skill.Rule != sysscan.RuleDocsIncomplete {
		t.Errorf("documented-skill rule = %q, want %q", skill.Rule, sysscan.RuleDocsIncomplete)
	}
	// Parsed out of the finding message — the rune-floor parenthetical the
	// linter appends must not leak into the section name.
	if len(skill.Missing) != 1 || skill.Missing[0] != "Worked example" {
		t.Errorf("documented-skill missing = %q, want [\"Worked example\"]", skill.Missing)
	}

	// The list is served even when empty, never null.
	if !strings.Contains(okBody(t, srv.URL+"/api/system/insights"), `"undocumented":[`) {
		t.Error("insights payload has no undocumented array")
	}
}

// ---- docs findings stay OUT of the row-level severity roll-up ---------------

// docsSevServer seeds three agents that differ only in which findings are open,
// so one world can prove the roll-up is rule-scoped and not severity-scoped:
//
//	agent 1  docs_missing (warn) ONLY          → lintMax null, documented false
//	agent 2  agent_no_description (warn) ONLY  → lintMax warn, documented true
//	agent 3  both                              → lintMax warn, documented false
//
// Agent 2 is the control: if the exclusion were written against `severity`
// rather than `rule`, its warn would vanish too and this test would catch it.
func docsSevServer(t *testing.T) *httptest.Server {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "docssev.db"))
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

	exec(`INSERT INTO agents (id, name, scope, project_id, file_path, model, description,
	                          current_version_id, origin, plugin_name) VALUES
	      (1, 'docs-only',  'global', NULL, '/u/.claude/agents/docs-only.md',  NULL, 'a', NULL, 'local', NULL),
	      (2, 'other-only', 'global', NULL, '/u/.claude/agents/other-only.md', NULL, 'b', NULL, 'local', NULL),
	      (3, 'both',       'global', NULL, '/u/.claude/agents/both.md',       NULL, 'c', NULL, 'local', NULL)`)

	// Severities are the ones sysscan/lint.go writes for these rules — the guide
	// rule stays a warn (docs/system-docs-format.md); only the ROLL-UP ignores it.
	exec(`INSERT INTO config_lint_findings (target, rule, severity, message, detected_at, resolved_at) VALUES
	      ('agent:1', ?, 'warn', 'docs-only.md: no usage guide section',   '2026-08-02T00:00:00Z', NULL),
	      ('agent:2', ?, 'warn', 'other-only.md: empty description',       '2026-08-02T00:00:00Z', NULL),
	      ('agent:3', ?, 'warn', 'both.md: no usage guide section',        '2026-08-02T00:00:00Z', NULL),
	      ('agent:3', ?, 'warn', 'both.md: empty description',             '2026-08-02T00:00:00Z', NULL)`,
		sysscan.RuleDocsMissing, sysscan.RuleAgentNoDescription,
		sysscan.RuleDocsMissing, sysscan.RuleAgentNoDescription)

	h, err := NewServer(db, false)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func TestDocsFindingsExcludedFromLintMax(t *testing.T) {
	srv := docsSevServer(t)

	type row struct {
		Name       string  `json:"name"`
		LintMax    *string `json:"lintMax"`
		Documented bool    `json:"documented"`
	}
	want := map[string]struct {
		lintMax    *string
		documented bool
	}{
		"docs-only":  {nil, false},
		"other-only": {strptr("warn"), true},
		"both":       {strptr("warn"), false},
	}

	// The list projection…
	var items []row
	getJSON(t, srv.URL+"/api/system/agents", &items)
	if len(items) != 3 {
		t.Fatalf("agents = %d rows, want 3", len(items))
	}
	for _, it := range items {
		w, ok := want[it.Name]
		if !ok {
			t.Fatalf("unexpected row %q", it.Name)
		}
		if !sameStr(it.LintMax, w.lintMax) || it.Documented != w.documented {
			t.Errorf("list %s: lintMax=%s documented=%v, want lintMax=%s documented=%v",
				it.Name, showStr(it.LintMax), it.Documented, showStr(w.lintMax), w.documented)
		}
	}

	// …and the detail projection, which splices the SAME aggregate.
	for id, name := range map[string]string{"1": "docs-only", "2": "other-only", "3": "both"} {
		var d row
		getJSON(t, srv.URL+"/api/system/agents/"+id, &d)
		w := want[name]
		if !sameStr(d.LintMax, w.lintMax) || d.Documented != w.documented {
			t.Errorf("detail %s: lintMax=%s documented=%v, want lintMax=%s documented=%v",
				name, showStr(d.LintMax), d.Documented, showStr(w.lintMax), w.documented)
		}
	}

	// The fleet headline follows the same rule, or the click-to-filter badges
	// would promise rows that ?lint=warn can no longer produce: 2 non-docs warns
	// (agents 2 and 3), not 4.
	var s struct {
		Lint struct {
			Error int64 `json:"error"`
			Warn  int64 `json:"warn"`
			Info  int64 `json:"info"`
		} `json:"lint"`
		Docs struct {
			Total      int64 `json:"total"`
			Documented int64 `json:"documented"`
		} `json:"docs"`
	}
	getJSON(t, srv.URL+"/api/system/summary", &s)
	if s.Lint.Warn != 2 || s.Lint.Error != 0 || s.Lint.Info != 0 {
		t.Errorf("summary.lint = %+v, want warn=2 error=0 info=0 (docs_* excluded)", s.Lint)
	}
	if s.Docs.Total != 3 || s.Docs.Documented != 1 {
		t.Errorf("summary.docs = %+v, want total=3 documented=1", s.Docs)
	}

	// The finding itself is NOT suppressed — it is still an active warn, and it
	// still reaches the client through the insights list.
	var ins struct {
		Undocumented []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
			Rule string `json:"rule"`
		} `json:"undocumented"`
	}
	getJSON(t, srv.URL+"/api/system/insights", &ins)
	found := map[string]string{}
	for _, u := range ins.Undocumented {
		found[u.Name] = u.Rule
	}
	if found["docs-only"] != sysscan.RuleDocsMissing {
		t.Errorf("insights.undocumented[docs-only] = %q, want %q — a finding kept out of the "+
			"severity roll-up must still be listed", found["docs-only"], sysscan.RuleDocsMissing)
	}
	if _, ok := found["other-only"]; ok {
		t.Error("insights.undocumented listed other-only, which has no docs finding")
	}
}

func strptr(s string) *string { return &s }

func sameStr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func showStr(s *string) string {
	if s == nil {
		return "null"
	}
	return `"` + *s + `"`
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
