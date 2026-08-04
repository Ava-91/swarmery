package pluginreq

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The fixture is a verbatim copy of plugins/jira-pack/requirements.json (the
// only real declaration that exists), so a change to the shipped format that
// this evaluator cannot read fails here. A domain word is legitimate in a test
// fixture — the rule it must not cross is into non-test daemon code, and
// nothing in pluginreq.go names a pack.
const jiraDeclaration = `{
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
            "properties": {
              "setup": { "type": "string" },
              "test":  { "type": "string" }
            },
            "required": ["test"]
          },
          "budget": {
            "type": "object",
            "properties": {
              "maxFiles":    { "type": "integer" },
              "maxAttempts": { "type": "integer" }
            }
          }
        },
        "required": ["baseUrl", "projectKey", "qaStatus", "repro"]
      }
    }
  ]
}`

// writePack drops a requirements.json into a fresh pack dir and returns it.
func writePack(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// firstReq parses the fixture and hands back its single requirement.
func firstReq(t *testing.T) Requirement {
	t.Helper()
	reqs, reason := Read(writePack(t, jiraDeclaration))
	if reason != "" || len(reqs) != 1 {
		t.Fatalf("Read(fixture) = %d reqs, reason %q; want 1 req and no reason", len(reqs), reason)
	}
	return reqs[0]
}

// ── Read ─────────────────────────────────────────────────────────────────────

func TestReadHappyPath(t *testing.T) {
	reqs, reason := Read(writePack(t, jiraDeclaration))
	if reason != "" {
		t.Fatalf("reason = %q, want empty", reason)
	}
	if len(reqs) != 1 {
		t.Fatalf("len(reqs) = %d, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Key != "jira" || r.Title != "Jira tracker" || r.Docs != "skills/jira-config/SKILL.md" {
		t.Errorf("requirement = %+v, want the declared key/title/docs", r)
	}
	if r.Why == "" {
		t.Error("Why is empty, want the declared rationale")
	}
	// Schema must survive as raw JSON — it drives a browser form verbatim.
	var probe map[string]any
	if err := json.Unmarshal(r.Schema, &probe); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	if probe["additionalProperties"] != false {
		t.Errorf("schema lost additionalProperties: %v", probe)
	}
}

// A pack without the file is the normal case, not an error — most packs declare
// nothing at all.
func TestReadMissingFileIsNotAnError(t *testing.T) {
	reqs, reason := Read(t.TempDir())
	if reqs != nil || reason != "" {
		t.Errorf("Read(empty dir) = (%v, %q), want (nil, \"\")", reqs, reason)
	}
}

func TestReadInvalidJSON(t *testing.T) {
	reqs, reason := Read(writePack(t, `{not json`))
	if reqs != nil {
		t.Errorf("reqs = %v, want nil", reqs)
	}
	if reason != "requirements.json: invalid JSON" {
		t.Errorf("reason = %q, want %q", reason, "requirements.json: invalid JSON")
	}
}

// Valid JSON of the wrong shape (an array, not the envelope object) is still a
// declaration this daemon cannot read.
func TestReadWrongTopLevelShape(t *testing.T) {
	if _, reason := Read(writePack(t, `[1, 2, 3]`)); reason != "requirements.json: invalid JSON" {
		t.Errorf("reason = %q, want the invalid-JSON reason", reason)
	}
}

func TestReadUnsupportedVersion(t *testing.T) {
	reqs, reason := Read(writePack(t, `{"version": 2, "projectConfig": [{"key": "x"}]}`))
	if reqs != nil {
		t.Errorf("reqs = %v, want nil (a format we cannot read is not one we may guess at)", reqs)
	}
	if reason != "requirements.json: unsupported version 2" {
		t.Errorf("reason = %q, want the unsupported-version reason", reason)
	}
}

// A missing version field is version 0 — equally unsupported, and the reason
// must say so rather than silently assuming 1.
func TestReadAbsentVersion(t *testing.T) {
	if _, reason := Read(writePack(t, `{"projectConfig": []}`)); reason != "requirements.json: unsupported version 0" {
		t.Errorf("reason = %q, want the unsupported-version-0 reason", reason)
	}
}

// A directory where the file should be is unreadable, not absent: the caller
// acts the same either way, but the reason must not claim "declares nothing".
func TestReadUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, FileName), 0o755); err != nil {
		t.Fatal(err)
	}
	reqs, reason := Read(dir)
	if reqs != nil {
		t.Errorf("reqs = %v, want nil", reqs)
	}
	if reason != "requirements.json: unreadable" {
		t.Errorf("reason = %q, want the unreadable reason", reason)
	}
}

// A well-formed declaration that simply declares nothing is not a failure.
func TestReadEmptyProjectConfig(t *testing.T) {
	reqs, reason := Read(writePack(t, `{"version": 1, "projectConfig": []}`))
	if len(reqs) != 0 || reason != "" {
		t.Errorf("Read = (%v, %q), want (empty, \"\")", reqs, reason)
	}
}

// ── MissingPaths ─────────────────────────────────────────────────────────────

func TestMissingPathsEmptyConfigReportsEveryRequiredLeaf(t *testing.T) {
	got := MissingPaths(nil, firstReq(t))
	want := []string{"baseUrl", "projectKey", "qaStatus", "repro.test"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MissingPaths(nil) = %v, want %v (schema order, nested leaf dotted)", got, want)
	}
}

// The acceptance case for descent: repro is present and satisfies "is it
// there?", but the pack asked for repro.test, and an empty object has none.
func TestMissingPathsDescendsIntoPresentButIncompleteObject(t *testing.T) {
	cfg := map[string]any{
		"baseUrl":    "example.atlassian.net",
		"projectKey": "ABC",
		"qaStatus":   "In QA",
		"repro":      map[string]any{},
	}
	got := MissingPaths(cfg, firstReq(t))
	if want := []string{"repro.test"}; !reflect.DeepEqual(got, want) {
		t.Errorf("MissingPaths = %v, want %v", got, want)
	}
}

// A present-but-blank string is a placeholder the operator has not filled in,
// not a satisfied field.
func TestMissingPathsTreatsBlankStringsAsUnset(t *testing.T) {
	cfg := map[string]any{
		"baseUrl":    "example.atlassian.net",
		"projectKey": "   ", // whitespace-only fails an exact-match lookup too
		"qaStatus":   "",
		"repro":      map[string]any{"test": "make test"},
	}
	got := MissingPaths(cfg, firstReq(t))
	if want := []string{"projectKey", "qaStatus"}; !reflect.DeepEqual(got, want) {
		t.Errorf("MissingPaths = %v, want %v", got, want)
	}
}

// An explicit null is as unset as an absent key.
func TestMissingPathsTreatsNullAsUnset(t *testing.T) {
	cfg := map[string]any{
		"baseUrl":    nil,
		"projectKey": "ABC",
		"qaStatus":   "In QA",
		"repro":      map[string]any{"test": "make test"},
	}
	got := MissingPaths(cfg, firstReq(t))
	if want := []string{"baseUrl"}; !reflect.DeepEqual(got, want) {
		t.Errorf("MissingPaths = %v, want %v", got, want)
	}
}

// A fully populated block reports nothing — and neither the optional leaf
// (repro.setup) nor the entirely optional object (budget) may ever appear,
// filled in or not.
func TestMissingPathsSatisfiedNeverReportsOptionalFields(t *testing.T) {
	cfg := map[string]any{
		"baseUrl":    "example.atlassian.net",
		"projectKey": "ABC",
		"qaStatus":   "In QA",
		"repro":      map[string]any{"test": "make test"}, // setup omitted
		// budget omitted entirely
	}
	if got := MissingPaths(cfg, firstReq(t)); len(got) != 0 {
		t.Errorf("MissingPaths = %v, want empty (optional fields are never walked)", got)
	}
}

// A required object of the wrong JSON type holds none of the leaves the pack
// asked for, so it reports them rather than passing as "present".
func TestMissingPathsRequiredObjectOfWrongType(t *testing.T) {
	cfg := map[string]any{
		"baseUrl":    "example.atlassian.net",
		"projectKey": "ABC",
		"qaStatus":   "In QA",
		"repro":      "make test", // a string where an object was declared
	}
	got := MissingPaths(cfg, firstReq(t))
	if want := []string{"repro.test"}; !reflect.DeepEqual(got, want) {
		t.Errorf("MissingPaths = %v, want %v", got, want)
	}
}

// false and 0 are deliberate values, not blanks — second-guessing them would
// make a filled-in boolean or zero impossible to express.
func TestMissingPathsFalseAndZeroAreFilledIn(t *testing.T) {
	r := Requirement{Key: "k", Schema: json.RawMessage(
		`{"properties":{"flag":{"type":"boolean"},"count":{"type":"integer"}},"required":["flag","count"]}`)}
	if got := MissingPaths(map[string]any{"flag": false, "count": float64(0)}, r); len(got) != 0 {
		t.Errorf("MissingPaths = %v, want empty", got)
	}
}

// A required property the schema never describes stays a leaf: presence is all
// the pack asked for.
func TestMissingPathsUndescribedRequiredProperty(t *testing.T) {
	r := Requirement{Key: "k", Schema: json.RawMessage(`{"required":["ghost"]}`)}
	if got := MissingPaths(nil, r); !reflect.DeepEqual(got, []string{"ghost"}) {
		t.Errorf("MissingPaths(nil) = %v, want [ghost]", got)
	}
	if got := MissingPaths(map[string]any{"ghost": "here"}, r); len(got) != 0 {
		t.Errorf("MissingPaths(present) = %v, want empty", got)
	}
}

// Three levels deep: descent is driven by the schema, not by a fixed depth.
func TestMissingPathsDescendsMoreThanOneLevel(t *testing.T) {
	r := Requirement{Key: "k", Schema: json.RawMessage(`{
		"properties": {"a": {"properties": {"b": {"properties": {"c": {"type": "string"}},
			"required": ["c"]}}, "required": ["b"]}},
		"required": ["a"]}`)}
	if got := MissingPaths(nil, r); !reflect.DeepEqual(got, []string{"a.b.c"}) {
		t.Errorf("MissingPaths = %v, want [a.b.c]", got)
	}
}

// A schema fragment we failed to read cannot be turned into a form, so claiming
// specific missing paths from it would be inventing them.
func TestMissingPathsUnparseableSchema(t *testing.T) {
	if got := MissingPaths(nil, Requirement{Key: "k", Schema: json.RawMessage(`{not json`)}); got != nil {
		t.Errorf("MissingPaths = %v, want nil", got)
	}
}

// A schema with no `required` at all asks for nothing.
func TestMissingPathsNoRequiredKeyword(t *testing.T) {
	r := Requirement{Key: "k", Schema: json.RawMessage(`{"properties":{"a":{"type":"string"}}}`)}
	if got := MissingPaths(nil, r); len(got) != 0 {
		t.Errorf("MissingPaths = %v, want empty", got)
	}
}

// ── ReadProjectConfig / Block ────────────────────────────────────────────────

// writeOverlay drops a project.json into <dir>/.claude/ and returns the project root.
func writeOverlay(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "project.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestReadProjectConfigHappyPath(t *testing.T) {
	root := writeOverlay(t, `{"projectName": "acme", "jira": {"projectKey": "ABC"}}`)
	cfg := ReadProjectConfig(root)
	if len(cfg) != 2 {
		t.Fatalf("cfg = %v, want 2 top-level keys", cfg)
	}
	raw, obj := Block(cfg, "jira")
	if obj["projectKey"] != "ABC" {
		t.Errorf("block = %v, want projectKey ABC", obj)
	}
	// Raw must round-trip verbatim — it is the browser form's prefill.
	if string(raw) != `{"projectKey": "ABC"}` {
		t.Errorf("raw = %s, want the bytes straight out of the file", raw)
	}
}

// Missing and malformed overlays are correct inputs, not failures: both mean
// the daemon cannot see the config, so it cannot claim the config is satisfied.
func TestReadProjectConfigMissingAndMalformed(t *testing.T) {
	if cfg := ReadProjectConfig(t.TempDir()); cfg != nil {
		t.Errorf("ReadProjectConfig(no overlay) = %v, want nil", cfg)
	}
	if cfg := ReadProjectConfig(writeOverlay(t, `{not json`)); cfg != nil {
		t.Errorf("ReadProjectConfig(malformed) = %v, want nil", cfg)
	}
}

// The multi-repo consumer pattern: <project>/.claude is a symlink into a shared
// overlay repo. It must resolve like any other path.
func TestReadProjectConfigFollowsSymlinkedClaudeDir(t *testing.T) {
	shared := t.TempDir()
	if err := os.WriteFile(filepath.Join(shared, "project.json"), []byte(`{"jira": {"projectKey": "ABC"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Symlink(shared, filepath.Join(root, ".claude")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, obj := Block(ReadProjectConfig(root), "jira"); obj["projectKey"] != "ABC" {
		t.Errorf("block through symlink = %v, want projectKey ABC", obj)
	}
}

func TestBlockAbsentNullAndNonObject(t *testing.T) {
	cfg := ReadProjectConfig(writeOverlay(t, `{"nul": null, "str": "scalar"}`))

	if raw, obj := Block(cfg, "absent"); raw != nil || obj != nil {
		t.Errorf("Block(absent) = (%s, %v), want (nil, nil)", raw, obj)
	}
	// A null decodes to a nil obj but keeps its raw form.
	if raw, obj := Block(cfg, "nul"); obj != nil || string(raw) != "null" {
		t.Errorf("Block(null) = (%s, %v), want (null, nil)", raw, obj)
	}
	// A scalar cannot decode into an object, but raw is still returned so a
	// caller can show the operator what is actually in the file.
	if raw, obj := Block(cfg, "str"); obj != nil || string(raw) != `"scalar"` {
		t.Errorf("Block(scalar) = (%s, %v), want (\"scalar\", nil)", raw, obj)
	}
}

// The end-to-end shape the API layer uses: overlay on disk → block → paths.
func TestBlockFeedsMissingPaths(t *testing.T) {
	root := writeOverlay(t, `{"jira": {"baseUrl": "example.atlassian.net", "repro": {"setup": "npm i"}}}`)
	_, obj := Block(ReadProjectConfig(root), "jira")
	got := MissingPaths(obj, firstReq(t))
	want := []string{"projectKey", "qaStatus", "repro.test"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MissingPaths = %v, want %v", got, want)
	}
}
