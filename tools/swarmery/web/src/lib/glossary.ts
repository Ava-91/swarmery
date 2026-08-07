// The single source of explainer copy for the dashboard. <Explain id="…"/> and
// <HowItWorks id="…"/> both read from here; docs/concepts.md carries the long
// form and is kept in lockstep by internal/docsfs/glossary_drift_test.go.
//
// Copy budget, enforced by review: `short` ≤ 220 chars, `actions` ≤ 4 entries.
// Anything longer belongs in the doc behind "Read more →".
//
// `tone` lives here rather than at the call site on purpose — a concept is
// either reference ('explain', renders "?") or a demand for operator action
// ('action', renders "!"), and it must read the same everywhere it appears.

export type Tone = 'explain' | 'action';

export interface Concept {
  /** Display name, also the heading text in docs/concepts.md. */
  term: string;
  /** 1–2 sentences: what this is. Always visible when the popover opens. */
  short: string;
  tone: Tone;
  /** What the operator should do about it. */
  actions?: string[];
  /** Numbered walkthrough — the "how it works" block. */
  steps?: { title: string; body: string }[];
  /** Hard numbers, paths and env vars worth having in reach. */
  facts?: { label: string; value: string }[];
  /** Deep link into the docs pane: /docs/{slug}#{anchor}. */
  doc?: { slug: string; anchor: string };
}

// `satisfies` keeps the literal shape of every entry (that is what makes
// ConceptId and StepConceptId derivable) — but consumers must NOT read the
// literal type, because on a union of entry types an optional field that some
// members do not declare is unreadable. So the widening happens once, here.
const RAW = {
  handoff: {
    term: 'Handoff',
    short:
      'The daemon pre-wrote a continuation brief because this session crossed 150k tokens of context. It is a parachute, not a problem — nothing is wrong with the session.',
    tone: 'explain',
    actions: [
      'Keep going if the session is still sharp — the chip is informational.',
      'Commit or stash your work first; the brief describes state, it does not save it.',
      'Open the rail’s Handoff section, read the brief, then "copy resume command".',
      'Stop the session and run that command in a fresh terminal.',
    ],
    facts: [
      { label: 'threshold', value: '150k ctx' },
      { label: 'regenerates after', value: '+75k ctx' },
      { label: 'written to', value: '~/.swarmery/handoffs/<uuid>.md' },
      { label: 'disable', value: 'SWARMERY_HANDOFF=off' },
    ],
    doc: { slug: 'concepts', anchor: 'handoff' },
  },

  'fat-session': {
    term: 'Context footprint',
    short:
      'How full the model’s window is on the newest assistant turn (input + cache read + cache write). At a near-full window every further turn re-reads almost everything — this is the main cost driver.',
    tone: 'explain',
    actions: [
      'Amber (150k): consider /compact or splitting the work.',
      'Red (300k): every turn is now expensive — restart on a clean context.',
      'Plan several short sessions rather than one marathon.',
    ],
    facts: [
      { label: 'chip appears', value: '≥150k (amber)' },
      { label: 'danger', value: '≥300k (red)' },
      { label: 'handoff trigger', value: '150k' },
    ],
    doc: { slug: 'concepts', anchor: 'context-footprint' },
  },

  'proc-badge': {
    term: 'Orphaned / dead',
    short:
      '"orphaned" means the transcript is still live but the daemon can no longer see a matching process; "dead" means the process is gone. Either way nothing is advancing this session.',
    tone: 'action',
    actions: [
      'Reattach in the terminal where it was started, if that terminal is still open.',
      'Otherwise close it out with whichever control the row offers — Stop while it still reads as active, Kill once it reads as stuck.',
      'If the work matters, resume from the session’s Handoff brief instead.',
    ],
    doc: { slug: 'concepts', anchor: 'orphaned-dead' },
  },

  'kill-vs-stop': {
    term: 'Stop vs Kill',
    short:
      'Both send SIGTERM and escalate to SIGKILL after a grace period. What differs is the status recorded: Stop writes completed, which ingest may revert if the session speaks again; Kill writes killed, which is terminal.',
    tone: 'explain',
    actions: [
      'Prefer Stop — it needs no PID, so it also closes out a zombie row Kill refuses.',
      'Kill needs a live, identity-checked PID in a running or orphaned state.',
      'Only "Force kill", offered 10s after a Kill, sends SIGKILL straight away.',
    ],
    facts: [
      { label: 'both signal', value: 'SIGTERM → SIGKILL' },
      { label: 'grace', value: '5s · SWARMERY_KILL_ESCALATION' },
      { label: 'stop records', value: 'completed (revertible)' },
      { label: 'kill records', value: 'killed (terminal)' },
    ],
    doc: { slug: 'concepts', anchor: 'stop-vs-kill' },
  },

  'attach-detach': {
    term: 'Attach / Detach',
    short:
      'Attach wires a project into swarmery — merged settings entries, project.json, statusline and hooks. Detach is its exact mirror. Both preview the change before writing anything.',
    tone: 'explain',
    actions: [
      'Read the dry-run preview before confirming — it lists every file that would change.',
      'Lines starting with "!" flag foreign values the merge refused to overwrite; resolve those by hand.',
      'Each writes a .bak before rewriting settings.json; a full detach also backs up project.json.',
      'Attach restores project.json from that backup only when the file itself is gone.',
    ],
    doc: { slug: 'concepts', anchor: 'attach-detach' },
  },

  'session-outcome': {
    term: 'Session outcome',
    short:
      'Your verdict on a finished session — success, fail, or abandoned. It is set by hand and never inferred, which is what makes it trustworthy in the analytics.',
    tone: 'explain',
    actions: [
      'Label sessions as you close them; unlabelled sessions are invisible to outcome analytics.',
      'Use "abandoned" for work you walked away from — it is not the same signal as "fail".',
    ],
    doc: { slug: 'concepts', anchor: 'session-outcome' },
  },

  'playbook-stages': {
    term: 'Playbooks',
    short:
      'A playbook is a selectable execution recipe: an ordered chain of stages, each run as its own headless pass, all sharing the task’s single worktree.',
    tone: 'explain',
    steps: [
      {
        title: 'Pick a recipe on the board',
        body: 'Every board task can select a playbook in its drawer (or at quick-entry). No selection means the standard recipe — a single implementation pass, identical to pre-playbook dispatch.',
      },
      {
        title: 'Stages run sequentially',
        body: 'The dispatcher runs each stage as its own headless pass in the task’s one worktree. {task_prompt} injects the task text, and {previous_stage_output} hands one stage’s full reply to the next — that’s how plan-first feeds its plan into implement, and review-heavy critiques the diff it just produced.',
      },
      {
        title: 'The verify knob sets the bar',
        body: 'Each playbook declares strict, normal, or off. The verifier’s read-only contract and its PASS/FAIL/INCONCLUSIVE vocabulary are identical at every level — strict only adds one clause demanding each criterion be positively demonstrated (review-heavy uses it), and off skips the run before a prompt is built, so no verdict is stamped.',
      },
      {
        title: 'Make it your own',
        body: 'Built-ins ship inside the daemon and are read-only. “Duplicate to project” copies the markdown into <project>/.claude/playbooks/ where its prompts become editable; a project file with the same name overrides the built-in. Frontmatter takes name, description, verify, and an optional model override.',
      },
    ],
    // `steps` is not rendered in the popover (see Explain.tsx) — these facts are
    // what the trigger shows, so they carry the reference load on their own.
    facts: [
      { label: 'built-ins', value: 'standard · quick-fix · plan-first · review-heavy' },
      { label: 'no selection', value: 'standard' },
      { label: 'make your own', value: '.claude/playbooks/<name>.md' },
    ],
    doc: { slug: 'concepts', anchor: 'playbooks' },
  },

  'verify-knob': {
    term: 'Verify level',
    short:
      'How hard the trajectory verifier judges a stage. Its read-only contract and PASS/FAIL/INCONCLUSIVE vocabulary are fixed at every level; the knob moves exactly one line of the prompt — the bar.',
    tone: 'explain',
    actions: [
      'Use strict for review stages, where a false pass is the expensive error.',
      'Strict adds one clause: every criterion positively demonstrated, not merely plausible.',
      'Use off only for stages with no reviewable output — it means no verdict, not a passing one.',
    ],
    doc: { slug: 'concepts', anchor: 'verify-level' },
  },

  worktree: {
    term: 'Task worktree',
    short:
      'Each board task gets one git worktree on its own swarm/<task-id> branch. Every stage of the playbook runs in that same worktree, so later stages see earlier stages’ edits.',
    tone: 'explain',
    actions: [
      'If dispatch reports a busy branch, the task branch is checked out elsewhere — resolve it by hand; the daemon will not silently rename.',
      'Never point a task at the repo root; the daemon refuses that outright.',
    ],
    doc: { slug: 'concepts', anchor: 'task-worktree' },
  },

  'planning-mode': {
    term: 'Planning Mode',
    short:
      'A headless planner interviews you one question at a time and writes a phased plan into the private workspace.',
    tone: 'explain',
    steps: [
      {
        title: 'Describe the idea',
        body: 'A headless planner session starts in this project’s repo — it sees the code, CLAUDE.md, and the core-pack planning agents.',
      },
      {
        title: 'Answer structured questions',
        body: 'The planner interviews you one question at a time — pick an option (or write your own) while the running plan rebuilds beside it after every answer.',
      },
      {
        title: 'Refine or proceed',
        body: '«Уточнити» steers the plan and the next questions; «Продовжуйте за планом» ends the interview and the planner writes the full plan.',
      },
      {
        title: 'The plan lands in the workspace',
        body: 'Phase-N docs with acceptance checkboxes are written to the private workspace — never the repo — and appear on the Plans page within seconds.',
      },
    ],
    // As with playbook-stages: `steps` stays out of the popover, so these facts
    // answer the one thing the page subtitle does not — where "the private
    // workspace" actually is.
    facts: [
      { label: 'plans land in', value: '<root>/<project>/workspace/working/' },
      { label: 'root', value: 'AGENT_WORKSPACE_ROOT · default ~/swarmery-workspace' },
      { label: 'plan docs', value: 'yours to edit; swarmery ticks acceptance boxes as phases finish' },
    ],
    doc: { slug: 'concepts', anchor: 'planning-mode' },
  },

  'claude-account': {
    term: 'Claude account',
    short:
      'A Claude Code identity on this machine: one config directory plus one credential. The directory names the account — ~/.claude-work is the account "work"; ~/.claude is the default.',
    tone: 'explain',
    actions: [
      '"+ add account" reserves the key and shows a login command — run it in your own terminal; swarmery never logs in for you.',
      'Use a private browser window for /login if it is a different subscription.',
      'A grey "unknown" dot means the daemon runs with SWARMERY_USAGE_OAUTH=0, not that the account is broken.',
    ],
    facts: [
      { label: 'config dir', value: '~/.claude-<key>' },
      { label: 'default', value: '~/.claude · not removable' },
      { label: 'credential', value: 'written by the Claude CLI, per-dir Keychain item' },
    ],
    doc: { slug: 'concepts', anchor: 'claude-account' },
  },

  'account-binding': {
    term: 'Account binding',
    short:
      'The project-level choice of which Claude account its sessions run under. Resolved once, at process spawn — a run already in flight keeps its account until it finishes.',
    tone: 'explain',
    actions: [
      'Bind here or with /account use <key> in a session; both write the same file.',
      'Changing it never touches a running process — it applies from the next spawn.',
      'Clear the binding to fall back to the machine default account.',
    ],
    facts: [
      { label: 'stored in', value: '.claude/settings.local.json · gitignored' },
      { label: 'unbound project', value: 'no CLAUDE_CONFIG_DIR set — behaves exactly as before' },
      { label: 'covers', value: 'dispatch · verify · planning · terminal dock · shell function' },
    ],
    doc: { slug: 'concepts', anchor: 'account-binding' },
  },
} satisfies Record<string, Concept>;

export type ConceptId = keyof typeof RAW;

/** The registry as consumers see it: every optional field readable on every id. */
export const CONCEPTS: Record<ConceptId, Concept> = RAW;

/** A concept that is guaranteed to carry a walkthrough. */
export type StepConcept = Concept & { steps: NonNullable<Concept['steps']> };

/** The ids whose entry actually declares `steps`. <HowItWorks id="handoff"/> is
 * therefore a compile error rather than a component that silently renders null. */
export type StepConceptId = {
  [K in ConceptId]: (typeof RAW)[K] extends { steps: unknown } ? K : never;
}[ConceptId];

/** The step-carrying subset, so <HowItWorks> reads a non-optional array. */
export const STEP_CONCEPTS: Record<StepConceptId, StepConcept> = RAW;
