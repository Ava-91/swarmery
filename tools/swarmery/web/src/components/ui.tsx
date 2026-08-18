// Small design-system primitives shared across screens (Canvas dark editorial
// language: JetBrains Mono uppercase eyebrows, hairline pill status chips,
// warm near-black hairline cards).

import { useEffect, useRef, type ReactNode } from 'react';
import type { SessionStatus } from '../api/types';
import { fmtSpan } from '../lib/format';

/* ----- shared eyebrow rhythm — one token for every section heading -----
 * Canvas section rhythm: the SAME vertical breathing applies to the mono
 * eyebrows ("The spine · today", rail headers) and the mono day-group rules
 * on Sessions, so all screens breathe identically. */

export const EYEBROW_SPACING = 'mt-[26px] mb-2.5';

/* ----- section heading — the Canvas JetBrains Mono uppercase eyebrow ----- */

export function SectionTitle({
  children,
  flush = false,
}: {
  children: ReactNode;
  /** Align with a sibling card top instead of the standard 26px rhythm
   * (Docs rail — the eyebrow sits beside content, not between sections). */
  flush?: boolean;
}): JSX.Element {
  return (
    <h2
      className={`${flush ? 'mt-1 mb-2.5' : EYEBROW_SPACING} font-mono text-[11px] font-medium tracking-[0.16em] text-ink-dim uppercase`}
    >
      {children}
    </h2>
  );
}

/* ----- mono group header — day rules on Sessions ("today · sun, jul 12 ·
 * 9 sessions" + trailing hairline), same rhythm as SectionTitle ----- */

export function GroupHeader({ children }: { children: ReactNode }): JSX.Element {
  return (
    <div
      className={`${EYEBROW_SPACING} flex items-center gap-2 font-mono text-[10.5px] tracking-[0.14em] text-ink-faint uppercase`}
    >
      {children}
      <span className="h-px flex-1 bg-line" aria-hidden="true" />
    </div>
  );
}

/* ----- session-table column templates (≥900px) -----
 * One column system shared by the Sessions day groups and the Overview
 * "Recently completed" card so both read as the same table:
 *   [status dot] [project] [title 1fr] [model] [branch] [start] [duration]
 * Overview drops the status-dot and branch columns (completed rows). */

/* Fixed widths everywhere (no max-content): each row is its own grid, so any
 * content-sized column would resolve per row and the table would shear —
 * durations like "active · 7 h 06 min" vs "37 s" must not move the columns. */
export const SESSION_ROW_GRID =
  'desk:grid-cols-[14px_120px_minmax(0,1fr)_130px_60px_44px_150px]';
export const COMPLETED_ROW_GRID =
  'desk:grid-cols-[120px_minmax(0,1fr)_130px_44px_150px]';

/* ----- duration pill — right column of session table rows -----
 * Active rows get the green-tinted "active · 3 h 43 min" pill; everything
 * else is a plain hairline duration ("37 s", "69 h 29 min"). */

export function DurationPill({
  status,
  startedAt,
  endedAt,
}: {
  status: SessionStatus;
  startedAt: string;
  endedAt: string | null;
}): JSX.Element {
  const span = fmtSpan(startedAt, endedAt);
  if (status === 'active') {
    return (
      <span className="rounded-full border border-green/40 bg-green/10 px-2 py-0.5 font-mono text-[10.5px] whitespace-nowrap text-green">
        active · {span}
      </span>
    );
  }
  return (
    <span className="rounded-full border border-line px-2 py-0.5 font-mono text-[10.5px] whitespace-nowrap text-ink-dim">
      {span}
    </span>
  );
}

/* ----- card shell — raised navy on page bg, 12px radius, hairline ----- */

export function Card({
  children,
  className = '',
}: {
  children: ReactNode;
  className?: string;
}): JSX.Element {
  return (
    <div className={`mb-2.5 rounded-xl border border-line bg-surface px-3.5 py-3 ${className}`}>
      {children}
    </div>
  );
}

/* ----- async states ----- */

export function Loading({ label = 'loading…' }: { label?: string }): JSX.Element {
  return (
    <div className="flex items-center gap-2.5 py-10 text-ink-dim justify-center" role="status">
      <span className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-line border-t-brand" />
      <span className="font-mono text-[12px]">{label}</span>
    </div>
  );
}

export function ErrorBox({
  message,
  onRetry,
}: {
  message: string;
  onRetry?: () => void;
}): JSX.Element {
  return (
    <div className="my-3 rounded-lg border border-red/25 bg-red/5 px-3.5 py-3" role="alert">
      <div className="font-mono text-[12px] text-red">{message}</div>
      {onRetry !== undefined && (
        <button
          type="button"
          onClick={onRetry}
          className="mt-2 rounded-lg border border-line bg-surface px-3 py-1.5 text-[12px] font-semibold text-ink-2 transition-colors hover:bg-surface2"
        >
          retry
        </button>
      )}
    </div>
  );
}

/**
 * Honesty hint for stats endpoints whose range overlaps pruned (rolled-up)
 * days: daily rollups keep aggregates only, so per-event breakdowns
 * (tools, error groups) undercount there. Mirrors the backend `approx` flag.
 */
export function ApproxHint(): JSX.Element {
  return (
    <p className="mt-2 font-mono text-[10px] text-amber/80">
      ≈ approximate — this range overlaps pruned days (daily rollups), so older detail is missing
    </p>
  );
}

export function Empty({ children }: { children: ReactNode }): JSX.Element {
  return (
    <div className="my-3 rounded-xl border border-dashed border-line px-3.5 py-6 text-center text-[12.5px] text-ink-dim">
      {children}
    </div>
  );
}

/* ----- confirmation dialog (phase 4 step-12) -----
 * Destructive actions (hook disable, rollback, delete, conflict reload) must
 * be deliberate: a fixed overlay + hairline card. The destructive confirm
 * button follows the Approvals deny-button style; cancel is the plain
 * hairline secondary. Render null while closed. */

export function ConfirmDialog({
  open,
  title,
  children,
  confirmLabel,
  danger = false,
  busy = false,
  onConfirm,
  onCancel,
}: {
  open: boolean;
  title: string;
  children: ReactNode;
  confirmLabel: string;
  /** Approvals deny-button tones for destructive confirms. */
  danger?: boolean;
  busy?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}): JSX.Element | null {
  if (!open) return null;
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-bg/70 p-4"
      role="dialog"
      aria-modal="true"
      aria-label={title}
      onClick={onCancel}
    >
      <div
        className="w-full max-w-md rounded-xl border border-line bg-surface px-4 py-4"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="font-display text-[14px] font-bold text-ink">{title}</div>
        <div className="mt-2 text-[12.5px] leading-relaxed text-ink-2">{children}</div>
        <div className="mt-3.5 flex flex-wrap justify-end gap-2">
          <button
            type="button"
            onClick={onCancel}
            disabled={busy}
            className="rounded-lg border border-line bg-surface px-3.5 py-1.5 font-mono text-[11.5px] text-ink-2 transition-colors hover:bg-surface2 disabled:opacity-50"
          >
            cancel
          </button>
          <button
            type="button"
            onClick={onConfirm}
            disabled={busy}
            className={`rounded-lg border px-3.5 py-1.5 font-mono text-[11.5px] font-semibold transition-colors disabled:opacity-50 ${
              danger
                ? 'border-red/40 bg-red/10 text-red hover:bg-red/20'
                : 'border-green/40 bg-green/10 text-green hover:bg-green/20'
            }`}
          >
            {busy ? '…' : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}

/* ----- expandable section — fullscreen without remounting the children -----
 * Embedded pages (architecture map, graphify viz, serena panes, docs) hand this
 * wrapper a HEAVY child — an iframe or a canvas that carries state the user is
 * mid-way through: pan/zoom, scroll position, a loaded document, a graph
 * layout. So expanding has to be a class change and nothing else.
 *
 * INVARIANT: `children` render from ONE subtree in both states. No conditional
 * render of the children, no `key` that varies with `expanded`, no portal.
 * Move them into a branch and React unmounts the old tree, which reloads every
 * iframe and throws away exactly the state that was being looked at — the same
 * wrapper-only rule pages/Architecture.tsx relies on so its fullscreen map does
 * not re-fetch. The close button is a SIBLING placed after the children, so it
 * never shifts their position in the tree. */

/** Trigger styling for the expand affordance, exported so a page that needs its
 * own label/placement still matches every other embedded page. */
export const EXPAND_BUTTON_CLASS =
  'rounded-[9px] border border-line-strong bg-field px-2.5 py-[6px] font-mono text-[12px] text-ink transition-colors hover:border-ink-dim';

/** The standard `⛶ expand` trigger — one glyph and one word, everywhere. */
export function ExpandButton({
  onClick,
  label = 'expand',
}: {
  onClick: () => void;
  /** Overrides the word after the glyph (the aria-label follows it). */
  label?: string;
}): JSX.Element {
  return (
    <button type="button" onClick={onClick} aria-label={label} className={EXPAND_BUTTON_CLASS}>
      ⛶ {label}
    </button>
  );
}

export function ExpandableSection({
  expanded,
  onToggle,
  label,
  children,
  className,
}: {
  expanded: boolean;
  onToggle: (next: boolean) => void;
  /** Names the overlay for screen readers while expanded (aria-label). */
  label: string;
  children: ReactNode;
  /** Extra classes for the collapsed wrapper; the overlay owns its own box. */
  className?: string;
}): JSX.Element {
  // onToggle may change identity on every render (callers pass inline arrows);
  // call the latest one without re-running the effect below, which would
  // re-capture the focus anchor and re-read the body overflow it must restore.
  const toggleRef = useRef(onToggle);
  toggleRef.current = onToggle;

  // While expanded: Esc collapses, the page behind is scroll-locked, and the
  // element that opened the overlay takes the focus back on collapse so
  // keyboard navigation resumes where it left off instead of at the document
  // top. Lifted from pages/Architecture.tsx — one copy lives here now.
  useEffect(() => {
    if (!expanded) return;
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') toggleRef.current(false);
    };
    window.addEventListener('keydown', onKey);
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      window.removeEventListener('keydown', onKey);
      document.body.style.overflow = prevOverflow;
      opener?.focus();
    };
  }, [expanded]);

  const collapsedClass =
    className === undefined
      ? 'flex min-h-0 flex-1 flex-col'
      : `flex min-h-0 flex-1 flex-col ${className}`;

  return (
    <div
      className={expanded ? 'fixed inset-0 z-[45] flex flex-col bg-bg p-3 desk:p-4' : collapsedClass}
      role={expanded ? 'dialog' : undefined}
      aria-modal={expanded ? true : undefined}
      aria-label={expanded ? label : undefined}
    >
      {children}
      {expanded && (
        <button
          type="button"
          onClick={() => onToggle(false)}
          aria-label={`collapse ${label}`}
          className="absolute top-5 right-6 rounded-[9px] border border-line-strong bg-surface px-2.5 py-[6px] font-mono text-[12px] text-ink shadow-lg transition-colors hover:border-ink-dim desk:top-6 desk:right-7"
        >
          ✕ close
        </button>
      )}
    </div>
  );
}
