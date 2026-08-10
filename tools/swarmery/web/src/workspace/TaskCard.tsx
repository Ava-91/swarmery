// Board task card (fusion phase 4): the draggable unit on the kanban board.
// Shows title, the T-xxxx id chip, a priority dot, a model chip (when set), an
// origin badge on cards the capture pipeline minted rather than a human, a
// verdict badge (pass/fail/inconclusive — phase 6 fills verifyVerdict), a
// dispatch-error warning icon with tooltip, a paused badge, and a session link
// glyph when a branch/worktree exists. Native HTML5 draggable; a keyboard
// alternative (a "move to →" menu) lives on the card via ColumnMenu so drag is
// never the only path (WCAG). Clicking the card body opens the TaskModal.

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
 * 'session' cards from a session's todos, 'llm' cards from a suggestion. Manual
 * cards (the overwhelming majority) carry no badge. */
const ORIGIN_BADGE: Record<Exclude<TaskOrigin, 'manual'>, { label: string; tip: string }> = {
  session: { label: 'from session', tip: 'captured from a session todo' },
  llm: { label: 'suggested', tip: 'suggested by a model, not hand-written' },
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

/**
 * The three one-click exits from Triage. A captured card is a suggestion, and
 * a suggestion needs a decision, not a drag: Run accepts it into the dispatch
 * queue, Plan carries it to Planning Mode, Dismiss retires it. Only Plan is a
 * new capability — Run and Dismiss are the existing column move, surfaced as a
 * verb so triaging 200 cards is 200 clicks instead of 200 drags.
 *
 * stopPropagation on the row (not per button): the whole card is a click target
 * that opens the modal, and every control here is an alternative to that.
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
  const base =
    'rounded border px-1.5 py-[2px] font-mono text-[9.5px] transition-colors disabled:opacity-50';
  return (
    <div
      className="mt-2 flex items-center gap-1.5"
      onClick={(e) => e.stopPropagation()}
      onKeyDown={(e) => e.stopPropagation()}
    >
      <button
        type="button"
        onClick={onRun}
        data-tip="accept into Todo — the dispatcher picks it up"
        className={`${base} border-brand/45 bg-brand/10 text-brand hover:bg-brand/20`}
      >
        ▶ Run
      </button>
      <button
        type="button"
        onClick={onPlan}
        data-tip="open Planning Mode prefilled with this card"
        className={`${base} border-line text-ink-dim hover:border-line-strong hover:text-ink`}
      >
        ◇ Plan
      </button>
      <button
        type="button"
        onClick={onDismiss}
        data-tip="archive — it stays findable, it stops being an inbox item"
        className={`${base} ml-auto border-transparent text-ink-faint hover:border-line hover:text-ink-dim`}
      >
        Dismiss
      </button>
    </div>
  );
}

/** Keyboard alternative to drag: a native <select> that moves the card. Sits
 * on every card so column changes never require a pointer. */
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
  onDragStart,
  onDragEnd,
  dragging,
  onPlan,
}: {
  task: BoardTask;
  onOpen: () => void;
  onMove: (to: BoardColumn) => void;
  onDragStart: () => void;
  onDragEnd: () => void;
  dragging: boolean;
  /**
   * Hand-off to Planning Mode for this card. Optional because it is the one
   * triage action the card cannot perform on its own (Run and Dismiss are just
   * `onMove`) — omit it and the action row does not render, which is what every
   * non-Triage caller wants anyway.
   */
  onPlan?: () => void;
}): JSX.Element {
  const blocked = task.paused || task.userPaused;
  // Triage is the only column where a card is still a question. Elsewhere it is
  // committed work, and both the verbs and the "why is this still here" hint
  // would be noise.
  const inTriage = task.boardColumn === 'triage';
  return (
    <div
      draggable
      onDragStart={(e) => {
        e.dataTransfer.setData('text/plain', String(task.id));
        e.dataTransfer.effectAllowed = 'move';
        onDragStart();
      }}
      onDragEnd={onDragEnd}
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
      className={`group cursor-pointer rounded-lg border bg-surface p-2.5 transition-colors hover:border-line-strong focus:border-ink-dim focus:outline-none ${
        dragging ? 'border-ink-dim opacity-40' : 'border-line'
      }`}
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

      {inTriage && onPlan !== undefined && (
        <TriageActions
          onRun={() => onMove('todo')}
          onPlan={onPlan}
          onDismiss={() => onMove('archived')}
        />
      )}
    </div>
  );
}
