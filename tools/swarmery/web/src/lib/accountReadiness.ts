// Shared read of GET /api/accounts for the header surfaces — the readiness
// banner and the usage chip both need the `runnable` verdict of the scoped
// project's effective account, and both render on every route. One module-level
// cache keeps them from being two fetchers of the same tiny endpoint, the same
// way usageData keeps the chip and the modal on one poller.
//
// The cache is demand-loaded on first subscribe and refreshed only on explicit
// events (a probe verdict, a completed connect) — accounts change rarely and
// the daemon's list endpoint serves the STORED probe verdict, so polling would
// add nothing. A failed read keeps the last known snapshot: the consumers'
// silence rules treat "unknown" as silence, and a transient fetch error must
// not conjure or clear a warning.

import { useSyncExternalStore } from 'react';
import { fetchAccounts } from '../api';
import type { Account, AccountProbeResponse } from '../api/types';

let cache: Account[] | null = null;
let inflight: Promise<void> | null = null;
const subscribers = new Set<() => void>();

function notify(): void {
  for (const fn of subscribers) fn();
}

/** Fetch (or re-fetch) the account list; concurrent callers share one flight. */
export function refreshReadiness(): Promise<void> {
  inflight ??= fetchAccounts()
    .then((resp) => {
      cache = resp.accounts;
    })
    .catch(() => undefined)
    .finally(() => {
      inflight = null;
      notify();
    });
  return inflight;
}

/** One account row with a fresh probe verdict applied. An absent optional in
 * the verdict CLEARS the stale one (dropped, not set to undefined — the types
 * are exact-optional). */
export function applyVerdict(a: Account, verdict: AccountProbeResponse): Account {
  const { runnableReason: _r, runnableCheckedAt: _c, ...rest } = a;
  return {
    ...rest,
    runnable: verdict.runnable,
    ...(verdict.runnableReason !== undefined ? { runnableReason: verdict.runnableReason } : {}),
    ...(verdict.runnableCheckedAt !== undefined
      ? { runnableCheckedAt: verdict.runnableCheckedAt }
      : {}),
  };
}

/** Patch one account's probe verdict in place — the probe response carries the
 * same three fields as the Account row exactly so clients can do this. */
export function patchReadiness(key: string, verdict: AccountProbeResponse): void {
  if (cache === null) return;
  cache = cache.map((a) => (a.key === key ? applyVerdict(a, verdict) : a));
  notify();
}

function subscribe(fn: () => void): () => void {
  subscribers.add(fn);
  if (cache === null) void refreshReadiness();
  return () => {
    subscribers.delete(fn);
  };
}

function snapshot(): Account[] | null {
  return cache;
}

/** The cached account list — null until the first read lands. */
export function useAccountReadiness(): Account[] | null {
  return useSyncExternalStore(subscribe, snapshot);
}
