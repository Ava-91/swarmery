// Project-scoped pending-approvals panel for the fleet sidebar — additive to the
// existing global Approvals nav badge in App.tsx, never a replacement. Renders
// only when a project is selected (App.tsx gates on scope !== null), so the
// unscoped behaviour (global badge, all pending visible) is unchanged. Per the
// invariant at pages/Overview.tsx:1008 ("a pending approval must never be
// invisible"), this panel narrows what's SHOWN here; it never hides another
// project's pending rows from the app — the footer link always routes there.

import { useCallback, useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import type { PermissionRequest, WSMessage } from '../api/types';
import { fetchApprovals, resolveApproval, type ApprovalAction } from '../api';
import { OPTIMISTIC_STATUS } from '../lib/approvals';
import { useSessionProjectIndex } from '../lib/sessionProjects';
import { applyPermissionMessage, useLiveUpdates } from '../lib/ws';
import { ApprovalContext } from './ApprovalContext';

/** Minimum gap between unknown-session re-resolution attempts. Approvals are
 * human-gated and rare, so 10s is generous for the legitimate "session started
 * after our last /api/sessions fetch" case while capping the pathological
 * out-of-window case at 2 requests per window instead of 2 per frame. */
const MISS_COOLDOWN_MS = 10_000;

export function ProjectApprovalsSection({
  scope,
  scopeSlug,
  totalPending,
}: {
  /** useScope().scope, passed verbatim to fetchApprovals's ?project= — the
   * server matches slug, id, and kebab name alike (lib/scope.tsx contract). */
  scope: string;
  /** scopeProject?.slug ?? scope — the DB path slug WS rows carry; NEVER compare
   * WS payloads against the raw `scope` value (lib/scope.tsx:33-40). */
  scopeSlug: string;
  /** Fleet-wide pending count — AppShell's existing unscoped badge state. */
  totalPending: number;
}): JSX.Element {
  const [requests, setRequests] = useState<PermissionRequest[] | null>(null);
  const [expanded, setExpanded] = useState(false);
  const [busyId, setBusyId] = useState<number | null>(null);
  const { bySessionId, refresh: refreshSessions } = useSessionProjectIndex(true);
  /** Guards the unknown-session miss path against a refetch storm (pre-mortem R3). */
  const lastMissRef = useRef(0);

  const load = useCallback((): void => {
    fetchApprovals('pending', scope)
      .then(setRequests)
      .catch(() => setRequests(null)); // endpoint unavailable → hide, not crash
  }, [scope]);
  useEffect(load, [load]);

  const resolve = (request: PermissionRequest, action: ApprovalAction, reason?: string): void => {
    setBusyId(request.id);
    // Optimistic: a resolved row leaves the PENDING-only sidebar list immediately
    // (unlike Approvals.tsx, there is no local history to transfer it into).
    const nowResolved = OPTIMISTIC_STATUS[action] !== 'pending';
    if (nowResolved) {
      setRequests((prev) => (prev === null ? prev : prev.filter((r) => r.id !== request.id)));
    }
    resolveApproval(request.id, action, reason)
      .catch(() => {
        // 409 (resolved elsewhere / expired first) or transport failure — the
        // authoritative refetch reconciles either way (same posture as
        // Approvals.tsx:688-692).
        load();
      })
      .finally(() => setBusyId(null));
  };

  const onMessage = useCallback(
    (msg: WSMessage): void => {
      if (msg.type !== 'permission_requested' && msg.type !== 'permission_resolved') return;
      const session = bySessionId.get(msg.payload.sessionId);
      if (session === undefined) {
        // Unknown session: never guess membership — re-resolve from the server.
        //
        // /api/sessions returns the NEWEST 100 sessions (measured) while the DB
        // may hold thousands. A session that just started is at the TOP of that
        // window, so refreshing resolves the common case. A session OLDER than
        // the window can never be resolved by retrying — so without a guard,
        // every frame for it would fire 2 requests forever (tech-lead pre-mortem
        // R3). Rate-limit the miss path to once per MISS_COOLDOWN_MS; the list
        // stays correct regardless because load() is server-authoritative via
        // the Phase 1 ?project= filter.
        const now = Date.now();
        if (now - lastMissRef.current >= MISS_COOLDOWN_MS) {
          lastMissRef.current = now;
          refreshSessions();
          load();
        }
        return;
      }
      if (session.projectSlug !== scopeSlug) return; // belongs to another project
      setRequests((prev) => {
        if (prev === null) return prev;
        if (msg.type === 'permission_resolved') return prev.filter((r) => r.id !== msg.payload.id);
        return applyPermissionMessage(prev, msg); // permission_requested: upsert
      });
    },
    [scopeSlug, bySessionId, refreshSessions, load],
  );
  useLiveUpdates(onMessage, load);

  if (requests === null) return <></>;
  const count = requests.length;
  const others = Math.max(0, totalPending - count);

  return (
    <div className="flex flex-col gap-0.5">
      <div className="mt-4 mb-1 px-3 font-mono text-[10px] font-medium tracking-[0.14em] text-ink-faint uppercase">
        Project approvals
      </div>
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
        disabled={count === 0}
        className="flex h-[38px] items-center gap-3 rounded-[10px] border border-transparent px-3 text-ink-dim transition-colors hover:bg-surface2/50 hover:text-ink disabled:cursor-default disabled:hover:bg-transparent disabled:hover:text-ink-dim"
      >
        <span className="w-[16px] shrink-0 text-center text-[16px] leading-none" aria-hidden="true">
          {expanded ? '▾' : '▸'}
        </span>
        <span className="truncate text-[13.5px] font-medium">Pending here</span>
        {count > 0 && (
          <span className="ml-auto flex h-[18px] min-w-[18px] items-center justify-center rounded-full bg-amber px-[5px] font-mono text-[10px] font-bold text-bg">
            {count}
          </span>
        )}
      </button>
      {expanded && count > 0 && (
        <div className="flex flex-col gap-1.5 px-2 py-1.5">
          {requests.map((r) => (
            <SidebarApprovalRow
              key={r.id}
              request={r}
              busy={busyId === r.id}
              onResolve={(action, reason) => resolve(r, action, reason)}
            />
          ))}
        </div>
      )}
      {others > 0 && (
        <Link
          to="/approvals"
          className="px-3 py-1 font-mono text-[10.5px] text-ink-faint transition-colors hover:text-ink-dim"
        >
          {others} more in other projects →
        </Link>
      )}
    </div>
  );
}

/**
 * One sidebar pending row: structured context via ApprovalContext plus inline
 * approve/deny (trimmed from Approvals.tsx's PendingCard — no raw-JSON toggle,
 * AskUserQuestion form, or "always allow" shortcut; those stay on the full
 * /approvals page).
 */
function SidebarApprovalRow({
  request,
  busy,
  onResolve,
}: {
  request: PermissionRequest;
  busy: boolean;
  onResolve: (action: ApprovalAction, reason?: string) => void;
}): JSX.Element {
  const [denying, setDenying] = useState(false);
  const [reason, setReason] = useState('');

  return (
    <div className="rounded-md border border-amber/30 bg-surface px-2.5 py-2">
      <ApprovalContext
        request={request}
        sessionSlot={
          <Link
            to={`/sessions/${String(request.sessionId)}`}
            className="font-mono text-[10px] text-ink-dim transition-colors hover:text-brand"
          >
            open session →
          </Link>
        }
      />
      <div className="mt-2 flex flex-wrap items-center gap-1.5">
        <button
          type="button"
          disabled={busy}
          onClick={() => onResolve('approve')}
          className="rounded-md border border-green/45 bg-green/12 px-2.5 py-1 font-mono text-[10.5px] font-bold text-green transition-colors hover:bg-green/20 disabled:opacity-50"
        >
          approve
        </button>
        <button
          type="button"
          disabled={busy}
          aria-expanded={denying}
          onClick={() => setDenying((v) => !v)}
          className="rounded-md border border-red/40 px-2.5 py-1 font-mono text-[10.5px] text-red transition-colors hover:bg-red/10 disabled:opacity-50"
        >
          deny{denying ? ' ▴' : ''}
        </button>
      </div>
      {denying && (
        <form
          className="mt-1.5 flex gap-1.5"
          onSubmit={(e) => {
            e.preventDefault();
            const trimmed = reason.trim();
            onResolve('deny', trimmed === '' ? undefined : trimmed);
          }}
        >
          <input
            type="text"
            autoFocus
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder="reason (optional)"
            aria-label="deny reason"
            className="min-w-0 flex-1 rounded-md border border-line bg-field px-2 py-1 font-mono text-[10.5px] text-ink outline-none placeholder:text-ink-faint focus:border-red/40"
          />
          <button
            type="submit"
            disabled={busy}
            className="rounded-md border border-red/40 bg-red/10 px-2 py-1 font-mono text-[10.5px] font-semibold text-red transition-colors hover:bg-red/20 disabled:opacity-50"
          >
            confirm
          </button>
        </form>
      )}
    </div>
  );
}
