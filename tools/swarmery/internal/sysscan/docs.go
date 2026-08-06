package sysscan

// Usage-guide parsing — the `# How to use` block every registrable item may
// carry. The normative contract is docs/system-docs-format.md; this file is
// its Go implementation and every rule below cites the section it comes from.
// Where system-config-format.md answers "what is an item", the docs format
// answers "is that item documented".
//
// Tolerant by contract, exactly like the rest of the package (sysscan.go:9-12):
// a malformed `docs:` value, a missing block, CRLF line endings, or a file that
// is not an item degrade to an empty/partial Docs — never to an error, never to
// a panic. Four consumers share this one parse: the scanner, the linter
// (phase 3), the generator, and the CI coverage gate.

import (
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Docs is one parsed "# How to use" block (docs/system-docs-format.md).
type Docs struct {
	// Present reports that a `# How to use` H1 was found (§1.1).
	Present bool
	// Duplicate reports a second matching H1 (§5.4) — a violation, not a
	// merge: the FIRST block wins and the second stays ordinary body text.
	Duplicate bool
	// Markdown is the block body with the heading line excluded, trimmed.
	Markdown string
	// Sections holds the `##` subsection titles found inside the block, in
	// document order, verbatim (§1.3). Never nil.
	Sections []string
	// Missing holds the RequiredDocSections that are absent or whose body is
	// under the minimum rune count (§2), in RequiredDocSections order. Never
	// nil. Empty when Present is false: "no guide at all" is a distinct
	// finding from "guide with gaps", and reporting all four here would make
	// phase 3 double-report the same undocumented item.
	Missing []string
	// Status is frontmatter docs.status (§3): generated | reviewed | "" when
	// absent. An unknown value is kept verbatim for the linter to report.
	Status string
	// SourceSHA is frontmatter docs.source_sha (§3) — the fingerprint recorded
	// when the guide was last written.
	SourceSHA string
	// ComputedSHA is SourceSHA() over the current body (§4).
	ComputedSHA string
	// Stale reports that the item changed since its guide was written:
	// SourceSHA is well-formed and differs from ComputedSHA (§4). A malformed
	// or absent SourceSHA is *unknown* staleness, not stale.
	Stale bool
}

// RequiredDocSections is the coverage gate's fixed set — the 4 of the 8
// subsections in §2 that gate. The other four (When not to use it, Inputs,
// What you get back, Related) are recommended and never gate.
var RequiredDocSections = []string{
	"What it does", "When to use it", "How to invoke", "Worked example",
}

// Threshold default and its env override, same precedence and naming as the
// lint thresholds in lint.go (explicit argument > env > default).
const (
	// DefaultMinDocsSection is the minimum body length of a REQUIRED
	// subsection, in runes (§2 — runes, not bytes, the same unit
	// skill_short_description counts in). A subsection under it counts as
	// absent.
	DefaultMinDocsSection = 40

	// EnvMinDocsSection overrides DefaultMinDocsSection.
	EnvMinDocsSection = "SWARMERY_LINT_MIN_DOCS_SECTION"
)

// MinDocsSection resolves the required-subsection minimum from the env
// override, falling back to the default — the same envInt idiom the lint
// thresholds use (lint.go:70).
func MinDocsSection() int { return envInt(EnvMinDocsSection, DefaultMinDocsSection) }

// howToUseHeading is the only H1 text that opens the block (§5.3: matched
// after trimming and lowercasing; rewording does not match).
const howToUseHeading = "how to use"

// ParseDocs extracts the usage guide from a component file's raw content.
//
// minSection is the required-subsection minimum in runes; a non-positive value
// resolves via MinDocsSection(). Content that is not a registrable item — no
// leading `---`, or unterminated frontmatter (§5.5) — yields the zero Docs:
// a guide in a file that is not an item is invisible.
func ParseDocs(content []byte, minSection int) Docs {
	d := Docs{Sections: []string{}, Missing: []string{}}
	if minSection <= 0 {
		minSection = MinDocsSection()
	}

	// §5.5: only files that start frontmatter are items. splitFrontmatter also
	// strips the UTF-8 BOM and rejects an unterminated block.
	block, body, err := splitFrontmatter(content)
	if err != nil {
		return d
	}

	raw, text, fenced := docsScanLines(string(body))
	start, end, hits := howToUseExtent(text, fenced)
	d.ComputedSHA = sourceSHAFromLines(raw, start, end)

	// Provenance is read even when the block is absent: `docs:` without a
	// guide is itself a finding phase 3 reports (§3).
	d.Status, d.SourceSHA = docsProvenance(block)
	// §4: staleness needs a well-formed fingerprint. A deliberately wrong but
	// well-formed value (the 000000000000 fixture) is stale; a malformed one
	// is unknown, never gating.
	d.Stale = isSourceSHA(d.SourceSHA) && d.SourceSHA != d.ComputedSHA

	if hits == 0 {
		return d
	}
	d.Present = true
	d.Duplicate = hits > 1
	// Raw lines, not CR-trimmed ones: what the reader wrote is what the reader
	// gets back. This is the block BODY, below the heading; the shell prelude's
	// `existing_block` keeps the heading line, so the two are not comparable.
	d.Markdown = strings.TrimSpace(strings.Join(raw[start+1:end], "\n"))

	secs := docsSubsections(raw, text, fenced, start+1, end)
	for _, s := range secs {
		d.Sections = append(d.Sections, s.title)
	}
	for _, want := range RequiredDocSections {
		if !docsSectionSatisfied(secs, want, minSection) {
			d.Missing = append(d.Missing, want)
		}
	}
	return d
}

// SourceSHA is the §4 fingerprint of an item body: CRLF normalized, the
// `# How to use` block deleted, every line right-trimmed of spaces and tabs,
// joined with LF, trailing newlines trimmed to exactly one, SHA-256, first 12
// hex characters.
//
// Frontmatter is deliberately not part of the input, so writing source_sha
// back cannot change the value it records; the guide is removed before hashing,
// so rewriting the guide never marks the item stale. Only a change to the
// item's real content does.
func SourceSHA(body []byte) string {
	raw, text, fenced := docsScanLines(string(body))
	start, end, _ := howToUseExtent(text, fenced)
	return sourceSHAFromLines(raw, start, end)
}

// sourceSHAFromLines is SourceSHA over an already-scanned body. start < 0 means
// there is no block to delete; otherwise lines[start:end] are dropped.
func sourceSHAFromLines(lines []string, start, end int) string {
	kept := lines
	if start >= 0 {
		kept = make([]string, 0, len(lines))
		kept = append(kept, lines[:start]...)
		kept = append(kept, lines[end:]...)
	}
	trimmed := make([]string, len(kept))
	for i, ln := range kept {
		trimmed[i] = strings.TrimRight(ln, " \t")
	}
	s := strings.TrimRight(strings.Join(trimmed, "\n"), "\n")
	if s != "" {
		s += "\n"
	}
	return sha256Hex([]byte(s))[:12]
}

// docsScanLines splits a body into lines (CRLF normalized away, §5.2) and, in
// the same pass, marks which lines sit inside a fenced region (§5.1). A fence
// delimiter line is itself marked fenced so it can never be read as a heading.
//
// It returns TWO views of every line, and keeping them apart is load-bearing:
//
//	raw  — the line exactly as it stands after the §4 CRLF→LF replacement. This
//	       is what gets HASHED. It is deliberately not stripped of a lone or
//	       doubled `\r`: §4 removes CRLF pairs and then trims spaces and tabs
//	       and nothing else, so a bare CR the author typed is item content and
//	       must move the fingerprint.
//	text — raw with ONE trailing `\r` removed (§5.2), used for heading and fence
//	       COMPARISON only, so a CRLF file's headings match an LF file's.
//
// The shell prelude (scripts/docgen/lib.sh, `scanLines`) has carried this same
// raw/text split from the start. Collapsing the two here — trimming `\r` off
// the line that was then hashed — is exactly how the Go and JS fingerprints
// drifted apart on `alpha\r\r\nbeta\n`; docs_parity_test.go now pins them.
func docsScanLines(s string) (raw, text []string, fenced []bool) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	raw = strings.Split(s, "\n")
	text = make([]string, len(raw))
	fenced = make([]bool, len(raw))
	var open docsFence
	inFence := false
	for i, ln := range raw {
		t := strings.TrimSuffix(ln, "\r")
		text[i] = t
		f, isFence := parseDocsFence(t)
		if inFence {
			// §5.1: a region closes only on a delimiter of the SAME character,
			// at least as long as the opener, carrying no info string. A blind
			// toggle would let ``` close ````, ~~~ close ```, and ```js close
			// ```js — each one silently moving the block extent AND the §4
			// fingerprint.
			if isFence && f.char == open.char && f.n >= open.n && !f.info {
				inFence = false
			}
			fenced[i] = true
			continue
		}
		if isFence {
			open, inFence = f, true
			fenced[i] = true
			continue
		}
		fenced[i] = false
	}
	return raw, text, fenced
}

// docsFence describes one fence delimiter line (§5.1).
type docsFence struct {
	char byte // '`' or '~' — the run character
	n    int  // run length, always >= 3
	info bool // a non-blank info string follows the run
}

// parseDocsFence reports whether line is a fence delimiter: a run of at least
// three backticks or tildes at column 0, optionally followed by an info string
// (§5.1). Column 0 is normative — an indented fence is not a delimiter, and
// neither is an indented `#` a heading, so the two stay consistent.
func parseDocsFence(line string) (docsFence, bool) {
	if line == "" {
		return docsFence{}, false
	}
	c := line[0]
	if c != '`' && c != '~' {
		return docsFence{}, false
	}
	n := 1
	for n < len(line) && line[n] == c {
		n++
	}
	if n < 3 {
		return docsFence{}, false
	}
	return docsFence{char: c, n: n, info: strings.TrimSpace(line[n:]) != ""}, true
}

// atxHeading returns the trimmed text of an ATX heading of exactly `level`
// hashes at column 0, followed by at least one space or tab (§1.1).
func atxHeading(line string, level int) (string, bool) {
	if len(line) <= level {
		return "", false
	}
	for i := 0; i < level; i++ {
		if line[i] != '#' {
			return "", false
		}
	}
	if line[level] != ' ' && line[level] != '\t' {
		return "", false
	}
	return strings.TrimSpace(line[level:]), true
}

// howToUseExtent locates the block: start is its heading line, end is the line
// after its last (the next H1 outside a fence, or EOF, §1.3). hits counts every
// matching H1 in the body — more than one is the §5.4 duplicate violation.
// start is -1 and end is len(text) when there is no block.
//
// text is the CR-trimmed view from docsScanLines; the returned indices address
// the raw view just as well, since both slices share one indexing.
func howToUseExtent(text []string, fenced []bool) (start, end, hits int) {
	start, end = -1, len(text)
	for i, ln := range text {
		if fenced[i] {
			continue
		}
		h, ok := atxHeading(ln, 1)
		if !ok {
			continue
		}
		if strings.EqualFold(h, howToUseHeading) {
			hits++
			if start < 0 {
				start = i
			}
			continue
		}
		// Any other H1 after the opening heading closes the block.
		if start >= 0 && end == len(text) {
			end = i
		}
	}
	// A second `# How to use` also closes the first block (§5.4: never merge).
	if start >= 0 && hits > 1 {
		for i := start + 1; i < end; i++ {
			if fenced[i] {
				continue
			}
			if h, ok := atxHeading(text[i], 1); ok && strings.EqualFold(h, howToUseHeading) {
				end = i
				break
			}
		}
	}
	return start, end, hits
}

// docsSubsection is one `##` subsection of the block with its body text.
type docsSubsection struct {
	title string
	body  string
}

// docsSubsections collects the `##` headings in [from, to) outside fences, in
// document order, each with the text up to the next `##` or the block end
// (§1.3: `###` and deeper are body content, never subsections).
//
// Titles are matched on the CR-trimmed view (§5.2) but bodies are collected
// from the RAW one, so the §2 rune count sees the same bytes the shell prelude
// counts (lib.sh `guideSubsections` pushes `e.raw`). Otherwise a stray CR could
// put one implementation on either side of the 40-rune floor.
func docsSubsections(raw, text []string, fenced []bool, from, to int) []docsSubsection {
	var out []docsSubsection
	starts := make([]int, 0, 8)
	for i := from; i < to; i++ {
		if fenced[i] {
			continue
		}
		title, ok := atxHeading(text[i], 2)
		if !ok {
			continue
		}
		out = append(out, docsSubsection{title: title})
		starts = append(starts, i)
	}
	for n, s := range starts {
		bodyEnd := to
		if n+1 < len(starts) {
			bodyEnd = starts[n+1]
		}
		out[n].body = strings.TrimSpace(strings.Join(raw[s+1:bodyEnd], "\n"))
	}
	return out
}

// docsSectionSatisfied reports whether want is present with at least
// minSection runes of body (§2 — heading-only or whitespace-only counts as
// absent). Titles match case-insensitively; the first occurrence wins.
func docsSectionSatisfied(secs []docsSubsection, want string, minSection int) bool {
	for _, s := range secs {
		if strings.EqualFold(s.title, want) {
			return utf8.RuneCountInString(s.body) >= minSection
		}
	}
	return false
}

// docsProvenance extracts docs.status and docs.source_sha from the raw
// frontmatter block as VERBATIM scalar text (§3).
//
// It walks a yaml.Node instead of the map[string]any parseFrontmatter returns,
// and that is load-bearing, not style: YAML resolves an unquoted all-digit
// fingerprint to a number, so `source_sha: 000000000000` — exactly what the
// stale fixture carries — arrives as int 0 and renders back as "0", losing the
// 12 characters the author wrote. Roughly one fingerprint in 280 is all-digits,
// so this is a real corpus case, not a fixture quirk. A node keeps the scalar
// as written, which is also what §3 means by storing an unknown status
// verbatim.
//
// A missing `docs:` key, or a value that is not a mapping (a bare string, a
// list), yields empty strings — unknown provenance for the linter to report,
// never an error here.
func docsProvenance(block []byte) (status, sourceSHA string) {
	var root yaml.Node
	if err := yaml.Unmarshal(block, &root); err != nil || len(root.Content) == 0 {
		return "", ""
	}
	docs := yamlMapValue(root.Content[0], "docs")
	if docs == nil || docs.Kind != yaml.MappingNode {
		return "", ""
	}
	return yamlScalar(yamlMapValue(docs, "status")), yamlScalar(yamlMapValue(docs, "source_sha"))
}

// yamlMapValue returns the value node for key in a mapping node, or nil.
func yamlMapValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// yamlScalar is a scalar node's text exactly as written, trimmed. A missing
// node, a null, or a nested collection yields "".
func yamlScalar(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
		return ""
	}
	return strings.TrimSpace(n.Value)
}

// isSourceSHA reports whether s is a well-formed §4 fingerprint: exactly 12
// lowercase hex characters. Anything else means staleness is unknown.
func isSourceSHA(s string) bool {
	if len(s) != 12 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
