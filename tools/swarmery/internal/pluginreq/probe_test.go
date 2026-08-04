package pluginreq

// Tests for the optional `probe` block.
//
// The package's standing rule is that a malformed declaration degrades instead
// of failing, and the probe raises the stakes on it: a probe block is optional,
// so a pack that gets one wrong must lose the SUGGESTIONS, never the config form
// it declared correctly. Half of what follows is that one property, stated for
// every way the block can be wrong.

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// writeProbeDecl writes a requirements.json whose single entry carries `probe`
// verbatim, and returns the pack dir.
func writeProbeDecl(t *testing.T, probe string) string {
	t.Helper()
	dir := t.TempDir()
	body := `{
	  "version": 1,
	  "projectConfig": [
	    {
	      "key": "tracker",
	      "title": "Tracker",
	      "why": "because",
	      "schema": {"type": "object", "properties": {"host": {"type": "string"}}, "required": ["host"]}`
	if probe != "" {
		body += ",\n      \"probe\": " + probe
	}
	body += "\n    }\n  ]\n}"
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const goodProbe = `{
        "needs": ["host"],
        "fields": ["status", "repro.test"],
        "timeoutSeconds": 42,
        "prompt": "Look it up and answer in JSON."
      }`

func TestReadParsesProbe(t *testing.T) {
	reqs, reason := Read(writeProbeDecl(t, goodProbe))
	if reason != "" {
		t.Fatalf("reason = %q, want none", reason)
	}
	if len(reqs) != 1 {
		t.Fatalf("reqs = %d, want 1", len(reqs))
	}
	p := reqs[0].Probe
	if p == nil {
		t.Fatal("Probe is nil for a well-formed block")
	}
	if !reflect.DeepEqual(p.Needs, []string{"host"}) {
		t.Errorf("Needs = %v, want [host]", p.Needs)
	}
	if !reflect.DeepEqual(p.Fields, []string{"status", "repro.test"}) {
		t.Errorf("Fields = %v, want the two declared fields", p.Fields)
	}
	if p.Prompt != "Look it up and answer in JSON." {
		t.Errorf("Prompt = %q, want it passed through verbatim", p.Prompt)
	}
	if got := p.Timeout(); got != 42*time.Second {
		t.Errorf("Timeout() = %s, want 42s", got)
	}
}

// The load-bearing property: a bad probe costs the pack its suggestions and
// nothing else. json.Unmarshal is all-or-nothing per document, so without the
// raw-then-decode split every row below would take the whole declaration down.
func TestReadBrokenProbeKeepsTheRequirement(t *testing.T) {
	cases := map[string]string{
		"absent":                "",
		"a string":              `"do the thing"`,
		"a list":                `["do the thing"]`,
		"a number":              `7`,
		"null":                  `null`,
		"no prompt":             `{"needs": ["host"], "fields": ["status"]}`,
		"blank prompt":          `{"fields": ["status"], "prompt": "   "}`,
		"no fields":             `{"needs": ["host"], "prompt": "look it up"}`,
		"empty fields":          `{"fields": [], "prompt": "look it up"}`,
		"fields is a string":    `{"fields": "status", "prompt": "look it up"}`,
		"needs is a string":     `{"needs": "host", "fields": ["status"], "prompt": "p"}`,
		"timeout is a string":   `{"fields": ["status"], "prompt": "p", "timeoutSeconds": "180"}`,
		"prompt is a structure": `{"fields": ["status"], "prompt": {"text": "p"}}`,
	}
	for name, probe := range cases {
		t.Run(name, func(t *testing.T) {
			reqs, reason := Read(writeProbeDecl(t, probe))
			if reason != "" {
				t.Fatalf("reason = %q — a broken probe must not invalidate the file", reason)
			}
			if len(reqs) != 1 || reqs[0].Key != "tracker" {
				t.Fatalf("reqs = %v, want the requirement to survive", reqs)
			}
			if reqs[0].Schema == nil {
				t.Error("Schema was lost with the probe; the config form would not render")
			}
			if reqs[0].Probe != nil {
				t.Errorf("Probe = %+v, want nil for an unusable block", reqs[0].Probe)
			}
		})
	}
}

// "declares nothing" has one representation in this package, and adding the
// probe decode must not have quietly turned it into an empty slice.
func TestReadEmptyProjectConfigStaysNil(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName),
		[]byte(`{"version": 1, "projectConfig": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	reqs, reason := Read(dir)
	if reason != "" {
		t.Fatalf("reason = %q, want none", reason)
	}
	if reqs != nil {
		t.Errorf("reqs = %#v, want nil", reqs)
	}
}

// ── Timeout ──────────────────────────────────────────────────────────────────

// Clamped, not rejected: a pack that overstates its timeout keeps a working
// probe, it just does not get to decide how long the operator waits.
func TestProbeTimeoutClamps(t *testing.T) {
	cases := map[string]struct {
		declared int
		want     time.Duration
	}{
		"unset":            {0, DefaultProbeTimeout},
		"negative":         {-30, DefaultProbeTimeout},
		"one second":       {1, time.Second},
		"in range":         {60, 60 * time.Second},
		"exactly the cap":  {int(MaxProbeTimeout / time.Second), MaxProbeTimeout},
		"over the ceiling": {100000, MaxProbeTimeout},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := ProbeSpec{TimeoutSeconds: tc.declared}
			if got := p.Timeout(); got != tc.want {
				t.Errorf("Timeout() = %s, want %s", got, tc.want)
			}
		})
	}
}

// ── MissingNeeds ─────────────────────────────────────────────────────────────

// Blank counts as missing here for the same reason it does in MissingPaths: a
// host of "" is a placeholder, and spending minutes of agent time on it would
// buy the operator a wait and no answer.
func TestMissingNeeds(t *testing.T) {
	spec := ProbeSpec{Needs: []string{"host", "key", "repro.test"}}
	cases := map[string]struct {
		value map[string]any
		want  []string
	}{
		"all filled": {
			map[string]any{"host": "h", "key": "K", "repro": map[string]any{"test": "make test"}},
			nil,
		},
		"nothing at all": {
			map[string]any{},
			[]string{"host", "key", "repro.test"},
		},
		"nil map": {
			nil,
			[]string{"host", "key", "repro.test"},
		},
		"blank string": {
			map[string]any{"host": "   ", "key": "K", "repro": map[string]any{"test": "t"}},
			[]string{"host"},
		},
		"explicit null": {
			map[string]any{"host": nil, "key": "K", "repro": map[string]any{"test": "t"}},
			[]string{"host"},
		},
		"nested parent missing": {
			map[string]any{"host": "h", "key": "K"},
			[]string{"repro.test"},
		},
		"nested parent is not an object": {
			map[string]any{"host": "h", "key": "K", "repro": "make test"},
			[]string{"repro.test"},
		},
		"nested leaf blank": {
			map[string]any{"host": "h", "key": "K", "repro": map[string]any{"test": ""}},
			[]string{"repro.test"},
		},
		"non-string values count as filled": {
			// A false bool and a 0 are deliberate values a pack may want; the
			// package refuses to second-guess them anywhere else either.
			map[string]any{"host": false, "key": float64(0), "repro": map[string]any{"test": "t"}},
			nil,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := spec.MissingNeeds(tc.value); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("MissingNeeds() = %v, want %v", got, tc.want)
			}
		})
	}
}

// Declaration order, not map order: the operator reads these back as a list of
// fields to fill in, and a list that reshuffles itself between two identical
// requests is a list nobody trusts.
func TestMissingNeedsKeepsDeclarationOrder(t *testing.T) {
	spec := ProbeSpec{Needs: []string{"zulu", "alpha", "mike"}}
	want := []string{"zulu", "alpha", "mike"}
	for i := 0; i < 20; i++ {
		if got := spec.MissingNeeds(map[string]any{}); !reflect.DeepEqual(got, want) {
			t.Fatalf("MissingNeeds() = %v, want %v", got, want)
		}
	}
}

func TestMissingNeedsWithNoNeedsDeclared(t *testing.T) {
	if got := (ProbeSpec{}).MissingNeeds(nil); got != nil {
		t.Errorf("MissingNeeds() = %v, want nil when the pack declared no inputs", got)
	}
}

// ── Trim ─────────────────────────────────────────────────────────────────────

func TestTrimKeepsOnlyDeclaredFields(t *testing.T) {
	spec := ProbeSpec{Fields: []string{"status", "repro.test"}}
	got := spec.Trim(map[string][]string{
		"status":     {"In QA"},
		"repro.test": {"make test"},
		"host":       {"evil.example.com"}, // an input, not an output
		"secret":     {"hunter2"},          // never declared at all
	})
	want := map[string][]string{"status": {"In QA"}, "repro.test": {"make test"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Trim() = %v, want %v", got, want)
	}
}

func TestTrimCapsValuesPerField(t *testing.T) {
	many := make([]string, 150)
	for i := range many {
		many[i] = string(rune('a'+i%26)) + "-value"
	}
	got := (ProbeSpec{Fields: []string{"status"}}).Trim(map[string][]string{"status": many})
	if len(got["status"]) != maxSuggestionsPerField {
		t.Errorf("len = %d, want %d", len(got["status"]), maxSuggestionsPerField)
	}
	if got["status"][0] != many[0] {
		t.Errorf("first kept = %q, want the cap to take from the front", got["status"][0])
	}
}

// Blanks are dropped because this package treats a blank string as an unfilled
// field everywhere else: offering "" in a datalist would hand the operator a
// value the very next save rejects as missing.
func TestTrimDropsBlanksAndEmptyFields(t *testing.T) {
	spec := ProbeSpec{Fields: []string{"status", "repro.test"}}
	got := spec.Trim(map[string][]string{
		"status":     {"", "   ", "In QA", "\t"},
		"repro.test": {"", "  "},
	})
	want := map[string][]string{"status": {"In QA"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Trim() = %v, want %v — a field left with nothing must not be reported at all", got, want)
	}
}

// The endpoint's contract is that `suggestions` is an object in every response,
// so the browser never distinguishes "no suggestions" from "no probe".
func TestTrimIsNeverNil(t *testing.T) {
	for name, raw := range map[string]map[string][]string{
		"nil input":     nil,
		"empty input":   {},
		"nothing kept":  {"other": {"x"}},
		"empty entries": {"status": {}},
	} {
		t.Run(name, func(t *testing.T) {
			got := (ProbeSpec{Fields: []string{"status"}}).Trim(raw)
			if got == nil {
				t.Fatal("Trim() = nil, want an empty map")
			}
			if len(got) != 0 {
				t.Errorf("Trim() = %v, want empty", got)
			}
		})
	}
}

func TestTrimWithNoFieldsDeclaredKeepsNothing(t *testing.T) {
	got := (ProbeSpec{}).Trim(map[string][]string{"status": {"In QA"}})
	if len(got) != 0 {
		t.Errorf("Trim() = %v, want nothing kept when the pack nominated no fields", got)
	}
}
