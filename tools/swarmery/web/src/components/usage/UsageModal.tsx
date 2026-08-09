// The Usage modal — the operator's live subscription quota, one card per
// provider per ACCOUNT (GET /api/usage → internal/usage). Replaces the old
// UsagePopover, which flattened every provider's windows into one list and
// rendered none of the per-provider plan/status/error detail the contract now
// carries.
//
// Accounts are the daemon's ingest roots: one per `claude` config dir. With a
// single account (the stock config) this renders exactly what it always did —
// account labels appear only when there is more than one to tell apart.
//
// Trigger-less by design: UsageChip owns the trigger and the `open` state, so
// both shells (fleet App + project WorkspaceShell) reuse this component and it
// stays pure presentation.
//
// It does not fetch: the snapshot arrives from the app-wide poller in
// lib/usageData.tsx, shared with the header chip so one timer serves both.
// Display mode and collapsed windows persist through lib/usagePrefs.ts.
//
// Presentation is viewport-keyed, not prop-keyed: ≥768px it is an anchored
// popover under the header trigger (so it must be rendered inside a `relative`
// wrapper); below that it becomes a centred modal over a dimmed backdrop,
// following AttachModal's overlay pattern.

import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ActiveAccount } from '../../lib/activeAccount';
import { useUsage } from '../../lib/usageData';
import {
  providerIdentity,
  pruneHiddenPrefs,
  readUsagePrefs,
  writeUsagePrefs,
  type UsageMode,
  type UsagePrefs,
} from '../../lib/usagePrefs';
import { Empty, ErrorBox } from '../ui';
import { UsageProviderCard } from './UsageProviderCard';
import { fmtClock } from './format';

/** Drives `now` for countdown fallbacks and the absolute reset chips. The data
 *  cadence itself belongs to lib/usageData.tsx; this is display-only. */
const TICK_MS = 30_000;
const DESKTOP_QUERY = '(min-width: 768px)';

const SEGMENTS = [
  { value: 'used', label: 'Used' },
  { value: 'remaining', label: 'Remaining' },
] as const;

export function UsageModal({
  open,
  onClose,
  active = null,
}: {
  open: boolean;
  onClose: () => void;
  /**
   * The account the current project scope runs under (lib/activeAccount.ts),
   * or null when unscoped. Passed in by UsageChip — this component stays
   * fetch-free. Its row sorts first and its cards carry an `active` badge.
   */
  active?: ActiveAccount | null;
}): JSX.Element | null {
  const { accounts, error, loading, lastUpdated, refresh } = useUsage();
  const [prefs, setPrefs] = useState<UsagePrefs>(readUsagePrefs);
  const [now, setNow] = useState(() => Date.now());
  const [desktop, setDesktop] = useState(() => window.matchMedia(DESKTOP_QUERY).matches);

  const dialogRef = useRef<HTMLDivElement>(null);
  const restoreFocus = useRef<HTMLElement | null>(null);

  /** Every card across every account, in payload order — one flat list because
   *  prefs are keyed on card identity, not on which row a card came from. */
  const cards = useMemo(() => accounts.flatMap((a) => a.providers), [accounts]);
  /** The account label only appears when there is more than one account, so a
   *  single-subscription machine sees exactly the card it saw before. */
  const multiAccount = accounts.length > 1;
  /** The scoped project's account. The banner names it whenever a project
   *  scope exists — ESPECIALLY when the usage payload has no row for it, which
   *  is exactly when the metrics on screen belong to a different subscription.
   *  Lifting and the ACTIVE badge additionally require a second account to
   *  mark it against — with one card, "active" would label the only card
   *  there is. */
  const activeKey = active?.account ?? null;
  const markActive = multiAccount && activeKey !== null;
  /** Payload order with the active account's row lifted to the front; identity-
   *  stable when there is nothing to lift, so single-account renders untouched. */
  const ordered = useMemo(() => {
    if (!markActive || activeKey === null) return accounts;
    const hit = accounts.find((a) => a.account === activeKey);
    if (hit === undefined || accounts[0] === hit) return accounts;
    return [hit, ...accounts.filter((a) => a !== hit)];
  }, [accounts, activeKey, markActive]);

  /** One writer for both prefs fields, so storage never drifts from state. */
  const commitPrefs = useCallback((next: UsagePrefs): void => {
    setPrefs(next);
    writeUsagePrefs(next);
  }, []);

  // Drop hidden keys whose window the daemon no longer reports, so a renamed or
  // retired window cannot linger in storage. Identity-stable when there is
  // nothing to prune, so this settles on the first pass instead of looping.
  useEffect(() => {
    if (lastUpdated === null) return;
    const pruned = pruneHiddenPrefs(prefs.hidden, cards);
    if (pruned === prefs.hidden) return;
    commitPrefs({ ...prefs, hidden: pruned });
  }, [cards, lastUpdated, prefs, commitPrefs]);

  // Display-only clock so "resets in …" stays live between polls.
  useEffect(() => {
    if (!open) return undefined;
    setNow(Date.now());
    const tick = window.setInterval(() => setNow(Date.now()), TICK_MS);
    return () => window.clearInterval(tick);
  }, [open]);

  useEffect(() => {
    if (!open) return undefined;
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [open, onClose]);

  // Move focus into the dialog on open; hand it back to the trigger on close.
  useEffect(() => {
    if (!open) return undefined;
    restoreFocus.current =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
    dialogRef.current?.focus();
    return () => {
      restoreFocus.current?.focus();
    };
  }, [open]);

  useEffect(() => {
    const mq = window.matchMedia(DESKTOP_QUERY);
    const sync = (): void => setDesktop(mq.matches);
    sync();
    mq.addEventListener('change', sync);
    return () => mq.removeEventListener('change', sync);
  }, []);

  const setMode = useCallback(
    (mode: UsageMode): void => {
      commitPrefs({ ...prefs, mode });
    },
    [prefs, commitPrefs],
  );

  // Hidden windows are stored per CARD IDENTITY (`${account}:${name}`) and keyed
  // on the server's `window.key`, so two cards may legitimately share a key
  // without one hiding the other's window — including the same provider name
  // reported by two different accounts.
  const toggleWindow = useCallback(
    (card: string, key: string): void => {
      const current = prefs.hidden[card] ?? [];
      const next = current.includes(key)
        ? current.filter((k) => k !== key)
        : [...current, key];
      const hidden = { ...prefs.hidden };
      if (next.length > 0) hidden[card] = next;
      else delete hidden[card]; // no empty arrays in storage
      commitPrefs({ ...prefs, hidden });
    },
    [prefs, commitPrefs],
  );

  const showAllHidden = useCallback(
    (card: string): void => {
      if (prefs.hidden[card] === undefined) return;
      const hidden = { ...prefs.hidden };
      delete hidden[card];
      commitPrefs({ ...prefs, hidden });
    },
    [prefs, commitPrefs],
  );

  if (!open) return null;

  /** Anything worth rendering: a card, or an account that failed loudly enough
   *  to have no cards. Without the second half, a failed account would show the
   *  "nothing reported" empty state and swallow its own error. */
  const hasContent = cards.length > 0 || accounts.some((a) => (a.error ?? '') !== '');
  const mode = prefs.mode;
  const segClass = (active: boolean): string =>
    `rounded-[6px] px-2 py-0.5 font-mono text-[10.5px] font-semibold transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand ${
      active ? 'bg-surface2 text-ink' : 'text-ink-dim hover:text-ink'
    }`;
  const btnClass =
    'rounded-[6px] border border-line px-2 py-0.5 font-mono text-[10px] text-ink-dim transition-colors hover:bg-surface2 hover:text-ink focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand disabled:opacity-50';

  const panel = (
    <div
      ref={dialogRef}
      tabIndex={-1}
      role="dialog"
      aria-label="Subscription usage"
      aria-modal={desktop ? undefined : true}
      onClick={desktop ? undefined : (e) => e.stopPropagation()}
      className={
        desktop
          ? 'absolute right-0 z-40 mt-2 flex max-h-[70vh] w-[380px] flex-col overflow-hidden rounded-xl border border-line bg-surface outline-none'
          : 'flex max-h-[80vh] w-full max-w-md flex-col overflow-hidden rounded-xl border border-line bg-surface outline-none'
      }
    >
      <div className="flex shrink-0 items-center justify-between gap-2 border-b border-line px-3 py-2">
        <span className="flex min-w-0 items-center gap-1.5 font-mono text-[10.5px] tracking-[0.14em] text-ink-dim uppercase">
          <span aria-hidden="true" className="text-brand">
            ◔
          </span>
          usage
        </span>
        <span className="flex shrink-0 items-center gap-1.5">
          <span
            role="group"
            aria-label="usage display mode"
            className="flex gap-0.5 rounded-lg border border-line-strong bg-field p-0.5"
          >
            {SEGMENTS.map((s) => (
              <button
                key={s.value}
                type="button"
                aria-pressed={mode === s.value}
                onClick={() => setMode(s.value)}
                className={segClass(mode === s.value)}
              >
                {s.label}
              </button>
            ))}
          </span>
          <button
            type="button"
            onClick={onClose}
            aria-label="Close usage"
            data-tip="close"
            className={btnClass}
          >
            <span aria-hidden="true">✕</span>
          </button>
        </span>
      </div>

      {/* Which account the scoped project runs under — the answer to "whose
          quota am I burning right now?". Rendered with any project scope; the
          unscoped (fleet-wide) modal is unchanged. NOT gated on multiAccount:
          a bound account the daemon does not poll yields a single-row payload,
          and that is precisely when the operator must be told the metrics on
          screen are another subscription's. */}
      {activeKey !== null && active !== null && (
        <div
          data-active-account={activeKey}
          className="flex shrink-0 items-center gap-1.5 border-b border-line bg-surface2/40 px-3 py-1.5 font-mono text-[10px] text-ink-dim"
        >
          <span aria-hidden="true" className="text-brand">
            ▸
          </span>
          <span className="min-w-0 truncate">
            {active.project} runs as <span className="font-semibold text-ink">{activeKey}</span>
            {active.source === 'default' && <span className="text-ink-faint"> (no binding)</span>}
            {/* An active account the daemon has no usage row for — bound but not
                ingested. Said out loud, or the banner would name an account the
                cards below never show. */}
            {!accounts.some((a) => a.account === activeKey) && (
              <span className="text-ink-faint"> · no usage data for this account</span>
            )}
          </span>
        </div>
      )}

      {/* The body is the only scroller — the header and footer stay pinned. */}
      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-2.5">
        {/* `lastUpdated === null` IS "nothing has ever loaded" — the shared
            poller reports no separate payload object. */}
        {lastUpdated === null && error === null && (
          <div className="flex flex-col gap-2" role="status" aria-label="loading usage">
            {['a', 'b', 'c'].map((k) => (
              <div
                key={k}
                className="h-[92px] animate-pulse rounded-xl border border-line bg-surface2/40"
              />
            ))}
          </div>
        )}

        {/* Hard failure only when nothing has EVER loaded; otherwise stale data
            stays on screen and the footer carries the failure. */}
        {lastUpdated === null && error !== null && (
          <ErrorBox message={error} onRetry={() => void refresh(true)} />
        )}

        {lastUpdated !== null && !hasContent && <Empty>no usage providers reported</Empty>}

        {hasContent && (
          <div className="flex flex-col gap-2">
            {ordered.map((a) => (
              <Fragment key={a.account}>
                {/* An account whose lookup produced no cards at all. Rare, and
                    deliberately not a card: there is nothing to render inside
                    one. The other accounts' cards stay untouched. */}
                {(a.error ?? '') !== '' && (
                  <div
                    data-account={a.account}
                    className="rounded-xl border border-red/40 bg-red/8 px-2.5 py-2 font-mono text-[10.5px] leading-snug break-words text-red"
                  >
                    {multiAccount && <span className="text-ink-dim">{a.account} · </span>}
                    {a.error}
                  </div>
                )}
                {a.providers.map((p) => {
                  const id = providerIdentity(p);
                  return (
                    <UsageProviderCard
                      key={id}
                      p={p}
                      showAccount={multiAccount}
                      active={markActive && a.account === activeKey}
                      mode={mode}
                      hidden={prefs.hidden[id] ?? []}
                      onToggleWindow={(key) => toggleWindow(id, key)}
                      onShowAllHidden={() => showAllHidden(id)}
                      now={now}
                    />
                  );
                })}
              </Fragment>
            ))}
          </div>
        )}
      </div>

      <div className="flex shrink-0 items-center justify-between gap-2 border-t border-line px-3 py-2">
        <span className="min-w-0 truncate font-mono text-[9.5px] text-ink-faint">
          {lastUpdated !== null ? `Last updated: ${fmtClock(lastUpdated)}` : 'not yet loaded'}
          {lastUpdated !== null && error !== null && (
            <span className="text-red"> · refresh failed</span>
          )}
        </span>
        <span className="flex shrink-0 items-center gap-1.5">
          <button
            type="button"
            onClick={() => void refresh(true)}
            disabled={loading}
            className={btnClass}
          >
            {loading ? '…' : 'refresh'}
          </button>
          <button type="button" onClick={onClose} className={btnClass}>
            close
          </button>
        </span>
      </div>
    </div>
  );

  if (desktop) {
    return (
      <>
        {/* Click-anywhere-else dismissal for the anchored popover. Keyboard users
            get Escape, the header ✕ and the footer Close, so the backdrop itself
            stays out of the tab order (same idiom as AttachModal's overlay). */}
        <div aria-hidden="true" className="fixed inset-0 z-30" onClick={onClose} />
        {panel}
      </>
    );
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-bg/70 p-4"
      onClick={onClose}
    >
      {panel}
    </div>
  );
}
