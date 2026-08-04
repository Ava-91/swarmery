package pluginreq

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// ── Validate ─────────────────────────────────────────────────────────────────
//
// Validate is the gate in front of the ONE place the daemon writes an
// operator's .claude/project.json, so these cases are weighted towards what a
// bad write would cost: a typo saved in silence, a required leaf accepted as
// blank, a nested block never descended into.

// reqFor builds a requirement straight from a schema fragment, so a case can
// state the exact construct it is about instead of threading a whole
// requirements.json through Read.
func reqFor(t *testing.T, schema string) Requirement {
	t.Helper()
	if !json.Valid([]byte(schema)) {
		t.Fatalf("test fixture is not valid JSON: %s", schema)
	}
	return Requirement{Key: "block", Schema: json.RawMessage(schema)}
}

// obj decodes a candidate value the way the handler does.
func obj(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("test fixture is not a JSON object: %v", err)
	}
	return m
}

// objNumbers decodes with UseNumber, the shape a caller gets from a decoder
// configured to keep integers exact.
func objNumbers(t *testing.T, body string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader([]byte(body)))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("test fixture is not a JSON object: %v", err)
	}
	return m
}

func assertProblems(t *testing.T, got, want []string) {
	t.Helper()
	if len(want) == 0 {
		if len(got) != 0 {
			t.Errorf("problems = %v, want none", got)
		}
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("problems = %v, want %v", got, want)
	}
}

// The headline case: the real shipped declaration, satisfied.
func TestValidateCompleteBlockPasses(t *testing.T) {
	r := firstReq(t)
	value := obj(t, `{
		"baseUrl": "acme.example.net", "projectKey": "ABC", "qaStatus": "In QA",
		"repro": {"setup": "npm ci", "test": "make test"},
		"budget": {"maxFiles": 5, "maxAttempts": 3}
	}`)
	assertProblems(t, Validate(value, r), nil)
}

// An empty block reports every required leaf, nested ones included — the same
// answer MissingPaths gives, worded for a human.
func TestValidateEmptyBlockReportsEveryRequiredLeaf(t *testing.T) {
	r := firstReq(t)
	assertProblems(t, Validate(obj(t, `{}`), r), []string{
		"baseUrl is required",
		"projectKey is required",
		"qaStatus is required",
		"repro.test is required",
	})
}

// A nil map is a legal input (the package is total), and means the same as an
// empty one.
func TestValidateNilValueIsEmptyBlock(t *testing.T) {
	r := firstReq(t)
	if got, want := len(Validate(nil, r)), 4; got != want {
		t.Errorf("problems = %d, want %d", got, want)
	}
}

// A present-but-half-filled nested object is descended into: presence is not
// the question for an object that states its own requirements.
func TestValidateDescendsIntoPresentNestedObject(t *testing.T) {
	r := firstReq(t)
	value := obj(t, `{
		"baseUrl": "acme.example.net", "projectKey": "ABC", "qaStatus": "In QA",
		"repro": {"setup": "npm ci"}
	}`)
	assertProblems(t, Validate(value, r), []string{"repro.test is required"})
}

// A required object of the wrong shape earns both lines: the leaves it does not
// carry, and the shape itself. Either alone would leave the operator guessing.
func TestValidateRequiredObjectOfWrongType(t *testing.T) {
	r := firstReq(t)
	value := obj(t, `{
		"baseUrl": "a", "projectKey": "B", "qaStatus": "QA", "repro": "make test"
	}`)
	assertProblems(t, Validate(value, r), []string{
		"repro.test is required",
		"repro must be an object",
	})
}

// Blank and whitespace-only strings are unset, exactly as MissingPaths treats
// them — otherwise a write could be accepted and still leave needs-config up.
func TestValidateBlankAndNullRequiredStrings(t *testing.T) {
	r := firstReq(t)
	value := obj(t, `{
		"baseUrl": "", "projectKey": "   ", "qaStatus": null,
		"repro": {"test": "make test"}
	}`)
	assertProblems(t, Validate(value, r), []string{
		"baseUrl is required",
		"projectKey is required",
		"qaStatus is required",
	})
}

// The typo case additionalProperties:false exists for. Note it earns TWO lines:
// the field nobody declared, and the field it was meant to be.
func TestValidateUnknownFieldAtRoot(t *testing.T) {
	r := firstReq(t)
	value := obj(t, `{
		"baseUrl": "a", "projectKey": "B", "qastatus": "QA",
		"repro": {"test": "make test"}
	}`)
	assertProblems(t, Validate(value, r), []string{
		"qaStatus is required",
		"unknown field: qastatus",
	})
}

// The same guard one level down, where the schema declares it.
func TestValidateUnknownFieldNested(t *testing.T) {
	r := reqFor(t, `{
		"type": "object",
		"additionalProperties": false,
		"properties": {
			"repro": {
				"type": "object",
				"additionalProperties": false,
				"properties": {"test": {"type": "string"}},
				"required": ["test"]
			}
		},
		"required": ["repro"]
	}`)
	value := obj(t, `{"repro": {"test": "make test", "tset": "typo"}}`)
	assertProblems(t, Validate(value, r), []string{"unknown field: repro.tset"})
}

// Without the declaration, an undeclared field is the pack's business, not the
// daemon's: an open schema must not be tightened on the pack's behalf.
func TestValidateOpenSchemaAllowsExtraFields(t *testing.T) {
	for name, schema := range map[string]string{
		"absent":          `{"type":"object","properties":{"a":{"type":"string"}}}`,
		"explicitly true": `{"type":"object","additionalProperties":true,"properties":{"a":{"type":"string"}}}`,
		"an object": `{"type":"object","additionalProperties":{"type":"string"},
		              "properties":{"a":{"type":"string"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			assertProblems(t, Validate(obj(t, `{"a":"x","extra":"y"}`), reqFor(t, schema)), nil)
		})
	}
}

func TestValidateTypeMismatches(t *testing.T) {
	r := reqFor(t, `{
		"type": "object",
		"properties": {
			"name":  {"type": "string"},
			"count": {"type": "integer"},
			"nested": {"type": "object", "properties": {"a": {"type": "string"}}}
		}
	}`)
	for name, tc := range map[string]struct {
		value string
		want  []string
	}{
		"string given a number": {`{"name": 7}`, []string{"name must be a string"}},
		"string given a bool":   {`{"name": true}`, []string{"name must be a string"}},
		"integer given a float": {`{"count": 1.5}`, []string{"count must be an integer"}},
		"integer given a string": {
			`{"count": "5"}`, []string{"count must be an integer"},
		},
		"object given a list":       {`{"nested": []}`, []string{"nested must be an object"}},
		"whole float is an integer": {`{"count": 5.0}`, nil},
		"all correct": {
			`{"name": "x", "count": 5, "nested": {"a": "y"}}`, nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			assertProblems(t, Validate(obj(t, tc.value), r), tc.want)
		})
	}
}

// json.Number is the other shape a JSON integer arrives in; it must not read as
// "not an integer" merely because the caller kept its precision.
func TestValidateAcceptsJSONNumber(t *testing.T) {
	r := reqFor(t, `{"type":"object","properties":{"count":{"type":"integer","minimum":1}}}`)
	assertProblems(t, Validate(objNumbers(t, `{"count": 9007199254740993}`), r), nil)
	assertProblems(t, Validate(objNumbers(t, `{"count": 1.5}`), r),
		[]string{"count must be an integer"})
}

func TestValidateMinimum(t *testing.T) {
	r := reqFor(t, `{
		"type": "object",
		"properties": {"maxFiles": {"type": "integer", "minimum": 1}}
	}`)
	assertProblems(t, Validate(obj(t, `{"maxFiles": 0}`), r),
		[]string{"maxFiles must be at least 1"})
	assertProblems(t, Validate(obj(t, `{"maxFiles": -3}`), r),
		[]string{"maxFiles must be at least 1"})
	assertProblems(t, Validate(obj(t, `{"maxFiles": 1}`), r), nil)
	// minimum on a value that is not an integer at all reports the type, once.
	assertProblems(t, Validate(obj(t, `{"maxFiles": "many"}`), r),
		[]string{"maxFiles must be an integer"})
}

// A sub-schema that omits `type` but declares properties/required is still an
// object as far as its own requirements go — otherwise leaving `type` out would
// silently switch the nested required check off.
func TestValidateDescendsIntoUntypedSubSchema(t *testing.T) {
	r := reqFor(t, `{
		"type": "object",
		"properties": {
			"repro": {"properties": {"test": {"type": "string"}}, "required": ["test"]}
		},
		"required": ["repro"]
	}`)
	assertProblems(t, Validate(obj(t, `{"repro": {"setup": "npm ci"}}`), r),
		[]string{"repro.test is required"})
	assertProblems(t, Validate(obj(t, `{"repro": {"test": "make test"}}`), r), nil)
}

// A required property the schema never describes stays a leaf: presence is all
// the pack asked for.
func TestValidateUndescribedRequiredProperty(t *testing.T) {
	r := reqFor(t, `{"type": "object", "required": ["token"]}`)
	assertProblems(t, Validate(obj(t, `{}`), r), []string{"token is required"})
	assertProblems(t, Validate(obj(t, `{"token": "abc"}`), r), nil)
}

// A type this evaluator does not model constrains nothing — ignoring beats
// guessing, and a half-implemented spec rejects documents the pack meant to allow.
func TestValidateIgnoresUnmodelledConstructs(t *testing.T) {
	r := reqFor(t, `{
		"type": "object",
		"properties": {
			"tags":    {"type": "array", "items": {"type": "string"}},
			"enabled": {"type": "boolean"},
			"either":  {"type": ["string", "null"]},
			"ratio":   {"type": "number", "minimum": 0.5}
		}
	}`)
	assertProblems(t, Validate(obj(t,
		`{"tags": "not-a-list", "enabled": 3, "either": 7, "ratio": 0.1}`), r), nil)
}

// A pack that cannot state its constraints has not stated any. Inventing
// rejections from a fragment we failed to read would lock the operator out of a
// key the daemon itself offered them.
func TestValidateUnparseableSchemaAcceptsAnything(t *testing.T) {
	for name, schema := range map[string]string{
		"a bare true": `true`,
		"a string":    `"object"`,
		"a list":      `["object"]`,
	} {
		t.Run(name, func(t *testing.T) {
			assertProblems(t, Validate(obj(t, `{"anything": 1}`), reqFor(t, schema)), nil)
		})
	}
	// A nil schema is the degenerate case of the same thing.
	assertProblems(t, Validate(obj(t, `{"anything": 1}`), Requirement{Key: "block"}), nil)
}

// The contract that ties this function to the GET path: anything Validate
// accepts must clear MissingPaths too. A write the daemon accepts while the
// needs-config badge stays up would be worse than an outright rejection.
func TestValidateAcceptanceImpliesNoMissingPaths(t *testing.T) {
	r := firstReq(t)
	for name, body := range map[string]string{
		"minimal": `{"baseUrl":"a","projectKey":"B","qaStatus":"QA","repro":{"test":"make test"}}`,
		"full": `{"baseUrl":"a","projectKey":"B","qaStatus":"QA",
		          "repro":{"setup":"npm ci","test":"make test"},
		          "budget":{"maxFiles":5,"maxAttempts":3}}`,
	} {
		t.Run(name, func(t *testing.T) {
			value := obj(t, body)
			if p := Validate(value, r); len(p) != 0 {
				t.Fatalf("Validate rejected %s: %v", name, p)
			}
			if m := MissingPaths(value, r); len(m) != 0 {
				t.Errorf("Validate accepted a value MissingPaths still reports as unfilled: %v", m)
			}
		})
	}
}
