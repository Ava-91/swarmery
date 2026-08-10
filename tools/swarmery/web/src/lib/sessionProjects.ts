// Shared session→project resolution (extracted from pages/Approvals.tsx —
// PermissionRequest carries only sessionId, nothing project-shaped, on both the
// Go and TS sides; every consumer that needs project attribution for a request
// must resolve it through this same /api/sessions join instead of forking its
// own copy.

import { useCallback, useEffect, useMemo, useState } from 'react';
import type { Session } from '../api/types';
import { fetchSessions } from '../api';

export interface SessionProjectIndex {
  /** sessionId → Session (carries projectId/projectSlug/projectName). */
  bySessionId: ReadonlyMap<number, Session>;
  /** Re-fetch on demand (e.g. a WS message references a session not yet indexed). */
  refresh: () => void;
}

/**
 * Lazily fetches every session once (`enabled` gates the fetch — callers pass
 * `requests !== null && requests.length > 0` or an equivalent "there is
 * something to attribute" condition) and exposes it as an id-keyed map.
 */
export function useSessionProjectIndex(enabled: boolean): SessionProjectIndex {
  const [sessions, setSessions] = useState<Session[] | null>(null);

  const load = useCallback((): void => {
    fetchSessions()
      .then((page) => setSessions(page.sessions))
      .catch(() => setSessions([]));
  }, []);

  useEffect(() => {
    if (!enabled || sessions !== null) return;
    load();
  }, [enabled, sessions, load]);

  const bySessionId = useMemo(() => {
    const m = new Map<number, Session>();
    for (const s of sessions ?? []) m.set(s.id, s);
    return m;
  }, [sessions]);

  return { bySessionId, refresh: load };
}
