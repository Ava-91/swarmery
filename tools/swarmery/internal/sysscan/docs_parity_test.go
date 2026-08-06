package sysscan

// Go ↔ shell parity for the §4 fingerprint.
//
// docs/system-docs-format.md §4 has TWO implementations: this package, and the
// JavaScript prelude in scripts/docgen/lib.sh that extract.sh, generate.sh and
// the CI coverage gate all share. extract.sh calls their agreement "a contract,
// not a coincidence" — but until this test existed, nothing enforced it: the
// two drifted apart on a lone `\r` and on three separate fence shapes, each
// drift silently moving both the fingerprint and the block extent.
//
// Every case below is a shape that broke one of the two scanners. The assertion
// is deliberately not "the SHA equals <constant>" — a constant would be another
// thing to keep in sync. It is "both implementations, given the same bytes,
// return the same 12 characters."
//
// Hermetic and offline: each case is written into a throwaway item tree under
// t.TempDir(), DOCGEN_ROOT is pointed at it, and the REAL extract.sh runs. No
// network, no model. The test skips rather than fails when bash, node, or the
// scripts/ tree is unavailable, because the Go module must stay buildable on a
// machine that carries none of them.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// parityFrontmatter is the minimal registrable-item header (§5.5) every case
// gets. It is not part of the fingerprint input (§4: frontmatter is excluded),
// so it can never influence the comparison.
const parityFrontmatter = "---\nname: parity\ndescription: A Go-to-shell parity fixture for the source_sha contract.\n---\n"

// parityCases are bodies whose fingerprint both implementations must agree on.
var parityCases = []struct {
	name string
	why  string
	body string
}{
	{
		name: "lone_cr_before_newline",
		why:  "§4 strips CRLF pairs and then trims only spaces and tabs — a bare CR is content and must survive into the hash",
		body: "alpha\r\r\nbeta\n",
	},
	{
		name: "double_cr_before_newline",
		why:  "two bare CRs are two bytes of content, not one line ending to normalize away",
		body: "gamma\r\r\r\ndelta\n",
	},
	{
		name: "cr_at_eof_without_newline",
		why:  "a CR that terminates the file has no LF to pair with, so no CRLF rule applies to it",
		body: "alpha\nbeta\r",
	},
	{
		name: "trailing_spaces_and_tabs",
		why:  "§4 trims trailing spaces and tabs from every line — the one whitespace class that IS normalized",
		body: "alpha \t\nbeta\r \ngamma\t\t\n",
	},
	{
		name: "tilde_line_inside_backtick_fence",
		why:  "§5.1: a fence closes only on its OWN character, so `~~~` inside a ``` block is code, not a delimiter",
		body: "# Role\n\n```\n~~~\n# not a heading\n```\n\n# How to use\n\n## What it does\nEnough body text to read like a real subsection.\n",
	},
	{
		name: "short_fence_inside_long_fence",
		why:  "§5.1: a closing fence must be at least as long as the opener, so ``` cannot close ````",
		body: "# Role\n\n````\n```\ninner sample\n````\n\n# How to use\n\n## What it does\nEnough body text to read like a real subsection.\n",
	},
	{
		name: "closing_fence_carries_info_string",
		why:  "§5.1: only a bare delimiter closes — ```js opens a nested-looking region, it never closes one",
		body: "# Role\n\n```js\nconst a = 1;\n```js\n# still inside the fence\n```\n\n# How to use\n\n## What it does\nEnough body text to read like a real subsection.\n",
	},
	{
		name: "indented_fence_is_not_a_fence",
		why:  "§5.1 pins fences to column 0, exactly as headings are pinned there",
		body: "# Role\n\n    ```\n    # indented, not a heading and not a fence\n    ```\n\n# How to use\n\n## What it does\nEnough body text to read like a real subsection.\n",
	},
	{
		name: "fence_at_eof_without_trailing_newline",
		why:  "an unterminated fence at EOF must not make the scanner walk off the end",
		body: "# Role\n\n# How to use\n\n## How to invoke\n\n```\n@testpack:parity run it\n```",
	},
	{
		name: "how_to_use_is_the_first_line",
		why:  "deleting a block that starts at line 0 leaves an empty body — the degenerate end of §4",
		body: "# How to use\n\n## What it does\nThe guide opens the body with nothing above it.\n",
	},
	{
		name: "no_guide_at_all",
		why:  "control: §4 is defined for an undocumented item too",
		body: "# Role\n\nNothing to see here.\n\n# Boundaries\n\n- never writes to the order store\n",
	},
	{
		name: "crlf_throughout",
		why:  "control: §5.2 — a CRLF file fingerprints exactly like its LF twin",
		body: "# Role\r\n\r\nA CRLF body.\r\n\r\n# How to use\r\n\r\n## What it does\r\nEnough body text to read like a real subsection.\r\n",
	},
	{
		name: "duplicate_guide_blocks",
		why:  "§5.4: only the FIRST block is deleted before hashing; the second stays ordinary body text",
		body: "# Role\n\nBody.\n\n# How to use\n\n## What it does\nThe first block.\n\n# How to use\n\n## What it does\nThe second block, which is body content.\n",
	},
}

func TestSourceSHAMatchesShellImplementation(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	// Skipping is a local-developer convenience only. On CI the cross-language
	// contract MUST be enforced, so a missing prerequisite there is a failure —
	// otherwise the whole parity guarantee silently stops being checked the day
	// node drops off the runner image.
	unavailable := func(format string, args ...any) {
		t.Helper()
		if os.Getenv("CI") != "" {
			t.Fatalf("parity test cannot run on CI: "+format, args...)
		}
		t.Skipf(format, args...)
	}
	extract := filepath.Join(root, "scripts", "docgen", "extract.sh")
	if _, err := os.Stat(extract); err != nil {
		unavailable("scripts/docgen/extract.sh not reachable from the module: %v", err)
	}
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		unavailable("bash not on PATH: %v", err)
	}
	if _, err := exec.LookPath("node"); err != nil {
		unavailable("node not on PATH: %v", err)
	}

	tree := t.TempDir()
	// The shell prelude classifies an item by PATH SHAPE (plugins/<pack>/agents/…),
	// so the throwaway tree has to look like a pack or extract.sh refuses the file.
	dir := filepath.Join(tree, "plugins", "testpack", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("build fixture tree: %v", err)
	}

	for _, tc := range parityCases {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(dir, tc.name+".md")
			if err := os.WriteFile(file, []byte(parityFrontmatter+tc.body), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			// Feed Go exactly the bytes the shell feeds its own hasher: the body
			// SplitFrontmatter returns, read back off disk rather than assumed.
			content, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read back fixture: %v", err)
			}
			_, body, err := SplitFrontmatter(content)
			if err != nil {
				t.Fatalf("split frontmatter: %v", err)
			}
			goSHA := SourceSHA(body)

			var stderr bytes.Buffer
			cmd := exec.Command(bashPath, extract, file)
			cmd.Env = append(os.Environ(), "DOCGEN_ROOT="+tree)
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("extract.sh failed: %v\nstderr: %s", err, stderr.String())
			}
			var brief struct {
				BodySHA string `json:"body_sha"`
			}
			if err := json.Unmarshal(out, &brief); err != nil {
				t.Fatalf("extract.sh emitted unparsable JSON: %v\nstdout: %s", err, out)
			}
			if brief.BodySHA == "" {
				t.Fatalf("extract.sh returned an empty body_sha\nstdout: %s", out)
			}

			if goSHA != brief.BodySHA {
				t.Errorf("§4 fingerprints disagree — %s\n  Go    = %s\n  shell = %s\n  body  = %q",
					tc.why, goSHA, brief.BodySHA, tc.body)
			}
		})
	}
}
