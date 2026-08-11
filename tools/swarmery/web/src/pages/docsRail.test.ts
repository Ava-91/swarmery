// Unit tests for the /docs rail grouping rule. Pure logic, no DOM.
//
// The web app ships no committed test runner (CI is `npm run build` only, and
// the Go coverage gate excludes web/), so this suite is dev-only: run it with
//   npx vitest run src/pages/docsRail.test.ts
// (vitest is fetched on demand; it is intentionally NOT a committed dependency.)

import { describe, expect, it } from 'vitest';
import type { DocMeta } from '../api/types';
import { groupDocs } from './docsRail';

const doc = (slug: string): DocMeta => ({ slug, title: slug, file: `${slug}.md` });

describe('groupDocs', () => {
  it('splits guides from reference docs', () => {
    const groups = groupDocs([
      doc('guide-getting-started'),
      doc('guide-board'),
      doc('onboarding'),
      doc('concepts'),
    ]);
    expect(groups.map((g) => g.label)).toEqual(['Guides', 'Reference']);
    expect(groups[0]?.items.map((d) => d.slug)).toEqual(['guide-getting-started', 'guide-board']);
    expect(groups[1]?.items.map((d) => d.slug)).toEqual(['onboarding', 'concepts']);
  });

  it('preserves the server order inside each group', () => {
    // The daemon pins nav order via docOrder; the rail must not re-sort.
    const groups = groupDocs([doc('guide-plans'), doc('guide-board'), doc('workflow')]);
    expect(groups[0]?.items.map((d) => d.slug)).toEqual(['guide-plans', 'guide-board']);
  });

  it('collapses to a single Reference list when no guides are embedded', () => {
    // Fresh clone / CI: `make copy-docs` never ran for guides.
    const groups = groupDocs([doc('onboarding'), doc('extending')]);
    expect(groups).toHaveLength(1);
    expect(groups[0]?.label).toBe('Reference');
  });

  it('collapses to a single Guides list when only guides are embedded', () => {
    const groups = groupDocs([doc('guide-board')]);
    expect(groups).toHaveLength(1);
    expect(groups[0]?.label).toBe('Guides');
  });

  it('returns no groups for an empty list, leaving the empty-state to the page', () => {
    expect(groupDocs([])).toEqual([]);
  });

  it('does not treat a doc merely containing "guide" as a guide', () => {
    const groups = groupDocs([doc('styleguide'), doc('guide-board')]);
    expect(groups[0]?.items.map((d) => d.slug)).toEqual(['guide-board']);
    expect(groups[1]?.items.map((d) => d.slug)).toEqual(['styleguide']);
  });
});
