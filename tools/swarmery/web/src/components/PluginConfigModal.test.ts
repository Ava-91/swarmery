// Unit tests for the probe's auto-fill rule — the one piece of the config modal
// that decides what lands in a project's .claude/project.json.
//
// The web app ships no committed test runner (CI is `npm run build` only, and
// the Go coverage gate excludes web/), so this suite is dev-only: run it with
//   npx vitest run src/components/PluginConfigModal.test.ts
// The file still type-checks under `tsc --noEmit` in the normal build.

import { describe, expect, it } from 'vitest';
import { buildSubmitValue, fillEmptyFrom } from './PluginConfigModal';

const LEAVES = ['tokensFile', 'componentsRoot', 'devUrl', 'verify.lint', 'verify.typecheck'];

describe('fillEmptyFrom', () => {
  it('writes the first candidate into every empty field', () => {
    const { next, filled } = fillEmptyFrom(
      {},
      { tokensFile: ['src/styles/globals.css', 'src/app/globals.css'], devUrl: ['http://localhost:3000'] },
      LEAVES,
    );
    expect(next).toEqual({ tokensFile: 'src/styles/globals.css', devUrl: 'http://localhost:3000' });
    expect(filled).toEqual(['tokensFile', 'devUrl']);
  });

  it('never overwrites a value that is already there', () => {
    const { next, filled } = fillEmptyFrom(
      { tokensFile: 'src/theme.css' },
      { tokensFile: ['src/styles/globals.css'] },
      LEAVES,
    );
    expect(next.tokensFile).toBe('src/theme.css');
    expect(filled).toEqual([]);
  });

  it('fills nested paths without dropping their siblings', () => {
    const { next, filled } = fillEmptyFrom(
      { verify: { lint: 'npm run lint' } },
      { 'verify.lint': ['eslint .'], 'verify.typecheck': ['npm run type-check'] },
      LEAVES,
    );
    expect(next).toEqual({ verify: { lint: 'npm run lint', typecheck: 'npm run type-check' } });
    expect(filled).toEqual(['verify.typecheck']);
  });

  it('ignores fields the schema does not render', () => {
    const { next, filled } = fillEmptyFrom({}, { smuggled: ['value'] }, LEAVES);
    expect(next).toEqual({});
    expect(filled).toEqual([]);
  });

  it('treats an empty candidate as no answer at all', () => {
    const { next, filled } = fillEmptyFrom({}, { devUrl: [''], tokensFile: [] }, LEAVES);
    expect(next).toEqual({});
    expect(filled).toEqual([]);
  });

  it('leaves the caller a new object rather than mutating theirs', () => {
    const before = { verify: {} };
    const { next } = fillEmptyFrom(before, { 'verify.lint': ['npm run lint'] }, LEAVES);
    expect(before).toEqual({ verify: {} });
    expect(next).not.toBe(before);
  });
});

// Every input hands back a string. A leaf the pack declared as a number must
// still reach project.json as JSON 5.5, not "5.5" — design-pack's diff block is
// the live case, and the daemon's validator ignores `number`, so nothing
// downstream would have caught the string.
describe('buildSubmitValue', () => {
  const SCHEMA = {
    type: 'object',
    properties: {
      tokensFile: { type: 'string' },
      diff: {
        type: 'object',
        properties: {
          threshold: { type: 'number', minimum: 0, maximum: 100 },
          pixelTolerance: { type: 'number' },
        },
      },
      budget: { type: 'object', properties: { maxFiles: { type: 'integer' } } },
    },
  };

  it('submits number and integer leaves as JSON numbers', () => {
    expect(
      buildSubmitValue(SCHEMA, {
        tokensFile: 'src/app/globals.css',
        diff: { threshold: '0.5' },
        budget: { maxFiles: '12' },
      }),
    ).toEqual({ tokensFile: 'src/app/globals.css', diff: { threshold: 0.5 }, budget: { maxFiles: 12 } });
  });

  it('omits empty optional objects instead of submitting {}', () => {
    expect(buildSubmitValue(SCHEMA, { tokensFile: 'a.css', diff: { threshold: '' } })).toEqual({
      tokensFile: 'a.css',
    });
  });
});
