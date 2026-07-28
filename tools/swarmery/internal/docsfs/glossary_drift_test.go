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
	"sort"
	"strings"
	"testing"
)

const (
	glossaryPath = "../../web/src/lib/glossary.ts"
	conceptsPath = "../../docs/concepts.md"
)

// `term: 'Handoff',` — the display name each concept declares. The quote after
// the colon is what keeps the interface's own `term: string;` out of the match.
var termRe = regexp.MustCompile(`(?m)^\s*term:\s*'([^']+)'`)

// `anchor: 'handoff' }` — the deep-link target each concept declares.
var anchorRe = regexp.MustCompile(`anchor:\s*'([^']+)'`)

// `## Handoff` — exactly two hashes: `###` fails the \s+ and is not a concept.
var headingRe = regexp.MustCompile(`(?m)^##\s+(.+?)\s*$`)

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

func matches(re *regexp.Regexp, src string) []string {
	out := []string{}
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		out = append(out, m[1])
	}
	sort.Strings(out)
	return out
}

// TestGlossaryAndConceptsAgree fails in BOTH directions: a concept with no doc
// section, and a doc section with no concept.
func TestGlossaryAndConceptsAgree(t *testing.T) {
	terms := matches(termRe, readAll(t, glossaryPath))
	headings := matches(headingRe, readAll(t, conceptsPath))

	if len(terms) == 0 {
		t.Fatalf("no concepts parsed from %s — the registry format changed", glossaryPath)
	}
	if len(headings) == 0 {
		t.Fatalf("no `## ` sections parsed from %s — the doc format changed", conceptsPath)
	}

	inHeadings := map[string]bool{}
	for _, h := range headings {
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
	for _, h := range headings {
		if !inTerms[h] {
			t.Errorf("docs/concepts.md documents %q, which is not a concept in glossary.ts", h)
		}
	}
}

// TestAnchorsMatchHeadingSlugs proves every "Read more →" link lands on a real
// heading id once markdown.tsx has slugified it.
func TestAnchorsMatchHeadingSlugs(t *testing.T) {
	anchors := matches(anchorRe, readAll(t, glossaryPath))
	headings := matches(headingRe, readAll(t, conceptsPath))

	if len(anchors) == 0 {
		t.Fatalf("no doc anchors parsed from %s — the registry format changed", glossaryPath)
	}

	slugs := map[string]bool{}
	for _, h := range headings {
		slugs[slugify(h)] = true
	}
	for _, a := range anchors {
		if !slugs[a] {
			t.Errorf("anchor %q matches no heading slug in docs/concepts.md", a)
		}
	}
}
