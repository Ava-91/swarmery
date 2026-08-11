// Unit tests for the board's card-label helpers (0049 UI: badges + filter).
// Pure logic, no DOM.
//
// The web app ships no committed test runner (CI is `npm run build` only, and
// the Go coverage gate excludes web/), so this suite is dev-only: run it with
//   npx vitest run src/workspace/boardModel.test.ts
// (vitest is fetched on demand; it is intentionally NOT a committed dependency).
// The file still type-checks under `tsc --noEmit` in the normal build.

import { describe, expect, it } from 'vitest';
import type { BoardColumn, BoardTask } from '../api/types';
import {
  amnestyCandidates,
  amnestyCutoff,
  BOARD_LANES,
  compareDispatchOrder,
  idleSince,
  labelColor,
  labelFilterOptions,
  LANE_TITLES,
  laneOf,
  matchesLabelFilter,
  splitLanes,
  uniqueLabels,
  visibleLabels,
} from './boardModel';

let nextId = 1;

function makeTask(over: Partial<BoardTask> = {}): BoardTask {
  return {
    id: nextId++,
    externalId: `T-${String(nextId)}`,
    projectId: 1,
    projectSlug: 'swarmery',
    title: 'a task',
    prompt: 'a task',
    priority: 'normal',
    status: 'queued',
    boardColumn: 'triage',
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

describe('visibleLabels', () => {
  it('shows everything and no overflow at or under the cap', () => {
    expect(visibleLabels([])).toEqual({ shown: [], overflow: 0 });
    expect(visibleLabels(['a', 'b', 'c'])).toEqual({ shown: ['a', 'b', 'c'], overflow: 0 });
  });

  it('caps at 3 and rolls the rest into overflow, preserving order', () => {
    const { shown, overflow } = visibleLabels(['a', 'b', 'c', 'd', 'e']);
    expect(shown).toEqual(['a', 'b', 'c']);
    expect(overflow).toBe(2);
  });
});

describe('labelColor', () => {
  it('is a pure function of the label text: same input, same output, every call', () => {
    const calls = Array.from({ length: 5 }, () => labelColor('jira-ticket'));
    expect(new Set(calls).size).toBe(1);
  });

  it('returns "H S% L%" components with H inside the reserved-safe bands', () => {
    const samples = ['jira-ticket', 'bug', 'bud', 'ui', 'needs-design', 'flaky', 'p1', 'p2', ''];
    for (const label of samples) {
      const [h, s, l] = labelColor(label).split(' ');
      const hue = Number(h);
      expect(Number.isInteger(hue)).toBe(true);
      // Never in the reserved red band (VerdictBadge fail) or green band (pass).
      const inRedBand = hue < 20 || hue >= 345;
      const inGreenBand = hue >= 90 && hue < 175;
      expect(inRedBand).toBe(false);
      expect(inGreenBand).toBe(false);
      expect(s).toBe('58%');
      expect(l).toBe('62%');
    }
  });

  it('different labels are not forced onto the same hue (spot check, not a full collision proof)', () => {
    const hues = new Set(['jira-ticket', 'bug', 'ui', 'flaky', 'p1'].map((l) => labelColor(l).split(' ')[0]));
    expect(hues.size).toBeGreaterThan(1);
  });
});

describe('uniqueLabels', () => {
  it('dedupes across tasks and sorts the result', () => {
    const tasks = [
      makeTask({ labels: ['jira-ticket', 'ui'] }),
      makeTask({ labels: ['ui', 'flaky'] }),
      makeTask({ labels: [] }),
    ];
    expect(uniqueLabels(tasks)).toEqual(['flaky', 'jira-ticket', 'ui']);
  });

  it('returns an empty list when no task carries a label', () => {
    expect(uniqueLabels([makeTask(), makeTask()])).toEqual([]);
  });
});

describe('matchesLabelFilter', () => {
  const withLabel = makeTask({ labels: ['jira-ticket'] });
  const withoutLabel = makeTask({ labels: [] });

  it('matches everything when the filter is null or empty', () => {
    expect(matchesLabelFilter(withLabel, null)).toBe(true);
    expect(matchesLabelFilter(withoutLabel, null)).toBe(true);
    expect(matchesLabelFilter(withoutLabel, '')).toBe(true);
  });

  it('matches only tasks carrying the exact label', () => {
    expect(matchesLabelFilter(withLabel, 'jira-ticket')).toBe(true);
    expect(matchesLabelFilter(withoutLabel, 'jira-ticket')).toBe(false);
    expect(matchesLabelFilter(withLabel, 'other')).toBe(false);
  });
});

describe('labelFilterOptions', () => {
  it('lists each label with how many tasks carry it, sorted by label', () => {
    const tasks = [
      makeTask({ labels: ['jira-ticket', 'ui'] }),
      makeTask({ labels: ['ui', 'flaky'] }),
      makeTask({ labels: [] }),
    ];
    expect(labelFilterOptions(tasks, null)).toEqual([
      { label: 'flaky', count: 1 },
      { label: 'jira-ticket', count: 1 },
      { label: 'ui', count: 2 },
    ]);
  });

  it('keeps a stale filter in the list with count 0 instead of dropping it', () => {
    // The filtered label no longer sits on any loaded task -- a bookmarked
    // URL, or the last card carrying it lost the label. The dropdown must
    // still offer this exact value so <select value={filter}> always has a
    // matching <option> and never silently disagrees with the applied filter.
    const tasks = [makeTask({ labels: ['ui'] })];
    expect(labelFilterOptions(tasks, 'gone')).toEqual([
      { label: 'gone', count: 0 },
      { label: 'ui', count: 1 },
    ]);
  });

  it('does not duplicate the filter when it is still a live label', () => {
    const tasks = [makeTask({ labels: ['ui'] })];
    expect(labelFilterOptions(tasks, 'ui')).toEqual([{ label: 'ui', count: 1 }]);
  });

  it('ignores a null or empty filter -- same as no filter applied', () => {
    const tasks = [makeTask({ labels: ['ui'] })];
    expect(labelFilterOptions(tasks, null)).toEqual([{ label: 'ui', count: 1 }]);
    expect(labelFilterOptions(tasks, '')).toEqual([{ label: 'ui', count: 1 }]);
  });
});

// --- inbox amnesty ------------------------------------------------------------

describe('amnestyCutoff', () => {
  it('renders the millisecond-Z shape the server stores, so string compares line up', () => {
    const now = Date.UTC(2026, 7, 11, 12, 0, 0);
    expect(amnestyCutoff(7, now)).toBe('2026-08-04T12:00:00.000Z');
  });
});

describe('idleSince', () => {
  it('falls back to createdAt — capture never writes columnMovedAt', () => {
    expect(idleSince(makeTask({ columnMovedAt: null, createdAt: '2026-01-01T00:00:00.000Z' }))).toBe(
      '2026-01-01T00:00:00.000Z',
    );
  });

  it('prefers columnMovedAt when the card was actually moved', () => {
    expect(
      idleSince(
        makeTask({ columnMovedAt: '2026-08-01T00:00:00.000Z', createdAt: '2026-01-01T00:00:00.000Z' }),
      ),
    ).toBe('2026-08-01T00:00:00.000Z');
  });
});

describe('amnestyCandidates', () => {
  const before = '2026-06-01T00:00:00.000Z';
  const old = '2026-01-01T00:00:00.000Z';

  it('counts captured triage cards idle since before the cutoff', () => {
    const tasks = [
      makeTask({ origin: 'session', boardColumn: 'triage', createdAt: old }),
      makeTask({ origin: 'llm', boardColumn: 'triage', createdAt: old }),
    ];
    expect(amnestyCandidates(tasks, before)).toBe(2);
  });

  it('mirrors every server-side exclusion, conjunct for conjunct', () => {
    const excluded = [
      makeTask({ origin: 'manual', boardColumn: 'triage', createdAt: old }),
      makeTask({ origin: 'session', boardColumn: 'todo', createdAt: old }),
      makeTask({ origin: 'session', boardColumn: 'triage', createdAt: old, worktreePath: '/tmp/wt' }),
      makeTask({ origin: 'session', boardColumn: 'triage', createdAt: '2026-07-15T00:00:00.000Z' }),
    ];
    expect(amnestyCandidates(excluded, before)).toBe(0);
    const withOneEligible = [...excluded, makeTask({ origin: 'session', createdAt: old })];
    expect(amnestyCandidates(withOneEligible, before)).toBe(1);
  });

  it('dates a moved card by columnMovedAt, not by when it was created', () => {
    const movedRecently = makeTask({
      origin: 'session',
      boardColumn: 'triage',
      createdAt: old,
      columnMovedAt: '2026-07-20T00:00:00.000Z',
    });
    expect(amnestyCandidates([movedRecently], before)).toBe(0);
  });

  it('is empty on an empty board', () => {
    expect(amnestyCandidates([], before)).toBe(0);
  });
});

// --- lanes (board redesign phase 4) -------------------------------------------

describe('laneOf', () => {
  it('collapses every live column into one of the three lanes', () => {
    expect(laneOf('triage')).toBe('inbox');
    expect(laneOf('todo')).toBe('working');
    expect(laneOf('in_progress')).toBe('working');
    expect(laneOf('in_review')).toBe('review');
  });

  it('gives the two history columns no lane at all', () => {
    // Not "some lane you should ignore" — null, so a caller that forgets to
    // handle history cannot silently render them into Inbox.
    expect(laneOf('done')).toBeNull();
    expect(laneOf('archived')).toBeNull();
  });

  it('covers the whole BoardColumn enum — a new column cannot be forgotten', () => {
    const all: BoardColumn[] = ['triage', 'todo', 'in_progress', 'in_review', 'done', 'archived'];
    for (const c of all) {
      const lane = laneOf(c);
      expect(lane === null || BOARD_LANES.includes(lane)).toBe(true);
    }
  });
});

describe('BOARD_LANES / LANE_TITLES', () => {
  it('renders exactly three lanes, left to right', () => {
    expect(BOARD_LANES).toEqual(['inbox', 'working', 'review']);
  });

  it('titles every lane it renders', () => {
    for (const lane of BOARD_LANES) expect(LANE_TITLES[lane]).toBeTruthy();
  });
});

describe('compareDispatchOrder', () => {
  // The contract under test is dispatch/service.go candidates(): priority asc →
  // created_at asc → id asc. If these ever disagree, the Queued group is lying
  // about which card runs next, which is the only reason the group exists.
  const early = '2026-01-01T00:00:00.000Z';
  const late = '2026-08-01T00:00:00.000Z';

  it('ranks by priority first, even against an older card', () => {
    const urgentNew = makeTask({ priority: 'urgent', createdAt: late });
    const lowOld = makeTask({ priority: 'low', createdAt: early });
    expect(compareDispatchOrder(urgentNew, lowOld)).toBeLessThan(0);
    expect(compareDispatchOrder(lowOld, urgentNew)).toBeGreaterThan(0);
  });

  it('breaks a priority tie with the older createdAt', () => {
    const older = makeTask({ priority: 'normal', createdAt: early });
    const newer = makeTask({ priority: 'normal', createdAt: late });
    expect(compareDispatchOrder(older, newer)).toBeLessThan(0);
  });

  it('breaks a full tie with the lower id, so the order is total and stable', () => {
    const first = makeTask({ id: 7, priority: 'high', createdAt: early });
    const second = makeTask({ id: 9, priority: 'high', createdAt: early });
    expect(compareDispatchOrder(first, second)).toBeLessThan(0);
    expect(compareDispatchOrder(second, first)).toBeGreaterThan(0);
    expect(compareDispatchOrder(first, first)).toBe(0);
  });

  it('sorts a mixed queue into the exact order the dispatcher would pick from', () => {
    const queue = [
      makeTask({ id: 1, priority: 'normal', createdAt: late }),
      makeTask({ id: 2, priority: 'urgent', createdAt: late }),
      makeTask({ id: 3, priority: 'normal', createdAt: early }),
      makeTask({ id: 4, priority: 'low', createdAt: early }),
      makeTask({ id: 5, priority: 'high', createdAt: late }),
    ];
    expect([...queue].sort(compareDispatchOrder).map((t) => t.id)).toEqual([2, 5, 3, 1, 4]);
  });
});

describe('splitLanes', () => {
  it('routes each column to the group that renders it', () => {
    const lanes = splitLanes([
      makeTask({ id: 1, boardColumn: 'triage' }),
      makeTask({ id: 2, boardColumn: 'todo' }),
      makeTask({ id: 3, boardColumn: 'in_progress' }),
      makeTask({ id: 4, boardColumn: 'in_review' }),
      makeTask({ id: 5, boardColumn: 'done' }),
    ]);
    expect(lanes.inbox.map((t) => t.id)).toEqual([1]);
    expect(lanes.queued.map((t) => t.id)).toEqual([2]);
    expect(lanes.running.map((t) => t.id)).toEqual([3]);
    expect(lanes.review.map((t) => t.id)).toEqual([4]);
    expect(lanes.done.map((t) => t.id)).toEqual([5]);
  });

  it('drops archived cards on the floor — they arrive by their own lazy fetch', () => {
    const lanes = splitLanes([makeTask({ boardColumn: 'archived' })]);
    expect([...lanes.inbox, ...lanes.queued, ...lanes.running, ...lanes.review, ...lanes.done]).toEqual(
      [],
    );
  });

  it('orders the Queued group the way the dispatcher orders candidates', () => {
    const lanes = splitLanes([
      makeTask({ id: 10, boardColumn: 'todo', priority: 'low', createdAt: '2026-01-01T00:00:00.000Z' }),
      makeTask({ id: 11, boardColumn: 'todo', priority: 'urgent', createdAt: '2026-08-01T00:00:00.000Z' }),
      makeTask({ id: 12, boardColumn: 'todo', priority: 'normal', createdAt: '2026-02-01T00:00:00.000Z' }),
    ]);
    expect(lanes.queued.map((t) => t.id)).toEqual([11, 12, 10]);
  });

  it('keeps a paused card in Queued rather than hiding it', () => {
    // The dispatcher's candidates() filters paused cards out; the board must
    // not, or a card someone parked would look deleted. It just sorts with the
    // rest and wears its `paused` badge.
    const lanes = splitLanes([
      makeTask({ id: 20, boardColumn: 'todo', priority: 'normal' }),
      makeTask({ id: 21, boardColumn: 'todo', priority: 'urgent', userPaused: true }),
    ]);
    expect(lanes.queued.map((t) => t.id)).toEqual([21, 20]);
  });

  it('orders Done most-recently-moved first', () => {
    const lanes = splitLanes([
      makeTask({ id: 30, boardColumn: 'done', columnMovedAt: '2026-03-01T00:00:00.000Z' }),
      makeTask({ id: 31, boardColumn: 'done', columnMovedAt: '2026-08-01T00:00:00.000Z' }),
      makeTask({ id: 32, boardColumn: 'done', columnMovedAt: null }),
    ]);
    expect(lanes.done.map((t) => t.id)).toEqual([31, 30, 32]);
  });

  it('returns five empty groups for an empty board', () => {
    const lanes = splitLanes([]);
    expect(lanes).toEqual({ inbox: [], queued: [], running: [], review: [], done: [] });
  });

  it('does not mutate the list it was handed', () => {
    const tasks = [
      makeTask({ id: 40, boardColumn: 'todo', priority: 'low' }),
      makeTask({ id: 41, boardColumn: 'todo', priority: 'urgent' }),
    ];
    const before = tasks.map((t) => t.id);
    splitLanes(tasks);
    expect(tasks.map((t) => t.id)).toEqual(before);
  });
});
