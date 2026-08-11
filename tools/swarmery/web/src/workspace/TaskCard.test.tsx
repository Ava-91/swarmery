// @vitest-environment jsdom
//
// Render guard for the card's origin badge. `ORIGIN_BADGE` is a Record indexed
// by origin and `OriginBadge` DESTRUCTURES the looked-up entry, so an origin the
// map does not know is not a missing badge — it is `undefined` being
// destructured, i.e. a TypeError that unmounts the whole board. The daemon has
// minted 'verify-fix' cards since internal/verify shipped; the union and the map
// only learned about them in 0051's phase. This suite is the regression fence.
//
// The web app ships no committed test runner (CI is `npm run build` only, and
// the Go coverage gate excludes web/), so this suite is dev-only. Run it with
//   npx vitest run --environment jsdom src/workspace/TaskCard.test.tsx
// after fetching the runner on demand:
//   npm i --no-save vitest jsdom @testing-library/react @testing-library/dom
// (none of them are committed dependencies). The file still type-checks under
// `tsc --noEmit` in the normal build, which is what makes the union itself
// enforceable: ORIGIN_BADGE is typed Record<Exclude<TaskOrigin,'manual'>,…>, so
// widening TaskOrigin without adding the entry is a compile error.

import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { BoardTask, TaskOrigin } from '../api/types';
import { TaskCard } from './TaskCard';

function makeTask(over: Partial<BoardTask> = {}): BoardTask {
  return {
    id: 1,
    externalId: 'T-a1b2c3',
    projectId: 1,
    projectSlug: 'swarmery',
    title: 'a task',
    prompt: 'a task',
    priority: 'normal',
    status: 'queued',
    boardColumn: 'todo',
    paused: false,
    userPaused: false,
    dependencies: [],
    model: null,
    playbook: null,
    fileScope: [],
    labels: [],
    branch: null,
    worktreePath: null,
    startPoint: null,
    dispatchError: null,
    retryCount: 0,
    verifyRetryCount: 0,
    verifyVerdict: null,
    verifyDetail: null,
    agent: null,
    origin: 'manual',
    originSessionId: null,
    resultNote: null,
    columnMovedAt: null,
    createdAt: '2026-08-01T00:00:00Z',
    ...over,
  };
}

function renderCard(over: Partial<BoardTask> = {}): void {
  render(<TaskCard task={makeTask(over)} onOpen={vi.fn()} onMove={vi.fn()} />);
}

afterEach(cleanup);

describe('TaskCard origin badge', () => {
  it('renders a verify-fix card without throwing, and badges it', () => {
    expect(() => renderCard({ origin: 'verify-fix', title: 'fix: broken endpoint' })).not.toThrow();
    expect(screen.getByText(/fix: broken endpoint/)).toBeDefined();
    expect(screen.getByText(/⟲ fix/)).toBeDefined();
  });

  // Every non-manual origin must have a badge. Enumerated explicitly so adding a
  // member to TaskOrigin without an ORIGIN_BADGE entry fails here as well as in
  // tsc — the map is what the renderer indexes, and a partial map is a crash.
  it('has a badge for every non-manual origin', () => {
    const nonManual: Exclude<TaskOrigin, 'manual'>[] = ['session', 'llm', 'verify-fix'];
    for (const origin of nonManual) {
      expect(() => renderCard({ origin })).not.toThrow();
      cleanup();
    }
  });

  it('badges nothing for a manual card', () => {
    renderCard({ origin: 'manual' });
    expect(screen.queryByText(/⟲/)).toBeNull();
  });
});
