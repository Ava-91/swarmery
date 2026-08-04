// Package pluginreq reads a pack's requirements.json — the pack's own
// declaration of which .claude/project.json keys it needs — and evaluates a
// project's overlay against it.
//
// The daemon stays vendor-neutral by construction: nothing here knows what any
// particular key means. A pack declares a key and a JSON Schema fragment for it;
// this package only answers "which required leaves of that fragment does the
// project's overlay not fill in?". Adding a pack, or a key to an existing pack,
// therefore needs no daemon change at all.
//
// Every entry point is total: no function here returns an error. A pack with a
// broken declaration must degrade to "no requirements known" rather than take
// the plugins list down with it — the list is the operator's only view of what
// is installed, and it has to render even when a pack ships malformed JSON.
package pluginreq

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// FileName is the declaration file, read from a pack's root.
const FileName = "requirements.json"

// schemaVersion is the only requirements.json version this daemon understands.
// A pack declaring anything else is treated as undeclared: a future format we
// cannot read is not a format we may guess at.
const schemaVersion = 1

// Requirement is one declared .claude/project.json key.
//
// Schema stays json.RawMessage rather than a parsed type because it travels to
// the browser verbatim to drive a form. Re-marshalling a parsed copy would
// reorder properties and drop anything this daemon does not model, and the
// pack — not the daemon — owns that fragment's shape.
type Requirement struct {
	Key    string          `json:"key"`
	Title  string          `json:"title"`
	Why    string          `json:"why"`
	Docs   string          `json:"docs,omitempty"`
	Schema json.RawMessage `json:"schema"`
}

// declaration is the requirements.json envelope.
type declaration struct {
	Version       int           `json:"version"`
	ProjectConfig []Requirement `json:"projectConfig"`
}

// Read parses <packDir>/requirements.json.
//
// A pack without the file is the normal case, not an error — most packs declare
// nothing — so it returns (nil, ""). A malformed or wrong-version file returns
// (nil, reason): the reason names what is wrong for a log line, while the nil
// requirements keep the caller on its "this pack declares nothing" path.
//
// It never returns an error. See the package comment for why.
func Read(packDir string) (reqs []Requirement, reason string) {
	raw, err := os.ReadFile(filepath.Join(packDir, FileName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ""
		}
		// Permission denied, a directory where the file should be, an unreadable
		// mount: indistinguishable from absent as far as the caller can act, but
		// worth naming so it is not silently mistaken for "declares nothing".
		return nil, FileName + ": unreadable"
	}
	var d declaration
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, FileName + ": invalid JSON"
	}
	if d.Version != schemaVersion {
		return nil, fmt.Sprintf("%s: unsupported version %d", FileName, d.Version)
	}
	return d.ProjectConfig, ""
}

// ReadProjectConfig reads <projectPath>/.claude/project.json as a map of
// top-level key to RAW JSON value.
//
// Raw, not map[string]any, because the value round-trips to the browser as the
// prefill for an edit form: decoding and re-encoding would reorder keys and turn
// every integer into a float. Callers that need to inspect a value use Block.
//
// A missing, unreadable, or malformed file all yield nil — the same shape as an
// empty overlay, which reads downstream as "every requirement is unfilled".
// That is the honest answer: the daemon cannot see the config, so it cannot
// claim the config is satisfied. os.ReadFile follows symlinks, so the multi-repo
// overlay pattern (<project>/.claude symlinked into a shared agents repo)
// resolves without a special case.
func ReadProjectConfig(projectPath string) map[string]json.RawMessage {
	raw, err := os.ReadFile(filepath.Join(projectPath, ".claude", "project.json"))
	if err != nil {
		return nil
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil
	}
	return cfg
}

// Block looks up one top-level key from a ReadProjectConfig result, returning
// both its raw form (for prefill) and its decoded object form (for MissingPaths).
//
// An absent key, a JSON null, and a non-object value all decode to a nil obj,
// which MissingPaths reads as "nothing is filled in". raw is returned even when
// the value fails to decode as an object, so a caller can still show the
// operator what is actually in the file.
func Block(cfg map[string]json.RawMessage, key string) (raw json.RawMessage, obj map[string]any) {
	raw, ok := cfg[key]
	if !ok {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw, nil
	}
	return raw, obj
}

// schemaNode is the subset of JSON Schema this evaluator understands: what is
// required, and what the properties are so a required object can be descended
// into. Everything else in the fragment (types, defaults, descriptions,
// additionalProperties) is the browser form's business, not the daemon's.
type schemaNode struct {
	Required   []string                   `json:"required"`
	Properties map[string]json.RawMessage `json:"properties"`
}

// MissingPaths walks r.Schema and returns the dotted paths of every REQUIRED
// leaf that cfg does not satisfy, in schema order (e.g. ["qaStatus", "repro.test"]).
//
// cfg is the requirement's OWN block — the value of r.Key in project.json — not
// the whole overlay, so the returned paths are relative to that key. A nil cfg
// means the key is absent and every required leaf is reported.
//
// Recursion follows the schema: a required object that declares its own
// `required` is descended into, because for such an object "present" is not the
// question — "complete" is. A required object WITHOUT its own `required` is a
// leaf: its presence is all the pack asked for. Optional properties are never
// walked at all, so they can never appear in the result.
//
// A present-but-blank string counts as missing. The operator experience this
// serves is a form that pre-fills from project.json: a key sitting there as ""
// is a placeholder the operator has not filled in yet, and reporting it as
// satisfied would hide the one field they still have to type. Whitespace-only
// is treated the same — a status name of "   " fails an exact-match lookup just
// as an empty one does.
//
// An unparseable schema yields nil: a pack that cannot state its requirements
// has not stated any, and inventing missing paths from a fragment we failed to
// read would put the operator in front of a form we cannot render.
func MissingPaths(cfg map[string]any, r Requirement) []string {
	var root schemaNode
	if err := json.Unmarshal(r.Schema, &root); err != nil {
		return nil
	}
	var out []string
	walk(root, cfg, "", &out)
	return out
}

func walk(node schemaNode, val map[string]any, prefix string, out *[]string) {
	for _, key := range node.Required {
		path := prefix + key

		var child schemaNode
		if rawChild, ok := node.Properties[key]; ok {
			// A property the schema requires but does not describe stays a leaf:
			// child keeps its zero value, which has no Required, so the branch
			// below treats it as one.
			_ = json.Unmarshal(rawChild, &child)
		}

		v, present := val[key]
		if len(child.Required) > 0 {
			// Descend. A missing key, a null, or a non-object value (say
			// `repro: "make test"`) all hand the recursion a nil map, which
			// reports every nested required leaf — the right answer in each
			// case, since none of them holds the leaves the pack asked for.
			nested, _ := v.(map[string]any)
			walk(child, nested, path+".", out)
			continue
		}
		if !present || isBlank(v) {
			*out = append(*out, path)
		}
	}
}

// ── Validate ─────────────────────────────────────────────────────────────────

// validateNode is the subset of JSON Schema the write path enforces. Wider than
// schemaNode above because a write must be judged, not merely surveyed: a type,
// a minimum, and the additionalProperties gate all decide whether bytes reach
// the operator's overlay.
//
// Type and AdditionalProperties stay RawMessage because JSON Schema allows both
// a scalar and a richer form for each ("type": ["string","null"],
// "additionalProperties": {…}). Decoding into a bool/string would fail the whole
// node on a construct this evaluator merely wants to IGNORE.
type validateNode struct {
	Type                 json.RawMessage            `json:"type"`
	Required             []string                   `json:"required"`
	Properties           map[string]json.RawMessage `json:"properties"`
	AdditionalProperties json.RawMessage            `json:"additionalProperties"`
	Minimum              *float64                   `json:"minimum"`
}

// typeName returns the declared type when it is a plain string, else "".
func (n validateNode) typeName() string {
	var s string
	if json.Unmarshal(n.Type, &s) != nil {
		return ""
	}
	return s
}

// closed reports whether the node declares additionalProperties: false. Only
// the literal false closes an object; an object-valued additionalProperties is
// a construct this evaluator does not model, and treating it as closed would
// reject fields the pack deliberately allowed.
func (n validateNode) closed() bool {
	var b bool
	return json.Unmarshal(n.AdditionalProperties, &b) == nil && !b
}

// property parses the sub-schema at key. A key the schema requires but does not
// describe yields the zero node, which constrains nothing — the same "ignore
// rather than guess" rule the package applies everywhere else.
func (n validateNode) property(key string) (validateNode, bool) {
	raw, ok := n.Properties[key]
	if !ok {
		return validateNode{}, false
	}
	var child validateNode
	_ = json.Unmarshal(raw, &child)
	return child, true
}

// Validate checks a candidate value against the requirement's schema fragment
// and returns one human-readable line per problem, in a stable order: the
// missing required fields of a level first, then its present fields in
// alphabetical order (a map has no order of its own, and an operator staring at
// a rejected form deserves the same list twice in a row).
//
// Deliberately NOT a general JSON Schema implementation. It enforces exactly the
// constructs the pack contract allows — object/string/integer types, recursively
// required leaves, minimum on integers, additionalProperties:false — and ignores
// everything else rather than guessing at it. A daemon that half-implements a
// spec rejects valid documents, and the operator has no recourse when it does.
//
// additionalProperties:false is not pedantry here: the overlay schema already
// declares it (overlays/_schema/project.schema.json), and the likeliest operator
// error is a typo in a field name, which would otherwise be saved in silence and
// leave the pack reading a default it was never told about.
//
// Required is judged exactly as MissingPaths judges it, blank strings included,
// so a value this function accepts is one that actually clears the needs-config
// badge. An accepted write that leaves the badge up would be worse than a
// rejection.
//
// An empty slice means valid. An unparseable schema also means valid: a pack
// that cannot state its constraints has not stated any, and inventing rejections
// out of a fragment we failed to read would lock the operator out of a key the
// daemon itself offered them.
func Validate(value map[string]any, r Requirement) []string {
	var root validateNode
	if err := json.Unmarshal(r.Schema, &root); err != nil {
		return nil
	}
	var out []string
	check(root, value, "", &out)
	return out
}

func check(node validateNode, val map[string]any, prefix string, out *[]string) {
	// ── required ─────────────────────────────────────────────────────────────
	for _, key := range node.Required {
		path := prefix + key
		child, _ := node.property(key)
		v, present := val[key]

		if len(child.Required) > 0 {
			// A required object that states its own requirements is not answered
			// by mere presence — "complete" is the question. When it IS a usable
			// object the descent below covers it; when it is absent, null, or the
			// wrong shape, report its nested leaves here, because nothing else
			// will reach them.
			if _, ok := v.(map[string]any); ok && present {
				continue
			}
			check(child, nil, path+".", out)
			continue
		}
		if !present || isBlank(v) {
			*out = append(*out, path+" is required")
		}
	}

	// ── declared-ness, types, descent ────────────────────────────────────────
	for _, key := range sortedKeys(val) {
		path := prefix + key
		child, declared := node.property(key)
		if !declared {
			if node.closed() {
				*out = append(*out, "unknown field: "+path)
			}
			continue
		}
		v := val[key]
		if v == nil {
			// JSON null is how a document says "unset". The required check above
			// already speaks for it; adding "must be a string" on top would give
			// the operator two lines about one empty field.
			continue
		}
		switch child.typeName() {
		case "object":
			nested, ok := v.(map[string]any)
			if !ok {
				*out = append(*out, path+" must be an object")
				continue
			}
			check(child, nested, path+".", out)
		case "string":
			if _, ok := v.(string); !ok {
				*out = append(*out, path+" must be a string")
			}
		case "integer":
			n, ok := asInteger(v)
			if !ok {
				*out = append(*out, path+" must be an integer")
				continue
			}
			if child.Minimum != nil && n < *child.Minimum {
				*out = append(*out, fmt.Sprintf("%s must be at least %s", path, formatNumber(*child.Minimum)))
			}
		default:
			// No modelled type — but a sub-schema that declares properties or
			// required IS an object as far as its own requirements go. Descending
			// anyway keeps an omitted `type` from silently switching the nested
			// required check off, which is the one construct MissingPaths would
			// have caught and this function must not lose.
			if nested, ok := v.(map[string]any); ok && (len(child.Required) > 0 || len(child.Properties) > 0) {
				check(child, nested, path+".", out)
			}
		}
	}
}

// sortedKeys gives the present fields a deterministic walk order.
func sortedKeys(val map[string]any) []string {
	keys := make([]string, 0, len(val))
	for k := range val {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// asInteger accepts the two shapes a JSON number arrives in — float64 from a
// plain unmarshal, json.Number from a UseNumber decoder — and insists the value
// is whole. 5.0 is an integer; 5.5 is not.
func asInteger(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, t == math.Trunc(t)
	case json.Number:
		f, err := t.Float64()
		return f, err == nil && f == math.Trunc(f)
	}
	return 0, false
}

// formatNumber renders a schema bound without a trailing ".0" on whole values.
func formatNumber(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// isBlank reports whether a present value still counts as unset. Only null and
// blank strings qualify: a false bool and a 0 number are deliberate values a
// pack may legitimately want, and second-guessing them would make a filled-in
// field impossible to express.
func isBlank(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	}
	return false
}
