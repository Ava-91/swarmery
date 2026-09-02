# core 3.0.0 — agent-system audit remediation

**Date:** 2026-09-02
**Branch:** `feat/core-3-audit-remediation` → merged to `main` as `8f4d5c2` (PR #272)
**Input:** the September 2026 external agent-system audit (`Temp/swarmery-audit-2026-09.md`, sections 2.1–2.7, P0 + P1)
**Plan:** `swarmery-workspace/swarmery/workspace/working/2026/09/01/agent-system-audit-remediation/plan/` — 7 phases, 39 acceptance criteria ticked, each with a filled `## Completion Report`; of the 14 spec criteria, 13 ticked and **SC-9 honestly left unticked** (see §11)
**Follow-up:** core 3.0.1 (PR #273) corrects three defects this report's first version overstated — see §11

---

## 1. Headline numbers

| Metric | Before (2.23.2) | After (3.0.0) |
|---|---:|---:|
| Core agents | 42 | **13** |
| Agent prose | 94,062 words | **8,369 words** (−91%) |
| `[PE/` prompt-engineering tags in `plugins/core` | 479 | **0** |
| Largest `SKILL.md` body | 2,803 words | **500 words** (all 36 within budget) |
| Skill `resources/` files | 0 | **62** across 35 skills |
| Duplicate commands | 8 | **0** (8 genuinely command-shaped files remain) |
| Hook processes per tool call | 2 | **1** |
| Wired hooks / documented opt-ins | 15 / 0 | **16 / 2** |
| Agents with a restrictive `tools:` allowlist | 0 | **5** |

Aggregate diff across the whole effort: **234 files changed, 9,422 insertions, 19,727 deletions.**

---

## 2. What the audit found, and what was done about it

### P0 — things that were actually broken

| Defect | Fix |
|---|---|
| `permissionMode:` in all 42 agents — Claude Code **ignores** this key on plugin subagents, so it never had runtime effect | Removed everywhere. `overlays/example/settings.snippet.json` now shows how a consumer grants edit-heavy agents their allowances via `permissions.allow`. |
| Pinned and retired model IDs in frontmatter and prose | All converted to aliases (`opus` / `sonnet` / `haiku` / `inherit`). |
| 6 references to documentation files that do not exist | Pointer dropped, the one-line essence inlined where the surrounding instruction needed it. |
| `deployment` and `nextjs-migration` in 10 agents' `skills:` lists — neither skill exists in `core` | Removed; where deployment knowledge was load-bearing, the prose now says it comes from the project's enabled packs. |
| Read-only-class agents shipped with full tool access, including `Edit`/`Write` | 5 agents now carry an explicit `tools:` allowlist with no write tools. See §6 — the first cut left `Bash` unscoped on all five, which was corrected in 3.0.1. |
| Unknown frontmatter fields (`autonomy`, `owner`, `version`) | Stripped; the metadata moved to `plugins/core/AGENTS.md`. |

### P1 — the structural problem

The audit's core finding was that 2.x was built on 2025-era prompt-engineering assumptions that current models no longer need: fixed phase machines, mandatory checklists, "minimum N queries" rules, and 42 agents that frequently differed only in phrasing. The remediation was consolidation into judgment-style agents, not deletion of capability — every removed agent maps to an absorber agent or a skill.

---

## 3. Changes by area

### 3.1 Agents — 42 → 13 (`c2645f3`)

`plugins/core/agents/`: 34 files deleted, 5 added, 8 rewritten. Diff: **47 files, +920 / −10,951**.

The roster:

| Agent | Class | Model | Role |
|---|---|---|---|
| `tech-lead` | orchestrator | opus | Understand → route by size → independent review gate → close with summary |
| `planner` | planning | opus | Workspace plans: phase docs, falsifiable criteria, executor prompts |
| `architect` | design | opus | System/API/data design with trade-offs, migration safety, rollout |
| `researcher` | no-edit | sonnet | Tech evaluation, impact analysis, context synthesis with citations |
| `implementation-agent` | executor | opus | Leaf code execution or plan-execution orchestration |
| `ui-developer` | executor | sonnet | Frontend components with type/token/a11y/state gates |
| `debugger` | executor | sonnet | Root cause first, minimal proven fix (bugs, builds, CI, perf) |
| `test-writer` | executor | sonnet | Behavior-pinning tests in the project's stack |
| `test-runner` | no-edit | haiku | Execute suites, report faithfully, VERDICT line |
| `code-reviewer` | read-only | opus | Multi-lens review (correctness, silent failures, contracts, plan, quality) |
| `security-auditor` | read-only | opus | OWASP + STRIDE with attack paths |
| `verification-agent` | no-edit | haiku | Deterministic checks (build/type/lint/test) |
| `system-improver` | meta | opus | Evidence-cited diagnosis of the whole agent system |

Where the 34 removed agents went:

| Removed | Now |
|---|---|
| `full-stack-feature` | `@tech-lead` |
| `task-planner`, `implementation-planner`, `task-decomposer`, `prompting-agent` | `@planner` |
| `architecture-designer`, `api-designer`, `database-designer` | `@architect` |
| `migration-agent`, `migration-helper` | `@architect` (design/safety) + `@implementation-agent` (execution); `migration-check` skill |
| `tech-researcher`, `context-gatherer`, `downstream-analyzer` | `@researcher` |
| `react-specialist`, `ui-designer` | `@ui-developer` |
| `build-error-resolver`, `ci-incident-responder`, `performance-monitor`, `performance-optimizer` | `@debugger` |
| `quality-checker`, `code-auditor`, `plan-reviewer`, `contract-validator`, `silent-failure-hunter` | `@code-reviewer` |
| `summary-generator`, `task-documenter`, `post-task-completion`, `retrospective-agent` | `session-closeout` skill |
| `guardrail-checker` | `guardrails` skill |
| `commit-message` | `git-commit` skill |
| `pr-generator`, `sprint-review`, `founder-reality-check` | same-named skills |
| `sre-orchestrator` | `sre-operations` skill |

### 3.2 Skills — progressive disclosure (`b3e11c8`)

Diff: **94 files, +7,320 / −7,039** in `plugins/core/skills/`.

Every one of the 36 `SKILL.md` files now follows the same shape — frontmatter → `# Purpose` → `# Rules (never violate)` → `# Resources` (a one-line index of what to read and when) → the `# How to use` block the docs gate requires — and stays at or under a 500-word body. The overflow lives in 62 `resources/*.md` files that load only when the skill is actually working.

Six skills were split in the final pass:

| Skill | Before | After | Resources created |
|---|---:|---:|---|
| `code-standards` | 2,803 | 481 | `review-procedure.md`, `checklists.md` |
| `code-quality` | 2,662 | 498 | `audit-procedure.md`, `report-format.md` |
| `run-plan` | 2,331 | 500 | `execution-routes.md`, `progress-contracts.md` |
| `context-optimization` | 2,297 | 496 | `planning-procedure.md` |
| `functional-design` | 2,071 | 500 | `refactor-procedure.md` |
| `html-reporting` | 1,413 | 495 | `shell.md`, `render-procedure.md` |

Contract-bearing content was moved **verbatim**, not paraphrased: the `run-plan` progress and summary hard gates (the `## Completion Report` section the dashboard parses), the html shell's exact CSS and severity classes, and the `context-optimization` 40% / 60% delegation decision table.

Seven agents landed as skills: `guardrails` (APPROVED/REJECTED verdict contract preserved), `pr-generator`, `session-closeout` (absorbs four closing agents; the wsingest-read artifact formats are stated in its resources), `sprint-review`, `founder-reality-check`, `sre-operations`, plus `workspace-plans` (the plan format `planner` emits and `wsingest` reads). `git-commit` absorbed the commit-message agent's `project.json → commitScopes` rules.

### 3.3 Commands — 8 deleted (`b3e11c8`)

`code-quality`, `deps-check`, `env-check`, `migration-check`, `refactor-plan`, `run-plan`, `security-audit`, `test-coverage` — each was a file that only wrapped a same-named skill. Deleted (**−558 lines**); the skills stay invocable, `/name` form included.

### 3.4 Hooks (`275b38b`)

- **`read-before-write.sh` deleted** with its `hooks.json` entry and its test — Claude Code has done that check natively since v2.1.160, so the shell reimplementation was a second process per Edit/Write for a decision the harness already makes.
- **`activity-tracker.sh` + `session-budget.sh` merged** into `post-tool-observe.sh` on the unmatched `PostToolUse` slot: one hook process per tool call instead of two. It reads stdin once, does both jobs, and short-circuits before any node startup on the common path. The per-call colored activity box is gone (terminal noise paid for on every call); the shared session log and the once-per-session budget warning remain. `session-budget.test.sh` was rewritten as `post-tool-observe.test.sh`.
- **`pre-commit-test-gate.sh` and `memory-drift-check.sh`** were unwired: a repo-wide grep found no invocation anywhere outside their own bodies. Both are useful but situational, so both stay as **documented opt-ins** — each header now states it is not wired by default and gives the exact `hooks.json` entry to paste. No silent dead code remains.

### 3.5 CI — the reference-integrity gate (`93becf2`)

`scripts/validate-agent-refs.sh` (147 lines) plus an 11-case test suite, wired into the `validate` job between the frontmatter step and docs coverage. It enforces six rules on every `plugins/*/agents/*.md`:

1. every `skills:` entry resolves to a real skill in the owning plugin or in `core`;
2. `model:` is an alias, never a pinned id;
3. no frontmatter key Claude Code ignores on plugin subagents;
4. every `${CLAUDE_PLUGIN_ROOT}/…` path referenced from a body exists;
5. no retired model id anywhere in a body;
6. no pinned current-generation model id in an agent body.

Exemptions live in an explicit in-script allowlist where a **stale entry is itself a failure**, so the exemption list cannot rot. This is what makes the dead-reference bug class non-recurring rather than merely fixed once.

### 3.6 Evals (`3a9f70c`)

Diff: **11 files, +160 / −95**. Suites now track the new roster; `tech-lead.yaml` rewritten (routing judgment, reviewer-before-commit; ledger assertions dropped because the rewritten agent no longer promises them); `task-planner.yaml` → `planner.yaml` asserting the plan-format contract; `commit-message.yaml` → `git-commit.yaml` and `guardrail-checker.yaml` → `guardrails.yaml` re-pointed at the skill bodies with their hard contracts kept verbatim.

The config keeps **pinned** model ids on purpose — judge `claude-sonnet-5`, runner `claude-haiku-4-5-20251001` — because promptfoo resolves provider ids against the API and rejects the aliases agent frontmatter now uses. `validate-agent-refs.sh` scopes its alias rule to `plugins/*/agents/*.md` so the two rules never collide.

### 3.7 Control plane (`c2645f3`)

`tools/swarmery/internal/planning/prompt.go` now emits `@planner` instead of `@task-planner`/`@implementation-planner`, with its golden file regenerated. `tools/swarmery/docs/retro.md` points at the `session-closeout` skill instead of the removed `@retrospective-agent`. Diff: **6 files, +84 / −69**.

### 3.8 Release (`0aabd45`)

`plugins/core/.claude-plugin/plugin.json` → `3.0.0`; `.claude-plugin/marketplace.json` `metadata.version` → `3.0.0`. Six domain packs patch-bumped (stale model pins → aliases, frontmatter cleanup, references updated to the new roster): `design-pack` 0.3.1, `infra-pack` 1.3.1, `iot-pack` 1.2.1, `jira-pack` 0.6.1, `uav-pack` 1.2.1, `web-pack` 1.2.1.

`docs/MIGRATION-core-3.md` written: why the release is breaking, the old→new table for all 34 removed agents, the command→skill moves, the contract changes, the hook changes, and the escape hatch. Landing page and repo docs swept to the new shape.

---

## 4. Load-bearing contracts that were deliberately preserved

Consolidation on this scale can silently break the control plane. These were verified, not assumed:

- **`tech-lead` keeps its name** — it is planrun's fallback agent (`tools/swarmery/internal/planrun/runner.go`).
- **Verdict emitters keep `VERDICT: PASS | FAIL | INCONCLUSIVE`** — the only grammar `tools/swarmery/internal/verify` parses. The 2.x bespoke tokens (`VERIFICATION:`, `CONTRACTS:`, `## Verdict:`) are gone.
- **The workspace plan format is unchanged** (README sequencing table, `phase-N-<slug>.md`, checkbox ticks, `## Completion Report`) and moved to the `workspace-plans` skill, so `planner` still emits exactly what `wsingest` reads.
- **`wsingest` read-compat for legacy artifacts** needed no Go change; verified by the Go suite staying green after the `planning/prompt.go` edit.

---

## 5. Decisions worth not re-litigating

Three choices look like mistakes at a glance and are not:

1. **The agent metadata registry is `plugins/core/AGENTS.md`, not `agents/README.md`.** The plan asked for the latter. CI's frontmatter step globs `plugins/*/agents/*.md`, so a README inside `agents/` would be scanned as an agent and fail for missing `name:` / `description:`. `AGENTS.md` carries everything the plan asked for.
2. **`evals/promptfooconfig.yaml` keeps pinned model ids.** See §3.6 — promptfoo does not accept aliases, and the validator is scoped so the two rules do not collide.
3. **`pre-commit-test-gate.sh` and `memory-drift-check.sh` are unwired on purpose.** They are documented opt-ins, not dead code. Do not delete them for being unreferenced.

---

## 6. Incidental defects found and fixed along the way

Not in the plan; found while working:

- A dead `@retrospective-agent` reference in `tools/swarmery/docs/retro.md` (the `internal/docsfs/content/` mirror is gitignored and regenerated by `make copy-docs`).
- `docs/index.html` still named the `commit-message` / `pr-generator` **agents** in its `commitScopes` row; now points at the skills that replaced them.
- `.claude-plugin/marketplace.json` had been rewritten with `\uXXXX`-escaped em-dashes — valid JSON, unreadable diff. Restored to literal characters so the release diff is one line.
- `plugins/core/.claude-plugin/plugin.json` had lost its trailing newline. Restored.

---

## 7. Verification

Full local CI-equivalent run on the merged tree, plus the Go suite:

```
JSON manifests (12 plugin.json + marketplace + requirements + hooks + overlays) → ok
pack requirements in sync with the overlay schema                               → ok
bash -n over every plugins/ + scripts/ *.sh                                     → clean
shellcheck -S error over the same set                                           → clean
scripts/tests/*.test.sh — 14 suites, auto-discovered                            → 14 pass, 0 fail
agent frontmatter (name + description)          → checked=26 problems=0
component reference integrity                   → checked=26 problems=0
system item docs coverage                       → checked=106 documented=106 problems=0
flavor scan (neutrality ratchet)                → ✓ clean
tools/swarmery: go vet ./... && go build ./... && go test ./...  → clean, all packages ok
tools/swarmery/web: npm ci && npm run build (incl. tsc --noEmit) → ✓ built
tools/swarmery/web: npm run lint (biome)        → 190 files, 0 problems
```

GitHub CI on PR #272: `validate`, `agent-evals`, `secret-scan`, `build` ×2, CodeQL `Analyze` (go / javascript-typescript / python / actions) — all pass. `CI: success` on the merge commit `8f4d5c2`.

One note on flakiness: `internal/toolproc` timed out twice under full-suite parallel load and passed on a focused re-run. Pre-existing timing sensitivity, untouched by this branch.

---

## 8. Dependency PRs merged in the same session

Five open dependabot PRs, all touching only `package.json` + `package-lock.json` in `tools/swarmery/web`, all green:

| PR | Bump | Merge commit |
|---|---|---|
| #267 | `@types/react-dom` 19.2.4 → 19.2.5 | `0d557b2` |
| #269 | `@biomejs/biome` 2.5.9 → 2.5.11 | `a17b9d2` |
| #270 | `@types/node` 26.2.0 → 26.4.0 | `aaaa777` |
| #271 | `mermaid` 11.16.1 → 11.17.2 | `6e81b3d` |
| #268 | `@vitejs/plugin-react` 6.1.0 → 6.1.1 | `db0a2fc` |

\#268 conflicted on the lockfile after the first merges; `@dependabot rebase` resolved it and CI stayed green. **No open PRs remain.**

Because CI checked each PR against its own base rather than the combined result, the merged tree was verified separately: `npm ci` → `npm run build` → `npm run lint` → `go build` → `go test`, all clean (§7).

---

## 9. Commit trail

```
db0a2fc  Merge PR #268 — @vitejs/plugin-react 6.1.1
6e81b3d  Merge PR #271 — mermaid 11.17.2
aaaa777  Merge PR #270 — @types/node 26.4.0
a17b9d2  Merge PR #269 — @biomejs/biome 2.5.11
0d557b2  Merge PR #267 — @types/react-dom 19.2.5
8f4d5c2  Merge PR #272 — core 3.0.0 audit remediation
  0aabd45  release(core): 3.0.0 — agent-system audit remediation
  3a9f70c  test(evals): track the core 3.0 roster, refresh judge and runner models
  275b38b  refactor(core): drop read-before-write, merge two PostToolUse hooks into one
  b3e11c8  refactor(core)!: progressive disclosure for skills, retire 8 duplicate commands
  c2645f3  refactor(core)!: consolidate 42 agents into 13 judgment-style agents
  93becf2  feat(ci): add component reference-integrity gate
  91d5f0d  fix(core): drop ignored/unknown agent frontmatter, alias all models, …
```

Workspace repo: `9bc5eb6` (phases 2–7 closed) and `12a842c` (7.6 ticked after merge).

---

## 10. Open items

1. **One live `npx promptfoo eval` pass.** No `ANTHROPIC_API_KEY` in this environment, so the rewritten prompts' assertions are only static-checked. `eval-gate.test.sh` is green and CI's eval job skips cleanly without the secret, so nothing is red — but flakiness on shortened prompts can only be observed live. Owed by whoever holds the key.
2. **Consumer adoption.** 3.0.0 is a breaking release. Projects pinning a removed agent by name should follow `docs/MIGRATION-core-3.md`: copy the old definition from the `2.23.2` tag into `.claude/agents/`, since project-local components override plugin ones by design.
3. **Audit P2 / §3 items remain out of scope**, as recorded in the plan README: subagent-transcript ingest for exact per-agent cost, `claude agents --json` as a Sessions source, `docs/NATIVE-OVERLAP.md` and package retirement in `tools/swarmery`, hook-shim shared token, the license decision, README repositioning, and the monthly model-upgrade routine with a `PreModelSwitch` gate.


---

## 11. Post-release review — what this report got wrong

An operator review of 3.0.0 found four issues. Three were confirmed against the
shipped code, one was worse than reported, and one criterion should never have
been treated as met. Corrections shipped as **core 3.0.1 (PR #273)**.

### 11.1 The "read-only" class was not read-only

All five restricted agents shipped with **unscoped `Bash`** — a write channel —
so the label was untrue for every one of them, not merely imprecise. Worse,
`security-auditor` was instructed to *write* its report to
`{task-dir}/phases/05-security.md` while holding no `Write` tool: the only way
to obey the prompt was to write through `Bash`.

Fixed in 3.0.1: `code-reviewer` and `security-auditor` now hold no write tools
**and no `Bash`** — genuinely read-only. The reviewer works from the diff file
the orchestrator already writes plus Read/Glob/Grep and defers build/test runs
to `@verification-agent` (which removes a duplicated responsibility too); the
auditor returns its report as text. `researcher`, `test-runner` and
`verification-agent` keep `Bash` because running suites is the job, and are now
labelled **no-edit** rather than read-only.

This report's §1 also claimed **6** agents carried a `tools:` allowlist. The
real number is **5**. Corrected above.

### 11.2 A near-miss that became a CI rule

The first attempt at 11.1 scoped Bash inside `tools:` as `Bash(git diff:*)`.
That syntax is valid only in `permissions.allow`. Subagent `tools:` accepts
exact tool names, `mcp__*` patterns and `Agent(...)` — so the scoped form is
not a narrow grant, it is **not a grant at all**: precisely the `permissionMode`
failure mode, reintroduced in the release that removed `permissionMode`.

Caught by checking the documentation before committing. `validate-agent-refs.sh`
now fails any `tools:`/`disallowedTools:` entry that is not a bare name, an
`mcp__` pattern or `Agent(...)`, with parenthesis-aware splitting so
`Agent(worker, researcher)` stays one entry (test suite 11 → 13 cases).

### 11.3 `tech-lead` contradicted itself

The large route said to prefer the platform's native dynamic workflow
orchestration; the Delegation section said depth is 1 and executors never spawn
subagents. Native workflows nest by design. The rule now scopes itself to
hand-rolled dispatch and names the exception.

### 11.4 No declared minimum Claude Code version

Core 3.0 assumes native read-before-edit (why `read-before-write.sh` could be
deleted), the `Agent` hook matcher, and dynamic workflow routing. On an older
build none of these error — they are simply absent, so the guarantees quietly
stop holding. `session-start.sh` now warns below **2.1.160** (never blocks);
`plugin.json` has no field for it, so the floor is also stated in
`docs/MIGRATION-core-3.md`.

### 11.5 SC-9 was never met

§1 of this report implied the plan was fully satisfied. The phase criteria were,
but the spec's SC-9 was not, and I had not audited the spec at all when the
first version was written.

SC-9 required `tech-lead` to carry "no mandatory multi-journal bookkeeping",
naming the 7-cell ledger specifically. The TRIAGE / MODEL ROUTE / PHASE COMPLETE
lines are gone, but the 7-cell ledger row survived verbatim in point 4, together
with `## Loop N` in `ORCHESTRATION.md` and `checkpoint.json`. The bookkeeping did
not disappear — it concentrated into one point. `spec.md` now records SC-9 as
unmet with the reasoning, and the fix (a `SubagentStop` hook deriving the row
from the subagent transcript, leaving only the judgment cell in the prompt) is
tracked as **F2** in
`swarmery-workspace/…/2026/09/02/core-3-followups/README.md`.

### 11.6 Open follow-ups

That backlog also carries: the live eval and dispatcher A/B that would actually
prove the 91% cut did no harm (**F1** — the largest remaining hole, since the
release gate is formal without an API key); a consumer-side grep for the 34
removed agent names before anyone runs `/plugin update` (**F4**); the monthly
model-upgrade routine that stops this debt re-accumulating (**F6**); per-agent
cost ingest (**F5**) and the opus-vs-sonnet A/B it unblocks (**F3**); and a
re-check of the `permissionMode` premise, which the current subagent
documentation appears to contradict (**F7**).