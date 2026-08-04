# Concepts

What the dashboard's vocabulary means, and what to do about each thing. Short definitions of the same terms appear in the UI itself behind the `?` and `!` buttons; this page is the long form.

`?` marks reference material. `!` marks a state that is asking you to do something.

## Handoff

When a live session's context footprint crosses **150k tokens**, the daemon spends one cheap model run writing a continuation brief — goal, current state, files touched, key decisions, next step — to `~/.swarmery/handoffs/<session_uuid>.md`. The session's card then shows a violet `Handoff` chip.

This is a parachute, not an alarm. Nothing is broken; the daemon has simply made it cheap for you to start over on a clean context whenever you want to.

**Why it exists.** At a near-full window every further turn re-reads almost the whole window. A long session does not just get slower — it gets monotonically more expensive per unit of work, and the model's attention is spread across a lot of history that stopped being relevant hours ago.

**Where the brief comes from.** The generator reads the daemon's **own SQLite database**, never the raw transcript. Feeding a 400k-token transcript to a model to explain why a 400k-token session is expensive would make the cure cost as much as the disease. It takes a bounded digest instead: the most recent assistant and user turns, truncated, plus the touched-file list.

**Using it.** Open the session, expand the rail's `handoff` section, read the brief, press *copy resume command*, then stop the session and paste the command into a fresh terminal. Commit or stash first: the brief describes the state of your work, it does not save it.

**Mechanics.**

| Setting | Value |
|---|---|
| Trigger | newest assistant turn's `tokens_in + cache_read + cache_write` ≥ 150k |
| Liveness | only sessions whose newest turn ended within the last 2 hours are candidates |
| Regeneration | not until the context grows another 75k past the last brief's footprint |
| Per-tick cap | 3 briefs per 30-minute tick; overflow is counted and logged, never silently dropped |
| Model | `SWARMERY_HANDOFF_MODEL`, default `claude-sonnet-5` |
| Off switch | `SWARMERY_HANDOFF=off` |

The regeneration gate matters: without it, a still-open fat session would pay for a new brief every tick forever.

## Context footprint

The `Nk ctx` chip on a session row is how full the model's window was on the newest assistant turn — input tokens plus cache reads plus cache writes. It is the single best predictor of what a session is costing you.

The chip is deliberately absent below 150k: a healthy context is not news. It appears **amber at 150k** and turns **red at 300k**, the same danger line the advisor's R9 fat-session rule uses.

At amber, `/compact` or splitting the work is usually enough. At red, every further turn re-reads a near-full window, and a restart is the cheap option rather than the drastic one — expect a [Handoff](#handoff) brief to already be waiting, since the two share the 150k trigger. Structurally, the fix is to stop planning one marathon session and start planning several short ones.

## Orphaned / dead

**orphaned** — the transcript is still being written but the daemon can no longer see a process that matches it. **dead** — the process is gone.

Either way, nothing is advancing this session, and it will sit in the list looking almost alive until you close it out.

**What to do.** If the terminal that started it is still open, reattach there. Otherwise close it out from the row's action button — but note that which button you get is decided by how recently the session was active, not by the badge. A freshly orphaned row still reads as live and offers **Stop**; only once it has gone quiet long enough to read as stuck does it offer **Kill**. (The kill endpoint accepts an orphaned session throughout; the button is simply not the one on offer.) A dead row offers neither: it shows a dimmed `exited` label, because there is no longer a process to signal. If the work matters, resume from the session's [Handoff](#handoff) brief rather than trying to revive the process.

## Stop vs Kill

Two different promises. The difference is **which row status you end up with**, not how hard the signal hits.

**Stop** is the graceful sibling. It records the session as **completed** and it succeeds *even with no known PID*, which is what lets it close out a zombie row that Kill cannot touch. When the process is alive and provably the same `claude` process, Stop sends `SIGTERM` and escalates to `SIGKILL` after the grace period; when the identity guard cannot confirm the process, it silently downgrades to marking the row. `completed` is **not terminal** in ingest — a stopped session that later produces transcript records legitimately resurrects to active.

**Kill** requires a known PID and a `running` or `orphaned` process state, and it refuses outright if the PID no longer belongs to a `claude` process or its start time does not match (PID reuse). A plain Kill also sends `SIGTERM` with the same escalation — the "immediate" path is the explicit **Force kill**, which the button offers after 10 seconds and which sends `SIGKILL` straight away with no escalation timer. Kill records the row as **killed**, and unlike `completed`, `killed` is terminal: neither procwatch nor ingest ever reverts it.

Prefer Stop. The grace period is what lets an agent finish the file it is halfway through writing, and `completed` leaves the row honest if the session turns out to still have life in it.

| | Stop | Kill |
|---|---|---|
| Recorded status | `completed` (revertible) | `killed` (terminal) |
| Needs a PID | no | yes |
| Allowed proc states | any unfinished session | `running`, `orphaned` |
| Default signal | `SIGTERM` → `SIGKILL` after the grace | `SIGTERM` → `SIGKILL` after the grace |
| Immediate `SIGKILL` | — | via *Force kill* |
| Grace period | `SWARMERY_KILL_ESCALATION`, default 5s | same |

On a session row the dashboard picks one: a stuck row whose process is confirmed alive keeps the hard Kill; any other live row gets Stop.

## Attach / Detach

**Attach** wires a project into swarmery: merged entries in the project's `.claude/settings.json` (`enabledPlugins`, the swarmery marketplace, the two `AGENT_*` env vars, the workspace `additionalDirectories` entry), `project.json`, the opt-in statusline, and the hooks the control plane needs. It **merges** — it adds only what is missing and never overwrites a foreign value.

**Detach** is the inverse. It prunes exactly the keys and values onboarding wrote and leaves every other key intact, never deletes the file, and is idempotent: a second run finds nothing to remove. Its `Full` variant also removes `.claude/project.json` and the statusline scripts. Project-local components (`agents/`, `skills/`, `commands/`) are never touched — those are yours.

Both write a `.bak` before any real write (`settings.json.bak`, and `project.json.bak` under a full detach), and Attach restores `project.json` from that backup when the file itself is gone — an existing file always wins over the backup.

Both also run a **dry run first** and show you every file that would change before anything is written. Lines beginning with `!` in that preview flag foreign values the merge refused to overwrite — a conflicting `env.AGENT_PROJECT`, a custom `statusLine` where swarmery's would go, a key with an unexpected shape. The tool will not clobber something you configured by hand, so those are yours to resolve.

Detach is fenced to the `SWARMERY_ONBOARD_ROOTS` allow-list; a project outside it shows the action as unavailable with that reason.

## Session outcome

Your verdict on a finished session: **success**, **fail**, or **abandoned**. It is set by hand and never inferred, which is exactly what makes it worth trusting in the analytics. Clicking the already-selected chip clears the verdict back to unset.

Use *abandoned* for work you walked away from. It is a different signal from *fail*: one says the approach did not work, the other says you never found out. Analytics treats them differently too — `abandoned` sessions are excluded from success-rate denominators rather than counted as failures. Sessions left unlabelled are invisible to outcome analytics entirely.

## Playbooks

A playbook is a selectable execution recipe: an ordered chain of stages, each run as its own headless pass, all sharing the task's single [worktree](#task-worktree). A playbook is a markdown file — frontmatter plus one or more `## Stage:` sections.

1. **Pick a recipe on the board.** Every board task can select a playbook in its drawer or at quick-entry. No selection means the `standard` recipe — a single implementation pass at the normal verify bar, identical to pre-playbook dispatch.
2. **Stages run sequentially.** `{task_prompt}` injects the task text and `{previous_stage_output}` hands one stage's full reply to the next. That is how `plan-first` feeds its plan into the implementation stage, and how `review-heavy` critiques the diff it just produced. The full set of template variables is `{task_prompt}`, `{previous_stage_output}`, `{start_point}`, `{branch}`, `{task_id}` and `{file_scope}`. An unrecognised one is rejected at parse time, so a typo fails on load instead of reaching a live prompt as literal `{typo}` — but only when it *looks* like a variable. Validation treats a brace pair as a placeholder solely when it wraps a bare lowercase `[a-z0-9_]` token, which is what lets prose and JSON braces through untouched; the cost is that `{Typo}` is read as prose and passes silently.
3. **The verify knob sets the bar.** See [Verify level](#verify-level).
4. **Make it your own.** Four built-ins ship inside the daemon and are read-only: `standard`, `quick-fix`, `plan-first`, `review-heavy`. *Duplicate to project* copies the markdown into `<project>/.claude/playbooks/`, where the prompts become editable; a project file with the same name overrides the built-in — the graduation rule, same as any other component. Frontmatter takes `name`, `description`, `verify`, and an optional `model` override.

## Verify level

How hard the trajectory verifier judges a stage's work. Declared per playbook in frontmatter; omitted means `normal`.

The verifier's contract is fixed at every level: it is **read-only** (it may build, test and read, but must not edit files or mutate git state), behavioral criteria default to FAIL unless it can confirm the behavior by running something, and it must end on `VERDICT: PASS | FAIL | INCONCLUSIVE`. The knob moves exactly **one line** of the prompt — the bar, never the verdict vocabulary.

- **strict** — adds a tightening clause: every acceptance criterion must be positively demonstrated rather than merely plausible, and any un-run behavioral check, any change outside the declared scope, or any ambiguity is a reason to withhold PASS. Use it where a false pass is the expensive error, such as a review stage. `review-heavy` uses it.
- **normal** — the default bar. The tightening clause is simply absent.
- **off** — the verification run is skipped entirely, before a prompt is ever built, and **no verdict is stamped at all**. "off" does not mean "passed"; it means nobody looked, and the task keeps whatever verdict it already had.

## Task worktree

Each board task gets one git worktree on its own `swarm/<task-id>` branch, and every stage of its playbook runs in that same worktree — which is what lets a later stage see an earlier stage's edits. Worktrees live under `~/.swarmery/worktrees/<project-slug>/<task-id>` by default.

The branch is always cut from an **explicit start point**, never from whatever HEAD happened to be — a task's diff is therefore always meaningful against a known base.

Two refusals are deliberate:

- If the task branch is already checked out in another worktree, dispatch fails with a busy-branch error instead of silently renaming anything. Conflicts are yours to resolve.
- A worktree path that would sit inside the repo root — in either direction, symlinks resolved — is refused outright. A task never gets handed the repo root.

Reuse is conservative in the same spirit: an existing worktree is reused only when its branch matches, recreated on a *proven* mismatch, and never destroyed because a probe happened to fail. Before acquiring, a stale `index.lock` older than 10 minutes is swept, so one crashed run does not block every future one.

## Planning Mode

A headless planner interviews you one question at a time and writes a phased plan into the private workspace.

1. **Describe the idea.** A headless planner session starts in this project's repo — it sees the code, `CLAUDE.md`, and the core-pack planning agents.
2. **Answer structured questions.** One question at a time; pick an option or write your own, while the running plan rebuilds beside it after every answer. If a reply fails the structured-protocol parse, the page falls back to showing the raw prose with a free-text box that answers through the same endpoint, so the interview never dead-ends.
3. **Refine or proceed.** «Уточнити» steers the plan and the questions that follow; «Продовжуйте за планом» ends the interview and the planner writes the full plan.
4. **The plan lands in the workspace.** A `plan/README.md` (objective, real file paths, phase sequencing table, risks, Definition of Done) plus `phase-N` docs with acceptance checkboxes are written to the private workspace — never into the repo — and appear on the Plans page within seconds.

---

Plugin and marketplace mechanics — how an enabled pack physically reaches a session, why a semver
bump is mandatory for consumers to adopt a change, and what `--plugin-dir` is for — live in
[PLUGINS.md](PLUGINS.md#how-a-plugin-reaches-your-session).

The two routes behind a pack row's `configure` modal — what the save writes into
`.claude/project.json`, what the probe may and may not do, and the fence both share — live in
[api-project-config.md](api-project-config.md).
