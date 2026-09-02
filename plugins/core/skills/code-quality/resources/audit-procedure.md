# code-quality — thresholds, procedure, and checks

## Thresholds

| Metric | TypeScript | Python |
|--------|------------|--------|
| Function length (warning) | >30 lines | >30 lines |
| Function length (error) | >50 lines | >50 lines |
| React component length (error) | >150 lines | N/A |
| Module/file length (warning) | >200 lines (lib), >150 lines (component) | >300 lines |
| Nesting depth (error) | >2 levels | >2 levels |
| Cyclomatic complexity (warning) | >10 | >10 |

These thresholds are authoritative for this skill. If the project adopts
different ones in `.eslintrc` or `pyproject.toml`, update this table to match.

## Inputs

- `scope` — file, directory, or repo path to audit (e.g.
  `apps/<mainApp>/src/app/api/<resource>/route.ts` or `apps/<mainApp>/src/lib/`).
- `repo_type` — `"typescript" | "python"`, which selects the threshold set.

## Procedure

**Success criteria:** every function in scope is assessed, every finding cites
`file:line`, and the overall score follows the formula below.

1. **Determine scope and repo type** — read the target path, identify TypeScript
   or Python, apply the matching thresholds. *Checkpoint: thresholds selected.*
2. **Read target files once** — cache contents; steps 3–6 all reuse them.
   *Checkpoint: files read.*
3. **Function length** (steps 3–6 are independent, run them in parallel) — flag
   functions over 50 lines (warning at 30), React components over 150. Blank and
   comment-only lines do not count. *Checkpoint: oversized functions listed.*
4. **Nesting depth** — blocks nested deeper than 2 levels inside a function body.
   *Checkpoint: deep-nesting locations listed.*
5. **Code smells** — duplicate blocks (3+ verbatim lines in two places),
   TODO/FIXME (not in test files), dead code and unused imports, missing error
   handling, magic numbers/strings, missing guard clauses that would flatten
   nesting. *Checkpoint: smells listed.*
6. **Project-specific checks** — read the consumer project's `CLAUDE.md` for its
   conventions. Typical set for a Next.js main app plus a Python device repo:
   - Main app: lazy DB init (no eager `export const db = …`).
   - Main app: `export const dynamic = 'force-dynamic'` on routes importing `auth()`.
   - Main app: no `next/font/google`; typed env helpers instead of scattered
     `process.env.*`; schema validation at route-handler boundaries.
   - Main app: route handlers stay thin — business logic in `src/lib/`.
   - Main app (React): `key` props in lists, complete `useEffect` dependency
     arrays, error boundaries around risky subtrees.
   - Device repo: `async`/`await` on all I/O; no bare `except:`; context managers
     for hardware handles; mock-mode (`MOCK_MODE`) coverage for hardware paths.

   *Checkpoint: violations listed with `file:line`.*
7. **Score** — per category: 100 minus 10 per error and 5 per warning, floored at
   0. Overall = mean of category scores, rounded. *Checkpoint: scores computed.*
8. **Report** — fill the template in `resources/report-format.md`; start with the
   `QUALITY-SCORE:` header. *Checkpoint: report complete.*
9. **Final acceptance check** — every finding cited, every score computed, no
   placeholder text, header populated.

## Self-check before returning

- [ ] Every finding cites `file:line` and carries a severity (Error / Warning).
- [ ] Scores follow the formula (100 minus deductions, floor 0).
- [ ] Findings below 80% confidence are marked `[LOW-CONFIDENCE]`.
- [ ] Component files judged at 150 lines, lib/utility files at 200.
- [ ] The `QUALITY-SCORE:` header is present and populated.
- [ ] No placeholder text remains.
- [ ] No `any`-type grep was performed — that check belongs to `code-standards`.

## Common mistakes to avoid

- Do not grep for `any` types or missing annotations — `code-standards` owns that.
- Do not flag generated files (`*.generated.ts`, migrations) or vendored code.
- Do not apply TypeScript thresholds to Python files or vice versa.
- Do not count blank or comment-only lines toward function length.
- Do not report `TODO` in test files as a smell — those are legitimate markers.
- Do not conflate the React component threshold (150) with the lib file one (200).

## Escalation

- **Stop and ask** when a single file yields more than 50 findings (that is an
  architectural problem, not a piecemeal one).
- **Stop and ask** before scanning a repository of more than 500 files.
- **Refuse and explain** when asked to auto-fix — this is an audit skill; fixes
  belong to `@implementation-agent`.

## Failure modes

- **Inflated length on React components** — multi-line JSX counted as statements.
  Detect: implausible line counts on render functions. Fix: count logical
  statements, not physical lines.
- **Symlinked scope resolving outside the repo** — detect: paths that do not match
  the repo structure. Fix: resolve symlinks and verify paths sit under the root.
- **Directory audit above 500 files** — detect: file count over threshold. Fix:
  escalate and ask the caller to narrow scope.

## Composition

`code-quality` and `code-standards` compose as a depth-1 fan-out under
`@code-reviewer`: both run as leaves over the SAME scope, and the reviewer
aggregates the `QUALITY-SCORE:` and `STANDARDS-VIOLATIONS:` headers. It is never
a nested delegation chain or a routing handoff.
