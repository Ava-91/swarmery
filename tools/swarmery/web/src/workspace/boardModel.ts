// Board presentation model (fusion phase 4): the closed column order, their
// display labels, and the client-side derivation of the status-bar counts.
// Kept pure so it is trivially unit-testable and shared by Board + StatusBar.

import type { BoardColumn, BoardTask, TaskPriority } from '../api/types';

/** Left-to-right column order on the board. */
export const BOARD_COLUMNS: BoardColumn[] = [
  'triage',
  'todo',
  'in_progress',
  'in_review',
  'done',
  'archived',
];

export const COLUMN_LABELS: Record<BoardColumn, string> = {
  triage: 'Triage',
  todo: 'Todo',
  in_progress: 'In Progress',
  in_review: 'In Review',
  done: 'Done',
  archived: 'Archived',
};

/** Priority tokens, highest first — the option order of every priority select. */
export const TASK_PRIORITIES: TaskPriority[] = ['urgent', 'high', 'normal', 'low'];

/**
 * Model tokens the dispatcher passes to `claude --model`. 'default' is the UI's
 * name for "inherit" and maps to a null `model` on the wire — every editor of a
 * task's model (the create modal, the detail modal) reads this one
 * list so they can never drift apart.
 */
export const TASK_MODELS = ['default', 'fable', 'opus', 'sonnet', 'haiku'] as const;

export interface BoardCounts {
  waiting: number;
  running: number;
  blocked: number;
}

/** A task is BLOCKED when either pause flag is set (mirrors the dispatcher's
 * two-flag park semantics). */
export function isBlocked(t: BoardTask): boolean {
  return t.paused || t.userPaused;
}

/**
 * Status-bar counts derived from the board (phase-4 spec):
 *   waiting = triage + todo (not blocked)
 *   running = in_progress (not blocked)
 *   blocked = any task parked by a pause flag (across live columns)
 * Blocked wins over waiting/running so a paused in_progress task counts once,
 * as Blocked. Done/archived never contribute.
 */
export function boardCounts(tasks: BoardTask[]): BoardCounts {
  let waiting = 0;
  let running = 0;
  let blocked = 0;
  for (const t of tasks) {
    if (t.boardColumn === 'done' || t.boardColumn === 'archived') continue;
    if (isBlocked(t)) {
      blocked += 1;
      continue;
    }
    if (t.boardColumn === 'triage' || t.boardColumn === 'todo') waiting += 1;
    else if (t.boardColumn === 'in_progress') running += 1;
  }
  return { waiting, running, blocked };
}

// --- card labels (0049 UI) ----------------------------------------------------

/** A card renders at most this many label chips before rolling the rest into
 * a single "+N" overflow chip, so a card with a dozen labels never blows out
 * its width. */
export const MAX_VISIBLE_LABELS = 3;

/** Split a task's labels into what a chip row shows directly vs. what rolls
 * into the "+N" overflow chip. Order is preserved (the server already
 * lowercases/trims/dedupes on write, first-seen order). */
export function visibleLabels(labels: readonly string[]): { shown: readonly string[]; overflow: number } {
  if (labels.length <= MAX_VISIBLE_LABELS) return { shown: labels, overflow: 0 };
  return { shown: labels.slice(0, MAX_VISIBLE_LABELS), overflow: labels.length - MAX_VISIBLE_LABELS };
}

// Reserved hue bands mirror the project-identity hasher (lib/colors.ts): red
// and green stay out because they already mean fail/pass via VerdictBadge on
// the same card. Kept as an independent hash rather than imported from there —
// labels and projects are different identity spaces and must never collide by
// construction.
const LABEL_HUE_RANGES: ReadonlyArray<readonly [number, number]> = [
  [20, 90], // orange → amber → yellow
  [175, 345], // cyan → blue → indigo → violet → purple → magenta → pink
];
const LABEL_HUE_SPAN = LABEL_HUE_RANGES.reduce((sum, [lo, hi]) => sum + (hi - lo), 0);

function hueFromLabel(label: string): number {
  // Avalanche mix (xmur3-style) so short labels that differ by one character
  // (e.g. "bug" / "bud") land far apart instead of adjacent hues.
  let hash = 1779033703 ^ label.length;
  for (let i = 0; i < label.length; i += 1) {
    hash = Math.imul(hash ^ label.charCodeAt(i), 3432918353);
    hash = (hash << 13) | (hash >>> 19);
  }
  hash = Math.imul(hash ^ (hash >>> 16), 2246822507);
  hash = Math.imul(hash ^ (hash >>> 13), 3266489909);
  let remaining = ((hash ^ (hash >>> 16)) >>> 0) % LABEL_HUE_SPAN;
  for (const [lo, hi] of LABEL_HUE_RANGES) {
    const span = hi - lo;
    if (remaining < span) return lo + remaining;
    remaining -= span;
  }
  /* istanbul ignore next -- unreachable: remaining < LABEL_HUE_SPAN by construction */
  return 20;
}

/**
 * Deterministic "H S% L%" HSL components for a label chip — a pure hash of
 * the label text, so the same label paints the same color on every render,
 * every card, and after a reload; there is no lookup table to keep in sync.
 * Consumers append their own alpha, e.g. `hsl(${labelColor(l)} / 0.4)`.
 */
export function labelColor(label: string): string {
  return `${String(hueFromLabel(label))} 58% 62%`;
}

/** Unique labels across a task list, sorted for a stable, scannable filter
 * dropdown. Each task's own array is already deduped by the server; this
 * dedups ACROSS tasks. */
export function uniqueLabels(tasks: readonly BoardTask[]): string[] {
  const set = new Set<string>();
  for (const t of tasks) for (const l of t.labels) set.add(l);
  return [...set].sort();
}

/** Board label-filter predicate: null/empty matches every task. */
export function matchesLabelFilter(task: BoardTask, filter: string | null): boolean {
  return filter === null || filter === '' || task.labels.includes(filter);
}

/** One entry in the label-filter `<select>` — `count` is how many currently-
 * loaded tasks carry it. */
export interface LabelFilterOption {
  readonly label: string;
  readonly count: number;
}

/**
 * Options for the board's label-filter dropdown, built so the `<select>` can
 * never hold a `value` that has no matching `<option>`. `uniqueLabels` alone
 * omits a `filter` that no task carries any more (a stale `?label=` from a
 * bookmark, or the last card carrying it just lost the label) — a controlled
 * select bound to that value then renders as if nothing were filtered while
 * the filter is still applied, making the board look broken instead of
 * filtered. Folding the orphaned filter in here, with `count: 0`, keeps the
 * dropdown and the applied filter permanently in agreement.
 */
export function labelFilterOptions(tasks: readonly BoardTask[], filter: string | null): LabelFilterOption[] {
  const counts = new Map<string, number>();
  for (const t of tasks) for (const l of t.labels) counts.set(l, (counts.get(l) ?? 0) + 1);
  if (filter !== null && filter !== '' && !counts.has(filter)) counts.set(filter, 0);
  return [...counts.keys()].sort().map((label) => ({ label, count: counts.get(label) ?? 0 }));
}
