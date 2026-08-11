// Grouping rule for the /docs rail: illustrated Guides above the Reference
// docs. Pure, and deliberately separate from Docs.tsx so it can be unit-tested
// without mounting the page.
//
// The split is CLIENT-SIDE, on the slug prefix, because /api/docs response
// shapes are frozen by the parity contract ({slug,title,file} — internal/api/
// docs.go). No group field is added server-side; the daemon only pins the
// order (docOrder), and `guide-` is the prefix the Makefile's flattening
// preserves.

import type { DocMeta } from '../api/types';

/** The slug prefix that marks a doc as an illustrated guide. */
export const GUIDE_PREFIX = 'guide-';

export interface DocGroup {
  label: string;
  items: DocMeta[];
}

/** Split the doc list into rail groups, preserving the server's order within
 * each group.
 *
 * Empty groups are dropped, which is what makes a fresh clone or CI build
 * degrade gracefully: with no guides embedded the rail is a single Reference
 * list, exactly the flat rail that shipped before guides existed — and with
 * no docs at all it is empty, leaving Docs.tsx's "no docs published by the
 * daemon" state reachable and unchanged. */
export function groupDocs(docs: DocMeta[]): DocGroup[] {
  const groups: DocGroup[] = [
    { label: 'Guides', items: docs.filter((d) => d.slug.startsWith(GUIDE_PREFIX)) },
    { label: 'Reference', items: docs.filter((d) => !d.slug.startsWith(GUIDE_PREFIX)) },
  ];
  return groups.filter((g) => g.items.length > 0);
}
