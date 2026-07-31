// Sessions list (design §3.3): status chip row with live counts, live updates
// over WS. Project filtering comes from the GLOBAL header scope switcher
// (useScope) — pushed to the API as a query param; text search lives in the
// header ⌘K palette; status is filtered CLIENT-side so the chip counts always
// reflect the scoped list.
// Redesign layout: status chips (wrapping row), sessions grouped by day under
// mono eyebrow rules, each day one navy list card — aligned table columns at
// ≥900px.

import { useCallback, useEffect, useRef, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import type { Session, SessionStatus, WSMessage } from '../api/types';
import { fetchSessions } from '../api';
import { liveActionText } from '../lib/payload';
import { usePageSearch } from '../lib/pageSearch';
import { useScope } from '../lib/scope';
import { accountKey, accountKeys } from '../lib/sessionAccount';
import { applySessionMessage, useLiveUpdates } from '../lib/ws';
import { ExplainPair } from '../components/Explain';
import { PageSearchInput } from '../components/PageSearchInput';
import { SessionCard } from '../components/SessionCard';
import { Empty, ErrorBox, GroupHeader, Loading } from '../components/ui';

const PAGE_LIMIT = 100;

/** Case-insensitive substring match over title / project / slug / branch. */
function matchesQuery(s: Session, q: string): boolean {
  if (q === '') return true;
  return [s.title, s.projectName, s.projectSlug, s.gitBranch].some(
    (v) => v != null && v.toLowerCase().includes(q),
  );
}

const STATUSES: SessionStatus[] = ['active', 'waiting_approval', 'idle', 'completed', 'killed'];
const STATUS_LABELS: Record<SessionStatus, string> = {
  active: 'active',
  waiting_approval: 'waiting',
  idle: 'idle',
  completed: 'done',
  killed: 'killed',
};

function FilterChip({
  selected,
  onClick,
  children,
}: {
  selected: boolean;
  onClick: () => void;
  children: string;
}): JSX.Element {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={selected}
      className={`shrink-0 rounded-full border px-[11px] py-1 font-mono text-[10.5px] whitespace-nowrap transition-colors ${
        selected
          ? 'border-line-strong bg-surface2 text-ink'
          : 'border-line-strong text-ink-dim hover:text-ink'
      }`}
    >
      {children}
    </button>
  );
}

/** Status chip row (page body) — text search lives in the header ⌘K palette. */
function StatusChips({
  status,
  onStatus,
  counts,
}: {
  status: SessionStatus | null;
  onStatus: (s: SessionStatus | null) => void;
  counts: Record<SessionStatus, number>;
}): JSX.Element {
  return (
    <>
      {STATUSES.map((s) => (
        <FilterChip key={s} selected={status === s} onClick={() => onStatus(status === s ? null : s)}>
          {counts[s] > 0 ? `${STATUS_LABELS[s]} · ${String(counts[s])}` : STATUS_LABELS[s]}
        </FilterChip>
      ))}
    </>
  );
}

/** Subscription chips (migration 0047) — rendered ONLY when the loaded list
 * spans more than one Claude Code account. A one-subscription machine (the
 * common case) gets no extra control: a filter whose every value selects the
 * whole list is a decoration, not a filter.
 *
 * Client-side like the status chips, and for the same reason: the API's
 * ?account= filter would shrink the loaded window, and the chip counts are
 * computed over exactly the rows this page holds. */
function AccountChips({
  keys,
  account,
  onAccount,
  counts,
}: {
  keys: string[];
  account: string | null;
  onAccount: (a: string | null) => void;
  counts: Record<string, number>;
}): JSX.Element | null {
  if (keys.length < 2) return null;
  return (
    <>
      {keys.map((k) => (
        <FilterChip
          key={k}
          selected={account === k}
          onClick={() => onAccount(account === k ? null : k)}
        >
          {`${k} · ${String(counts[k] ?? 0)}`}
        </FilterChip>
      ))}
    </>
  );
}

/* ----- day grouping (presentation only — Redesign "today · sun, jul 6") ----- */

interface DayGroup {
  label: string;
  rows: Session[];
}

function dayLabel(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return 'unknown day';
  const name = d
    .toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' })
    .toLowerCase();
  return d.toDateString() === new Date().toDateString() ? `today · ${name}` : name;
}

function groupByDay(sorted: Session[]): DayGroup[] {
  const groups: DayGroup[] = [];
  for (const s of sorted) {
    const label = dayLabel(s.startedAt);
    const last = groups[groups.length - 1];
    if (last !== undefined && last.label === label) last.rows.push(s);
    else groups.push({ label, rows: [s] });
  }
  return groups;
}

/* ----- plan grouping -------------------------------------------------------
 * A plan run fans out into a controller session plus one session per phase,
 * and every one of them opens with the same boilerplate prompt ("You are
 * executing ONE phase of an approved implementation plan…"). Flat, those rows
 * are indistinguishable. The server resolves the owning plan from the run
 * branch it stamped (session.planGroup), so this view can stack a plan's whole
 * fan-out under one header instead. */

interface PlanGroup {
  taskId: number;
  title: string;
  /** Project of the group's newest row — the Plans page is project-scoped. */
  projectSlug: string;
  rows: Session[];
}

/** Controller (0) → phases (1) → anything else the run pulled in (2). */
function planRank(s: Session): number {
  const role = s.planGroup?.role;
  if (role === 'controller') return 0;
  if (role === 'phase') return 1;
  return 2;
}

/** In-group order: controller first, then phases by seq, then start time desc. */
function comparePlanRows(a: Session, b: Session): number {
  const ra = planRank(a);
  const rb = planRank(b);
  if (ra !== rb) return ra - rb;
  if (ra === 1) {
    // A phase with no seq sorts after the numbered ones rather than at 0.
    const sa = a.planGroup?.phaseSeq ?? Number.MAX_SAFE_INTEGER;
    const sb = b.planGroup?.phaseSeq ?? Number.MAX_SAFE_INTEGER;
    if (sa !== sb) return sa - sb;
  }
  return b.startedAt.localeCompare(a.startedAt);
}

/**
 * Split an already-sorted (newest-first) list into plan groups plus the
 * leftovers. Nothing is dropped: every row lands in exactly one bucket.
 */
function groupByPlan(sorted: Session[]): { plans: PlanGroup[]; other: Session[] } {
  const byTask = new Map<number, PlanGroup>();
  const other: Session[] = [];
  for (const s of sorted) {
    const g = s.planGroup;
    if (g == null) {
      other.push(s);
      continue;
    }
    const existing = byTask.get(g.taskId);
    if (existing === undefined) {
      byTask.set(g.taskId, { taskId: g.taskId, title: g.title, projectSlug: s.projectSlug, rows: [s] });
    } else {
      existing.rows.push(s);
    }
  }
  // Input order is newest-first, so each group's FIRST row is its newest
  // session — order the groups by that before re-sorting rows inside them.
  const plans = [...byTask.values()];
  plans.sort((a, b) => (b.rows[0]?.startedAt ?? '').localeCompare(a.rows[0]?.startedAt ?? ''));
  for (const p of plans) p.rows.sort(comparePlanRows);
  return { plans, other };
}

/** `#<seq> <name>` for a phase row; null keeps the session's own title. */
function planRowLabel(s: Session): string | null {
  const g = s.planGroup;
  if (g == null || g.role !== 'phase' || g.phaseSeq == null || g.phaseName == null) return null;
  return `#${String(g.phaseSeq)} ${g.phaseName}`;
}

/** Per-status tallies for a group header, in the page's chip vocabulary. */
function statusSummary(rows: Session[]): string {
  const counts: Record<SessionStatus, number> = {
    active: 0,
    waiting_approval: 0,
    idle: 0,
    completed: 0,
    killed: 0,
  };
  for (const s of rows) counts[s.status] += 1;
  return STATUSES.filter((s) => counts[s] > 0)
    .map((s) => `${STATUS_LABELS[s]} ${String(counts[s])}`)
    .join(' · ');
}

/** Collapsible header for one plan's fan-out. */
function PlanGroupHeader({
  group,
  collapsed,
  onToggle,
  slug,
}: {
  group: PlanGroup;
  collapsed: boolean;
  onToggle: () => void;
  /** Project slug of the surrounding route, when the page is project-scoped. */
  slug: string | undefined;
}): JSX.Element {
  const plansHref = `/p/${slug ?? group.projectSlug}/plans?plan=${String(group.taskId)}`;
  return (
    <div className="mt-6 mb-1.5 flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={!collapsed}
        className="flex min-w-0 items-baseline gap-2 text-left focus-visible:outline-2 focus-visible:outline-brand"
      >
        <span className="font-mono text-[10.5px] text-ink-faint">{collapsed ? '▸' : '▾'}</span>
        <span className="min-w-0 truncate text-[13.5px] font-semibold">{group.title}</span>
        <span className="shrink-0 font-mono text-[10.5px] text-ink-faint">
          {group.rows.length} session{group.rows.length === 1 ? '' : 's'}
        </span>
      </button>
      <span className="font-mono text-[10.5px] text-ink-dim">{statusSummary(group.rows)}</span>
      <Link
        to={plansHref}
        className="ml-auto shrink-0 font-mono text-[10.5px] text-ink-dim hover:text-brand"
      >
        open plan →
      </Link>
    </div>
  );
}

export function Sessions(): JSX.Element {
  // Project filtering comes from the global header scope switcher; the title
  // filter comes from the contextual header search input.
  const { scope, scopeProject } = useScope();
  const { slug } = useParams<{ slug?: string }>();
  const query = usePageSearch();
  const [status, setStatus] = useState<SessionStatus | null>(null);
  // null = every subscription (migration 0047); a key narrows to one account.
  const [account, setAccount] = useState<string | null>(null);
  // null = follow the default (grouped whenever the result set HAS a plan
  // group); a boolean is the operator's explicit override for this visit.
  const [groupPref, setGroupPref] = useState<boolean | null>(null);
  const [collapsed, setCollapsed] = useState<ReadonlySet<number>>(new Set());
  const [sessions, setSessions] = useState<Session[] | null>(null);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [nowById, setNowById] = useState<Record<number, string>>({});
  const loadingMoreRef = useRef(false);
  const sentinelRef = useRef<HTMLDivElement>(null);
  // Request generation: bumped whenever the scope resets the list so stale
  // in-flight page responses (old scope) are dropped, not appended —
  // otherwise a slow page-2 fetch would leak old-scope rows and resurrect
  // the old cursor (scope filtering is server-side, not re-checked here).
  const genRef = useRef(0);

  // First page — also the WS-reconnect refetch: the live socket keeps the
  // loaded window fresh in between, so resetting to page 1 on reconnect is
  // the simplest correct behaviour (older pages reload on scroll).
  // Only the scope filter goes to the API — status stays client-side so
  // the chip counts can be computed over every status of the loaded list.
  const load = useCallback((): void => {
    const gen = genRef.current;
    const filters: { project?: string } = {};
    if (scope !== null) filters.project = scope;
    fetchSessions(filters, { limit: PAGE_LIMIT })
      .then((page) => {
        if (gen !== genRef.current) return; // stale — filter changed mid-flight
        setSessions(page.sessions);
        setNextCursor(page.nextCursor);
        setError(null);
      })
      .catch((e: unknown) => {
        if (gen !== genRef.current) return;
        setError(String(e));
      });
  }, [scope]);

  useEffect(() => {
    genRef.current += 1; // invalidate in-flight responses for the old filter
    setSessions(null);
    setNextCursor(null);
    load();
  }, [load]);

  // Next page: append, dedup by id (a WS prepend may already hold a row).
  const loadMore = useCallback((): void => {
    if (nextCursor === null || loadingMoreRef.current) return;
    loadingMoreRef.current = true;
    const gen = genRef.current;
    const filters: { project?: string } = {};
    if (scope !== null) filters.project = scope;
    fetchSessions(filters, { limit: PAGE_LIMIT, cursor: nextCursor })
      .then((page) => {
        if (gen !== genRef.current) return; // stale — would leak old-project rows
        setSessions((prev) => {
          const seen = new Set((prev ?? []).map((s) => s.id));
          return [...(prev ?? []), ...page.sessions.filter((s) => !seen.has(s.id))];
        });
        setNextCursor(page.nextCursor);
      })
      .catch((e: unknown) => {
        if (gen !== genRef.current) return;
        setError(String(e));
      })
      .finally(() => {
        loadingMoreRef.current = false;
      });
  }, [nextCursor, scope]);

  // Infinite scroll: a sentinel row after the last day group fetches the next
  // page while one exists (rootMargin prefetches before it is visible).
  useEffect(() => {
    const el = sentinelRef.current;
    if (el === null || nextCursor === null) return undefined;
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) loadMore();
      },
      { rootMargin: '400px' },
    );
    io.observe(el);
    return () => io.disconnect();
  }, [nextCursor, loadMore]);

  // Rows carry the DB path slug, while scope may be the pretty name slug —
  // compare against the resolved project's slug (raw scope as fallback).
  const matchesProject = useCallback(
    (s: Session): boolean => scope === null || s.projectSlug === (scopeProject?.slug ?? scope),
    [scope, scopeProject],
  );

  const onMessage = useCallback(
    (msg: WSMessage): void => {
      if (msg.type === 'event_appended') {
        // step-10 contract: the payload carries sessionId → live "now" line.
        const text = liveActionText(msg.payload.event);
        if (text !== null) {
          const { sessionId } = msg.payload;
          setNowById((prev) => ({ ...prev, [sessionId]: text }));
        }
        return;
      }
      setSessions((prev) => {
        if (prev === null) return prev;
        const next = applySessionMessage(prev, msg);
        return next.filter(matchesProject);
      });
    },
    [matchesProject],
  );
  useLiveUpdates(onMessage, load);

  // Chip counts come from the scoped + title-filtered list (pre-status), over
  // the LOADED pages only — deeper history loads on scroll.
  const loaded = (sessions ?? []).filter((s) => matchesQuery(s, query));
  const counts: Record<SessionStatus, number> = {
    active: 0,
    waiting_approval: 0,
    idle: 0,
    completed: 0,
    killed: 0,
  };
  for (const s of loaded) counts[s.status] += 1;

  // Subscription tallies over the same pre-status list, so switching status
  // never changes what the account chips offer.
  const accounts = accountKeys(loaded);
  const accountCounts: Record<string, number> = {};
  for (const s of loaded) {
    const k = accountKey(s);
    accountCounts[k] = (accountCounts[k] ?? 0) + 1;
  }

  // Status + account filtering happens HERE, before either grouping pass, so
  // both views show exactly the same rows — only their arrangement differs.
  const sorted = loaded
    .filter((s) => status === null || s.status === status)
    .filter((s) => account === null || accountKey(s) === account)
    .sort((a, b) => b.startedAt.localeCompare(a.startedAt));
  const groups = groupByDay(sorted);

  const hasPlanGroups = sorted.some((s) => s.planGroup != null);
  const grouped = hasPlanGroups && (groupPref ?? true);
  const { plans, other } = grouped
    ? groupByPlan(sorted)
    : { plans: [] as PlanGroup[], other: [] as Session[] };
  const otherGroups = groupByDay(other);
  const toggleGroup = (taskId: number): void => {
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(taskId)) next.delete(taskId);
      else next.add(taskId);
      return next;
    });
  };

  return (
    <div className="px-4 pt-6 pb-20 desk:px-10 desk:pt-[34px] desk:pb-28">
      {/* Stop vs Kill is explained once here, not beside each row's button:
          SessionCard's action slot renders for every live row and for every
          finished row with a known PID, so a per-row explainer would stamp a
          "?" down the whole list for a distinction read exactly once. */}
      {/* The h1 stays block-level and the pair does the flexing: an inline-flex
          heading shrink-wraps, and a heading that wraps then parks its trigger
          in the vertical middle of the right margin, touching neither line. */}
      <h1 className="font-display text-[26px] font-medium tracking-[-0.01em] desk:text-[30px]">
        <ExplainPair id="kill-vs-stop">Sessions</ExplainPair>
      </h1>
      <div className="mt-1.5 font-mono text-[11px] text-ink-dim">
        {sorted.length} match · newest first
      </div>

      {/* Status chips + in-page title search (moved out of the header). Project
          filtering lives in the sidebar scope switcher; ⌘K opens global search. */}
      <div className="mt-5 flex flex-wrap items-center gap-2">
        <StatusChips status={status} onStatus={setStatus} counts={counts} />
        <AccountChips
          keys={accounts}
          account={account}
          onAccount={setAccount}
          counts={accountCounts}
        />
        {/* Offered only when the loaded list actually holds a plan run —
            otherwise the toggle would promise an arrangement it cannot make. */}
        {hasPlanGroups && (
          <FilterChip selected={grouped} onClick={() => setGroupPref(!grouped)}>
            group by plan
          </FilterChip>
        )}
        <PageSearchInput className="sm:ml-auto" />
      </div>

      {error !== null && <ErrorBox message={error} onRetry={load} />}
      {sessions === null && error === null && <Loading label="sessions…" />}
      {sessions !== null && sorted.length === 0 && (
        <Empty>
          {query !== '' ? (
            <>no sessions match the current filter — try a different search or clear it</>
          ) : status !== null ? (
            <>no {STATUS_LABELS[status]} sessions — clear the status filter to see the rest</>
          ) : account !== null ? (
            <>no sessions from the {account} account — clear the account filter to see the rest</>
          ) : (
            <>
              no sessions yet — run{' '}
              <span className="font-mono text-ink">swarmery ingest &lt;file.jsonl&gt;</span>
            </>
          )}
        </Empty>
      )}
      {grouped ? (
        <>
          {plans.map((g) => (
            <section key={g.taskId}>
              <PlanGroupHeader
                group={g}
                collapsed={collapsed.has(g.taskId)}
                onToggle={() => toggleGroup(g.taskId)}
                slug={slug}
              />
              {!collapsed.has(g.taskId) && (
                <div className="divide-y divide-line-soft">
                  {g.rows.map((s) => (
                    <SessionCard
                      key={s.id}
                      session={s}
                      now={nowById[s.id] ?? null}
                      flat
                      hideProject={scope !== null}
                      label={planRowLabel(s)}
                    />
                  ))}
                </div>
              )}
            </section>
          ))}
          {/* Everything a plan did not spawn keeps its day grouping and stays
              fully visible — grouping rearranges the list, it never hides it. */}
          {other.length > 0 && (
            <>
              <div className="mt-7 flex flex-wrap items-baseline gap-x-2.5 border-t border-line pt-5">
                <span className="text-[13.5px] font-semibold">Other sessions</span>
                <span className="font-mono text-[10.5px] text-ink-faint">
                  {other.length} not spawned by a plan
                </span>
              </div>
              {otherGroups.map((g) => (
                <section key={g.label}>
                  <GroupHeader>{g.label}</GroupHeader>
                  <div className="divide-y divide-line-soft">
                    {g.rows.map((s) => (
                      <SessionCard
                        key={s.id}
                        session={s}
                        now={nowById[s.id] ?? null}
                        flat
                        hideProject={scope !== null}
                      />
                    ))}
                  </div>
                </section>
              ))}
            </>
          )}
        </>
      ) : (
        groups.map((g) => (
          <section key={g.label}>
            <GroupHeader>{g.label}</GroupHeader>
            <div className="divide-y divide-line-soft">
              {g.rows.map((s) => (
                <SessionCard
                  key={s.id}
                  session={s}
                  now={nowById[s.id] ?? null}
                  flat
                  hideProject={scope !== null}
                />
              ))}
            </div>
          </section>
        ))
      )}
      {nextCursor !== null && (
        <div ref={sentinelRef} className="py-6 text-center font-mono text-[11px] text-ink-faint">
          loading more…
        </div>
      )}
    </div>
  );
}
