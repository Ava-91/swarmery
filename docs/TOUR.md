# The dashboard — a screen-by-screen tour

This is the long-form companion to the [README](../README.md): every screen in the
Swarmery dashboard, in the order you meet them — the fleet scope first, then the
per-project workspace. None of it is required reading to install or run swarmery;
the README covers that in two commands.

![Command deck](screenshots/overview.png)

---

## The dashboard

The UI has two scopes, switched in the header: **Sessions** — the fleet view across every
project — and **Projects** — a per-project workspace with its own board, plans, and terminal.

### Fleet · Command deck

The landing view is built around a number most tooling never shows you: **how long your agents
spent waiting on a human**. The hero says it outright — how much they waited today, and which
handful of tools caused most of it.

Under it, a three-cell ledger splits that time into *waited*, *auto-approved*, and *still
blocked*. Then **The day** rasterises each project into a lane of working / waiting / idle
segments, so a morning lost to one prompt nobody saw becomes a visible gap rather than a vague
feeling that things were slow.

**Where your time went** groups today's approvals by tool, and every row is one click from a
**stop asking** control that writes an auto-approve rule on the spot — the deck is both where
you notice a recurring prompt and where you kill it. Below that runs the spine of today's
notable sessions, next to a sticky rail of what is blocked on you and what is erroring.

Nothing here is a new metric pipeline: wait time is `resolvedAt − requestedAt` over permission
requests the daemon already stores (live-aged for anything still pending), and auto-approved
rows are the ones a rule resolved. On a day when nothing waited on you, the wait sections say
exactly that instead of showing a decorative zero.

<div align="center">
  <a href="screenshots/overview.png"><img src="screenshots/overview.png" width="720" alt="Command deck — wait-time hero, per-project day lanes, and the blocked rail"></a>
  <br><sub><i>The hero reads the whole day in one sentence; the right rail is what still needs you.</i></sub>
</div>

### Fleet · Sessions

Every session the daemon has indexed, newest first, grouped under day rules (`today · wed, jul
29`). The status chips carry live counts — `active`, `waiting`, `idle`, `done`, `killed` — and
filter client-side, so the counts keep describing the whole loaded list rather than the slice
you are currently looking at. Project scope comes from the header switcher; text search is in
the page, or `⌘K` for the global palette.

A row is a status dot, the project, the title, the model, and a chip. What makes it useful
while work is happening is the line under the title: for a live session it reads
`now: <what the agent is doing this second>`, pushed over WebSocket as events land — for a
finished one, why it ran in the first place.

The chip separates states that most dashboards flatten into one. A working session shows
elapsed time; a **stuck** one shows *quiet time* — how long the transcript has been silent —
because `working · 17 h 32 min` was a lie worth deleting. Two more chips appear only when they
have something to say: a context badge (amber past 150k tokens, red past 300k — a fat context is
the thing that quietly multiplies cost, since every turn re-reads it) and a purple **Handoff**
badge once the daemon has written a continuation brief for a session that got too heavy.

Live rows offer a graceful **Stop**; a stuck one with a confirmed-alive process gets a hard
**Kill**.

<div align="center">
  <a href="screenshots/sessions.png"><img src="screenshots/sessions.png" width="720" alt="Sessions list grouped by day with live status chips"></a>
  <br><sub><i>Chips filter and count, day rules group, and the <code>now:</code> line under each title updates live.</i></sub>
</div>

### Fleet · Session detail — Chat · Timeline · Diffs

Opening a session gives you the entire run in three tabs, under a header that stays pinned while
the content scrolls: live tokens, cost, error count, and the same working/quiet chip the list
speaks.

**Chat** replays the run as a conversation — your turns as bubbles, the agent's as prose. Runs of
tool activity collapse into one unobtrusive line (*"Ran 2 agents, ran 4 commands, used a tool"*)
that expands inline into the real timeline rows, nested sub-agents included. The reading order
stays human, but nothing is actually hidden. A composer at the bottom resumes the session: your
message appears immediately and reconciles against the real turn once it is ingested — and flips
to a retry affordance in place if the send died.

**Timeline** is every tool call with its duration and pass/fail. **Diffs** aggregates every file
the session touched into unified diffs with `+`/`−` counters, grouped per file.

On a wide screen the right rail turns the run into numbers without issuing a single extra
request — all of it derived from the detail already loaded: models by share of tokens, agents by
runs × duration, skills by uses, each with a bar scaled against the group's top entry. Then the
call tree of who invoked what (skills → tools → sub-agents, recursive), and every file touched
ranked by churn.

<div align="center">
  <a href="screenshots/session-detail.png"><img src="screenshots/session-detail.png" width="720" alt="Session detail — Chat tab with the usage rail"></a>
  <br><sub><i>Chat keeps the run readable; the rail turns it into models, agents, skills, call tree and churn.</i></sub>
</div>

### Fleet · Approvals

Every pending permission request and `AskUserQuestion` from every session, in one queue. A card
shows the tool, the essential part of its input (expandable to the full hook payload), which
session it belongs to, how long it has been hanging, and a countdown against the 120-second
window before it expires.

Answer it in place: **Approve**, **Deny** with an optional reason, or jump into the session.
`AskUserQuestion` prompts render as what they actually are — radio groups for single-select,
checkboxes for multi-select, plus a free-text *own answer* box — rather than being flattened into
a yes/no. **Answer in the terminal** is deliberately a separate handoff rather than an approval,
because approving would mark the questions resolved without any of them ever being answered.

Resolved requests fall into a history log that records *how* each one ended — including
`answered in the terminal`, so a decision you made somewhere else doesn't read as a gap.

<div align="center">
  <a href="screenshots/approvals.png"><img src="screenshots/approvals.png" width="720" alt="Approvals queue with pending requests and resolution history"></a>
  <br><sub><i>Pending cards carry the tool input, the owning session, and an expiry countdown.</i></sub>
</div>

### Fleet · Analytics

Cost, tokens, and runs over any day range. The metric switch (`$` / tokens / runs) gates what you
can pivot by, because the two come from different places: dollars and tokens pivot by **project**
or **model** and are computed from turns, while runs pivot by **agent** or **skill** and come
from events.

The headline names the top driver, the biggest mover since the previous window, and a projected
monthly figure. Under it: a stacked trend whose legend doubles as the include/exclude control
(click a series to drop it), a ranked breakdown for the current pivot, and an agents × projects
cross-tab that transposes when you flip the pivot. KPI tiles cover autonomy (tool calls per human
intervention), cost per task, cache-hit rate, and productivity by language. Everything exports to
CSV.

One honest limitation is stated in the UI rather than papered over: **agents and skills carry run
counts, not dollars.** Claude Code's transcripts don't record sub-agent turns, so a per-agent cost
figure would be invented. Session- and project-level cost is exact.

<div align="center">
  <a href="screenshots/analytics.png"><img src="screenshots/analytics.png" width="720" alt="Analytics — stacked cost trend with ranked breakdowns"></a>
  <br><sub><i>The metric switch decides the pivot; the legend is the include/exclude control.</i></sub>
</div>

### Fleet · Retro

Where the loop stops being a dashboard and starts changing the agents. Per-agent scorecards over
a range — runs, success rate, error rate, cost, p95 — each compared against the previous window,
so you see direction, not just a level. A system-health strip carries orchestrator cost and total
runs/errors above them.

The friction board is the diagnostic half: which tools got denied most (each one click from an
auto-approve rule), the top error groups, and how long approvals kept agents waiting.

Above all of it sits the advisor rail. A deterministic rule engine raises evidenced
recommendations — every one carries the data that triggered it — which you **Accept** or
**Dismiss**, and which then move through `proposed → accepted → adopted → verified` so a
suggestion can't quietly go stale in a list. `Analyze now` re-runs it on demand, and verified
recommendations keep their own history. When a recommendation implies rewriting an agent, you get
the rewrite **as a diff to review** — nothing touches an agent file unapproved. A lessons feed
parsed from your workspace retrospectives runs alongside.

<div align="center">
  <a href="screenshots/retro.png"><img src="screenshots/retro.png" width="720" alt="Retro — advisor recommendations, agent scorecards and the friction board"></a>
  <br><sub><i>Recommendations carry their evidence and a lifecycle, so accepting one is trackable.</i></sub>
</div>

### Fleet · Routines

Scheduled automation with three triggers — cron, webhook, or manual. The list gives each routine
its scope, a human-readable rendering of its cron expression, an enable toggle, last and next run,
and a status dot for how the last one ended.

Editing happens in a drawer with a typed step builder: a step is a `command`, an `ai-prompt`, or a
`create-task`, and the cron field validates as you type. Any routine can be run on demand, and
expanding one shows its (pruned) run history. The global project scope filters the list the same
way it filters Retro and Analytics.

<div align="center">
  <a href="screenshots/routines.png"><img src="screenshots/routines.png" width="720" alt="Routines list with cron schedules and run history"></a>
  <br><sub><i>Cron, webhook or manual; steps are typed, and the schedule is rendered back in English.</i></sub>
</div>

### Fleet · System

The answer to *"which agent is actually going to run here, and where did it come from?"* — the
entire Claude config graph on the machine, across all three scopes that can define it: global
(`~/.claude`), project (`.claude/`), and the plugin cache.

Tabs for Agents, Skills, Hooks, Commands and Templates (each deep-linkable via `?tab=`), with
origin and scope badges on every item, usage stats, and lint severity badges in the header that
double as filters — click a warning count to see only what triggered it.

Because config drift is the failure mode here, every item keeps a **content-addressed version
history**: you can diff any two versions of an agent and see exactly what changed between them.
The page is live — when the daemon re-scans, the open tab refetches itself.

<div align="center">
  <a href="screenshots/system.png"><img src="screenshots/system.png" width="720" alt="System registry — agents, skills, hooks and commands by scope"></a>
  <br><sub><i>Every agent, skill, hook and command on the machine, with scope, origin and version history.</i></sub>
</div>

### Fleet · Projects

Every project the daemon knows about, split by the distinction that matters operationally:
**managed** (the swarmery plugin is enabled in its `.claude/settings.json`) versus
**telemetry-only** (sessions are indexed, but the project never opted in). Each row carries
lifetime sessions, tokens and spend, plus last activity.

Pinned projects sort first and every row has a pin toggle; tags are editable inline and the chip
row filters by them. Row actions cover archive, restore, detach and tagging. Below the list, a
week-over-week health table compares each project against its own previous week.

<div align="center">
  <a href="screenshots/projects.png"><img src="screenshots/projects.png" width="720" alt="Projects list with managed/telemetry flags and lifetime totals"></a>
  <br><sub><i>Managed vs telemetry-only, lifetime spend, and a week-over-week health table underneath.</i></sub>
</div>

---

### Project workspace · Overview

Switch to a project and the whole app re-scopes — its own board, plans, memory and terminal. The
project home opens with a hero sentence pairing what actually shipped this week against how often
the agents had to stop and ask you, which is the ratio that decides whether delegation is working.

Below it: right-now tiles (what is running, blocked, in flight, or leaking stale worktrees),
week-over-week deltas, a funnel showing where work really sits, and open insights.

The capability inventory at the bottom answers a question that is otherwise guesswork — which
agents, skills, commands and hooks this project *actually uses*, as opposed to which ones are
installed — with per-agent error rates and an autonomy badge. Telemetry-only projects hide that
section entirely rather than showing an empty shell.

<div align="center">
  <a href="screenshots/project-overview.png"><img src="screenshots/project-overview.png" width="720" alt="Project overview — shipped-vs-asked hero, right-now tiles and capability cards"></a>
  <br><sub><i>Shipped this week vs. how often you were interrupted — then what is running right now.</i></sub>
</div>

### Project workspace · Board → worktree → agent

A six-column board: Triage → Todo → In Progress → In Review → Done → Archived. Drag and drop is
native, and a column drop issues an optimistic update that reverts with a toast if the server
refuses it — but every card also carries a keyboard **move to →** menu, so dragging is never the
only path. Quick entry sits at the top of Triage, Done sorts by when it got there, and Archived
loads lazily on first expand to keep the default view light.

Moving a card into **Todo** is the part that does real work. The dispatcher wakes, acquires a
dedicated `swarm/<task-id>` git worktree so the agent can never collide with your working tree,
runs a headless `claude -p` session under that task's playbook, and hands the result to the
verifier — which grades it against the task and stamps `PASS` / `FAIL` / `INCONCLUSIVE` before
the card lands in review.

<div align="center">
  <a href="screenshots/board.png"><img src="screenshots/board.png" width="720" alt="Kanban board with six columns and running task cards"></a>
  <br><sub><i>Todo is the trigger: worktree, headless agent, verification, then In Review.</i></sub>
</div>

### Project workspace · Planning Mode

Describe what you want to build, in prose. A planner session takes the idea and interviews you
through a wizard — one question at a time on the left, the plan as it currently stands sticky on
the right, where you can refine it or tell the planner to proceed. While it is thinking you get
elapsed time and its last reasoning snippet rather than an opaque spinner.

The output is a structured plan written into your private workspace, which then shows up on the
Plans page. If a planner reply ever fails to parse against the wizard protocol, the page shows the
raw prose plus a free-text box that answers through the same endpoint — a malformed reply degrades
into a conversation instead of dead-ending the session.

<div align="center">
  <a href="screenshots/planning.png"><img src="screenshots/planning.png" width="720" alt="Planning Mode wizard — question card beside the running plan panel"></a>
  <br><sub><i>Question on the left, the plan so far on the right — refine or proceed at any point.</i></sub>
</div>

### Project workspace · Plans

A workspace plan *is* an epic. The markdown on disk stays the source of truth — this page is the
control surface over it, so the workspace folder becomes infrastructure you never have to open.

Epics filter by Active / Done / Archived and drill into a phase timeline in sequence order, with
depends-on badges between phases and per-phase progress derived from the acceptance-criteria
checkboxes in the doc itself. Lifecycle controls (pause / resume / archive / restore) are real
file operations on the daemon side, not database flags.

Both the plan and each phase open into tabbed detail — **Phase** (run state, interactive criteria,
the full doc), **Summary** (completion report, ticked criteria, execution record), **Edit** (raw
markdown). Ticking a criterion in the UI patches that exact `- [ ]` ↔ `- [x]` line in the file.

Work runs from here two ways: a single phase executes headlessly from its own doc, or the **whole
plan** goes to one agent via `Run plan` with an agent and mode picker. While a plan-level run owns
the docs, the per-phase Run buttons stand down and progress surfaces as checkboxes ticking
themselves — which is the honest signal, since that is literally what the agent is doing.

<div align="center">
  <a href="screenshots/plans.png"><img src="screenshots/plans.png" width="720" alt="Plans page — epic list with phase timeline and depends-on badges"></a>
  <br><sub><i>Phases in sequence with their dependencies; progress comes from real checkboxes in the markdown.</i></sub>
</div>

Each phase carries its own acceptance criteria and a self-contained agent prompt, and — once it
has been executed — a completion report plus the execution record appended to the doc.

<div align="center">
  <a href="screenshots/plan-phase.png"><img src="screenshots/plan-phase.png" width="720" alt="Phase detail with acceptance criteria and completion report"></a>
  <br><sub><i>Criteria are clickable: ticking one rewrites that line in the phase document.</i></sub>
</div>

### Project workspace · Playbooks

A playbook is the recipe a dispatched task runs under, and this page makes it visible instead of
implicit. Selecting one renders its stage chain — boxes joined by arrows — showing which model
runs `plan`, `implement`, `verify` and `review`, and the exact prompt each stage receives.

Four built-ins ship with the daemon and stay read-only. **Duplicate to project** copies the
markdown into `<project>/.claude/playbooks/`, at which point the prompts are yours to edit — the
same graduation rule the marketplace uses, applied to execution recipes. (Visual authoring of the
graph is deliberately not built; you edit the file.)

<div align="center">
  <a href="screenshots/playbooks.png"><img src="screenshots/playbooks.png" width="720" alt="Playbook stage chain with per-stage prompt preview"></a>
  <br><sub><i>Which model runs each stage, and the literal prompt it gets — before you dispatch anything.</i></sub>
</div>

### Project workspace · Architecture

The project's architecture map (`/architecture-map` from `architecture-pack`) embedded in the
dashboard — modules by layer, named end-to-end flows, API surface — instead of living as an HTML
file you forget you generated.

A staleness badge compares the commit baked into the map against current `HEAD`, so you always
know whether you are reading the repo or a memory of it, and a rebuild runs headlessly from the
page. The project dropdown lists anything with `architecture-pack` enabled *or* an existing
artifact, so a pack-enabled project that has never generated a map still appears — and can be
built from here.

The embedded map ships its own palette and light/dark toggle, which would fight the dashboard; the
app's live design tokens are pushed into the iframe as inline custom properties, so the map
follows your theme instead of choosing its own.

<div align="center">
  <a href="screenshots/architecture.png"><img src="screenshots/architecture.png" width="720" alt="Embedded architecture map with staleness badge"></a>
  <br><sub><i>The staleness badge compares the map's commit against <code>HEAD</code> — rebuild is one click.</i></sub>
</div>

### Project workspace · Memory

Everything a project remembers, in one editor, grouped by the three roots it actually lives in:
project instructions (`CLAUDE.md`), Claude Code's auto-memory, and Serena notes.

Markdown editing with a preview toggle, and two guards that matter when a running agent may be
writing the same files: saving checks a base hash and offers a reload instead of silently
clobbering a change made underneath you, and navigating away with unsaved edits asks first. Files
the daemon reports unwritable — a Serena note, or everything when the read-only kill-switch is
on — are badged rather than failing at save time.

<div align="center">
  <a href="screenshots/memory.png"><img src="screenshots/memory.png" width="720" alt="Memory editor with CLAUDE.md, auto-memory and Serena notes"></a>
  <br><sub><i>Three memory roots, one editor — with a conflict guard for files an agent may be writing.</i></sub>
</div>

### Project workspace · Terminal

Real PTYs docked under the dashboard, opened in the project root or directly in a task's worktree
— so checking what an agent actually did doesn't cost you a context switch out of the page.

Each tab is its own live shell, with font sizing, clear, fullscreen and a drag handle to resize
the dock. Open state, height and the tab list persist per project, so a browser reload restores
the workspace you left. The heavy terminal bundle is lazy-loaded — it is never fetched until you
first open a tab.

<div align="center">
  <a href="screenshots/project-terminal.png"><img src="screenshots/project-terminal.png" width="720" alt="Terminal dock open under the project workspace"></a>
  <br><sub><i>One live PTY per tab, opened in the repo root or in a task's worktree.</i></sub>
</div>
