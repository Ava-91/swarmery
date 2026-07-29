// Mode-preserving links to a session detail page.
//
// The session detail view is mounted twice: globally at /sessions/:id and under
// the project subtree at /p/:slug/sessions/:id. A link that hard-codes the
// global route drops the reader out of the project (header, sidebar and the
// "← sessions" back link all flip to global mode), so every in-app link builds
// its href from the current mount instead.

import { useParams } from 'react-router-dom';

/** Returns a builder for a session detail href that stays in the current mode:
 * project-scoped under /p/:slug/…, global everywhere else. */
export function useSessionHref(): (id: string | number) => string {
  const { slug } = useParams<{ slug?: string }>();
  return (id) =>
    slug != null ? `/p/${slug}/sessions/${String(id)}` : `/sessions/${String(id)}`;
}
