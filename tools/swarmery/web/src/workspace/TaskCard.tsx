// Board task card (fusion phase 4): the unit on the board.
// Shows title, the T-xxxx id chip, a priority dot, a model chip (when set), an
// origin badge on cards the capture pipeline minted rather than a human, a
// verdict badge (pass/fail/inconclusive — phase 6 fills verifyVerdict), a
// dispatch-error warning icon with tooltip, a paused badge, and a session link
// glyph when a branch/worktree exists. Clicking the card body opens the TaskModal.
//
// Drag&drop is GONE (board redesign phase 4). It used to be the dispatch
// mechanism — HTML5 draggable on the card, a drop handler per column — and it
// made the one irreversible thing on this board (accepting a card into the
// dispatch queue) the easiest thing to do by accident, while being invisible to
// a keyboard and unusable on touch. Movement is verbs now: each lane renders the
// exits that make sense for a card in it (CardActions below), and the "move
// to →" ColumnMenu stays as the escape hatch covering every remaining legal
// transition.

import type { ReactNode } from 'react';
import type { BoardColumn, BoardTask, TaskOrigin, TaskPriority } from '../api/types';
import { BOARD_COLUMNS, COLUMN_LABELS, labelColor, visibleLabels } from './boardModel';

const PRIORITY_DOT: Record<TaskPriority, string> = {
  urgent: 'bg-red',
  high: 'bg-amber',
  normal: 'bg-ink-faint',
  low: 'bg-line-strong',
};

const VERDICT_STYLE: Record<string, string> = {
  pass: 'border-green/40 bg-green/10 text-green',
  fail: 'border-red/40 bg-red/10 text-red',
  inconclusive: 'border-amber/40 bg-amber/10 text-amber',
};

function VerdictBadge({ verdict }: { verdict: string }): JSX.Element {
  const style = VERDICT_STYLE[verdict] ?? 'border-line text-ink-faint';
  return (
    <span className={`rounded-full border px-1.5 py-[1px] font-mono text-[9px] uppercase ${style}`}>
      {verdict}
    </span>
  );
}

/** Provenance badge for a card the user did not write by hand: capture mints
 * 'session' cards from a session's todos, 'llm' cards from a suggestion, and the
 * verifier mints 'verify-fix' cards when a graded card fails. Manual cards (the
 * overwhelming majority) carry no badge.
 *
 * This Record MUST stay total over the union: OriginBadge destructures the
 * looked-up entry, so a missing key is not a missing badge — it is a TypeError
 * that takes down the whole board render. 'verify-fix' was minted by the daemon
 * long before it was listed here, which is exactly how that happened. */
const ORIGIN_BADGE: Record<Exclude<TaskOrigin, 'manual'>, { label: string; tip: string }> = {
  session: { label: 'from session', tip: 'captured from a session todo' },
  llm: { label: 'suggested', tip: 'suggested by a model, not hand-written' },
  'verify-fix': { label: 'fix', tip: 'Spawned by verification to repair a failed card' },
};

function OriginBadge({ origin }: { origin: Exclude<TaskOrigin, 'manual'> }): JSX.Element {
  const { label, tip } = ORIGIN_BADGE[origin];
  return (
    <span
      data-tip={tip}
      className="rounded-full border border-ink-faint/40 bg-field px-1.5 py-[1px] font-mono text-[9px] text-ink-dim"
    >
      ⟲ {label}
    </span>
  );
}

/** One label chip: a small colored pill, no icon. Color is a pure hash of the
 * label text (see `labelColor`) so e.g. "jira-ticket" always reads the same
 * accent everywhere it appears — stable across renders because nothing but
 * the label string feeds it. */
function LabelBadge({ label }: { label: string }): JSX.Element {
  const hsl = labelColor(label);
  return (
    <span
      className="rounded-full border px-1.5 py-[1px] font-mono text-[9px]"
      style={{
        borderColor: `hsl(${hsl} / 0.4)`,
        backgroundColor: `hsl(${hsl} / 0.12)`,
        color: `hsl(${hsl})`,
      }}
    >
      {label}
    </span>
  );
}

/** Renders a card's label chips: up to `MAX_VISIBLE_LABELS` directly, the rest
 * rolled into a single "+N" chip whose tooltip lists every label. Nothing
 * renders for an empty array — an unlabeled card looks exactly as before. */
function LabelBadges({ labels }: { labels: readonly string[] }): JSX.Element | null {
  if (labels.length === 0) return null;
  const { shown, overflow } = visibleLabels(labels);
  return (
    <>
      {shown.map((l) => (
        <LabelBadge key={l} label={l} />
      ))}
      {overflow > 0 && (
        <span
          data-tip={labels.join(', ')}
          className="rounded-full border border-line px-1.5 py-[1px] font-mono text-[9px] text-ink-dim"
        >
          +{overflow}
        </span>
      )}
    </>
  );
}

/** One size for every card verb. */
const ACTION_BTN =
  'rounded border px-1.5 py-[2px] font-mono text-[9.5px] transition-colors disabled:opacity-50';
/** The primary verb of a lane — the one the card is usually here to receive. */
const ACTION_PRIMARY = `${ACTION_BTN} border-brand/45 bg-brand/10 text-brand hover:bg-brand/20`;
/** A secondary verb: available, not the point of the lane. */
const ACTION_PLAIN = `${ACTION_BTN} border-line text-ink-dim hover:border-line-strong hover:text-ink`;
/** A retiring verb, pushed to the right edge of its row. */
const ACTION_QUIET = `${ACTION_BTN} border-transparent text-ink-faint hover:border-line hover:text-ink-dim`;

/**
 * Shared chrome of every action row. stopPropagation sits on the ROW, not on
 * each button: the whole card is a click target that opens the modal, and every
 * control in here is an alternative to that.
 */
function ActionRow({ children }: { children: ReactNode }): JSX.Element {
  return (
    <div
      className="mt-2 flex items-center gap-1.5"
      onClick={(e) => e.stopPropagation()}
      onKeyDown={(e) => e.stopPropagation()}
    >
      {children}
    </div>
  );
}

/**
 * The three one-click exits from the Inbox. A captured card is a suggestion, and
 * a suggestion needs a decision: Run accepts it into the dispatch queue, Plan
 * carries it to Planning Mode, Dismiss retires it. Only Plan is a new
 * capability — Run and Dismiss are the existing column move, surfaced as a verb
 * so triaging 200 cards is 200 clicks instead of 200 drags.
 */
function TriageActions({
  onRun,
  onPlan,
  onDismiss,
}: {
  onRun: () => void;
  onPlan: () => void;
  onDismiss: () => void;
}): JSX.Element {
  return (
    <ActionRow>
      <button
        type="button"
        onClick={onRun}
        data-tip="accept into the Working queue — the dispatcher picks it up"
        className={ACTION_PRIMARY}
      >
        ▶ Run
      </button>
      <button
        type="button"
        onClick={onPlan}
        data-tip="open Planning Mode prefilled with this card"
        className={ACTION_PLAIN}
      >
        ◇ Plan
      </button>
      <button
        type="button"
        onClick={onDismiss}
        data-tip="archive — it stays findable, it stops being an inbox item"
        className={`${ACTION_QUIET} ml-auto`}
      >
        Dismiss
      </button>
    </ActionRow>
  );
}

/**
 * Exits for a QUEUED card — one that has been accepted but has not started. All
 * three are still cheap here, which is the point of showing them: the window
 * between "accepted" and "running" is the last moment a wrong card costs
 * nothing, so taking it back has to be as easy as sending it was.
 */
function QueuedActions({
  onBackToInbox,
  onTogglePause,
  paused,
  onEdit,
}: {
  onBackToInbox: () => void;
  onTogglePause: (() => void) | undefined;
  paused: boolean;
  onEdit: () => void;
}): JSX.Element {
  return (
    <ActionRow>
      <button
        type="button"
        onClick={onBackToInbox}
        data-tip="take it back out of the queue — nothing has run yet"
        className={ACTION_PLAIN}
      >
        ↩ Inbox
      </button>
      {onTogglePause !== undefined && (
        <button
          type="button"
          onClick={onTogglePause}
          data-tip={
            paused
              ? 'let the dispatcher consider this card again'
              : 'hold this card in the queue without losing its place'
          }
          className={ACTION_PLAIN}
        >
          {paused ? '▶ Resume' : '❙❙ Pause'}
        </button>
      )}
      <button type="button" onClick={onEdit} data-tip="open the full card" className={`${ACTION_QUIET} ml-auto`}>
        Edit
      </button>
    </ActionRow>
  );
}

/**
 * Exits for a RUNNING card. Deliberately the thinnest row on the board: the two
 * useful things to do to work in flight are stop feeding it and go look at it.
 * Everything else is a decision for when it lands in Review.
 */
function RunningActions({
  onTogglePause,
  paused,
  onOpenTerminal,
}: {
  onTogglePause: (() => void) | undefined;
  paused: boolean;
  onOpenTerminal: (() => void) | undefined;
}): JSX.Element | null {
  if (onTogglePause === undefined && onOpenTerminal === undefined) return null;
  return (
    <ActionRow>
      {onTogglePause !== undefined && (
        <button
          type="button"
          onClick={onTogglePause}
          data-tip={paused ? 'unpause this card' : 'pause — the run finishes, nothing new starts'}
          className={ACTION_PLAIN}
        >
          {paused ? '▶ Resume' : '❙❙ Pause'}
        </button>
      )}
      {onOpenTerminal !== undefined && (
        <button
          type="button"
          onClick={onOpenTerminal}
          data-tip="open a terminal in this card's worktree"
          className={`${ACTION_PLAIN} ml-auto`}
        >
          ❯_ Terminal
        </button>
      )}
    </ActionRow>
  );
}

/**
 * Exits for a card in REVIEW. The four real decisions — Land, Re-run with
 * feedback, Discard, Re-verify — live in the TaskModal, because each of them
 * needs something the card has no room for (a confirm, a feedback textarea, the
 * diff, the verdict). So the card offers the one-click override that needs no
 * context, and a labelled door to the rest rather than making the reviewer guess
 * that clicking the card is where the decisions are.
 */
function ReviewActions({ onMarkDone, onReview }: { onMarkDone: () => void; onReview: () => void }): JSX.Element {
  return (
    <ActionRow>
      <button
        type="button"
        onClick={onReview}
        data-tip="Land, Re-run with feedback, Discard, Re-verify — with the diff and the verdict"
        className={ACTION_PRIMARY}
      >
        ◇ Review…
      </button>
      <button
        type="button"
        onClick={onMarkDone}
        data-tip="mark done without landing a branch — the manual override"
        className={`${ACTION_QUIET} ml-auto`}
      >
        ✓ Mark done
      </button>
    </ActionRow>
  );
}

/**
 * The escape hatch: a native <select> covering every legal column, on every
 * card. The lane verbs above are the paths worth naming; this is what makes the
 * remaining transitions reachable at all — and, being a real <select>, it is
 * what keeps them reachable from a keyboard and a screen reader. It still speaks
 * COLUMNS, not lanes, because that is what the PATCH carries.
 */
function ColumnMenu({
  column,
  onMove,
}: {
  column: BoardColumn;
  onMove: (to: BoardColumn) => void;
}): JSX.Element {
  return (
    <select
      value={column}
      aria-label="move task to column"
      onClick={(e) => e.stopPropagation()}
      onChange={(e) => {
        const to = e.target.value as BoardColumn;
        if (to !== column) onMove(to);
      }}
      className="rounded-md border border-line bg-field px-1 py-[1px] font-mono text-[9.5px] text-ink-dim outline-none transition-colors hover:border-line-strong focus:border-ink-dim"
    >
      {BOARD_COLUMNS.map((c) => (
        <option key={c} value={c}>
          {COLUMN_LABELS[c]}
        </option>
      ))}
    </select>
  );
}

export function TaskCard({
  task,
  onOpen,
  onMove,
  onPlan,
  onTogglePause,
  onOpenTerminal,
}: {
  task: BoardTask;
  onOpen: () => void;
  onMove: (to: BoardColumn) => void;
  /**
   * Hand-off to Planning Mode for this card. Optional because it is the one
   * triage action the card cannot perform on its own (Run and Dismiss are just
   * `onMove`) — omit it and the Inbox action row does not render, which is what
   * every history caller wants anyway.
   */
  onPlan?: (() => void) | undefined;
  /** Flip `user_paused`. Omitted in the history drawer, where it is meaningless. */
  onTogglePause?: (() => void) | undefined;
  /** Open a terminal in this card's worktree; omitted when there is no worktree
   * to open, or outside a workspace layout that owns a dock. */
  onOpenTerminal?: (() => void) | undefined;
}): JSX.Element {
  const blocked = task.paused || task.userPaused;
  // Triage is the only column where a card is still a question. Elsewhere it is
  // committed work, and the "why is this still here" hint would be noise.
  const inTriage = task.boardColumn === 'triage';
  return (
    <div
      role="button"
      tabIndex={0}
      aria-label={`task ${task.externalId}: ${task.title}`}
      onClick={onOpen}
      onKeyDown={(e) => {
        // Only the card itself opens on Enter/Space. Without this guard the
        // preventDefault below would run during the bubble phase of a keydown
        // aimed at a nested control (the action buttons, the column select,
        // the session link) and cancel that control's own activation — the
        // card would open instead of the button firing.
        if (e.target !== e.currentTarget) return;
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onOpen();
        }
      }}
      className="group cursor-pointer rounded-lg border border-line bg-surface p-2.5 transition-colors hover:border-line-strong focus:border-ink-dim focus:outline-none"
    >
      <div className="flex items-start gap-2">
        <span
          aria-hidden="true"
          data-tip={`${task.priority} priority`}
          className={`mt-[5px] h-[7px] w-[7px] shrink-0 rounded-full ${PRIORITY_DOT[task.priority]}`}
        />
        <span className="min-w-0 flex-1 text-[12.5px] leading-snug text-ink">{task.title}</span>
        {task.dispatchError !== null && (
          <span
            aria-label={`dispatch error: ${task.dispatchError}`}
            data-tip={task.dispatchError}
            className="shrink-0 text-[12px] leading-none text-red"
          >
            ⚠
          </span>
        )}
      </div>

      <div className="mt-2 flex flex-wrap items-center gap-1.5">
        <span className="rounded border border-line px-1 py-[1px] font-mono text-[9px] text-ink-faint">
          {task.externalId}
        </span>
        {task.model !== null && (
          <span className="rounded border border-line px-1 py-[1px] font-mono text-[9px] text-ink-dim">
            {task.model}
          </span>
        )}
        {task.playbook !== null && (
          <span
            data-tip={`playbook: ${task.playbook}`}
            className="rounded border border-brand/40 bg-brand/5 px-1 py-[1px] font-mono text-[9px] text-brand"
          >
            ▤ {task.playbook}
          </span>
        )}
        {task.origin !== 'manual' && <OriginBadge origin={task.origin} />}
        {task.verifyVerdict !== null && <VerdictBadge verdict={task.verifyVerdict} />}
        <LabelBadges labels={task.labels} />
        {blocked && (
          <span className="rounded-full border border-amber/40 bg-amber/10 px-1.5 py-[1px] font-mono text-[9px] text-amber">
            paused
          </span>
        )}
        {task.branch !== null && (
          <a
            href={`/sessions?scope=${task.projectSlug ?? ''}`}
            onClick={(e) => e.stopPropagation()}
            data-tip={`branch ${task.branch}`}
            aria-label={`sessions for ${task.branch}`}
            className="font-mono text-[9px] text-ink-faint transition-colors hover:text-ink"
          >
            ❯ session
          </a>
        )}
        <span className="ml-auto opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100">
          <ColumnMenu column={task.boardColumn} onMove={onMove} />
        </span>
      </div>

      {/* Why this card is still here. The server has computed this on every
          board read since staleness landed and nothing ever rendered it; on a
          Triage card it is the difference between Dismiss as a guess and
          Dismiss as an informed click. */}
      {inTriage && task.stalenessReason !== undefined && task.stalenessReason !== '' && (
        <div className="mt-1.5 font-mono text-[9.5px] leading-snug text-ink-faint">
          {task.stalenessReason}
        </div>
      )}

      {/* The lane's own verbs. Every card carries exactly the exits that make
          sense where it is standing — which is what replaced dragging it
          somewhere else. History cards (done/archived) get none: they are a
          record, and the card body still opens the full modal. */}
      {task.boardColumn === 'triage' && onPlan !== undefined && (
        <TriageActions onRun={() => onMove('todo')} onPlan={onPlan} onDismiss={() => onMove('archived')} />
      )}
      {task.boardColumn === 'todo' && (
        <QueuedActions
          onBackToInbox={() => onMove('triage')}
          onTogglePause={onTogglePause}
          paused={task.userPaused}
          onEdit={onOpen}
        />
      )}
      {task.boardColumn === 'in_progress' && (
        <RunningActions
          onTogglePause={onTogglePause}
          paused={task.userPaused}
          onOpenTerminal={onOpenTerminal}
        />
      )}
      {task.boardColumn === 'in_review' && (
        <ReviewActions onMarkDone={() => onMove('done')} onReview={onOpen} />
      )}
    </div>
  );
}
