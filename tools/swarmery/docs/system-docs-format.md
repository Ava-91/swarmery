# System docs format — the usage-guide contract

**Status:** normative. This is the parsing contract for the `# How to use` block that
every registrable System item (agent, skill, command) may carry, and the sibling of
[`system-config-format.md`](system-config-format.md), which `internal/sysscan` already
names as its parsing reference for *config* shape. That doc answers "what is an item";
this one answers "is that item **documented**".

One definition of "documented" is shared by four consumers, and all four read this file:

| Consumer | What it does with the contract |
|---|---|
| The Go parser (`internal/sysscan`) | Extracts the block, its subsections, and the `docs:` provenance |
| The linter | Emits coverage findings for missing required subsections |
| The generator | Rewrites the block idempotently, in place, at the end of the body |
| The CI gate | Fails when required-subsection coverage regresses |

A usage guide is written for the **reader of the item**, not for the machine. The machine
rules below exist only so that the reader always finds the same eight questions answered
in the same order.

The three fixtures that pin this contract for the test suite live in
`testdata/sysconfig/`: `claude/agents/documented-agent.md` (complete, `status: reviewed`),
`claude/agents/stale-docs-agent.md` (required-only, `status: generated`, deliberately
stale `source_sha`), and `claude/skills/documented-skill/SKILL.md` (missing
`## Worked example`, plus the fenced false-heading trap from §5).

---

## §1 Heading level & placement

The guide is **one H1 heading whose text is `How to use`**, followed by its subsections:

```markdown
# How to use
```

Rules, all enforceable by the parser:

1. **Level is H1** — exactly one `#` and at least one space or tab before the text. This
   matches the corpus: top-level sections of real items are H1 (`plugins/core/agents/tech-lead.md`
   opens with `# Role` and `# Examples`; `plugins/core/skills/testing/SKILL.md` uses
   `# Purpose` and `# When to use`). An H2 `## How to use` is **not** the block and must
   not be matched.
2. **Placement is last.** The block is appended at the **end of the markdown body** so the
   generator can rewrite it idempotently: everything from the opening heading to EOF is
   replaceable without touching a single byte the author wrote. A block that is not last
   is still parsed (the reader's content wins over the generator's convenience), but the
   generator rewrites it in place rather than appending.
3. **Extent.** The block runs from its own heading line to the **next H1 at column 0
   outside a fenced region**, or to EOF — whichever comes first. Every `##` heading in
   that range is one of its subsections; deeper headings (`###` and below) are body
   content of the subsection they sit under, never subsections themselves.
4. **Exactly one.** Two `# How to use` headings in one file is a **violation**, not a
   merge. The parser keeps the first block, ignores the second, and the linter reports a
   duplicate-block finding. Never concatenate them.
5. **Scope.** Only files that are registrable items carry a guide — i.e. files whose first
   line is `---` (`isFrontmatterStart`, see §5.5). A file with no frontmatter is not an
   item and is out of scope entirely, guide or no guide.

---

## §2 Subsection schema

The block has **exactly 8 subsections**, always H2, always in this order:

| # | Subsection | Required? | What belongs in it |
|---|---|---|---|
| 1 | `## What it does` | **required** | One short paragraph in plain language: the problem this solves for the reader |
| 2 | `## When to use it` | **required** | 2–4 bullets, each a concrete situation |
| 3 | `## When not to use it` | recommended | 2–4 bullets, each naming what to reach for instead |
| 4 | `## How to invoke` | **required** | A fenced block with the literal invocation, then one line of prose |
| 5 | `## Inputs` | recommended | What the caller supplies: name — what it is — required/optional |
| 6 | `## What you get back` | recommended | Files written, the shape of the final message, side effects |
| 7 | `## Worked example` | **required** | A fenced block: the real request, what happens, what you end up with |
| 8 | `## Related` | recommended | Sibling items by name, one clause each on when to prefer them |

**4 of the 8 are required** and they alone drive the coverage gate: `What it does`,
`When to use it`, `How to invoke`, `Worked example`. The other **4 are recommended**:
`When not to use it`, `Inputs`, `What you get back`, `Related`.

Each **required** subsection must carry at least **40 runes** of body text (runes, not
bytes — `utf8.RuneCountInString`, the same unit the `skill_short_description` rule
already counts in). Body text means everything between this subsection's heading and the
next `##` heading (or the end of the block), with the heading line itself excluded and
leading/trailing whitespace trimmed. Fenced-block content counts toward the 40 runes; a
subsection that contains only a heading, or only whitespace, counts as **absent**.

The 4 recommended subsections are reported at severity `info` and **never gate** anything:
a guide with all four required subsections and none of the recommended ones is a passing,
documented item.

The canonical block, verbatim:

```markdown
# How to use

## What it does
One short paragraph in plain language. The problem this solves for the reader.

## When to use it
2–4 bullets, each a concrete situation.

## When not to use it
2–4 bullets, each naming what to reach for instead.

## How to invoke
A fenced block with the literal invocation, then one line of prose.

## Inputs
What the caller supplies: name — what it is — required/optional.

## What you get back
Files written, the shape of the final message, side effects.

## Worked example
A fenced block: the real request, what happens, what you end up with.

## Related
Sibling items by name, one clause each on when to prefer them.
```

Subsection matching is on the heading **text**, case-insensitively and with surrounding
whitespace trimmed (§5.3). An unrecognized `##` heading inside the block is kept as
content and reported at `info` — it is never an error, and it never counts toward
coverage.

---

## §3 Frontmatter provenance — the `docs:` key

Provenance lives in the item's own YAML frontmatter, under a single mapping key `docs:`:

```yaml
docs:
  status: reviewed        # generated | reviewed
  source_sha: a1b2c3d4e5f6
  updated: 2026-08-12
```

| Field | Type | Required | Meaning |
|---|---|---|---|
| `status` | string enum | yes | `generated` — written by the generator, never read by a human. `reviewed` — a human read it and stands behind it. No other value is valid; an unknown value is stored verbatim and reported at `warn`. |
| `source_sha` | 12 hex chars | yes | The §4 fingerprint of the item body **as of the last time the guide was written**. It is how staleness is detected: recompute it, compare, and if it differs the item changed since its guide was written. |
| `updated` | `YYYY-MM-DD` | yes | The date the guide was last written or reviewed. Display only — never used for staleness (a stale date with a matching `source_sha` means nothing changed). |

`docs:` is **optional**, like every other frontmatter field (`system-config-format.md`
§1.2: "every field must be treated as optional by the parser"). An item with a guide but
no `docs:` key is documented but has unknown provenance: treat it as `status: generated`,
`source_sha` empty, staleness unknown. An item with `docs:` but no guide is a violation
reported at `warn` — provenance for a guide that does not exist.

Parsing is tolerant in exactly the way §1.3 of the config format doc requires: a
`docs:` value that is not a mapping (a bare string, a list) is stored as unknown
provenance and reported, never crashed on.

---

## §4 `source_sha` computation

`source_sha` is the first **12 hex characters** of the **SHA-256** of the item's markdown
body, normalized as follows: take the body — everything after the closing `---` of the
frontmatter, exactly what `sysscan.SplitFrontmatter` returns; replace every CRLF with LF;
**delete the `# How to use` block** (the opening heading line through the character before
the next column-0 H1 outside a fenced region, or through EOF if there is none); strip
trailing spaces and tabs from every remaining line; join the lines back with LF; then trim
all trailing newlines and, if anything is left, terminate with exactly one LF. Hash those
bytes; lowercase-hex the digest; keep the first 12 characters. Frontmatter is **not**
part of the input, so writing `source_sha` back into it cannot change the value it
records — and because the guide itself is removed before hashing, editing, regenerating,
or reformatting the guide never marks the item stale. Only a change to the item's real
content does.

Reference implementation:

```go
func SourceSHA(body []byte) string {
	s := strings.ReplaceAll(string(body), "\r\n", "\n")
	s = removeHowToUseBlock(s) // §1.3 extent rules
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t")
	}
	s = strings.TrimRight(strings.Join(lines, "\n"), "\n")
	if s != "" {
		s += "\n"
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}
```

Staleness is then a two-line rule: recompute `SourceSHA` over the current body; if it
equals the stored `source_sha`, the guide is **current**; if it differs, the guide is
**stale**; if `source_sha` is absent or is not 12 lowercase hex characters, staleness is
**unknown** (reported at `info`, never gating). A deliberately wrong value — the fixture
uses `000000000000` — is a stale guide, not a parse error.

---

## §5 Parser edge cases

These are the cases that break naive line scanners. Every one of them is a phase-2 test
case, and two of them are planted in the fixtures.

### §5.1 Fenced regions are skipped

A line starting with `#` at column 0 **inside a fenced code block** is a comment, not a
heading. Fences are opened and closed by ``` or `~~~` at column 0 (optionally with an
info string); track fence state while scanning and ignore every heading candidate while
a fence is open. The planted trap is a bash block whose first line is:

```bash
# deploy the thing
```

That must **not** terminate the `# How to use` block, and must **not** register as an H1.
`testdata/sysconfig/claude/skills/documented-skill/SKILL.md` carries exactly this trap.

### §5.2 CRLF is tolerated

Files with `\r\n` line endings parse identically to LF files. Trim a trailing `\r` before
comparing any line — the frontmatter splitter already does this
(`strings.TrimRight(string(line), "\r \t")`), and heading and subsection matching must
do the same. CRLF also never changes a `source_sha`: §4 normalizes it away.

### §5.3 Heading text matches case-insensitively

The match is on the text **after** the `# ` marker, trimmed and compared
case-insensitively: `# How to use`, `# HOW TO USE`, and `#   how to use   ` are the same
block. The same rule applies to the eight `##` subsection titles. What is **not**
tolerated: a different level (`## How to use` is not the block, §1.1), and rewording
(`# How to use this agent` does not match — the text must be exactly `how to use` after
trimming and lowercasing).

### §5.4 A second block is a violation, not a merge

If a file contains two headings that match §5.3, the **first** is the block. The second
is left as ordinary body content, and the linter reports a duplicate-block finding at
`warn`. Never concatenate the two, and never let the second one's subsections count
toward coverage — a merge would let an item pass the gate on subsections the reader will
never see together.

### §5.5 A file with no frontmatter is not an item

If the first line is not `---` (`isFrontmatterStart` is false, after the UTF-8 BOM is
stripped), the file is skipped silently and completely — no item, no guide, no findings.
This is the existing rule for helper files such as the `README.md` inside an `agents/`
directory (`system-config-format.md` §1.1), and the docs scanner inherits it unchanged: a
`# How to use` block in a file that is not an item is invisible.

---

## §6 Style rules

The guide is prose a person reads at the moment they are deciding whether to use the
item. Machine rules make it findable; these make it worth finding.

- **Plain language.** Short sentences. No jargon the reader has not already met on the
  page they came from. If a sentence needs a second reading, split it.
- **Second person.** Write to the reader: "you get back a summary", not "the caller is
  returned a summary". Describe the item in the third person, the reader in the second.
- **English only.** Every guide, in every item, in every repository. No mixed-language
  bodies.
- **Neutral placeholders.** No brand tokens, no internal repo names, no environment
  aliases, no cloud regions — the rules in [`docs/NEUTRALITY.md`](../../../docs/NEUTRALITY.md)
  apply to guide prose exactly as they apply to the rest of `plugins/**`. Use
  `apps/<mainApp>`, `<device>`, `<envAlias>`, or a neutral example domain such as
  `orders/line-items`.
- **No placeholders of the other kind.** No `TBD`, no `see above`, no `similar to the
  other agent`. A guide that defers is a guide that fails the gate; write the sentence or
  delete the subsection.
- **Concrete over complete.** One real invocation beats three hypothetical ones.
  `## Worked example` shows a request that was actually made and what came back.
- **Fenced blocks for anything literal** — invocations, file paths that are typed,
  command output. Prose for everything else.
- **Length.** A guide is a page, not a manual: aim for under 60 lines total. The
  reference documentation for the item lives in the item body above the guide.
