package pluginreq

// The optional `probe` block of a requirements.json entry.
//
// A declared key is only half the operator's problem. The other half is that the
// obvious value is often the wrong one — a status name that reads right in the
// form does not exist on the board, and the only way to learn the real one is to
// ask the system that owns it. The daemon may not ask: it holds no credentials
// for anything a pack integrates with, and it is not going to start. So the pack
// ships a PROMPT instead, and the daemon hands that prompt to a `claude` session
// that already has the operator's live connectors. The daemon learns nothing
// about the domain and gains no client, no token, and no new dependency.
//
// Everything here is parsed defensively for the same reason the rest of the
// package is: a pack with a broken probe block must keep its declaration — the
// operator loses the suggestions, not the config form.

import (
	"encoding/json"
	"strings"
	"time"
)

// Probe timeout bounds.
//
// The default is generous because the run is a live agent session doing a
// handful of reads, not a local computation. The ceiling exists because the
// request is synchronous and an operator is sitting in front of the modal: a
// pack must not be able to hold that modal open for an hour by declaring
// timeoutSeconds: 100000.
const (
	DefaultProbeTimeout = 180 * time.Second
	MaxProbeTimeout     = 300 * time.Second
)

// maxSuggestionsPerField caps what one field may offer. A datalist is a
// convenience, not a catalogue: past a few dozen entries the operator is
// scrolling rather than recognising, and an agent that misreads its instructions
// and dumps every issue on the board must not be able to push megabytes through
// a modal.
const maxSuggestionsPerField = 50

// ProbeSpec is a pack's declaration of how to discover live values for some of
// its own fields.
//
// Prompt is the whole of the daemon's domain knowledge: it is passed through
// verbatim, with only the operator's partial value appended as JSON. Nothing in
// this daemon parses it, and nothing here knows what any Field means.
type ProbeSpec struct {
	// Needs are the dotted paths that must be filled before the probe can run
	// at all — its inputs. Probing for a project's statuses without knowing the
	// project is not a degraded run, it is a meaningless one.
	Needs []string `json:"needs"`
	// Fields are the dotted paths the probe may suggest values for. It is a
	// whitelist, not a hint: anything the agent returns outside this list is
	// discarded, so a wandering session cannot put a value in front of the
	// operator for a field the pack never nominated.
	Fields []string `json:"fields"`
	// TimeoutSeconds is clamped by Timeout; 0 means "use the default".
	TimeoutSeconds int `json:"timeoutSeconds"`
	// Prompt is the self-contained instruction handed to the agent session.
	Prompt string `json:"prompt"`
}

// parseProbe decodes a requirements.json `probe` block.
//
// It returns nil for every kind of problem — absent, malformed, or declared
// without the two things that make a probe runnable (a prompt to run, and at
// least one field it is allowed to answer for). nil is not an error state: it is
// exactly the state of the overwhelming majority of packs, which declare no
// probe at all, and the caller has one code path for both.
func parseProbe(raw json.RawMessage) *ProbeSpec {
	if len(raw) == 0 {
		return nil
	}
	var spec ProbeSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil
	}
	if strings.TrimSpace(spec.Prompt) == "" {
		return nil
	}
	if len(spec.Fields) == 0 {
		return nil
	}
	return &spec
}

// Timeout is the declared timeout, clamped into [1s, MaxProbeTimeout], with a
// non-positive declaration meaning the default.
//
// Clamped rather than rejected: a pack that overstates its timeout still has a
// working probe, it just does not get to decide how long the operator waits.
func (p ProbeSpec) Timeout() time.Duration {
	if p.TimeoutSeconds <= 0 {
		return DefaultProbeTimeout
	}
	d := time.Duration(p.TimeoutSeconds) * time.Second
	if d > MaxProbeTimeout {
		return MaxProbeTimeout
	}
	return d
}

// MissingNeeds returns the declared Needs paths that value does not fill, in
// declaration order.
//
// Blank counts as missing, exactly as MissingPaths and Validate judge it — a
// baseUrl of "" is a placeholder the operator has not typed yet, and running a
// three-minute agent session against it would spend the wait to learn nothing.
func (p ProbeSpec) MissingNeeds(value map[string]any) []string {
	var out []string
	for _, path := range p.Needs {
		if v, ok := lookupPath(value, path); !ok || isBlank(v) {
			out = append(out, path)
		}
	}
	return out
}

// lookupPath resolves a dotted path against a decoded JSON object. A segment
// that is not an object stops the walk: `repro.test` asked of `repro: "x"` is
// absent, not an error.
func lookupPath(value map[string]any, path string) (any, bool) {
	// Split never yields an empty slice, so the last segment always exists and
	// the walk below is over the parents only.
	segments := strings.Split(path, ".")
	cur := value
	for _, seg := range segments[:len(segments)-1] {
		next, ok := cur[seg].(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	v, ok := cur[segments[len(segments)-1]]
	return v, ok
}

// Trim reduces an agent's raw suggestions to what the pack actually declared:
// only Fields, at most maxSuggestionsPerField values each, no blanks.
//
// The whitelist is the security-relevant half — see Fields. The blank filter is
// the honest half: this package treats a blank string as an unfilled field
// everywhere else, so offering one as a suggestion would put a value in the
// datalist that the very next save rejects as missing.
//
// The result is always non-nil. The endpoint's contract is that `suggestions` is
// an object in every response, including the ones where nothing was found, so
// the browser never has to distinguish "no suggestions" from "no probe".
func (p ProbeSpec) Trim(raw map[string][]string) map[string][]string {
	out := make(map[string][]string, len(p.Fields))
	for _, field := range p.Fields {
		values, ok := raw[field]
		if !ok {
			continue
		}
		kept := make([]string, 0, len(values))
		for _, v := range values {
			if strings.TrimSpace(v) == "" {
				continue
			}
			kept = append(kept, v)
			if len(kept) == maxSuggestionsPerField {
				break
			}
		}
		if len(kept) > 0 {
			out[field] = kept
		}
	}
	return out
}
