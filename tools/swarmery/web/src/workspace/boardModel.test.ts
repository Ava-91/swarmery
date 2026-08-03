// Unit tests for the board's card-label helpers (0049 UI: badges + filter).
// Pure logic, no DOM.
//
// The web app ships no committed test runner (CI is `npm run build` only, and
// the Go coverage gate excludes web/), so this suite is dev-only: run it with
//   npx vitest run src/workspace/boardModel.test.ts
// (vitest is fetched on demand; it is intentionally NOT a committed dependency).
// The file still type-checks under `tsc --noEmit` in the normal build.

import { describe, expect, it } from 'vitest';
import type { BoardTask } from '../api/types';
import { labelColor, matchesLabelFilter, uniqueLabels, visibleLabels } from './boardModel';

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
    dispatchError: null,
    retryCount: 0,
    verifyVerdict: null,
    verifyDetail: null,
    agent: null,
    origin: 'manual',
    originSessionId: null,
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
