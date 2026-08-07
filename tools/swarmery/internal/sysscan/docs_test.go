package sysscan

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The three committed fixtures that pin docs/system-docs-format.md. Their
// source_sha values were computed independently when the contract was frozen
// (phase 1), so asserting against them cross-checks this implementation of §4
// rather than merely re-asserting whatever it happens to produce.
const (
	fixtureDocumentedAgent = "claude/agents/documented-agent.md"
	fixtureStaleAgent      = "claude/agents/stale-docs-agent.md"
	fixtureDocumentedSkill = "claude/skills/documented-skill/SKILL.md"

	shaDocumentedAgent = "bf1f17459cf5" // matches its recorded source_sha
	shaStaleAgent      = "ff5722f9923d" // its recorded source_sha is deliberately 000000000000
	shaDocumentedSkill = "7031e8347e4e" // matches its recorded source_sha
)

func readFixture(t *testing.T, rel string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtureRoot, rel))
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	return raw
}

func TestParseDocs(t *testing.T) {
	t.Run("complete_block", func(t *testing.T) {
		d := ParseDocs(readFixture(t, fixtureDocumentedAgent), DefaultMinDocsSection)
		if !d.Present || d.Duplicate {
			t.Fatalf("present=%v duplicate=%v, want true/false", d.Present, d.Duplicate)
		}
		want := []string{
			"What it does", "When to use it", "When not to use it", "How to invoke",
			"Inputs", "What you get back", "Worked example", "Related",
		}
		if !reflect.DeepEqual(d.Sections, want) {
			t.Errorf("sections = %q, want %q", d.Sections, want)
		}
		if len(d.Missing) != 0 {
			t.Errorf("missing = %q, want none", d.Missing)
		}
		if d.Status != "reviewed" {
			t.Errorf("status = %q, want reviewed", d.Status)
		}
		// §4: the recorded fingerprint must reproduce, and a matching one is
		// never stale.
		if d.ComputedSHA != shaDocumentedAgent || d.SourceSHA != shaDocumentedAgent {
			t.Errorf("computed=%q source=%q, want both %q", d.ComputedSHA, d.SourceSHA, shaDocumentedAgent)
		}
		if d.Stale {
			t.Error("stale = true for a fingerprint that matches")
		}
		// The heading line is excluded; the body starts at the first subsection.
		if strings.Contains(d.Markdown, "# How to use") {
			t.Errorf("markdown must exclude the heading line, got %.30q", d.Markdown)
		}
		if !strings.HasPrefix(d.Markdown, "## What it does") {
			t.Errorf("markdown = %.30q, want it to start at the first subsection", d.Markdown)
		}
	})

	t.Run("missing_required_section", func(t *testing.T) {
		// The skill fixture omits ## Worked example and nothing else (§2).
		d := ParseDocs(readFixture(t, fixtureDocumentedSkill), DefaultMinDocsSection)
		if !d.Present {
			t.Fatal("present = false, want true")
		}
		if want := []string{"Worked example"}; !reflect.DeepEqual(d.Missing, want) {
			t.Errorf("missing = %q, want %q", d.Missing, want)
		}
		if d.ComputedSHA != shaDocumentedSkill {
			t.Errorf("computed sha = %q, want %q", d.ComputedSHA, shaDocumentedSkill)
		}
	})

	t.Run("fenced_comment_does_not_terminate_block", func(t *testing.T) {
		// The phase-1 trap: the skill's ## How to invoke holds a bash fence whose
		// first line is `# deploy the thing` (§5.1). Without fence tracking the
		// block truncates there and everything after it disappears.
		d := ParseDocs(readFixture(t, fixtureDocumentedSkill), DefaultMinDocsSection)
		want := []string{
			"What it does", "When to use it", "When not to use it", "How to invoke",
			"Inputs", "What you get back", "Related",
		}
		if !reflect.DeepEqual(d.Sections, want) {
			t.Fatalf("sections = %q, want %q — the fenced `# deploy the thing` line truncated the block", d.Sections, want)
		}
		if !strings.Contains(d.Markdown, "# deploy the thing") {
			t.Error("markdown lost the fenced comment line")
		}
		// Exactly one required subsection is absent — not three, which is what a
		// fence-blind scan reports.
		if len(d.Missing) != 1 {
			t.Errorf("missing = %q, want exactly [Worked example]", d.Missing)
		}
	})

	t.Run("fenced_how_to_use_is_not_a_heading", func(t *testing.T) {
		// The opening direction of §5.1: a `# How to use` inside a fence is a
		// comment in a code sample, not the block.
		content := []byte("---\nname: x\n---\n\n# Role\n\nShows the guide markup:\n\n```markdown\n# How to use\n\n## What it does\nnope\n```\n")
		d := ParseDocs(content, DefaultMinDocsSection)
		if d.Present {
			t.Errorf("present = true, want false — a fenced heading must not open the block (sections=%q)", d.Sections)
		}
		if len(d.Sections) != 0 || len(d.Missing) != 0 {
			t.Errorf("sections=%q missing=%q, want both empty", d.Sections, d.Missing)
		}
	})

	t.Run("stale_provenance", func(t *testing.T) {
		d := ParseDocs(readFixture(t, fixtureStaleAgent), DefaultMinDocsSection)
		if !d.Present || len(d.Missing) != 0 {
			t.Fatalf("present=%v missing=%q, want true/none", d.Present, d.Missing)
		}
		if want := []string{"What it does", "When to use it", "How to invoke", "Worked example"}; !reflect.DeepEqual(d.Sections, want) {
			t.Errorf("sections = %q, want the 4 required only %q", d.Sections, want)
		}
		if d.Status != "generated" {
			t.Errorf("status = %q, want generated", d.Status)
		}
		if d.SourceSHA != "000000000000" {
			t.Errorf("source sha = %q, want the deliberately wrong 000000000000", d.SourceSHA)
		}
		if d.ComputedSHA != shaStaleAgent {
			t.Errorf("computed sha = %q, want %q", d.ComputedSHA, shaStaleAgent)
		}
		// §4: a well-formed but wrong fingerprint is stale, not a parse error.
		if !d.Stale {
			t.Error("stale = false, want true")
		}
	})

	t.Run("required_section_under_minimum", func(t *testing.T) {
		// §2: a required subsection under the rune floor counts as absent.
		content := []byte(docsFixture(map[string]string{
			"What it does":   "Prices a batch of order line items and returns the total for the order.",
			"When to use it": "- A caller sent line items and wants one priced order back in return.",
			"How to invoke":  "Call it with the order path; everything else is read from the document.",
			"Worked example": "too short",
		}))
		d := ParseDocs(content, DefaultMinDocsSection)
		if !d.Present {
			t.Fatal("present = false, want true")
		}
		if want := []string{"Worked example"}; !reflect.DeepEqual(d.Missing, want) {
			t.Errorf("missing = %q, want %q", d.Missing, want)
		}
		// The section is still listed — it exists, it is just too thin.
		if len(d.Sections) != 4 {
			t.Errorf("sections = %q, want all four listed", d.Sections)
		}
		// A lower floor accepts it (the env override drives this in phase 3).
		if got := ParseDocs(content, 5); len(got.Missing) != 0 {
			t.Errorf("missing with minSection=5 = %q, want none", got.Missing)
		}
	})

	t.Run("heading_only_section_counts_absent", func(t *testing.T) {
		content := []byte(docsFixture(map[string]string{
			"What it does":   "Prices a batch of order line items and returns the total for the order.",
			"When to use it": "- A caller sent line items and wants one priced order back in return.",
			"How to invoke":  "Call it with the order path; everything else is read from the document.",
			"Worked example": "   ",
		}))
		d := ParseDocs(content, DefaultMinDocsSection)
		if want := []string{"Worked example"}; !reflect.DeepEqual(d.Missing, want) {
			t.Errorf("missing = %q, want %q", d.Missing, want)
		}
	})

	t.Run("duplicate_block", func(t *testing.T) {
		// §5.4: the FIRST block wins, the second is ordinary body content and
		// must never contribute coverage.
		content := []byte("---\nname: x\n---\n\n# How to use\n\n## What it does\n" +
			strings.Repeat("a", 60) + "\n\n# How to use\n\n## Worked example\n" +
			strings.Repeat("b", 60) + "\n")
		d := ParseDocs(content, DefaultMinDocsSection)
		if !d.Present || !d.Duplicate {
			t.Fatalf("present=%v duplicate=%v, want true/true", d.Present, d.Duplicate)
		}
		if want := []string{"What it does"}; !reflect.DeepEqual(d.Sections, want) {
			t.Errorf("sections = %q, want only the first block's %q", d.Sections, want)
		}
		// The merge that must not happen would have satisfied Worked example.
		for _, m := range d.Missing {
			if m == "Worked example" {
				return
			}
		}
		t.Errorf("missing = %q, want it to still include Worked example (no merge)", d.Missing)
	})

	t.Run("no_block", func(t *testing.T) {
		d := ParseDocs([]byte("---\nname: x\n---\n\n# Role\n\nNo guide here.\n"), DefaultMinDocsSection)
		if d.Present || d.Duplicate {
			t.Errorf("present=%v duplicate=%v, want false/false", d.Present, d.Duplicate)
		}
		// Empty, never nil, and never a pre-filled list of all four required
		// sections: "no guide" is phase 3's own finding, not four coverage gaps.
		if d.Sections == nil || len(d.Sections) != 0 {
			t.Errorf("sections = %#v, want empty non-nil", d.Sections)
		}
		if d.Missing == nil || len(d.Missing) != 0 {
			t.Errorf("missing = %#v, want empty non-nil", d.Missing)
		}
		if d.ComputedSHA == "" {
			t.Error("computed sha empty — it is defined for an undocumented item too")
		}
	})

	t.Run("crlf_parses_identically", func(t *testing.T) {
		raw := readFixture(t, fixtureDocumentedAgent)
		lf := ParseDocs(raw, DefaultMinDocsSection)
		crlf := ParseDocs([]byte(strings.ReplaceAll(string(raw), "\n", "\r\n")), DefaultMinDocsSection)
		if !reflect.DeepEqual(lf, crlf) {
			t.Errorf("CRLF parse differs:\n LF  = %+v\n CRLF= %+v", lf, crlf)
		}
		// §4 explicitly: line endings never move the fingerprint.
		if crlf.ComputedSHA != shaDocumentedAgent {
			t.Errorf("CRLF computed sha = %q, want %q", crlf.ComputedSHA, shaDocumentedAgent)
		}
	})

	t.Run("lone_cr_is_content_not_a_line_ending", func(t *testing.T) {
		// The case the clean `\n`→`\r\n` conversion above cannot reach. §4
		// replaces CRLF *pairs* and then trims spaces and tabs — and nothing
		// else — so a BARE `\r` is a byte of item content and must move the
		// fingerprint. Stripping it off the line before hashing is precisely how
		// this implementation drifted from the shell one (docs_parity_test.go).
		sha := func(body string) string {
			return ParseDocs([]byte("---\nname: x\n---\n"+body), DefaultMinDocsSection).ComputedSHA
		}
		lf := sha("alpha\nbeta\n")
		if got := sha("alpha\r\nbeta\n"); got != lf {
			t.Errorf("a CRLF pair moved the fingerprint: %q vs LF %q (§5.2)", got, lf)
		}
		oneCR := sha("alpha\r\r\nbeta\n")
		if oneCR == lf {
			t.Errorf("a lone \\r was normalized away (%q) — §4 trims spaces and tabs, never CR", oneCR)
		}
		if twoCR := sha("alpha\r\r\r\nbeta\n"); twoCR == oneCR {
			t.Errorf("one CR and two CRs fingerprint alike (%q) — each CR is its own byte", twoCR)
		}
		if eof, plain := sha("alpha\nbeta\r"), sha("alpha\nbeta"); eof == plain {
			t.Errorf("a CR terminating the body vanished (%q) — it has no LF to pair with", eof)
		}
		// The other half of the raw/text split: a trailing CR must still never
		// break the COMPARISON that locates the block and its subsections (§5.2).
		crlf := strings.ReplaceAll("---\nname: x\n---\n\n# How to use\n\n## What it does\n"+
			strings.Repeat("a", 60)+"\n", "\n", "\r\n")
		d := ParseDocs([]byte(crlf), DefaultMinDocsSection)
		if !d.Present || !reflect.DeepEqual(d.Sections, []string{"What it does"}) {
			t.Errorf("present=%v sections=%q — a CRLF guide must parse like an LF one", d.Present, d.Sections)
		}
	})

	t.Run("fence_torture", func(t *testing.T) {
		// §5.1 is not a boolean toggle. Every delimiter shape below is mis-read
		// by a scanner that flips a flag on any line starting with ``` or ~~~,
		// and every mis-read moves BOTH the block extent and the §4 fingerprint.
		const filler = "Enough body text here to clear the forty-rune floor a required subsection carries."
		none := []string{}
		one := []string{"What it does"}
		cases := []struct {
			name     string
			why      string
			body     string
			present  bool
			sections []string
		}{
			{
				name:     "tilde_line_inside_backtick_fence",
				why:      "a region closes only on its OWN character, so ~~~ inside ``` is code",
				body:     "# Role\n\n```\n~~~\n# not a heading\n```\n\n# How to use\n\n## What it does\n" + filler + "\n",
				present:  true,
				sections: one,
			},
			{
				name:     "short_fence_inside_long_fence",
				why:      "a closing delimiter must be at least as long as the opener, so ``` cannot close ````",
				body:     "# Role\n\n````\n```\ninner sample\n````\n\n# How to use\n\n## What it does\n" + filler + "\n",
				present:  true,
				sections: one,
			},
			{
				name:     "closing_fence_carries_info_string",
				why:      "only a bare delimiter closes — ```js never closes anything",
				body:     "# Role\n\n```js\nconst a = 1;\n```js\n# still inside the fence\n```\n\n# How to use\n\n## What it does\n" + filler + "\n",
				present:  true,
				sections: one,
			},
			{
				name:     "longer_fence_closes_a_shorter_one",
				why:      "at least as long is the rule, not exactly as long",
				body:     "# Role\n\n```\ncode\n````\n\n# How to use\n\n## What it does\n" + filler + "\n",
				present:  true,
				sections: one,
			},
			{
				name:     "indented_fence_is_not_a_fence",
				why:      "§5.1 pins delimiters to column 0, exactly as §1.1 pins headings",
				body:     "# Role\n\n    ```\n    # indented, neither heading nor fence\n    ```\n\n# How to use\n\n## What it does\n" + filler + "\n",
				present:  true,
				sections: one,
			},
			{
				name:     "fence_at_eof_without_trailing_newline",
				why:      "a delimiter on the last line, unterminated by a newline, must not walk off the end",
				body:     "# How to use\n\n## What it does\n" + filler + "\n\n## How to invoke\n\n```\n@testpack:x run it\n```",
				present:  true,
				sections: []string{"What it does", "How to invoke"},
			},
			{
				name:     "how_to_use_is_the_very_first_line",
				why:      "a block starting at line 0 leaves nothing above it — the degenerate end of §1.3",
				body:     "# How to use\n\n## What it does\n" + filler + "\n",
				present:  true,
				sections: one,
			},
			{
				name:     "unclosed_fence_swallows_the_guide",
				why:      "a region left open at EOF runs to EOF, so the heading inside it is a comment",
				body:     "# Role\n\n```\n# How to use\n\n## What it does\n" + filler + "\n",
				present:  false,
				sections: none,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				d := ParseDocs([]byte("---\nname: x\n---\n"+tc.body), DefaultMinDocsSection)
				if d.Present != tc.present {
					t.Fatalf("present = %v, want %v — %s", d.Present, tc.present, tc.why)
				}
				if !reflect.DeepEqual(d.Sections, tc.sections) {
					t.Errorf("sections = %q, want %q — %s", d.Sections, tc.sections, tc.why)
				}
			})
		}
	})

	t.Run("guide_at_body_start_hashes_an_empty_body", func(t *testing.T) {
		// §4 deletes the block before hashing; when the block IS the whole body
		// what is left is the empty string, and the empty string has a defined
		// fingerprint rather than a special case.
		content := []byte("---\nname: x\n---\n# How to use\n\n## What it does\n" + strings.Repeat("a", 60) + "\n")
		if got, want := ParseDocs(content, DefaultMinDocsSection).ComputedSHA, SourceSHA(nil); got != want {
			t.Errorf("computed sha = %q, want the empty-body fingerprint %q", got, want)
		}
	})

	t.Run("no_frontmatter", func(t *testing.T) {
		// §5.5: a file that does not start `---` is not a registrable item, so
		// its guide is invisible — no item, no guide, no findings.
		content := []byte("# How to use\n\n## What it does\n" + strings.Repeat("a", 60) + "\n")
		d := ParseDocs(content, DefaultMinDocsSection)
		if d.Present {
			t.Error("present = true for a file with no frontmatter, want false (§5.5)")
		}
		if len(d.Sections) != 0 || len(d.Missing) != 0 {
			t.Errorf("sections=%q missing=%q, want both empty", d.Sections, d.Missing)
		}
	})

	t.Run("docs_key_is_a_string", func(t *testing.T) {
		// §3: a `docs:` value that is not a mapping is unknown provenance,
		// never a crash.
		content := []byte("---\nname: x\ndocs: totally-not-a-mapping\n---\n\n# How to use\n\n## What it does\n" +
			strings.Repeat("a", 60) + "\n")
		d := ParseDocs(content, DefaultMinDocsSection)
		if !d.Present {
			t.Fatal("present = false, want true")
		}
		if d.Status != "" || d.SourceSHA != "" {
			t.Errorf("status=%q sourceSHA=%q, want both empty", d.Status, d.SourceSHA)
		}
		if d.Stale {
			t.Error("stale = true with no fingerprint, want false (unknown, never gating)")
		}
	})

	t.Run("all_digit_source_sha_survives_yaml", func(t *testing.T) {
		// Regression: YAML resolves an unquoted all-digit fingerprint to a
		// number, so reading `docs:` through map[string]any turns
		// `source_sha: 000000000000` into "0" and `000000000012` into "10"
		// (leading zero = octal). The node-based reader keeps the text.
		for _, want := range []string{"000000000000", "000000000012", "123456789012"} {
			content := []byte("---\nname: x\ndocs:\n  status: generated\n  source_sha: " + want +
				"\n---\n\n# Role\n\nBody.\n")
			if got := ParseDocs(content, DefaultMinDocsSection).SourceSHA; got != want {
				t.Errorf("source sha = %q, want %q verbatim", got, want)
			}
		}
	})

	t.Run("malformed_source_sha_is_unknown_not_stale", func(t *testing.T) {
		// §4: staleness needs 12 lowercase hex characters; anything else is
		// unknown and must not gate.
		content := []byte("---\nname: x\ndocs:\n  source_sha: nope\n---\n\n# How to use\n\n## What it does\n" +
			strings.Repeat("a", 60) + "\n")
		d := ParseDocs(content, DefaultMinDocsSection)
		if d.SourceSHA != "nope" {
			t.Errorf("source sha = %q, want it kept verbatim", d.SourceSHA)
		}
		if d.Stale {
			t.Error("stale = true for a malformed fingerprint, want false")
		}
	})

	t.Run("computed_sha_stable_across_block_edit", func(t *testing.T) {
		// The load-bearing property of §4: rewriting the guide never marks the
		// item stale, because the guide is removed before hashing.
		base := "---\nname: x\ndocs:\n  source_sha: %s\n---\n\n# Role\n\nThe real content.\n\n# How to use\n\n## What it does\n"
		a := ParseDocs([]byte(strings.Replace(base, "%s", "000000000000", 1)+"first wording of the guide body\n"), DefaultMinDocsSection)
		b := ParseDocs([]byte(strings.Replace(base, "%s", "000000000000", 1)+"a completely different wording, longer than before\n"), DefaultMinDocsSection)
		if a.ComputedSHA != b.ComputedSHA {
			t.Errorf("computed sha moved on a guide-only edit: %q vs %q", a.ComputedSHA, b.ComputedSHA)
		}
		// Trailing-whitespace-only noise is normalized away too.
		c := ParseDocs([]byte("---\nname: x\n---\n\n# Role   \n\nThe real content.\t\n"), DefaultMinDocsSection)
		dd := ParseDocs([]byte("---\nname: x\n---\n\n# Role\n\nThe real content.\n\n\n"), DefaultMinDocsSection)
		if c.ComputedSHA != dd.ComputedSHA {
			t.Errorf("computed sha moved on whitespace-only noise: %q vs %q", c.ComputedSHA, dd.ComputedSHA)
		}
	})

	t.Run("h2_how_to_use_is_not_the_block", func(t *testing.T) {
		// §1.1: level is H1. An H2 with the same text must not match.
		d := ParseDocs([]byte("---\nname: x\n---\n\n## How to use\n\n### What it does\nbody\n"), DefaultMinDocsSection)
		if d.Present {
			t.Error("present = true for an H2 heading, want false")
		}
	})

	t.Run("heading_match_is_case_and_space_insensitive", func(t *testing.T) {
		// §5.3: `#   HOW TO USE   ` is the same block; rewording is not.
		d := ParseDocs([]byte("---\nname: x\n---\n\n#   HOW TO USE   \n\n## What it does\n"+
			strings.Repeat("a", 60)+"\n"), DefaultMinDocsSection)
		if !d.Present {
			t.Error("present = false for `#   HOW TO USE   `, want true")
		}
		reworded := ParseDocs([]byte("---\nname: x\n---\n\n# How to use this agent\n\n## What it does\nbody\n"), DefaultMinDocsSection)
		if reworded.Present {
			t.Error("present = true for `# How to use this agent`, want false (no rewording)")
		}
	})

	t.Run("block_ends_at_next_h1", func(t *testing.T) {
		// §1.3: extent runs to the next H1 outside a fence. Subsections after
		// it belong to the following section, not to the guide.
		content := []byte("---\nname: x\n---\n\n# How to use\n\n## What it does\n" +
			strings.Repeat("a", 60) + "\n\n# Appendix\n\n## Worked example\n" + strings.Repeat("b", 60) + "\n")
		d := ParseDocs(content, DefaultMinDocsSection)
		if want := []string{"What it does"}; !reflect.DeepEqual(d.Sections, want) {
			t.Errorf("sections = %q, want %q", d.Sections, want)
		}
		if strings.Contains(d.Markdown, "Appendix") {
			t.Error("markdown ran past the next H1")
		}
	})

	t.Run("deeper_headings_are_body_not_subsections", func(t *testing.T) {
		// §1.3: `###` and below are content of the subsection above them.
		content := []byte("---\nname: x\n---\n\n# How to use\n\n## What it does\n### A detail\n" +
			strings.Repeat("a", 60) + "\n")
		d := ParseDocs(content, DefaultMinDocsSection)
		if want := []string{"What it does"}; !reflect.DeepEqual(d.Sections, want) {
			t.Errorf("sections = %q, want %q", d.Sections, want)
		}
	})
}

// docsFixture renders a minimal item carrying a guide with the four required
// subsections in RequiredDocSections order, each with the supplied body.
func docsFixture(bodies map[string]string) string {
	var b strings.Builder
	b.WriteString("---\nname: fixture\n---\n\n# Role\n\nBody.\n\n# How to use\n")
	for _, name := range RequiredDocSections {
		b.WriteString("\n## " + name + "\n" + bodies[name] + "\n")
	}
	return b.String()
}

func TestSourceSHAIgnoresGuideAndFrontmatter(t *testing.T) {
	// SourceSHA takes the BODY (what SplitFrontmatter returns), so writing the
	// fingerprint back into frontmatter cannot change the value it records.
	raw := readFixture(t, fixtureDocumentedAgent)
	_, body, err := SplitFrontmatter(raw)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if got := SourceSHA(body); got != shaDocumentedAgent {
		t.Errorf("SourceSHA(body) = %q, want %q", got, shaDocumentedAgent)
	}
	if got := ParseDocs(raw, DefaultMinDocsSection).ComputedSHA; got != shaDocumentedAgent {
		t.Errorf("ParseDocs computed = %q, want %q", got, shaDocumentedAgent)
	}
}

func TestMinDocsSectionEnvOverride(t *testing.T) {
	if got := MinDocsSection(); got != DefaultMinDocsSection {
		t.Errorf("default = %d, want %d", got, DefaultMinDocsSection)
	}
	t.Setenv(EnvMinDocsSection, "5")
	if got := MinDocsSection(); got != 5 {
		t.Errorf("with env override = %d, want 5", got)
	}
	// A non-positive / unparseable value falls back (envInt contract).
	t.Setenv(EnvMinDocsSection, "not-a-number")
	if got := MinDocsSection(); got != DefaultMinDocsSection {
		t.Errorf("with bad override = %d, want the default %d", got, DefaultMinDocsSection)
	}
}
