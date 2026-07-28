package docsfs

// Guards the contract between the UI's concept registry
// (web/src/lib/glossary.ts) and its long-form documentation (docs/concepts.md):
// every concept must have a doc section and every doc section must have a
// concept. This test lives here rather than in web/ because that package has no
// JavaScript test runner, and here because internal/docsfs is already excluded
// from the CI coverage gate — so a pure-text guard costs nothing in coverage.
//
// Both files it reads are COMMITTED (only internal/docsfs/content/ is
// gitignored), so the guard does not depend on `make copy-docs` having run.

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	glossaryPath = "../../web/src/lib/glossary.ts"
	conceptsPath = "../../docs/concepts.md"
	// conceptsSlug is the doc /docs/{slug} every concept must point at — the
	// basename of conceptsPath, which is how internal/api/docs.go slugs it.
	conceptsSlug = "concepts"
)

// `term: 'Handoff',` — the display name each concept declares. The quote after
// the colon is what keeps the interface's own `term: string;` out of the match.
var termRe = regexp.MustCompile(`(?m)^\s*term:\s*'([^']+)'`)

// `slug: 'concepts', anchor: 'handoff' }` — the two halves of a deep link.
var slugRe = regexp.MustCompile(`slug:\s*'([^']+)'`)
var anchorRe = regexp.MustCompile(`anchor:\s*'([^']+)'`)

// `## Handoff` — exactly two hashes: `###` fails the \s+ and is not a concept.
// Only ever applied to fenceStripped() output; see there for why.
var headingRe = regexp.MustCompile(`(?m)^##\s+(.+?)\s*$`)

// fenceStripped blanks the body of every ``` fenced block, keeping the fence
// lines themselves so line-anchored matching elsewhere stays aligned.
//
// Without this, a `## Stage:` line inside a playbook example — exactly the kind
// of snippet the Playbooks section invites — is read as a concept heading, and
// the suite fails with "documents "Stage: implement", which is not a concept"
// while pointing the reader at the wrong file entirely.
func fenceStripped(md string) string {
	lines := strings.Split(md, "\n")
	inFence := false
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
}

// slugify mirrors the heading-id function in web/src/lib/markdown.tsx.
func slugify(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteRune('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func readAll(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// matches returns capture group 1 of every match. Sorted so that a failing run
// lists its complaints in a stable order — the results feed t.Errorf loops, not
// just set membership, and flaky message ordering makes a diff of two failures
// unreadable.
func matches(re *regexp.Regexp, src string) []string {
	var out []string
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		out = append(out, m[1])
	}
	slices.Sort(out)
	return out
}

// headings returns the `## ` section titles of a markdown doc, ignoring
// anything inside a fenced code block.
func headings(t *testing.T, path string) []string {
	t.Helper()
	return matches(headingRe, fenceStripped(readAll(t, path)))
}

// TestGlossaryAndConceptsAgree fails in BOTH directions: a concept with no doc
// section, and a doc section with no concept.
func TestGlossaryAndConceptsAgree(t *testing.T) {
	terms := matches(termRe, readAll(t, glossaryPath))
	sections := headings(t, conceptsPath)

	if len(terms) == 0 {
		t.Fatalf("no concepts parsed from %s — the registry format changed", glossaryPath)
	}
	if len(sections) == 0 {
		t.Fatalf("no `## ` sections parsed from %s — the doc format changed", conceptsPath)
	}

	inHeadings := map[string]bool{}
	for _, h := range sections {
		inHeadings[h] = true
	}
	for _, term := range terms {
		if !inHeadings[term] {
			t.Errorf("concept %q has no `## %s` section in docs/concepts.md", term, term)
		}
	}

	inTerms := map[string]bool{}
	for _, term := range terms {
		inTerms[term] = true
	}
	for _, h := range sections {
		if !inTerms[h] {
			t.Errorf("docs/concepts.md documents %q, which is not a concept in glossary.ts", h)
		}
	}
}

// TestAnchorsMatchHeadingSlugs proves every "Read more →" link lands on a real
// heading id once markdown.tsx has slugified it.
func TestAnchorsMatchHeadingSlugs(t *testing.T) {
	anchors := matches(anchorRe, readAll(t, glossaryPath))
	sections := headings(t, conceptsPath)

	if len(anchors) == 0 {
		t.Fatalf("no doc anchors parsed from %s — the registry format changed", glossaryPath)
	}

	slugs := map[string]bool{}
	for _, h := range sections {
		slugs[slugify(h)] = true
	}
	for _, a := range anchors {
		if !slugs[a] {
			t.Errorf("anchor %q matches no heading slug in docs/concepts.md", a)
		}
	}
}

// TestDocSlugsPointAtConcepts guards the OTHER half of the deep link. An anchor
// can be perfect while `slug: 'concept'` (a typo) sends "Read more →" to
// /docs/concept — a 404 the anchor check would never notice.
func TestDocSlugsPointAtConcepts(t *testing.T) {
	slugs := matches(slugRe, readAll(t, glossaryPath))
	anchors := matches(anchorRe, readAll(t, glossaryPath))

	if len(slugs) == 0 {
		t.Fatalf("no doc slugs parsed from %s — the registry format changed", glossaryPath)
	}
	// Every `doc` literal carries both halves; a mismatch means one was dropped.
	if len(slugs) != len(anchors) {
		t.Errorf("registry has %d doc slugs but %d anchors — a doc literal is missing a half",
			len(slugs), len(anchors))
	}
	for _, s := range slugs {
		if s != conceptsSlug {
			t.Errorf("doc slug %q is not %q — /docs/%s is not a doc the daemon serves",
				s, conceptsSlug, s)
		}
	}
}
