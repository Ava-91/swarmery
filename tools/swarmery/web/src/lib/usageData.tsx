// The app's SINGLE subscription-usage poller (GET /api/usage → internal/usage).
//
// Why a context instead of a hook each consumer calls: the header chip and the
// Usage modal both want this data, and every /api/usage miss costs a real
// upstream Anthropic call. Two independent 30-second timers against one endpoint
// would double that rate for no added information, so exactly one timer lives
// here and both consumers read the same snapshot.
//
// Naming: the component is `UsageDataProvider`, NOT `UsageProvider` — the latter
// is already an API type (one provider card in the payload, api/types.ts). The
// collision is easy to make and confusing to debug.
//
// Cadence (deliberately cheaper than the reference implementation, which polls
// only while its modal is open and not at all otherwise):
//   · 120s while mounted — the chip stays meaningful without the modal;
//   · 30s while the modal is open — matches the daemon's own 30s usage cache;
//   · paused entirely while the tab is hidden, with ONE catch-up fetch on
//     return if the snapshot is staler than the cadence then in force.
// Automatic polls never send `?fresh=1`: they are absorbed by the daemon cache.
// Only the modal's explicit Refresh button bypasses it, via refresh(true).

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { fetchUsage } from '../api';
import type { UsageProvider } from '../api/types';

/** Cadence while only the chip is mounted. */
const CHIP_POLL_MS = 120_000;
/** Cadence while the modal is open. */
const MODAL_POLL_MS = 30_000;

export interface UsageState {
  providers: UsageProvider[];
  error: string | null;
  loading: boolean;
  /** Epoch ms of the last SUCCESSFUL load; null until one lands. */
  lastUpdated: number | null;
  refresh: (fresh?: boolean) => Promise<void>;
  /**
   * Register/unregister an open modal, bumping the shared cadence to 30s.
   * Reference-counted so a transient double-mount (React StrictMode remounts
   * every effect in dev) cannot leave the interval stuck at the fast rate.
   */
  setModalOpen: (open: boolean) => void;
}

const UsageDataContext = createContext<UsageState>({
  providers: [],
  error: null,
  loading: false,
  lastUpdated: null,
  refresh: async () => undefined,
  setModalOpen: () => undefined,
});

export function UsageDataProvider({ children }: { children: ReactNode }): JSX.Element {
  const [providers, setProviders] = useState<UsageProvider[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [lastUpdated, setLastUpdated] = useState<number | null>(null);
  const [openCount, setOpenCount] = useState(0);
  const [visible, setVisible] = useState(() => document.visibilityState !== 'hidden');

  const alive = useRef(true);
  /** Monotonic request id — a slow earlier response must not clobber a newer one. */
  const seq = useRef(0);
  /** Read by the visibility handler without re-binding it on every load. */
  const lastUpdatedRef = useRef<number | null>(null);
  /** In-flight non-fresh load, deduped so StrictMode's double mount and a
   *  cadence change landing on the same tick cannot stack requests. */
  const inFlight = useRef<Promise<void> | null>(null);

  const pollMs = openCount > 0 ? MODAL_POLL_MS : CHIP_POLL_MS;
  const pollMsRef = useRef(pollMs);
  pollMsRef.current = pollMs;

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  const load = useCallback(async (fresh: boolean): Promise<void> => {
    if (!fresh && inFlight.current !== null) return inFlight.current;
    const id = ++seq.current;
    // True request cancellation would need api.ts's shared `get()` helper to
    // accept an AbortSignal — shared surface outside this phase. This guard is
    // equivalent from the outside: after unmount, or once a newer request is
    // out, the response is dropped without touching state.
    const current = (): boolean => alive.current && id === seq.current;
    setLoading(true);
    const run = fetchUsage(fresh)
      .then((resp) => {
        if (!current()) return;
        const stamp = Date.now();
        setProviders(resp.providers);
        setError(null);
        setLastUpdated(stamp);
        lastUpdatedRef.current = stamp;
      })
      .catch((e: unknown) => {
        if (!current()) return;
        setError(e instanceof Error ? e.message : String(e));
      })
      .finally(() => {
        if (!fresh) inFlight.current = null;
        if (!current()) return;
        setLoading(false);
      });
    if (!fresh) inFlight.current = run;
    return run;
  }, []);

  const refresh = useCallback(
    async (fresh = false): Promise<void> => {
      await load(fresh);
    },
    [load],
  );

  const setModalOpen = useCallback((open: boolean): void => {
    setOpenCount((n) => (open ? n + 1 : Math.max(0, n - 1)));
  }, []);

  // First load. Separate from the interval effect so a cadence change (modal
  // open/close) restarts only the timer, never refetches.
  useEffect(() => {
    void load(false);
  }, [load]);

  // The one timer. Re-created when the cadence changes or the tab becomes
  // visible again; absent entirely while hidden, which IS the pause.
  useEffect(() => {
    if (!visible) return undefined;
    const id = window.setInterval(() => {
      void load(false);
    }, pollMs);
    return () => window.clearInterval(id);
  }, [visible, pollMs, load]);

  useEffect(() => {
    const onVisibility = (): void => {
      const now = document.visibilityState !== 'hidden';
      setVisible(now);
      if (!now) return;
      // Catch-up: only when the snapshot already outlived the cadence that was
      // in force, so a quick tab-away/tab-back costs nothing.
      const last = lastUpdatedRef.current;
      if (last === null || Date.now() - last > pollMsRef.current) void load(false);
    };
    document.addEventListener('visibilitychange', onVisibility);
    return () => document.removeEventListener('visibilitychange', onVisibility);
  }, [load]);

  const value = useMemo<UsageState>(
    () => ({ providers, error, loading, lastUpdated, refresh, setModalOpen }),
    [providers, error, loading, lastUpdated, refresh, setModalOpen],
  );
  return <UsageDataContext.Provider value={value}>{children}</UsageDataContext.Provider>;
}

/** The shared usage snapshot + controls. */
export function useUsage(): UsageState {
  return useContext(UsageDataContext);
}
