// Plans / Epics (fusion phase 10 + plans-page-lifecycle phase 2): a workspace
// plan IS an epic. This tab lists the project's epics (plan dirs the ingester
// parsed) behind Active/Done/Archived filter tabs, drills into a phase
// timeline (seq order, depends-on badges, per-phase progress, derived status
// chips), offers plan lifecycle controls (Pause / Resume / Archive / Restore —
// file operations on the daemon side), and opens any plan doc for EDITING in
// the existing drawer — the workspace folder becomes invisible infrastructure
// (read, edit, track from the platform; files stay the storage).
//
// Details are INLINE and full-width: the plan list column stays visible on the
// left, and the detail panel REPLACES the phase list in the main column, under
// the plan header (title, status, progress, lifecycle buttons) — plan docs and
// completion reports get the whole column instead of a 360px rail. The "← all
// phases" control (or Escape) sits ABOVE the panel, not inside its header,
// where it used to get lost against the panel's own identity block.
//
// BOTH detail panels are tabbed, with the same tab-bar idiom:
//   phase → Phase (run state, interactive acceptance criteria, full doc)
//         | Summary (Completion Report, ticked criteria, `## Execution record`)
//         | Edit (raw markdown + Save)
//   plan  → Plan (README markdown)
//         | Summary (per-phase executed work; only when every phase is done)
//         | Edit (raw README + Save)
// Editing is inline on the Edit tab — there is no doc modal anymore; the
// checkbox toggling the old modal offered lives on the phase's Phase tab
// (clicking a criterion PATCHes that exact `- [ ]`↔`- [x]` line).
//
// Running work: a phase runs headlessly from its own doc (the phase-run
// mechanism), and the WHOLE plan can be handed to one agent instead — "Run
// plan" in the action row opens an inline agent + mode picker and POSTs
// /api/epics/{taskId}/run. That run drives core's `run-plan` skill; its progress
// shows up as the phase docs' checkboxes ticking, not as per-phase run chips, so
// per-phase Run buttons stand down while it owns the docs.
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

import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import type { BoardColumn, Epic, EpicPhase, PhaseRunOutcome, WSMessage } from '../api/types';
import {
  cancelEpicPhaseRun,
  cancelEpicPlanRun,
  epicLifecycle,
  fetchEpics,
  fetchPlanDoc,
  runEpicPhase,
  runEpicPlan,
  savePlanDoc,
  togglePlanCheckbox,
  type EpicLifecycleAction,
} from '../api';
import { fetchSystemItems } from '../api/system';
import type { PlanRunMode } from '../api/types';
import { useProjectWorkspace } from '../workspace/ProjectContext';
import { useLiveUpdates } from '../lib/ws';
import { Markdown } from '../lib/markdown';
import { fmtElapsed } from '../lib/format';
import { Empty, ErrorBox, Loading } from '../components/ui';
import { RunOutcomeModal } from '../components/RunOutcomeModal';

/** A board column that counts as "resolved" for the dependency gate. */
function isResolvedColumn(col: BoardColumn | null): boolean {
  return col === 'done' || col === 'archived';
}

type PhaseStatus = 'pending' | 'in_progress' | 'done' | 'blocked';

/** Derives a phase's display status from checkbox progress, a live run, and the
 * dependency gate. The result stays a coarse LIFECYCLE state — whether a process
 * is attached to it is resolved by `phaseChip` at the call site. */
function phaseStatus(p: EpicPhase, resolvedSeqs: Set<number>): PhaseStatus {
  // An activated phase whose board task is resolved is done regardless of
  // checkbox progress — the board is the source of truth once dispatched.
  if (isResolvedColumn(p.boardColumn)) return 'done';
  if (p.checkboxesTotal > 0 && p.checkboxesDone === p.checkboxesTotal) return 'done';
  // The doc's own `Status: In progress` marker wins over the dependency gate —
  // an executor writing it is literally working on the phase right now.
  // (`done` must be earned by ticking every checkbox; a `done` marker alone is ignored.)
  if (p.docStatus === 'in_progress') return 'in_progress';
  // A run in flight is the strongest statement that can be made about a phase —
  // stronger than a dependency gate computed from checkboxes the run is in the
  // middle of ticking. Without this a phase whose dependency sits at 11/12
  // renders `blocked` while its executor is demonstrably working.
  if (p.runState === 'running') return 'in_progress';
  if (p.dependsOn.some((seq) => !resolvedSeqs.has(seq))) return 'blocked';
  if (p.checkboxesDone > 0 || p.boardColumn === 'in_progress' || p.boardColumn === 'in_review')
    return 'in_progress';
  return 'pending';
}

/** Which seq numbers are "resolved" — their board task is done/archived OR
 * every checkbox in their doc is ticked (file-driven completion without
 * board activation) OR a headless run actually COMPLETED the phase. Renders
 * depends-on badges and feeds the phase-status derivation.
 *
 * `runOutcome === 'completed'`, never `runState === 'done'`: a process that
 * exited 0 having ticked nothing is `noop`, and treating that as resolved is
 * the client-side twin of the dependency-gate bug the daemon removed — it let
 * a 0/7 phase mark its dependents runnable. */
function computeResolvedSeqs(phases: EpicPhase[]): Set<number> {
  const s = new Set<number>();
  for (const p of phases) {
    if (
      isResolvedColumn(p.boardColumn) ||
      (p.checkboxesTotal > 0 && p.checkboxesDone === p.checkboxesTotal) ||
      p.runOutcome === 'completed'
    )
      s.add(p.seq);
  }
  return s;
}

/** A plan is complete when every phase reads done — the gate for the plan
 * rail's Summary tab. */
function planComplete(epic: Epic, resolvedSeqs: Set<number>): boolean {
  return (
    epic.phases.length > 0 &&
    epic.phases.every((p) => phaseStatus(p, resolvedSeqs) === 'done')
  );
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

/** Liveness pulse for a phase with a LIVE run. Every executor edit (checkbox
 * tick, Status flip) touches the phase doc, so "time since last doc edit"
 * answers the question the chip alone can't: is the session actually working
 * right now, or has it silently died?
 *
 * That question only exists while a process is attached. With `running={false}`
 * there is no executor to be stuck, so the pulse and the stuck-executor tooltip
 * are dropped for a neutral `edited <ago>` — the doc's mtime, stated as such.
 * The old markup showed `no activity · 1h 7m` in amber on phases whose run had
 * exited cleanly an hour earlier, which read as an alarm about nothing. */
function PhaseActivity({
  docUpdatedAt,
  running,
}: {
  docUpdatedAt: string | null;
  running: boolean;
}): JSX.Element | null {
  const now = useNow(30_000);
  if (docUpdatedAt === null) return null;
  const elapsed = now - Date.parse(docUpdatedAt);
  if (!running)
    return (
      <span
        className="inline-flex shrink-0 items-center gap-1 font-mono text-[9.5px] text-ink-faint"
        data-tip="when this phase doc was last edited — no run is attached to it"
      >
        edited {formatAgo(elapsed)} ago
      </span>
    );
  const stalled = elapsed > STALL_AFTER_MS;
  return (
    <span
      className={`inline-flex shrink-0 items-center gap-1 font-mono text-[9.5px] ${
        stalled ? 'text-amber' : 'text-brand'
      }`}
      data-tip={
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

/** Status chips. `in_progress` says `started` because that is all the DOCUMENT
 * can support — checkboxes ticked, or a `Status: In progress` marker. `running`
 * is reserved for a live process and is resolved by `phaseChip` below. */
const PHASE_CHIP: Record<PhaseStatus, { label: string; cls: string }> = {
  done: { label: 'done', cls: 'border-green/40 text-green' },
  in_progress: { label: 'started', cls: 'border-brand/40 text-brand' },
  blocked: { label: 'blocked', cls: 'border-red/40 text-red' },
  pending: { label: 'pending', cls: 'border-line text-ink-faint' },
};

/** The chip a phase row actually renders. The `running` variant lives HERE and
 * not in `PhaseStatus`, so the union — and every dependency/completion decision
 * derived from it — stays a pure function of the plan documents. */
function phaseChip(status: PhaseStatus, running: boolean): { label: string; cls: string } {
  if (running && status === 'in_progress')
    return { label: 'running', cls: PHASE_CHIP.in_progress.cls };
  return PHASE_CHIP[status];
}

/** Terminal run outcomes that did NOT complete the phase. Each renders as a
 * chip BUTTON opening the diagnosis modal, because "why?" is the only useful
 * next question — the old markup showed a green "Run done" here whenever the
 * process exited 0, which is precisely the claim these outcomes refute. */
type UnresolvedOutcome = 'noop' | 'partial' | 'failed';

function isUnresolvedOutcome(o: PhaseRunOutcome): o is UnresolvedOutcome {
  return o === 'noop' || o === 'partial' || o === 'failed';
}

/** Chip copy + styling per unresolved outcome. `amber` is the app's semantic
 * needs-a-human color (same token as workspace/TaskCard.tsx's inconclusive). */
const OUTCOME_CHIP: Record<UnresolvedOutcome, { cls: string; title: string }> = {
  noop: {
    cls: 'border-amber/40 bg-amber/10 text-amber',
    title: 'the run finished but ticked no acceptance criteria — click for why',
  },
  partial: {
    cls: 'border-amber/40 bg-amber/10 text-amber',
    title: 'the run ticked some criteria but did not finish the phase — click for why',
  },
  failed: {
    cls: 'border-red/40 bg-red/10 text-red',
    title: 'the run failed — click for why',
  },
};

/** Run/Retry button styling — keyed on the OUTCOME, so a retry after a
 * ticked-nothing run reads amber like its chip instead of neutral brand. */
function runButtonCls(outcome: PhaseRunOutcome): string {
  if (outcome === 'failed') return 'border-red/40 text-red hover:bg-red/10';
  if (outcome === 'noop' || outcome === 'partial')
    return 'border-amber/40 text-amber hover:bg-amber/10';
  return 'border-brand/40 text-brand hover:bg-brand/10';
}

function outcomeChipLabel(p: EpicPhase, outcome: UnresolvedOutcome): string {
  if (outcome === 'failed') return 'failed';
  if (outcome === 'partial') return `ran · ${String(p.checkboxesDone)}/${String(p.checkboxesTotal)}`;
  return 'ran · no progress';
}

/** The clickable outcome chip. READ-ONLY — it stays enabled while a plan run
 * owns the docs, because a diagnosis is exactly what a user wants then. */
function RunOutcomeChip({
  phase,
  outcome,
  onOpen,
}: {
  phase: EpicPhase;
  outcome: UnresolvedOutcome;
  onOpen: () => void;
}): JSX.Element {
  const { cls, title } = OUTCOME_CHIP[outcome];
  return (
    <button
      type="button"
      data-tip={title}
      onClick={(e) => {
        e.stopPropagation();
        onOpen();
      }}
      className={`rounded border px-1.5 py-px font-mono text-[9.5px] transition-colors hover:brightness-125 ${cls}`}
    >
      {outcomeChipLabel(phase, outcome)}
    </button>
  );
}

/** The completed-outcome chip, shared by every render site. It describes the
 * LAST RUN, not the present — hence `last run: done` rather than `Run done`,
 * which read as a claim about the phase and collided with a live `11/12`
 * beside it.
 *
 * Detecting that collision without a `runCheckboxesAfter` field (the DTO exposes
 * only `runCheckboxesBefore`): `completed` is derivable ONLY when the run's
 * stamped end count reached the total (internal/phasediag/outcome.go:35), and
 * that stamped count wins over the live one (`OutcomeFromRow`). So a `completed`
 * outcome sitting next to `checkboxesDone < checkboxesTotal` is precisely — and
 * only — the case where the two counts disagree, i.e. the doc changed after the
 * run ended. The measured count is rendered as `total/total`: `after >= total`
 * is exactly what the daemon asserted. */
function RunCompletedChip({ phase }: { phase: EpicPhase }): JSX.Element {
  const total = phase.checkboxesTotal;
  const drifted = total > 0 && phase.checkboxesDone < total;
  return (
    <span
      className="rounded border border-green/40 bg-green/10 px-1.5 py-px font-mono text-[9.5px] text-green"
      data-tip={
        drifted
          ? `the run ticked every criterion (${String(total)}/${String(total)}); the doc has since changed to ${String(phase.checkboxesDone)}/${String(total)}`
          : 'the run ticked every acceptance criterion'
      }
    >
      last run: done
    </span>
  );
}

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

/** Plan-details tab ids. Summary exists only on complete plans. */
type PlanDetailTab = 'plan' | 'summary' | 'edit';

/** Phase-details tab ids. All three always exist — a phase with nothing shipped
 * yet still shows Summary (with an empty note) rather than hiding the tab, which
 * is what made "where do I read the summary?" a dead end. */
type PhaseDetailTab = 'phase' | 'summary' | 'edit';

/** What the inline detail panel shows: one phase's details, or the plan's (both
 * tabbed). `null` means "no details — show the phase list".
 *
 * The phase is addressed by SEQ, not by `epic_phases.id`: a plan rescan
 * (wsingest/epics.go applyEpics) DELETEs and re-INSERTs every phase row, so the
 * surrogate id churns on each rescan — and a rescan is exactly what a checkbox
 * tick triggers. Keying by id made the open panel close, or silently jump to
 * whichever phase inherited the old id. Seq is the plan's own numbering and
 * survives. */
type DetailTarget =
  | { kind: 'phase'; seq: number; tab: PhaseDetailTab }
  | { kind: 'plan'; tab: PlanDetailTab };

/** Same selection with the phase resolved against the current epic. */
type DetailSel =
  | { kind: 'phase'; phase: EpicPhase; tab: PhaseDetailTab }
  | { kind: 'plan'; tab: PlanDetailTab };

export function Plans(): JSX.Element {
  const { project, projectId, loading: projLoading } = useProjectWorkspace();
  const [epics, setEpics] = useState<Epic[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<number | null>(null); // taskId
  const [filter, setFilter] = useState<EpicFilter>('active');
  const [actionError, setActionError] = useState<string | null>(null);
  const [busyLifecycle, setBusyLifecycle] = useState(false);
  const [detailTarget, setDetailTarget] = useState<DetailTarget | null>(null);

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
    () =>
      (epics ?? []).some(
        (e) => e.planRun?.runState === 'running' || e.phases.some((p) => p.runState === 'running'),
      ),
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

  // The detail panel describes ONE epic's phase/plan — switching plans closes it,
  // and the run-diagnosis modal with it (its phase id belongs to the old plan).
  useEffect(() => {
    setDetailTarget(null);
    setOutcomeFor(null);
  }, [selected]);

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

  // Phase-run controls (interactive-planning-v2 phase 6), lifted here so the
  // phase rail can offer Retry on a failed run. The server re-checks every
  // gate; the client-side disable is a courtesy, and a 409 body (unmet deps,
  // already running) surfaces in the inline error strip in EpicDetail.
  const [runBusy, setRunBusy] = useState<number | null>(null); // phase id
  const [runMsg, setRunMsg] = useState<string | null>(null);
  // Which phase's run diagnosis is open (phase id) — the modal is read-only, so
  // it can be open over any state, including a live plan run.
  const [outcomeFor, setOutcomeFor] = useState<number | null>(null);

  const startRun = useCallback(
    (taskId: number, phaseId: number): void => {
      setRunBusy(phaseId);
      setRunMsg(null);
      runEpicPhase(taskId, phaseId)
        .then(() => reload())
        .catch((e: unknown) => {
          setRunMsg(e instanceof Error ? e.message : String(e));
          // The branch-holds-commits 409 carries `branch` — that is not a
          // message to read, it is a blocker with an action, so land the user
          // on the diagnosis (which offers Delete branch) instead of a toast.
          if (e instanceof Error && typeof (e as Error & { branch?: string }).branch === 'string')
            setOutcomeFor(phaseId);
        })
        .finally(() => setRunBusy(null));
    },
    [reload],
  );
  const cancelRun = useCallback(
    (taskId: number, phaseId: number): void => {
      setRunBusy(phaseId);
      setRunMsg(null);
      cancelEpicPhaseRun(taskId, phaseId)
        .then(() => reload())
        .catch((e: unknown) => setRunMsg(e instanceof Error ? e.message : String(e)))
        .finally(() => setRunBusy(null));
    },
    [reload],
  );

  // Whole-plan runs: one agent driving core's run-plan skill over every phase.
  // Same error strip as phase runs — the server re-checks every gate and its
  // 409 body (a phase run holds the docs, plan not active, already complete)
  // says which one bit.
  const [planRunBusy, setPlanRunBusy] = useState(false);

  const startPlanRun = useCallback(
    (taskId: number, agent: string, mode: PlanRunMode): void => {
      setPlanRunBusy(true);
      setRunMsg(null);
      runEpicPlan(taskId, { agent, mode })
        .then(() => reload())
        .catch((e: unknown) => setRunMsg(e instanceof Error ? e.message : String(e)))
        .finally(() => setPlanRunBusy(false));
    },
    [reload],
  );
  const cancelPlanRun = useCallback(
    (taskId: number): void => {
      setPlanRunBusy(true);
      setRunMsg(null);
      cancelEpicPlanRun(taskId)
        .then(() => reload())
        .catch((e: unknown) => setRunMsg(e instanceof Error ? e.message : String(e)))
        .finally(() => setPlanRunBusy(false));
    },
    [reload],
  );

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

  // Resolve the selection against the CURRENT epic — a refetch can drop the
  // referenced phase (plan rescan), in which case the panel closes itself and
  // the phase list comes back.
  let detail: DetailSel | null = null;
  if (activeEpic !== null && detailTarget !== null) {
    if (detailTarget.kind === 'plan') {
      detail = { kind: 'plan', tab: detailTarget.tab };
    } else {
      const p = activeEpic.phases.find((x) => x.seq === detailTarget.seq);
      detail = p !== undefined ? { kind: 'phase', phase: p, tab: detailTarget.tab } : null;
    }
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
              detail={detail}
              busyLifecycle={busyLifecycle}
              onLifecycle={lifecycle}
              onDocChanged={reload}
              onOpenPhase={(seq, tab) => setDetailTarget({ kind: 'phase', seq, tab })}
              onOpenPlan={(tab) => setDetailTarget({ kind: 'plan', tab })}
              onCloseDetail={() => setDetailTarget(null)}
              runBusy={runBusy}
              runMsg={runMsg}
              onRun={(phaseId) => startRun(activeEpic.taskId, phaseId)}
              onCancelRun={(phaseId) => cancelRun(activeEpic.taskId, phaseId)}
              planRunBusy={planRunBusy}
              onRunPlan={(agent, mode) => startPlanRun(activeEpic.taskId, agent, mode)}
              onCancelPlanRun={() => cancelPlanRun(activeEpic.taskId)}
              onOpenOutcome={setOutcomeFor}
            />
          )}
        </div>
      </div>

      {/* Why a run did not move the phase. Read-only, so it opens over any
          state — including a live plan run, where only its write actions
          (Delete branch / Retry run) stand down. */}
      {outcomeFor !== null && activeEpic !== null && (
        <RunOutcomeModal
          taskId={activeEpic.taskId}
          phaseId={outcomeFor}
          writesDisabled={
            activeEpic.planRun?.runState === 'running' ||
            activeEpic.status !== 'active' ||
            runBusy !== null
          }
          writesDisabledReason={
            activeEpic.planRun?.runState === 'running'
              ? 'a whole-plan run is executing this plan'
              : activeEpic.status !== 'active'
                ? 'plan is not active'
                : 'a phase run is already starting'
          }
          onClose={() => setOutcomeFor(null)}
          onRetry={() => startRun(activeEpic.taskId, outcomeFor)}
        />
      )}
    </div>
  );
}

function EpicDetail({
  epic,
  detail,
  busyLifecycle,
  onLifecycle,
  onDocChanged,
  onOpenPhase,
  onOpenPlan,
  onCloseDetail,
  runBusy,
  runMsg,
  onRun,
  onCancelRun,
  planRunBusy,
  onRunPlan,
  onCancelPlanRun,
  onOpenOutcome,
}: {
  epic: Epic;
  detail: DetailSel | null;
  busyLifecycle: boolean;
  onLifecycle: (epic: Epic, action: EpicLifecycleAction) => void;
  /** A doc write (save or checkbox toggle) landed — refetch so the rollup follows. */
  onDocChanged: () => void;
  onOpenPhase: (seq: number, tab: PhaseDetailTab) => void;
  onOpenPlan: (tab: PlanDetailTab) => void;
  onCloseDetail: () => void;
  runBusy: number | null;
  runMsg: string | null;
  onRun: (phaseId: number) => void;
  onCancelRun: (phaseId: number) => void;
  planRunBusy: boolean;
  onRunPlan: (agent: string, mode: PlanRunMode) => void;
  onCancelPlanRun: () => void;
  /** Open the run-diagnosis modal for a phase id. */
  onOpenOutcome: (phaseId: number) => void;
}): JSX.Element {
  const resolvedSeqs = useMemo(() => computeResolvedSeqs(epic.phases), [epic.phases]);
  const complete = planComplete(epic, resolvedSeqs);
  const now = useNow(1000);
  const phaseRunActive = epic.phases.some((p) => p.runState === 'running');
  // A plan run owns every phase doc for its duration — offering per-phase runs
  // next to it would invite two worktrees editing the same files.
  const planRunning = epic.planRun?.runState === 'running';

  // Escape backs out of the details — the same affordance the rail's ✕ had,
  // without stealing the key while the phase list is showing.
  const open = detail !== null;
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') onCloseDetail();
    };
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('keydown', onKey);
    };
  }, [open, onCloseDetail]);

  return (
    <div className="pr-1">
      <div className="mb-3 flex items-baseline justify-between gap-3">
        <div className="flex min-w-0 items-baseline gap-2">
          {/* The plan title opens the plan rail — same click-to-detail idiom
              as the phase rows below. */}
          <button
            type="button"
            onClick={() => onOpenPlan('plan')}
            data-tip="open plan details"
            className="min-w-0 truncate text-left text-[15px] font-semibold text-ink transition-colors hover:text-brand"
          >
            {epic.title}
          </button>
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
        {/* Leaving the details is the first thing offered while they are open,
            in the same row as the plan actions. */}
        {detail !== null && <BackToPhases onBack={onCloseDetail} />}
        <button
          type="button"
          onClick={() => onOpenPlan('plan')}
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
        {complete && (
          <button
            type="button"
            onClick={() => onOpenPlan('summary')}
            data-tip="what was shipped — per-phase executed work"
            className="rounded-md border border-green/40 px-2 py-1 font-mono text-[10.5px] text-green transition-colors hover:bg-green/10"
          >
            ✓ summary
          </button>
        )}
        <PlanRunControls
          epic={epic}
          complete={complete}
          now={now}
          busy={planRunBusy}
          phaseRunActive={phaseRunActive}
          onRun={onRunPlan}
          onCancel={onCancelPlanRun}
        />
      </div>

      {runMsg !== null && (
        <div className="mb-2 rounded-md border border-red/40 bg-red/10 px-2.5 py-1.5 font-mono text-[10.5px] text-red">
          {runMsg}
        </div>
      )}

      {detail !== null ? (
        <>
          {detail.kind === 'phase' ? (
            <PhaseDetailPanel
              epic={epic}
              phase={detail.phase}
              tab={detail.tab}
              onTab={(t) => onOpenPhase(detail.phase.seq, t)}
              runBusy={runBusy}
              planRunning={planRunning}
              onRetry={() => onRun(detail.phase.id)}
              onCancelRun={() => onCancelRun(detail.phase.id)}
              onOpenOutcome={() => onOpenOutcome(detail.phase.id)}
              onDocChanged={onDocChanged}
            />
          ) : (
            <PlanDetailPanel
              epic={epic}
              tab={detail.tab}
              onTab={onOpenPlan}
              onDocChanged={onDocChanged}
            />
          )}
        </>
      ) : (
        <PhaseList
          epic={epic}
          resolvedSeqs={resolvedSeqs}
          now={now}
          runBusy={runBusy}
          planRunning={planRunning}
          onOpenPhase={onOpenPhase}
          onRun={onRun}
          onCancelRun={onCancelRun}
          onOpenOutcome={onOpenOutcome}
        />
      )}
    </div>
  );
}

/** The phase timeline — seq order, depends-on badges, per-phase progress and
 * run controls. A row opens that phase's details, which replace this list. */
function PhaseList({
  epic,
  resolvedSeqs,
  now,
  runBusy,
  planRunning,
  onOpenPhase,
  onRun,
  onCancelRun,
  onOpenOutcome,
}: {
  epic: Epic;
  resolvedSeqs: Set<number>;
  now: number;
  runBusy: number | null;
  /** A whole-plan run owns the phase docs — per-phase runs stand down. */
  planRunning: boolean;
  onOpenPhase: (seq: number, tab: PhaseDetailTab) => void;
  onRun: (phaseId: number) => void;
  onCancelRun: (phaseId: number) => void;
  onOpenOutcome: (phaseId: number) => void;
}): JSX.Element {
  return (
    <ol className="space-y-2">
      {epic.phases.map((p) => {
        const activated = p.activatedAt !== null;
        const status = phaseStatus(p, resolvedSeqs);
        // Process state, kept out of `phaseStatus` on purpose: only a live run
        // may say `running`, and only a live run may claim liveness.
        const running = p.runState === 'running';
        const chip = phaseChip(status, running);
        const depsUnmet = p.dependsOn.filter((seq) => !resolvedSeqs.has(seq));
        const runDisabled =
          runBusy !== null || planRunning || epic.status !== 'active' || depsUnmet.length > 0;
        const runTitle = planRunning
          ? 'a whole-plan run is executing this plan'
          : epic.status !== 'active'
            ? 'plan is not active'
            : depsUnmet.length > 0
              ? `waiting on phase ${depsUnmet.join(', ')}`
              : 'run this phase headlessly in an isolated worktree';
        const openPhase = (): void => {
          onOpenPhase(p.seq, 'phase');
        };
        return (
          <li
            key={p.id}
            role="button"
            tabIndex={0}
            aria-label={`open Phase ${String(p.seq)} — ${p.name} details`}
            onClick={openPhase}
            onKeyDown={(e) => {
              if (e.target !== e.currentTarget) return; // inner buttons handle their own keys
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                openPhase();
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
                    className={`shrink-0 rounded border px-1.5 py-px font-mono text-[9px] ${chip.cls}`}
                  >
                    {chip.label}
                  </span>
                  {status === 'in_progress' && (
                    <PhaseActivity docUpdatedAt={p.docUpdatedAt} running={running} />
                  )}
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
                      data-tip={resolvedSeqs.has(seq) ? 'dependency resolved' : 'dependency pending'}
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
                    data-tip={p.boardTaskExternalId ?? undefined}
                  >
                    activated{p.boardColumn !== null ? ` · ${p.boardColumn}` : ''}
                  </span>
                )}

                {/* Direct phase run (no board task): running shows elapsed +
                    Cancel + session link; a DONE phase retires the Run button
                    and offers ✓ summary (opens the details rail); idle/failed
                    offer Run/Retry. */}
                {p.runState === 'running' ? (
                  <span className="flex items-center gap-1.5" onClick={(e) => e.stopPropagation()}>
                    <span
                      className="inline-flex items-center gap-1 rounded border border-brand/40 bg-brand/10 px-1.5 py-px font-mono text-[9.5px] text-brand"
                      data-tip="headless run in progress"
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
                        onCancelRun(p.id);
                      }}
                      className="rounded-md border border-red/40 px-1.5 py-px font-mono text-[9.5px] text-red transition-colors hover:bg-red/10 disabled:cursor-not-allowed disabled:opacity-50"
                    >
                      Cancel
                    </button>
                  </span>
                ) : status === 'done' ? (
                  <span className="flex items-center gap-1.5" onClick={(e) => e.stopPropagation()}>
                    {p.runOutcome === 'completed' && <RunCompletedChip phase={p} />}
                    {isUnresolvedOutcome(p.runOutcome) && (
                      <RunOutcomeChip
                        phase={p}
                        outcome={p.runOutcome}
                        onOpen={() => onOpenOutcome(p.id)}
                      />
                    )}
                    <button
                      type="button"
                      onClick={() => {
                        onOpenPhase(p.seq, 'summary');
                      }}
                      data-tip="what was shipped — completed criteria, Completion Report, execution record"
                      className="font-mono text-[9.5px] text-green underline-offset-2 transition-colors hover:underline"
                    >
                      ✓ summary
                    </button>
                  </span>
                ) : (
                  <span className="flex items-center gap-1.5" onClick={(e) => e.stopPropagation()}>
                    {p.runOutcome === 'completed' && <RunCompletedChip phase={p} />}
                    {isUnresolvedOutcome(p.runOutcome) && (
                      <RunOutcomeChip
                        phase={p}
                        outcome={p.runOutcome}
                        onOpen={() => onOpenOutcome(p.id)}
                      />
                    )}
                    <button
                      type="button"
                      disabled={runDisabled}
                      data-tip={runTitle}
                      onClick={(e) => {
                        e.stopPropagation();
                        onRun(p.id);
                      }}
                      className={`rounded-md border px-1.5 py-px font-mono text-[9.5px] transition-colors disabled:cursor-not-allowed disabled:border-line disabled:text-ink-faint ${runButtonCls(p.runOutcome)}`}
                    >
                      {runBusy === p.id
                        ? '…'
                        : isUnresolvedOutcome(p.runOutcome)
                          ? 'Retry run'
                          : 'Run phase'}
                    </button>
                  </span>
                )}
                <button
                  type="button"
                  onClick={(e) => {
                    e.stopPropagation();
                    onOpenPhase(p.seq, 'edit');
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
  );
}

/** One acceptance checkbox: its 0-based SOURCE line (what the PATCH endpoint
 * addresses), tick state and label. */
interface Check {
  line: number;
  text: string;
  done: boolean;
}

/** Acceptance-criteria checkbox lines of a markdown doc, in order. The line
 * index is what makes the list toggleable — togglePlanCheckbox rewrites that
 * exact `- [ ]`↔`- [x]` line. */
function extractChecks(md: string): Check[] {
  const out: Check[] = [];
  md.split('\n').forEach((raw, i) => {
    const m = /^\s*[-*]\s+\[( |x)\]\s+(.*)$/i.exec(raw);
    if (m !== null) out.push({ line: i, done: (m[1] ?? '').toLowerCase() === 'x', text: m[2] ?? '' });
  });
  return out;
}

/** The body of one `## <heading>` markdown section (up to the next `## `
 * heading), or null when the doc has no such section / it is empty. */
function extractSection(md: string, heading: string): string | null {
  const lines = md.split('\n');
  const want = `## ${heading}`.toLowerCase();
  const start = lines.findIndex((l) => l.trim().toLowerCase() === want);
  if (start === -1) return null;
  const body: string[] = [];
  for (const line of lines.slice(start + 1)) {
    if (/^##\s/.test(line)) break;
    body.push(line);
  }
  const text = body.join('\n').trim();
  return text === '' ? null : text;
}

/** Lazily fetches one plan doc; errors fold into the returned text (same
 * behaviour the old details modal had). `version` re-triggers the fetch when
 * the doc changes server-side (docUpdatedAt from a plan_updated refetch). The
 * setter lets a local write (checkbox toggle, Save) adopt the server's response
 * without waiting for the next refetch. */
function usePlanDoc(
  taskId: number,
  path: string,
  version: string | null,
): [string | null, (text: string) => void] {
  const [text, setText] = useState<string | null>(null);
  useEffect(() => {
    let alive = true;
    setText(null);
    fetchPlanDoc(taskId, path)
      .then((d) => {
        if (alive) setText(d.content);
      })
      .catch((e: unknown) => {
        if (alive)
          setText(`failed to load ${path}: ${e instanceof Error ? e.message : String(e)}`);
      });
    return () => {
      alive = false;
    };
  }, [taskId, path, version]);
  return [text, setText];
}

/** The back-out control. Lives in the plan's action row (first, ahead of "open
 * plan README" / lifecycle), not inside the detail panel's header where it used
 * to get lost against the panel's own identity block. Sized like its row
 * siblings, weighted like the lifecycle buttons so it still reads as the
 * primary way out. */
function BackToPhases({ onBack }: { onBack: () => void }): JSX.Element {
  return (
    <button
      type="button"
      onClick={onBack}
      data-tip="back to the phase list (Esc)"
      className="inline-flex items-center gap-1.5 rounded-md border border-line-strong bg-surface2 px-2 py-1 font-mono text-[10.5px] text-ink transition-colors hover:bg-surface2/70 hover:text-brand focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand"
    >
      <span aria-hidden="true">←</span> all phases
    </button>
  );
}

/** The execution modes offered when starting a whole-plan run. The run-plan
 * skill triages its own ROUTE (sequential / parallel group / workflow) from the
 * phase DAG — the UI must not duplicate that, since it needs the graph to be
 * meaningful. What the skill cannot derive is the call an interactive session
 * always asks about: dispatch per-phase executors, or do the work inline. */
const PLAN_RUN_MODES: { id: PlanRunMode; label: string; hint: string }[] = [
  { id: 'auto', label: 'auto', hint: 'let the run-plan skill triage the route from the phase DAG' },
  { id: 'subagents', label: 'subagent-driven', hint: 'dispatch an implementer + a fresh reviewer per phase' },
  { id: 'inline', label: 'inline', hint: 'the controller implements the phases itself, no dispatch' },
];

/** Whole-plan run controls in the plan action row: a Run plan button that opens
 * an inline agent+mode picker, the live chip (elapsed + session link + Cancel)
 * while a run is in flight, and the failure chip + Retry afterwards. */
function PlanRunControls({
  epic,
  complete,
  now,
  busy,
  phaseRunActive,
  onRun,
  onCancel,
}: {
  epic: Epic;
  complete: boolean;
  now: number;
  busy: boolean;
  phaseRunActive: boolean;
  onRun: (agent: string, mode: PlanRunMode) => void;
  onCancel: () => void;
}): JSX.Element | null {
  const [open, setOpen] = useState(false);
  const run = epic.planRun;

  if (run?.runState === 'running') {
    return (
      <span className="flex items-center gap-1.5">
        <span
          className="inline-flex items-center gap-1 rounded-md border border-brand/40 bg-brand/10 px-2 py-1 font-mono text-[10.5px] text-brand"
          data-tip={`the whole plan is running${run.agent !== null ? ` — agent ${run.agent}` : ''} (mode: ${run.mode})`}
        >
          <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-brand" />
          Running plan
          {run.runStartedAt !== null ? ` · ${fmtElapsed(run.runStartedAt, now)}` : ''}
        </span>
        {run.runSessionUuid !== null && (
          <Link
            to={`/sessions/${run.runSessionUuid}`}
            className="font-mono text-[10.5px] text-ink-dim underline-offset-2 transition-colors hover:text-brand hover:underline"
          >
            session
          </Link>
        )}
        <button
          type="button"
          disabled={busy}
          onClick={onCancel}
          className="rounded-md border border-red/40 px-2 py-1 font-mono text-[10.5px] text-red transition-colors hover:bg-red/10 disabled:cursor-not-allowed disabled:opacity-50"
        >
          Cancel
        </button>
      </span>
    );
  }

  // A finished plan has nothing left to hand over; the ✓ summary button next to
  // this one is the right affordance there.
  if (complete) return null;

  const failed = run?.runState === 'failed';
  const disabled = busy || phaseRunActive || epic.status !== 'active';
  const title = phaseRunActive
    ? 'a phase run is active — cancel it before running the whole plan'
    : epic.status !== 'active'
      ? 'plan is not active'
      : 'hand the whole plan to one agent, headlessly, in an isolated worktree';

  return (
    <>
      <span className="flex items-center gap-1.5">
        {failed && (
          <span
            className="rounded-md border border-red/40 bg-red/10 px-2 py-1 font-mono text-[10.5px] text-red"
            data-tip={run?.runError ?? 'plan run failed'}
          >
            Run failed
          </span>
        )}
        <button
          type="button"
          disabled={disabled}
          data-tip={title}
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          className={`rounded-md border px-2 py-1 font-mono text-[10.5px] transition-colors disabled:cursor-not-allowed disabled:border-line disabled:text-ink-faint ${
            failed ? 'border-red/40 text-red hover:bg-red/10' : 'border-brand/40 text-brand hover:bg-brand/10'
          }`}
        >
          {busy ? '…' : failed ? '▶ Retry plan' : '▶ Run plan'}
        </button>
      </span>
      {open && (
        <PlanRunDialog
          initialAgent={run?.agent ?? ''}
          initialMode={run?.mode ?? 'auto'}
          onCancel={() => setOpen(false)}
          onRun={(agent, mode) => {
            setOpen(false);
            onRun(agent, mode);
          }}
        />
      )}
    </>
  );
}

/** The inline "how should this run?" panel — agent + execution mode. Inline
 * rather than modal, matching the rest of this page. It takes the full row so
 * it reads as a step, not as a dropdown hanging off the button. */
function PlanRunDialog({
  initialAgent,
  initialMode,
  onRun,
  onCancel,
}: {
  initialAgent: string;
  initialMode: PlanRunMode;
  onRun: (agent: string, mode: PlanRunMode) => void;
  onCancel: () => void;
}): JSX.Element {
  const [agent, setAgent] = useState(initialAgent);
  const [mode, setMode] = useState<PlanRunMode>(initialMode);
  const [agents, setAgents] = useState<string[] | null>(null);

  // The picker is the reason this is a panel and not a bare button: the agent
  // list is only worth fetching once someone actually opens it.
  useEffect(() => {
    let alive = true;
    fetchSystemItems('agents')
      .then((items) => {
        if (!alive) return;
        const names = [...new Set(items.map((i) => i.name))].sort((a, b) => a.localeCompare(b));
        setAgents(names);
      })
      .catch(() => {
        if (alive) setAgents([]); // the free-text field still works
      });
    return () => {
      alive = false;
    };
  }, []);

  return (
    <div className="mt-1 w-full rounded-lg border border-line bg-surface/60 px-3 py-2.5">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <label className="flex items-center gap-1.5">
          <span className="font-mono text-[10px] uppercase tracking-wider text-ink-faint">agent</span>
          <input
            list="plan-run-agents"
            value={agent}
            onChange={(e) => setAgent(e.target.value)}
            placeholder={agents === null ? 'loading…' : 'tech-lead (default)'}
            className="w-[190px] rounded-md border border-line bg-field px-2 py-1 font-mono text-[11px] text-ink outline-none focus:border-ink-dim"
          />
          <datalist id="plan-run-agents">
            {(agents ?? []).map((a) => (
              <option key={a} value={a} />
            ))}
          </datalist>
        </label>

        <div className="flex items-center gap-1.5" role="radiogroup" aria-label="execution mode">
          <span className="font-mono text-[10px] uppercase tracking-wider text-ink-faint">mode</span>
          {PLAN_RUN_MODES.map((m) => (
            <button
              key={m.id}
              type="button"
              role="radio"
              aria-checked={mode === m.id}
              data-tip={m.hint}
              onClick={() => setMode(m.id)}
              className={`rounded-md border px-2 py-1 font-mono text-[10.5px] transition-colors ${
                mode === m.id
                  ? 'border-brand/50 bg-brand/10 text-brand'
                  : 'border-line text-ink-dim hover:border-line-strong hover:text-ink'
              }`}
            >
              {m.label}
            </button>
          ))}
        </div>

        <div className="ml-auto flex items-center gap-1.5">
          <button
            type="button"
            onClick={onCancel}
            className="rounded-md border border-line px-2 py-1 font-mono text-[10.5px] text-ink-dim transition-colors hover:text-ink"
          >
            cancel
          </button>
          <button
            type="button"
            onClick={() => onRun(agent.trim(), mode)}
            className="rounded-md border border-brand/50 bg-brand/10 px-2.5 py-1 font-mono text-[10.5px] text-brand transition-colors hover:bg-brand/20"
          >
            ▶ Run
          </button>
        </div>
      </div>
      <p className="mt-2 font-mono text-[10px] leading-relaxed text-ink-faint">
        {PLAN_RUN_MODES.find((m) => m.id === mode)?.hint} · the run executes core&apos;s{' '}
        <code className="text-ink-dim">run-plan</code> skill headlessly in an isolated worktree:
        it commits per phase but never pushes, and stops with PLAN BLOCKED if a phase needs a human.
      </p>
    </div>
  );
}

/** Shell of the inline detail panel: an identity header, an optional tab bar
 * and the body — full column width (the parent column owns the scroll), so
 * plan docs and completion reports read as prose instead of a 360px rail. The
 * back control lives outside, above the panel (see BackToPhases). */
function DetailShell({
  ariaLabel,
  header,
  tabBar,
  children,
}: {
  ariaLabel: string;
  header: ReactNode;
  tabBar?: ReactNode;
  children: ReactNode;
}): JSX.Element {
  return (
    <section
      aria-label={ariaLabel}
      className="overflow-hidden rounded-xl border border-line bg-surface"
    >
      <div className="border-b border-line px-4 pt-3 pb-3">{header}</div>
      {tabBar}
      <div className="px-4 py-3 text-[13px] leading-relaxed">{children}</div>
    </section>
  );
}

/** The shared tab bar of both detail panels. */
function DetailTabs<T extends string>({
  label,
  tabs,
  active,
  onTab,
}: {
  label: string;
  tabs: { id: T; label: string }[];
  active: T;
  onTab: (tab: T) => void;
}): JSX.Element {
  return (
    <div
      className="flex shrink-0 gap-1 overflow-x-auto border-b border-line px-4"
      role="tablist"
      aria-label={label}
    >
      {tabs.map((t) => (
        <button
          key={t.id}
          type="button"
          role="tab"
          aria-selected={active === t.id}
          onClick={() => onTab(t.id)}
          className={`-mb-px shrink-0 border-b-2 px-3 py-[8px] text-[12.5px] font-medium whitespace-nowrap transition-colors ${
            active === t.id
              ? 'border-brand text-brand'
              : 'border-transparent text-ink-dim hover:text-ink'
          }`}
        >
          {t.label}
        </button>
      ))}
    </div>
  );
}

/** One labeled section inside a detail body. */
function RailSection({ label, children }: { label: string; children: ReactNode }): JSX.Element {
  return (
    <section className="mb-4 last:mb-0">
      <div className="mb-1.5 font-mono text-[10px] uppercase tracking-wider text-ink-faint">
        {label}
      </div>
      {children}
    </section>
  );
}

/** Acceptance-criteria list with tick state (✓ done / ○ open). With `onToggle`
 * the rows become buttons that flip the criterion in the doc (the affordance
 * the retired doc modal owned); without it the list is read-only. */
function ChecksList({
  checks,
  onToggle,
  busyLine,
}: {
  checks: Check[];
  onToggle?: (c: Check) => void;
  busyLine?: number | null;
}): JSX.Element {
  return (
    <ul className="space-y-1.5">
      {checks.map((c) => {
        const mark = (
          <span
            aria-hidden="true"
            className={`mt-px shrink-0 font-mono text-[12px] ${
              c.done ? 'text-green' : 'text-ink-faint'
            }`}
          >
            {c.done ? '✓' : '○'}
          </span>
        );
        if (onToggle === undefined) {
          return (
            <li key={c.line} className="flex items-start gap-2">
              {mark}
              <span className={c.done ? 'text-ink-dim' : 'text-ink'}>{c.text}</span>
            </li>
          );
        }
        return (
          <li key={c.line}>
            <button
              type="button"
              disabled={busyLine === c.line}
              aria-pressed={c.done}
              onClick={() => onToggle(c)}
              data-tip={c.done ? 'un-tick this criterion in the doc' : 'tick this criterion in the doc'}
              className="flex w-full items-start gap-2 rounded px-1 py-0.5 text-left transition-colors hover:bg-surface2/50 disabled:opacity-50 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand"
            >
              {mark}
              <span className={c.done ? 'text-ink-dim line-through' : 'text-ink'}>{c.text}</span>
            </button>
          </li>
        );
      })}
    </ul>
  );
}

/** Inline raw-markdown editor — the Edit tab of both detail panels. Loads the
 * doc itself, PUTs on Save (the daemon writes a timestamped backup), and tells
 * the page to refetch so the rollup follows. Replaces the doc modal. */
function DocEditor({
  taskId,
  path,
  version,
  onSaved,
}: {
  taskId: number;
  path: string;
  /** Bumps to re-fetch when the doc changed server-side. */
  version: string | null;
  onSaved: () => void;
}): JSX.Element {
  const [content, setContent] = useState<string | null>(null);
  const [draft, setDraft] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [note, setNote] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    setContent(null);
    setError(null);
    setNote(null);
    fetchPlanDoc(taskId, path)
      .then((d) => {
        if (!alive) return;
        setContent(d.content);
        setDraft(d.content);
      })
      .catch((e: unknown) => {
        if (alive) setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      alive = false;
    };
  }, [taskId, path, version]);

  const save = (): void => {
    setSaving(true);
    setError(null);
    savePlanDoc(taskId, path, draft)
      .then((doc) => {
        setContent(doc.content);
        setDraft(doc.content);
        setNote(doc.backup !== undefined ? 'saved · backup written' : 'saved');
        onSaved();
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setSaving(false));
  };

  if (content === null && error === null) return <Loading label="doc…" />;
  const dirty = draft !== content;

  return (
    <div className="space-y-2">
      <div className="font-mono text-[10px] text-ink-faint">{path}</div>
      {error !== null && (
        <div
          role="alert"
          className="rounded-md border border-red/40 bg-red/10 px-2.5 py-1.5 font-mono text-[11px] text-red"
        >
          {error}
        </div>
      )}
      {note !== null && !dirty && (
        <div className="rounded-md border border-green/30 bg-green/10 px-2.5 py-1.5 font-mono text-[11px] text-green">
          {note}
        </div>
      )}
      <textarea
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        spellCheck={false}
        aria-label="plan doc source"
        className="min-h-[460px] w-full resize-y rounded-lg border border-line bg-field px-3.5 py-3 font-mono text-[12.5px] leading-relaxed text-ink outline-none focus:border-ink-dim"
      />
      <div className="flex items-center justify-end gap-2">
        <button
          type="button"
          disabled={!dirty || saving}
          onClick={() => setDraft(content ?? '')}
          className="rounded-md border border-line px-3 py-1.5 font-mono text-[11px] text-ink-dim transition-colors hover:text-ink disabled:cursor-not-allowed disabled:text-ink-faint focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand"
        >
          revert
        </button>
        <button
          type="button"
          disabled={!dirty || saving}
          onClick={save}
          className="rounded-md border border-line-strong bg-surface2 px-3 py-1.5 font-mono text-[11px] text-brand transition-colors hover:bg-surface2/70 disabled:cursor-not-allowed disabled:text-ink-faint focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand"
        >
          {saving ? 'saving…' : 'Save'}
        </button>
      </div>
    </div>
  );
}

/** Run chip for the phase rail header. Switches on the run's OUTCOME, not its
 * process state, so the panel tells the same four-way story the list does — a
 * `runState: 'done'` process that ticked nothing reads `ran · no progress`,
 * clickable, instead of a green "Run done" the phase never earned. */
function RunStateChip({
  phase,
  onOpenOutcome,
}: {
  phase: EpicPhase;
  onOpenOutcome: () => void;
}): JSX.Element | null {
  if (phase.runOutcome === 'running')
    return (
      <span
        className="inline-flex items-center gap-1 rounded border border-brand/40 bg-brand/10 px-1.5 py-px font-mono text-[9.5px] text-brand"
        data-tip="headless run in progress"
      >
        <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-brand" />
        Running
      </span>
    );
  if (phase.runOutcome === 'completed') return <RunCompletedChip phase={phase} />;
  if (isUnresolvedOutcome(phase.runOutcome))
    return <RunOutcomeChip phase={phase} outcome={phase.runOutcome} onOpen={onOpenOutcome} />;
  return null;
}

/** Phase details panel, tabbed: Phase (run state, interactive acceptance
 * criteria, full doc), Summary (what was shipped) and Edit (raw markdown).
 * Rendered inline in place of the phase list. */
function PhaseDetailPanel({
  epic,
  phase,
  tab,
  onTab,
  runBusy,
  planRunning,
  onRetry,
  onCancelRun,
  onOpenOutcome,
  onDocChanged,
}: {
  epic: Epic;
  phase: EpicPhase;
  tab: PhaseDetailTab;
  onTab: (tab: PhaseDetailTab) => void;
  runBusy: number | null;
  /** A whole-plan run owns the phase docs — per-phase runs stand down. */
  planRunning: boolean;
  onRetry: () => void;
  onCancelRun: () => void;
  /** Open the run-diagnosis modal for this phase. */
  onOpenOutcome: () => void;
  onDocChanged: () => void;
}): JSX.Element {
  const resolvedSeqs = useMemo(() => computeResolvedSeqs(epic.phases), [epic.phases]);
  const status = phaseStatus(phase, resolvedSeqs);
  // Same split as the list row: `phaseStatus` reads the doc, `running` reads the
  // process, and only the second one may put the word `running` on screen.
  const running = phase.runState === 'running';
  const chip = phaseChip(status, running);
  const [doc, setDoc] = usePlanDoc(epic.taskId, phase.docRelPath, phase.docUpdatedAt);
  const checks = useMemo(() => (doc !== null ? extractChecks(doc) : null), [doc]);
  const [busyLine, setBusyLine] = useState<number | null>(null);
  const [toggleErr, setToggleErr] = useState<string | null>(null);

  // Toggling a criterion PATCHes that exact source line; the response is the
  // fresh doc, and the page refetch keeps the rollup/list in step.
  const toggle = (c: Check): void => {
    setBusyLine(c.line);
    setToggleErr(null);
    togglePlanCheckbox(epic.taskId, phase.docRelPath, c.line, !c.done)
      .then((d) => {
        setDoc(d.content);
        onDocChanged();
      })
      .catch((e: unknown) => setToggleErr(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusyLine(null));
  };

  // The phase list — which normally carries the run controls — is hidden while
  // this panel is open, so the same actions live in the header here.
  const depsUnmet = phase.dependsOn.filter((seq) => !resolvedSeqs.has(seq));
  const runDisabled =
    runBusy !== null || planRunning || epic.status !== 'active' || depsUnmet.length > 0;
  const runTitle = planRunning
    ? 'a whole-plan run is executing this plan'
    : epic.status !== 'active'
      ? 'plan is not active'
      : depsUnmet.length > 0
        ? `waiting on phase ${depsUnmet.join(', ')}`
        : 'run this phase headlessly in an isolated worktree';

  return (
    <DetailShell
      ariaLabel={`Phase ${String(phase.seq)} — ${phase.name} details`}
      header={
        <>
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <div className="font-mono text-[10px] text-ink-faint">Phase {phase.seq}</div>
              <div className="text-[14px] font-semibold text-ink">{phase.name}</div>
            </div>
            <div className="flex shrink-0 items-center gap-1.5">
              {phase.runState === 'running' ? (
                <>
                  {phase.runSessionUuid !== null && (
                    <Link
                      to={`/sessions/${phase.runSessionUuid}`}
                      className="font-mono text-[10px] text-ink-dim underline-offset-2 transition-colors hover:text-brand hover:underline"
                    >
                      session
                    </Link>
                  )}
                  <button
                    type="button"
                    disabled={runBusy !== null}
                    onClick={onCancelRun}
                    className="rounded-md border border-red/40 px-2 py-1 font-mono text-[10px] text-red transition-colors hover:bg-red/10 disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    Cancel
                  </button>
                </>
              ) : status !== 'done' ? (
                <button
                  type="button"
                  disabled={runDisabled}
                  data-tip={runTitle}
                  onClick={onRetry}
                  className={`rounded-md border px-2 py-1 font-mono text-[10px] transition-colors disabled:cursor-not-allowed disabled:border-line disabled:text-ink-faint ${runButtonCls(phase.runOutcome)}`}
                >
                  {runBusy === phase.id
                    ? '…'
                    : isUnresolvedOutcome(phase.runOutcome)
                      ? 'Retry run'
                      : 'Run phase'}
                </button>
              ) : null}
            </div>
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-1.5">
            <span className={`rounded border px-1.5 py-px font-mono text-[9px] ${chip.cls}`}>
              {chip.label}
            </span>
            {status === 'in_progress' && (
              <PhaseActivity docUpdatedAt={phase.docUpdatedAt} running={running} />
            )}
            <RunStateChip phase={phase} onOpenOutcome={onOpenOutcome} />
            <span className="font-mono text-[10px] text-ink-faint">
              {phase.checkboxesDone}/{phase.checkboxesTotal || 0}
            </span>
            {phase.dependsOn.map((seq) => (
              <span
                key={seq}
                className={`rounded border px-1 py-px font-mono text-[9px] ${
                  resolvedSeqs.has(seq) ? 'border-green/40 text-green' : 'border-line text-ink-faint'
                }`}
                data-tip={resolvedSeqs.has(seq) ? 'dependency resolved' : 'dependency pending'}
              >
                ← #{seq}
              </span>
            ))}
          </div>
        </>
      }
      tabBar={
        <DetailTabs
          label="phase details tabs"
          tabs={PHASE_TABS}
          active={tab}
          onTab={onTab}
        />
      }
    >
      {tab === 'edit' ? (
        <DocEditor
          taskId={epic.taskId}
          path={phase.docRelPath}
          version={phase.docUpdatedAt}
          onSaved={onDocChanged}
        />
      ) : tab === 'summary' ? (
        <PhaseSummary phase={phase} doc={doc} />
      ) : (
        <>
          {phase.runState === 'failed' && (
            <div className="mb-4 rounded-md border border-red/40 bg-red/10 px-2.5 py-2">
              <div className="font-mono text-[10.5px] break-words text-red">
                {phase.runError ?? 'run failed'}
              </div>
            </div>
          )}

          {toggleErr !== null && (
            <div
              role="alert"
              className="mb-4 rounded-md border border-red/40 bg-red/10 px-2.5 py-1.5 font-mono text-[10.5px] text-red"
            >
              {toggleErr}
            </div>
          )}

          <RailSection label="acceptance criteria">
            {checks === null ? (
              <Loading label="criteria…" />
            ) : checks.length === 0 ? (
              <div className="font-mono text-[11.5px] text-ink-faint">no checkboxes in this doc</div>
            ) : (
              <ChecksList checks={checks} onToggle={toggle} busyLine={busyLine} />
            )}
          </RailSection>

          <RailSection label="doc">
            {doc === null ? <Loading label="doc…" /> : <Markdown text={doc} />}
          </RailSection>
        </>
      )}
    </DetailShell>
  );
}

const PHASE_TABS: { id: PhaseDetailTab; label: string }[] = [
  { id: 'phase', label: 'Phase' },
  { id: 'summary', label: 'Summary' },
  { id: 'edit', label: 'Edit' },
];

/** Summary tab of one phase: what was actually shipped — the Completion Report
 * the executor wrote, the criteria it ticked, and the doc's `## Execution
 * record` section. Nothing written yet reads as an explicit empty state rather
 * than a missing tab. */
function PhaseSummary({ phase, doc }: { phase: EpicPhase; doc: string | null }): JSX.Element {
  const ticked = useMemo(() => (doc !== null ? extractChecks(doc).filter((c) => c.done) : []), [doc]);
  const execRecord = useMemo(() => (doc !== null ? extractSection(doc, 'Execution record') : null), [doc]);

  if (doc === null) return <Loading label="summary…" />;

  const empty = phase.completionReport === null && ticked.length === 0 && execRecord === null;
  if (empty) {
    return (
      <div className="font-mono text-[11.5px] text-ink-faint">
        nothing shipped yet — no ticked criteria, no Completion Report, no execution record
      </div>
    );
  }

  return (
    <>
      {phase.completionReport !== null && (
        <RailSection label="completion report">
          <Markdown text={phase.completionReport} />
        </RailSection>
      )}
      {ticked.length > 0 && (
        <RailSection label={`completed criteria (${String(ticked.length)})`}>
          <ChecksList checks={ticked} />
        </RailSection>
      )}
      {execRecord !== null && (
        <RailSection label="execution record">
          <Markdown text={execRecord} />
        </RailSection>
      )}
    </>
  );
}

/** Plan details panel: a Plan tab (the plan README markdown), an Edit tab (raw
 * README) and — only when every phase is done — a Summary tab with the
 * per-phase executed work. Rendered inline in place of the phase list. */
function PlanDetailPanel({
  epic,
  tab,
  onTab,
  onDocChanged,
}: {
  epic: Epic;
  tab: PlanDetailTab;
  onTab: (tab: PlanDetailTab) => void;
  onDocChanged: () => void;
}): JSX.Element {
  const resolvedSeqs = useMemo(() => computeResolvedSeqs(epic.phases), [epic.phases]);
  const complete = planComplete(epic, resolvedSeqs);
  // The Summary tab exists only on complete plans — a stale selection (e.g. a
  // rescan un-ticked a box) degrades to the Plan tab instead of a dead panel.
  const activeTab: PlanDetailTab = tab === 'summary' && !complete ? 'plan' : tab;
  const tabs: { id: PlanDetailTab; label: string }[] = [
    { id: 'plan', label: 'Plan' },
    ...(complete ? [{ id: 'summary' as const, label: 'Summary' }] : []),
    { id: 'edit', label: 'Edit' },
  ];

  return (
    <DetailShell
      ariaLabel={`${epic.title} — plan details`}
      header={
        <div className="min-w-0">
          <div className="text-[14px] font-semibold text-ink">{epic.title}</div>
          <div className="mt-1 flex flex-wrap items-center gap-1.5">
            <span
              className={`rounded border px-1.5 py-px font-mono text-[9.5px] ${STATUS_BADGE[epic.status]}`}
            >
              {epic.status}
            </span>
            <span className="font-mono text-[10px] text-ink-faint">
              {epic.rollup.done}/{epic.rollup.total} ({Math.round(epic.rollup.pct)}%)
            </span>
          </div>
        </div>
      }
      tabBar={
        <DetailTabs label="plan details tabs" tabs={tabs} active={activeTab} onTab={onTab} />
      }
    >
      {activeTab === 'edit' ? (
        <DocEditor taskId={epic.taskId} path="README.md" version={null} onSaved={onDocChanged} />
      ) : activeTab === 'plan' ? (
        <PlanReadme epic={epic} />
      ) : (
        <PlanSummary epic={epic} resolvedSeqs={resolvedSeqs} />
      )}
    </DetailShell>
  );
}

/** Plan tab body — the plan README markdown. */
function PlanReadme({ epic }: { epic: Epic }): JSX.Element {
  const [readme] = usePlanDoc(epic.taskId, 'README.md', null);
  if (readme === null) return <Loading label="readme…" />;
  return <Markdown text={readme} />;
}

/** Summary tab body (complete plans only): per-phase executed work — phase
 * title + N/N, its TICKED acceptance criteria, and, when present, the phase's
 * Completion Report and/or `## Execution record` doc section. Derived
 * client-side from the existing docs endpoint (one fetch per phase doc, plus
 * plan/SUMMARY.md when the executor wrote one) — no API extension needed. */
function PlanSummary({
  epic,
  resolvedSeqs,
}: {
  epic: Epic;
  resolvedSeqs: Set<number>;
}): JSX.Element {
  const [docs, setDocs] = useState<Record<string, string> | null>(null);
  const [summaryMd, setSummaryMd] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    setDocs(null);
    setSummaryMd(null);
    void Promise.all(
      epic.phases.map(async (p) => {
        try {
          const d = await fetchPlanDoc(epic.taskId, p.docRelPath);
          return [p.docRelPath, d.content] as const;
        } catch {
          return [p.docRelPath, ''] as const; // a missing doc degrades to DTO-only data
        }
      }),
    ).then((entries) => {
      if (alive) setDocs(Object.fromEntries(entries));
    });
    if (epic.hasSummary) {
      fetchPlanDoc(epic.taskId, 'SUMMARY.md')
        .then((d) => {
          if (alive) setSummaryMd(d.content);
        })
        .catch(() => undefined); // optional artifact — skip silently
    }
    return () => {
      alive = false;
    };
  }, [epic.taskId, epic.hasSummary, epic.phases]);

  if (docs === null) return <Loading label="summary…" />;

  return (
    <div className="space-y-4">
      {summaryMd !== null && (
        <RailSection label="plan summary">
          <Markdown text={summaryMd} />
        </RailSection>
      )}
      {epic.phases.map((p) => {
        const doc = docs[p.docRelPath] ?? '';
        const ticked = extractChecks(doc).filter((c) => c.done);
        const execRecord = extractSection(doc, 'Execution record');
        const status = phaseStatus(p, resolvedSeqs);
        return (
          <section key={p.id} className="rounded-lg border border-line bg-surface/40 px-3 py-2.5">
            <div className="flex items-center gap-2">
              <span className="shrink-0 font-mono text-[10px] text-ink-faint">Phase {p.seq}</span>
              <span className="min-w-0 flex-1 truncate text-[13px] font-medium text-ink">
                {p.name}
              </span>
              <span
                className={`shrink-0 font-mono text-[10px] ${
                  status === 'done' ? 'text-green' : 'text-ink-faint'
                }`}
              >
                {p.checkboxesDone}/{p.checkboxesTotal || 0}
              </span>
            </div>
            {ticked.length > 0 && (
              <div className="mt-2">
                <ChecksList checks={ticked} />
              </div>
            )}
            {p.completionReport !== null && (
              <div className="mt-2 border-t border-line pt-2">
                <div className="mb-1 font-mono text-[10px] uppercase tracking-wider text-ink-faint">
                  completion report
                </div>
                <Markdown text={p.completionReport} />
              </div>
            )}
            {execRecord !== null && (
              <div className="mt-2 border-t border-line pt-2">
                <div className="mb-1 font-mono text-[10px] uppercase tracking-wider text-ink-faint">
                  execution record
                </div>
                <Markdown text={execRecord} />
              </div>
            )}
          </section>
        );
      })}
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
