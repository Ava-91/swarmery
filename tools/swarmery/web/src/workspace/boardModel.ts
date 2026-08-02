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
 * name for "inherit" and maps to a null `model` on the wire — the create and
 * detail modals both depend on that mapping, so it lives here rather than in
 * either of them.
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
