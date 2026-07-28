// Plans / Epics (fusion phase 10 + plans-page-lifecycle phase 2): a workspace
// plan IS an epic. This tab lists the project's epics (plan dirs the ingester
// parsed) behind Active/Done/Archived filter tabs, drills into a phase
// timeline (seq order, depends-on badges, per-phase progress, derived status
// chips), offers plan lifecycle controls (Pause / Resume / Archive / Restore —
// file operations on the daemon side), and opens any plan doc in a
// preview/edit drawer — the workspace folder becomes invisible infrastructure
// (read, edit, track from the platform; files stay the storage).
//
// Legacy chip: phases that were activated into a board task before the
// plan↔board decoupling (interactive-planning-v2 phase 4) still show the
// "activated · <column>" chip from the DTO's boardTaskExternalId/boardColumn
// fields. No new activations can be created; the Board page is exclusively for
// tasks created on the board. Phase runs are handled by the phase-run mechanism
// (phase 5).
//
// Liveness: the epic list refetches on the board's `task_updated` WS signal
// (column moves on legacy-linked board tasks) AND on `plan_updated` (checkbox
// flips, lifecycle transitions, plan rescans) so progress ticks without a reload.

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import type { BoardColumn, Epic, EpicPhase, WSMessage } from '../api/types';
import {
  cancelEpicPhaseRun,
  epicLifecycle,
  fetchEpics,
  fetchPlanDoc,
  runEpicPhase,
  type EpicLifecycleAction,
} from '../api';
import { useProjectWorkspace } from '../workspace/ProjectContext';
import { useLiveUpdates } from '../lib/ws';
import { Markdown } from '../lib/markdown';
import { Empty, ErrorBox, Loading } from '../components/ui';
import { PlanDocDrawer } from '../workspace/PlanDocDrawer';

type DrawerMode = 'preview' | 'edit';

/** A board column that counts as "resolved" for the dependency gate. */
function isResolvedColumn(col: BoardColumn | null): boolean {
  return col === 'done' || col === 'archived';
}

type PhaseStatus = 'pending' | 'in_progress' | 'done' | 'blocked';

/** Derives a phase's display status from checkbox progress and the dependency gate. */
function phaseStatus(p: EpicPhase, resolvedSeqs: Set<number>): PhaseStatus {
  // An activated phase whose board task is resolved is done regardless of
  // checkbox progress — the board is the source of truth once dispatched.
  if (isResolvedColumn(p.boardColumn)) return 'done';
  if (p.checkboxesTotal > 0 && p.checkboxesDone === p.checkboxesTotal) return 'done';
  // The doc's own `Status: In progress` marker wins over the dependency gate —
  // an executor writing it is literally working on the phase right now.
  // (`done` must be earned by ticking every checkbox; a `done` marker alone is ignored.)
  if (p.docStatus === 'in_progress') return 'in_progress';
  if (p.dependsOn.some((seq) => !resolvedSeqs.has(seq))) return 'blocked';
  if (p.checkboxesDone > 0 || p.boardColumn === 'in_progress' || p.boardColumn === 'in_review')
    return 'in_progress';
  return 'pending';
}

/** Re-renders on an interval so elapsed labels (doc activity, running phases)
 * stay fresh. */
function useNow(intervalMs: number): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => {
      setNow(Date.now());
    }, intervalMs);
    return () => {
      clearInterval(id);
    };
  }, [intervalMs]);
  return now;
}

function formatAgo(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${String(s)}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${String(m)}m`;
  const h = Math.floor(m / 60);
  return `${String(h)}h ${String(m % 60)}m`;
}

/** Past this silence window an in-progress phase is flagged as possibly stuck. */
const STALL_AFTER_MS = 15 * 60_000;

/** Liveness pulse for an in-progress phase. Every executor edit (checkbox
 * tick, Status flip) touches the phase doc, so "time since last doc edit"
 * answers the question the chip alone can't: is the session actually working
 * right now, or has it silently died? */
function PhaseActivity({ docUpdatedAt }: { docUpdatedAt: string | null }): JSX.Element | null {
  const now = useNow(30_000);
  if (docUpdatedAt === null) return null;
  const elapsed = now - Date.parse(docUpdatedAt);
  const stalled = elapsed > STALL_AFTER_MS;
  return (
    <span
      className={`inline-flex shrink-0 items-center gap-1 font-mono text-[9.5px] ${
        stalled ? 'text-amber' : 'text-brand'
      }`}
      title={
        stalled
          ? 'no phase-doc edits for a while — the executor may be stuck'
          : 'the executor is actively editing this phase doc'
      }
    >
      <span className={`h-1.5 w-1.5 rounded-full ${stalled ? 'bg-amber' : 'animate-pulse bg-brand'}`} />
      {stalled ? `no activity · ${formatAgo(elapsed)}` : `active · ${formatAgo(elapsed)} ago`}
    </span>
  );
}

/** Elapsed since a phase run started — the chip on a running phase. */
function fmtElapsed(fromIso: string, now: number): string {
  const s = Math.max(0, Math.floor((now - Date.parse(fromIso)) / 1000));
  if (s < 60) return `${String(s)}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${String(m)}m ${String(s % 60)}s`;
  return `${String(Math.floor(m / 60))}h ${String(m % 60)}m`;
}

const PHASE_CHIP: Record<PhaseStatus, { label: string; cls: string }> = {
  done: { label: 'done', cls: 'border-green/40 text-green' },
  in_progress: { label: 'in progress', cls: 'border-brand/40 text-brand' },
  blocked: { label: 'blocked', cls: 'border-red/40 text-red' },
  pending: { label: 'pending', cls: 'border-line text-ink-faint' },
};

// Plan status badge — the theme has no `yellow` token; `amber` is the app's
// semantic waiting/approval color, so paused uses it.
const STATUS_BADGE: Record<Epic['status'], string> = {
  active: 'border-brand/40 text-brand',
  paused: 'border-amber/40 text-amber',
  done: 'border-green/40 text-green',
  archived: 'border-line text-ink-faint',
};

/** Which lifecycle buttons a plan in a given status offers. */
const LIFECYCLE_ACTIONS: Record<Epic['status'], { action: EpicLifecycleAction; label: string }[]> = {
  active: [
    { action: 'pause', label: 'Pause' },
    { action: 'archive', label: 'Archive' },
  ],
  paused: [
    { action: 'resume', label: 'Resume' },
    { action: 'archive', label: 'Archive' },
  ],
  done: [{ action: 'archive', label: 'Archive' }],
  archived: [{ action: 'restore', label: 'Restore' }],
};

type EpicFilter = 'active' | 'done' | 'archived';
const FILTERS: EpicFilter[] = ['active', 'done', 'archived'];

/** Which filter tab an epic belongs to (paused plans live under Active). */
function epicFilterOf(status: Epic['status']): EpicFilter {
  return status === 'active' || status === 'paused' ? 'active' : status;
}

export function Plans(): JSX.Element {
  const { project, projectId, loading: projLoading } = useProjectWorkspace();
  const [epics, setEpics] = useState<Epic[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<number | null>(null); // taskId
  const [filter, setFilter] = useState<EpicFilter>('active');
  const [actionError, setActionError] = useState<string | null>(null);
  const [busyLifecycle, setBusyLifecycle] = useState(false);
  const [editDoc, setEditDoc] = useState<{
    taskId: number;
    path: string;
    title: string;
    mode: DrawerMode;
  } | null>(null);

  const reload = useCallback((): void => {
    if (projectId === null) {
      setEpics([]);
      return;
    }
    fetchEpics(projectId)
      .then((rows) => {
        setEpics(rows);
        setError(null);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)));
  }, [projectId]);

  useEffect(() => {
    reload();
  }, [reload]);

  // A board task changing (activation, a move to done) can change a phase's
  // gate, and plan_updated fires on checkbox flips / lifecycle transitions —
  // refetch the epics on either signal for this project.
  const onMessage = useCallback(
    (msg: WSMessage): void => {
      if (msg.type !== 'task_updated' && msg.type !== 'plan_updated') return;
      if (projectId !== null && msg.payload.projectId !== projectId) return;
      reload();
    },
    [projectId, reload],
  );
  useLiveUpdates(onMessage, reload);

  // Reconcile net while any visible phase run is live: plan_updated covers the
  // run edges, this 5s poll covers a missed frame mid-run. Cleared when none run.
  const anyRunning = useMemo(
    () => (epics ?? []).some((e) => e.phases.some((p) => p.runState === 'running')),
    [epics],
  );
  useEffect(() => {
    if (!anyRunning) return;
    const id = setInterval(reload, 5000);
    return () => {
      clearInterval(id);
    };
  }, [anyRunning, reload]);

  const filtered = useMemo(
    () => (epics ?? []).filter((e) => epicFilterOf(e.status) === filter),
    [epics, filter],
  );
  const counts = useMemo(() => {
    const c: Record<EpicFilter, number> = { active: 0, done: 0, archived: 0 };
    for (const e of epics ?? []) c[epicFilterOf(e.status)] += 1;
    return c;
  }, [epics]);

  // Keep the selection while it stays inside the filtered set; when it leaves
  // (filter switch, lifecycle transition, deletion) fall back to the first.
  useEffect(() => {
    setSelected((cur) => {
      if (cur !== null && filtered.some((e) => e.taskId === cur)) return cur;
      return filtered[0]?.taskId ?? null;
    });
  }, [filtered]);

  const activeEpic = useMemo(
    () => (selected !== null ? (filtered.find((e) => e.taskId === selected) ?? null) : null),
    [filtered, selected],
  );

  const lifecycle = (epic: Epic, action: EpicLifecycleAction): void => {
    if (
      action === 'archive' &&
      !window.confirm('Archive this plan? The task folder moves to the archive/ zone.')
    )
      return;
    setBusyLifecycle(true);
    setActionError(null);
    epicLifecycle(epic.taskId, action)
      .then(() => reload())
      .catch((e: unknown) => {
        setActionError(e instanceof Error ? e.message : String(e));
        // A 409 usually means the UI acted on stale state — refresh to reconcile.
        reload();
      })
      .finally(() => setBusyLifecycle(false));
  };

  if (projLoading) return <Loading label="workspace…" />;
  if (project === null) {
    return (
      <div className="px-4 py-8 desk:px-8">
        <Empty>unknown project — pick one from the switcher</Empty>
      </div>
    );
  }
  if (error !== null) {
    return (
      <div className="px-4 py-6 desk:px-8">
        <ErrorBox message={error} onRetry={reload} />
      </div>
    );
  }
  if (epics === null) return <Loading label="epics…" />;
  if (epics.length === 0) {
    return (
      <div className="px-4 py-8 desk:px-8">
        <Empty>
          no epics yet — a plan under this project&apos;s workspace (a{' '}
          <code className="font-mono text-[11px]">plan/</code> dir with a phase table) becomes an epic here
        </Empty>
      </div>
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col px-3 py-4 desk:px-6">
      {actionError !== null && (
        <div
          role="alert"
          className="mb-3 flex items-center gap-2 rounded-lg border border-red/40 bg-red/10 px-3 py-1.5 font-mono text-[11px] text-red"
        >
          <span className="min-w-0 flex-1">{actionError}</span>
          <button type="button" onClick={() => setActionError(null)} aria-label="dismiss" className="text-red/70">
            ×
          </button>
        </div>
      )}

      <div className="flex min-h-0 flex-1 gap-5">
        {/* Epic list behind status filter tabs. */}
        <div className="flex w-[280px] shrink-0 flex-col">
          <div className="mb-2 flex items-center gap-1" role="tablist" aria-label="plan status filter">
            {FILTERS.map((f) => (
              <button
                key={f}
                type="button"
                role="tab"
                aria-selected={filter === f}
                onClick={() => setFilter(f)}
                className={`rounded-md border px-2 py-1 font-mono text-[10.5px] capitalize transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand ${
                  filter === f
                    ? 'border-line-strong bg-surface2 text-brand'
                    : 'border-transparent text-ink-dim hover:text-ink'
                }`}
              >
                {f} ({counts[f]})
              </button>
            ))}
          </div>
          <div className="min-h-0 flex-1 space-y-1.5 overflow-y-auto pr-1">
            {filtered.length === 0 ? (
              <Empty>no {filter} plans</Empty>
            ) : (
              filtered.map((e) => (
                <button
                  key={e.taskId}
                  type="button"
                  onClick={() => setSelected(e.taskId)}
                  aria-current={selected === e.taskId}
                  className={`block w-full rounded-lg border px-3 py-2.5 text-left transition-colors ${
                    selected === e.taskId
                      ? 'border-line-strong bg-surface2'
                      : 'border-line bg-surface/40 hover:border-line-strong'
                  }`}
                >
                  <div className="truncate text-[13px] font-medium text-ink">{e.title}</div>
                  <div className="mt-0.5 font-mono text-[10px] text-ink-faint">
                    {e.startedAt !== null ? e.startedAt.slice(0, 10) : e.externalId}
                    {' · '}
                    {e.phases.length} phase{e.phases.length === 1 ? '' : 's'}
                  </div>
                  <ProgressBar done={e.rollup.done} total={e.rollup.total} className="mt-2" />
                </button>
              ))
            )}
          </div>
        </div>

        {/* Epic detail: phase timeline. */}
        <div className="flex min-w-0 flex-1 flex-col overflow-y-auto">
          {activeEpic === null ? (
            <>
              {/* Invisible spacer — same height as the tablist above the list column so the
                  empty states in both columns share the same vertical baseline. */}
              <div className="mb-2 flex items-center gap-1 opacity-0 pointer-events-none" aria-hidden="true">
                <span className="rounded-md border px-2 py-1 font-mono text-[10.5px]">&nbsp;</span>
              </div>
              <Empty>select an epic</Empty>
            </>
          ) : (
            <EpicDetail
              epic={activeEpic}
              busyLifecycle={busyLifecycle}
              onLifecycle={lifecycle}
              onOpenDoc={(path, title, mode) =>
                setEditDoc({ taskId: activeEpic.taskId, path, title, mode })
              }
              onChanged={reload}
            />
          )}
        </div>
      </div>

      {editDoc !== null && (
        <PlanDocDrawer
          taskId={editDoc.taskId}
          path={editDoc.path}
          title={editDoc.title}
          initialMode={editDoc.mode}
          onClose={() => setEditDoc(null)}
          onChanged={reload}
        />
      )}
    </div>
  );
}

function EpicDetail({
  epic,
  busyLifecycle,
  onLifecycle,
  onOpenDoc,
  onChanged,
}: {
  epic: Epic;
  busyLifecycle: boolean;
  onLifecycle: (epic: Epic, action: EpicLifecycleAction) => void;
  onOpenDoc: (path: string, title: string, mode: DrawerMode) => void;
  onChanged: () => void;
}): JSX.Element {
  // Which seq numbers are "resolved" — their board task is done/archived OR
  // every checkbox in their doc is ticked (file-driven completion without
  // board activation). Used to render depends-on badges.
  const resolvedSeqs = useMemo(() => {
    const s = new Set<number>();
    for (const p of epic.phases) {
      if (
        isResolvedColumn(p.boardColumn) ||
        (p.checkboxesTotal > 0 && p.checkboxesDone === p.checkboxesTotal) ||
        p.runState === 'done'
      )
        s.add(p.seq);
    }
    return s;
  }, [epic.phases]);

  // Details modal (tabbed summary / checks / doc): one phase, or — when
  // `phase` is undefined — the plan itself (SUMMARY.md / per-phase checks / README).
  const [modal, setModal] = useState<{ phase?: EpicPhase } | null>(null);

  // Phase-run controls (interactive-planning-v2 phase 6). The server re-checks
  // every gate; the client-side disable is a courtesy, and a 409 body (unmet
  // deps, already running) surfaces in the inline error strip below.
  const [runBusy, setRunBusy] = useState<number | null>(null); // phase id
  const [runMsg, setRunMsg] = useState<string | null>(null);
  const now = useNow(1000);

  const startRun = (phaseId: number): void => {
    setRunBusy(phaseId);
    setRunMsg(null);
    runEpicPhase(epic.taskId, phaseId)
      .then(() => onChanged())
      .catch((e: unknown) => setRunMsg(e instanceof Error ? e.message : String(e)))
      .finally(() => setRunBusy(null));
  };
  const cancelRun = (phaseId: number): void => {
    setRunBusy(phaseId);
    setRunMsg(null);
    cancelEpicPhaseRun(epic.taskId, phaseId)
      .then(() => onChanged())
      .catch((e: unknown) => setRunMsg(e instanceof Error ? e.message : String(e)))
      .finally(() => setRunBusy(null));
  };

  return (
    <div className="pr-1">
      <div className="mb-3 flex items-baseline justify-between gap-3">
        <div className="flex min-w-0 items-baseline gap-2">
          <h2 className="truncate text-[15px] font-semibold text-ink">{epic.title}</h2>
          <span
            className={`shrink-0 rounded border px-1.5 py-px font-mono text-[9.5px] ${STATUS_BADGE[epic.status]}`}
          >
            {epic.status}
          </span>
        </div>
        <span className="shrink-0 font-mono text-[11px] text-ink-dim">
          {epic.rollup.done}/{epic.rollup.total} ({Math.round(epic.rollup.pct)}%)
        </span>
      </div>
      <div className="mb-4 flex flex-wrap items-center gap-1.5">
        <button
          type="button"
          onClick={() => onOpenDoc('README.md', `${epic.title} — README`, 'preview')}
          className="rounded-md border border-line px-2 py-1 font-mono text-[10.5px] text-ink-dim transition-colors hover:border-line-strong hover:text-ink"
        >
          ❐ open plan README
        </button>
        {LIFECYCLE_ACTIONS[epic.status].map(({ action, label }) => (
          <button
            key={action}
            type="button"
            disabled={busyLifecycle}
            onClick={() => onLifecycle(epic, action)}
            className="rounded-md border border-line-strong bg-surface2 px-2 py-1 font-mono text-[10.5px] text-ink-dim transition-colors hover:bg-surface2/70 hover:text-ink disabled:cursor-not-allowed disabled:border-line disabled:text-ink-faint"
          >
            {busyLifecycle ? '…' : label}
          </button>
        ))}
        {epic.hasSummary && (
          <button
            type="button"
            onClick={() => setModal({})}
            title="what was shipped — plan/SUMMARY.md, per-phase checks, README"
            className="rounded-md border border-green/40 px-2 py-1 font-mono text-[10.5px] text-green transition-colors hover:bg-green/10"
          >
            ✓ summary
          </button>
        )}
      </div>

      {runMsg !== null && (
        <div className="mb-2 rounded-md border border-red/40 bg-red/10 px-2.5 py-1.5 font-mono text-[10.5px] text-red">
          {runMsg}
        </div>
      )}

      <ol className="space-y-2">
        {epic.phases.map((p) => {
          const activated = p.activatedAt !== null;
          const status = phaseStatus(p, resolvedSeqs);
          const depsUnmet = p.dependsOn.filter((seq) => !resolvedSeqs.has(seq));
          const runDisabled =
            runBusy !== null || epic.status !== 'active' || depsUnmet.length > 0;
          const runTitle =
            epic.status !== 'active'
              ? 'plan is not active'
              : depsUnmet.length > 0
                ? `waiting on phase ${depsUnmet.join(', ')}`
                : 'run this phase headlessly in an isolated worktree';
          const openDoc = (): void => {
            onOpenDoc(p.docRelPath, `Phase ${String(p.seq)} — ${p.name}`, 'preview');
          };
          return (
            <li
              key={p.id}
              role="button"
              tabIndex={0}
              aria-label={`open Phase ${String(p.seq)} — ${p.name}`}
              onClick={openDoc}
              onKeyDown={(e) => {
                if (e.target !== e.currentTarget) return; // inner buttons handle their own keys
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault();
                  openDoc();
                }
              }}
              className="cursor-pointer rounded-lg border border-line bg-surface/40 px-3 py-2.5 transition-colors hover:border-line-strong focus-visible:outline focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-brand"
            >
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="shrink-0 font-mono text-[10px] text-ink-faint">
                      Phase {p.seq}
                    </span>
                    <span className="truncate text-[13px] font-medium text-ink">{p.name}</span>
                    <span
                      className={`shrink-0 rounded border px-1.5 py-px font-mono text-[9px] ${PHASE_CHIP[status].cls}`}
                    >
                      {PHASE_CHIP[status].label}
                    </span>
                    {status === 'in_progress' && <PhaseActivity docUpdatedAt={p.docUpdatedAt} />}
                  </div>
                  <div className="mt-1 flex flex-wrap items-center gap-1.5">
                    {p.dependsOn.map((seq) => (
                      <span
                        key={seq}
                        className={`rounded border px-1 py-px font-mono text-[9px] ${
                          resolvedSeqs.has(seq)
                            ? 'border-green/40 text-green'
                            : 'border-line text-ink-faint'
                        }`}
                        title={resolvedSeqs.has(seq) ? 'dependency resolved' : 'dependency pending'}
                      >
                        ← #{seq}
                      </span>
                    ))}
                    <span className="font-mono text-[10px] text-ink-faint">
                      {p.checkboxesDone}/{p.checkboxesTotal || 0}
                    </span>
                  </div>
                  <ProgressBar done={p.checkboxesDone} total={p.checkboxesTotal} className="mt-2 max-w-[220px]" />
                </div>

                <div className="flex shrink-0 flex-col items-end gap-1.5">
                  {/* Legacy chip: phases activated into a board task before the plan↔board
                      decoupling still show their board column for historical context. */}
                  {activated && (
                    <span
                      className="rounded border border-brand/40 bg-brand/10 px-1.5 py-px font-mono text-[9.5px] text-brand"
                      title={p.boardTaskExternalId ?? undefined}
                    >
                      activated{p.boardColumn !== null ? ` · ${p.boardColumn}` : ''}
                    </span>
                  )}

                  {/* Direct phase run (no board task): idle/failed offer Run,
                      running shows elapsed + Cancel + session link, done a chip. */}
                  {p.runState === 'running' ? (
                    <span className="flex items-center gap-1.5" onClick={(e) => e.stopPropagation()}>
                      <span
                        className="inline-flex items-center gap-1 rounded border border-brand/40 bg-brand/10 px-1.5 py-px font-mono text-[9.5px] text-brand"
                        title="headless run in progress"
                      >
                        <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-brand" />
                        Running
                        {p.runStartedAt !== null ? ` · ${fmtElapsed(p.runStartedAt, now)}` : ''}
                      </span>
                      {p.runSessionUuid !== null && (
                        <Link
                          to={`/sessions/${p.runSessionUuid}`}
                          onClick={(e) => e.stopPropagation()}
                          className="font-mono text-[9.5px] text-ink-dim underline-offset-2 transition-colors hover:text-brand hover:underline"
                        >
                          session
                        </Link>
                      )}
                      <button
                        type="button"
                        disabled={runBusy !== null}
                        onClick={(e) => {
                          e.stopPropagation();
                          cancelRun(p.id);
                        }}
                        className="rounded-md border border-red/40 px-1.5 py-px font-mono text-[9.5px] text-red transition-colors hover:bg-red/10 disabled:cursor-not-allowed disabled:opacity-50"
                      >
                        Cancel
                      </button>
                    </span>
                  ) : p.runState === 'done' ? (
                    <span
                      className="rounded border border-green/40 bg-green/10 px-1.5 py-px font-mono text-[9.5px] text-green"
                      title="headless run finished"
                    >
                      Run done
                    </span>
                  ) : (
                    <span className="flex items-center gap-1.5" onClick={(e) => e.stopPropagation()}>
                      {p.runState === 'failed' && (
                        <span
                          className="rounded border border-red/40 bg-red/10 px-1.5 py-px font-mono text-[9.5px] text-red"
                          title={p.runError ?? 'run failed'}
                        >
                          Failed
                        </span>
                      )}
                      <button
                        type="button"
                        disabled={runDisabled}
                        title={runTitle}
                        onClick={(e) => {
                          e.stopPropagation();
                          startRun(p.id);
                        }}
                        className={`rounded-md border px-1.5 py-px font-mono text-[9.5px] transition-colors disabled:cursor-not-allowed disabled:border-line disabled:text-ink-faint ${
                          p.runState === 'failed'
                            ? 'border-red/40 text-red hover:bg-red/10'
                            : 'border-brand/40 text-brand hover:bg-brand/10'
                        }`}
                      >
                        {runBusy === p.id ? '…' : p.runState === 'failed' ? 'Retry run' : 'Run phase'}
                      </button>
                    </span>
                  )}
                  {status === 'done' && p.completionReport !== null && (
                    <button
                      type="button"
                      onClick={(e) => {
                        e.stopPropagation();
                        setModal({ phase: p });
                      }}
                      title="what was shipped — Completion Report, checks, full doc"
                      className="font-mono text-[9.5px] text-green underline-offset-2 transition-colors hover:underline"
                    >
                      ✓ summary
                    </button>
                  )}
                  <button
                    type="button"
                    onClick={(e) => {
                      e.stopPropagation();
                      onOpenDoc(p.docRelPath, `Phase ${String(p.seq)} — ${p.name}`, 'edit');
                    }}
                    className="font-mono text-[9.5px] text-ink-dim underline-offset-2 transition-colors hover:text-ink hover:underline"
                  >
                    edit doc
                  </button>
                </div>
              </div>
            </li>
          );
        })}
      </ol>

      {modal !== null && (
        <DetailsModal
          epic={epic}
          phase={modal.phase}
          resolvedSeqs={resolvedSeqs}
          onClose={() => setModal(null)}
        />
      )}
    </div>
  );
}

type DetailTab = 'summary' | 'checks' | 'doc';

/** Acceptance-criteria checkbox lines of a markdown doc, in order. */
function extractChecks(md: string): { text: string; done: boolean }[] {
  const out: { text: string; done: boolean }[] = [];
  for (const line of md.split('\n')) {
    const m = /^\s*[-*]\s+\[( |x)\]\s+(.*)$/i.exec(line);
    if (m !== null) out.push({ done: (m[1] ?? '').toLowerCase() === 'x', text: m[2] ?? '' });
  }
  return out;
}

/**
 * Tabbed details modal. For a phase: summary (the doc's Completion Report) /
 * checks (its acceptance-criteria states) / doc (full markdown). For the plan
 * (phase undefined): summary (plan/SUMMARY.md) / checks (per-phase rollup) /
 * readme. Esc / backdrop close; doc content fetched lazily per tab.
 */
function DetailsModal({
  epic,
  phase,
  resolvedSeqs,
  onClose,
}: {
  epic: Epic;
  phase: EpicPhase | undefined;
  resolvedSeqs: Set<number>;
  onClose: () => void;
}): JSX.Element {
  const [tab, setTab] = useState<DetailTab>('summary');
  const [docText, setDocText] = useState<string | null>(null); // doc/readme + phase checks
  const [planSummary, setPlanSummary] = useState<string | null>(null);
  const docPath = phase !== undefined ? phase.docRelPath : 'README.md';

  useEffect(() => {
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('keydown', onKey);
    };
  }, [onClose]);

  useEffect(() => {
    const needDoc = tab === 'doc' || (tab === 'checks' && phase !== undefined);
    if (needDoc && docText === null) {
      fetchPlanDoc(epic.taskId, docPath)
        .then((d) => setDocText(d.content))
        .catch((e: unknown) =>
          setDocText(`failed to load ${docPath}: ${e instanceof Error ? e.message : String(e)}`),
        );
    }
    if (tab === 'summary' && phase === undefined && planSummary === null) {
      fetchPlanDoc(epic.taskId, 'SUMMARY.md')
        .then((d) => setPlanSummary(d.content))
        .catch((e: unknown) =>
          setPlanSummary(
            `failed to load SUMMARY.md: ${e instanceof Error ? e.message : String(e)}`,
          ),
        );
    }
  }, [tab, phase, docText, planSummary, epic.taskId, docPath]);

  const title =
    phase !== undefined ? `Phase ${String(phase.seq)} — ${phase.name}` : epic.title;
  const tabs: { id: DetailTab; label: string }[] = [
    { id: 'summary', label: 'summary' },
    { id: 'checks', label: 'checks' },
    { id: 'doc', label: phase !== undefined ? 'doc' : 'readme' },
  ];

  const phaseChecks = phase !== undefined && docText !== null ? extractChecks(docText) : null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-bg/70 p-4"
      role="dialog"
      aria-modal="true"
      aria-label={title}
      onClick={onClose}
    >
      <div
        className="flex max-h-[82vh] w-full max-w-2xl flex-col rounded-xl border border-line bg-surface"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between gap-3 border-b border-line px-4 py-3">
          <div className="min-w-0 truncate font-display text-[14px] font-bold text-ink">
            {title}
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <div className="flex items-center gap-1" role="tablist" aria-label="details tabs">
              {tabs.map((t) => (
                <button
                  key={t.id}
                  type="button"
                  role="tab"
                  aria-selected={tab === t.id}
                  onClick={() => setTab(t.id)}
                  className={`rounded-md border px-2 py-0.5 font-mono text-[10px] transition-colors ${
                    tab === t.id
                      ? 'border-brand/40 bg-brand/10 text-brand'
                      : 'border-line text-ink-dim hover:border-line-strong hover:text-ink'
                  }`}
                >
                  {t.label}
                </button>
              ))}
            </div>
            <button
              type="button"
              onClick={onClose}
              aria-label="close details"
              className="rounded px-1.5 font-mono text-[13px] text-ink-dim transition-colors hover:text-ink"
            >
              ✕
            </button>
          </div>
        </div>

        <div className="min-h-0 overflow-y-auto px-4 py-3 text-[13px] leading-relaxed">
          {tab === 'summary' &&
            (phase !== undefined ? (
              phase.completionReport !== null ? (
                <Markdown text={phase.completionReport} />
              ) : (
                <div className="font-mono text-[11.5px] text-ink-faint">
                  no completion report yet — the executor fills «## Completion Report» when the
                  phase lands
                </div>
              )
            ) : planSummary === null ? (
              <Loading label="summary…" />
            ) : (
              <Markdown text={planSummary} />
            ))}

          {tab === 'checks' &&
            (phase !== undefined ? (
              phaseChecks === null ? (
                <Loading label="checks…" />
              ) : phaseChecks.length === 0 ? (
                <div className="font-mono text-[11.5px] text-ink-faint">
                  no checkboxes in this doc
                </div>
              ) : (
                <ul className="space-y-1.5">
                  {phaseChecks.map((c, i) => (
                    <li key={i} className="flex items-start gap-2">
                      <span
                        className={`mt-px shrink-0 font-mono text-[12px] ${
                          c.done ? 'text-green' : 'text-ink-faint'
                        }`}
                      >
                        {c.done ? '✓' : '○'}
                      </span>
                      <span className={c.done ? 'text-ink-dim' : 'text-ink'}>{c.text}</span>
                    </li>
                  ))}
                </ul>
              )
            ) : (
              <ul className="space-y-2">
                {epic.phases.map((p) => {
                  const st = phaseStatus(p, resolvedSeqs);
                  return (
                    <li key={p.id} className="flex items-center gap-2">
                      <span className="shrink-0 font-mono text-[10px] text-ink-faint">
                        Phase {p.seq}
                      </span>
                      <span className="min-w-0 truncate text-ink">{p.name}</span>
                      <span
                        className={`shrink-0 rounded border px-1.5 py-px font-mono text-[9px] ${PHASE_CHIP[st].cls}`}
                      >
                        {PHASE_CHIP[st].label}
                      </span>
                      <span className="ml-auto shrink-0 font-mono text-[10px] text-ink-faint">
                        {p.checkboxesDone}/{p.checkboxesTotal || 0}
                      </span>
                    </li>
                  );
                })}
              </ul>
            ))}

          {tab === 'doc' &&
            (docText === null ? <Loading label="doc…" /> : <Markdown text={docText} />)}
        </div>
      </div>
    </div>
  );
}

/** A thin progress bar. total===0 renders an empty track (no divide-by-zero). */
function ProgressBar({
  done,
  total,
  className = '',
}: {
  done: number;
  total: number;
  className?: string;
}): JSX.Element {
  const pct = total > 0 ? Math.round((done / total) * 100) : 0;
  return (
    <div
      // Track must contrast with every card fill it sits on — the selected plan
      // card is itself bg-surface2, which made a bg-surface2 track invisible and
      // a partial fill read as a complete bar.
      className={`h-1.5 overflow-hidden rounded-full bg-line-strong/50 ${className}`}
      role="progressbar"
      aria-valuenow={pct}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-label={`${done} of ${total} checkboxes done`}
    >
      <div className="h-full rounded-full bg-brand transition-[width]" style={{ width: `${String(pct)}%` }} />
    </div>
  );
}
