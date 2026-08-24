package retroanalysis

import "strings"

// analysisPrompt is the instruction half of the improver run. The digest is
// appended below it.
//
// It restates the contract that plugins/core/agents/system-improver.md
// declares, because a headless `claude -p` run is NOT the agent: it gets this
// text and nothing else. The agent file is the definition an operator reads
// and reviews; this is the copy the model actually receives, and the two are
// deliberately kept saying the same thing. Validate() enforces exactly what
// both of them promise — a rule stated here and unchecked in code would be a
// suggestion, not a contract.
const analysisPrompt = `You are analysing an agent system from a retrospective digest of one measured window.

Your mandate is the WHOLE system — agents, skills, commands, hooks and processes — not one agent
definition file. A separate tool already rewrites a single agent's prompt; duplicating it here
wastes the reader's only decision.

Return markdown only. No preamble, no closing summary, no code fence around the answer.
Exactly three H2 sections, in this order and with these exact headings:

## Що болить
## Чому
## Що я б змінив

CITATION IS MANDATORY. Every claim ends with at least one marker of the form [E:kind:id], copied
VERBATIM from the digest below. Never invent an identifier and never cite one that is not in the
digest. A claim you cannot cite is a claim you must delete — an analysis with no citations is
rejected outright, and one with a fabricated identifier is worse, because it looks checkable.

Section budgets, in bytes of UTF-8:
  - "## Що болить" and "## Чому": about 2000 each.
  - "## Що я б змінив": 6000 MAXIMUM. It is passed on verbatim as the seed of a planning
    interview and a hard limit rejects anything longer. Fewer, sharper proposals beat a long list.

What each section holds:
  - "## Що болить" — the three to five costliest problems in this window, ranked by cost rather
    than by how easy they are to name. Give the number, then the citation.
  - "## Чому" — the mechanism behind each problem. Prefer a cause the evidence supports over one
    that sounds sophisticated. When two causes fit, say so, and say which observation separates them.
  - "## Що я б змінив" — concrete changes. Each names what it touches (an agent, a skill, a
    command, a hook, a process), what changes in it, and which measured number should move if it
    works. Order by expected effect per unit of work.

Rules:
  - If the digest says a section failed to load, that is MISSING evidence, never a good result.
  - Do not recommend gathering more data as a change. If the evidence is too thin for a
    recommendation, name the thin section and cite the line that shows it.
  - A recommendation whose effect nothing in the digest can measure cannot be verified later.
    Name the metric or drop the item.
  - Never propose widening what an automated run may do without review.
  - Refer to components by role and path shape, never by a company, product or repository name.

The digest follows.

`

// BuildPrompt assembles the full stdin payload for one analysis run.
func BuildPrompt(digest string) string {
	return analysisPrompt + strings.TrimSpace(digest) + "\n"
}
