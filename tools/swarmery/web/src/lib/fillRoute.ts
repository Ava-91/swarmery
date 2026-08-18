// Fill-mode route flag — which routes hand the vertical scroll to the page.
//
// Both shells (src/App.tsx, src/workspace/ProjectWorkspaceLayout.tsx) wrap the
// <Outlet/> in a scroller, which is the right frame for document-shaped pages:
// they grow, the shell scrolls them. Embedded pages are the opposite shape — a
// map/graph iframe or a doc pane wants to BE the viewport and scroll inside
// itself, so a scroller above it yields two nested scrollbars and a page that
// never fits.
//
// The flag rides on the route table as `handle: { fill: true }` (src/main.tsx)
// rather than a pathname list inside each shell: a route can be renamed or
// moved between subtrees and its layout contract travels with it, and there is
// exactly one place to answer "does this page own its scroll?".
//
// Limit: only routes in the data router's own table appear in useMatches(). A
// screen rendered by a descendant <Routes> (SystemShell under `system/*`) is
// invisible here — it has to declare fill on its parent route entry instead.

import { useMatches } from 'react-router-dom';

/** The `handle` shape a fill route declares in the route table. */
export interface FillHandle {
  /** true → the shell stops scrolling and the page fills the leftover height. */
  fill: boolean;
}

/** Narrows react-router's deliberately-`unknown` handle without `any`. A route
 * carrying no handle, or an unrelated one, simply does not match. */
function isFillHandle(handle: unknown): handle is FillHandle {
  return (
    typeof handle === 'object' &&
    handle !== null &&
    'fill' in handle &&
    typeof handle.fill === 'boolean'
  );
}

/** Whether the active route asked its shell for fill mode.
 *
 * Matches are ordered root → leaf, so the DEEPEST declaration wins: a child
 * route can later opt out of a parent subtree's fill mode without either shell
 * learning a single pathname. */
export function useFillRoute(): boolean {
  const matches = useMatches();
  for (let i = matches.length - 1; i >= 0; i -= 1) {
    const handle = matches[i]?.handle;
    if (isFillHandle(handle)) return handle.fill;
  }
  return false;
}
