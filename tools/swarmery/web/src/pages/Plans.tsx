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
import type { BoardColumn, Epic, EpicPhase, WSMessage } from '../api/types';
import {
  epicLifecycle,
  fetchEpics,
  type EpicLifecycleAction,
} from '../api';
import { useProjectWorkspace } from '../workspace/ProjectContext';
import { useLiveUpdates } from '../lib/ws';
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
  if (p.dependsOn.some((seq) => !resolvedSeqs.has(seq))) return 'blocked';
  if (p.checkboxesDone > 0 || p.boardColumn === 'in_progress' || p.boardColumn === 'in_review')
    return 'in_progress';
  return 'pending';
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
}: {
  epic: Epic;
  busyLifecycle: boolean;
  onLifecycle: (epic: Epic, action: EpicLifecycleAction) => void;
  onOpenDoc: (path: string, title: string, mode: DrawerMode) => void;
}): JSX.Element {
  // Which seq numbers are "resolved" — their board task is done/archived OR
  // every checkbox in their doc is ticked (file-driven completion without
  // board activation). Used to render depends-on badges.
  const resolvedSeqs = useMemo(() => {
    const s = new Set<number>();
    for (const p of epic.phases) {
      if (isResolvedColumn(p.boardColumn) || (p.checkboxesTotal > 0 && p.checkboxesDone === p.checkboxesTotal))
        s.add(p.seq);
    }
    return s;
  }, [epic.phases]);

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
      </div>

      <ol className="space-y-2">
        {epic.phases.map((p) => {
          const activated = p.activatedAt !== null;
          const status = phaseStatus(p, resolvedSeqs);
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
      className={`h-1.5 overflow-hidden rounded-full bg-surface2 ${className}`}
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
