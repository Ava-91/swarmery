// Phase-run diagnosis modal — mirrors ImproveModal's structure. Opened from the
// outcome chip on a phase whose run ended without completing it (noop / partial
// / failed), it answers the one question the chip cannot: WHY.
//
// On open it fetches GET /api/epics/{taskId}/phases/{phaseId}/diagnosis — the
// derived outcome, the criteria delta, the deterministic blockers the daemon can
// prove (unmerged dependency, dirty run branch, no criteria at all) and, for
// everything it cannot, the executor's own last word.
//
// Two things this modal refuses to do:
//   • render a fabricated baseline — `criteriaBefore: null` means the run's
//     starting count was never measured, and it says so instead of printing 0;
//   • hide behind a plan run — the diagnosis is READ-ONLY, so it stays open and
//     fetchable while a whole-plan run owns the docs. Only the write actions
//     (Delete branch / Retry run) stand down.

import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import type { PhaseDiagnosis, PhaseRunOutcome } from '../api/types';
import { deletePhaseRunBranch, fetchPhaseDiagnosis } from '../api';
import { fmtElapsed } from '../lib/format';

type Phase =
  | { kind: 'loading' }
  | { kind: 'ready'; diag: PhaseDiagnosis }
  | { kind: 'error'; message: string };

/** Chip styling + prose for each terminal outcome. `amber` is the app's
 * semantic "needs a human look" color (the same token workspace/TaskCard.tsx
 * uses for inconclusive) — a run that ended without finishing the phase is
 * exactly that, and must never read green. */
const OUTCOME_CHIP: Record<PhaseRunOutcome, { label: string; cls: string; title: string }> = {
  completed: {
    label: 'completed',
    cls: 'border-green/40 bg-green/10 text-green',
    title: 'the run ticked every acceptance criterion',
  },
  partial: {
    label: 'partial',
    cls: 'border-amber/40 bg-amber/10 text-amber',
    title: 'the run ticked some criteria but did not finish the phase',
  },
  noop: {
    label: 'no progress',
    cls: 'border-amber/40 bg-amber/10 text-amber',
    title: 'the run finished but ticked no acceptance criteria',
  },
  failed: {
    label: 'failed',
    cls: 'border-red/40 bg-red/10 text-red',
    title: 'the run failed',
  },
  running: {
    label: 'running',
    cls: 'border-brand/40 bg-brand/10 text-brand',
    title: 'headless run in progress',
  },
  idle: {
    label: 'never run',
    cls: 'border-line text-ink-faint',
    title: 'this phase has not been run',
  },
};

export function RunOutcomeModal({
  taskId,
  phaseId,
  writesDisabled,
  writesDisabledReason,
  onClose,
  onRetry,
}: {
  taskId: number;
  phaseId: number;
  /** A plan run owns the docs — the write actions stand down, the diagnosis does not. */
  writesDisabled: boolean;
  writesDisabledReason: string;
  onClose: () => void;
  /** Fires the caller's own phase-run handler (Plans.tsx owns the busy state). */
  onRetry: () => void;
}): JSX.Element {
  const [phase, setPhase] = useState<Phase>({ kind: 'loading' });
  const [busy, setBusy] = useState(false);
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const [actionErr, setActionErr] = useState<string | null>(null);

  // One fetch path for both the mount load and the post-delete refetch: the
  // `alive` box lets the effect drop a response that lands after unmount.
  const load = useCallback(
    (alive: { current: boolean } = { current: true }): Promise<void> =>
      fetchPhaseDiagnosis(taskId, phaseId)
        .then((diag) => {
          if (alive.current) setPhase({ kind: 'ready', diag });
        })
        .catch((e: unknown) => {
          if (alive.current)
            setPhase({ kind: 'error', message: e instanceof Error ? e.message : String(e) });
        }),
    [taskId, phaseId],
  );

  // Fetch the diagnosis on mount / whenever the target phase changes.
  useEffect(() => {
    const alive = { current: true };
    setPhase({ kind: 'loading' });
    void load(alive);
    return () => {
      alive.current = false;
    };
  }, [load]);

  // Esc closes, except while a branch delete is in flight. While the delete is
  // ARMED it disarms instead of closing — Esc is the universal "back out", and
  // closing the whole modal on it would leave the user unsure whether the branch
  // survived.
  useEffect(() => {
    const onKey = (e: KeyboardEvent): void => {
      if (e.key !== 'Escape' || busy) return;
      if (confirmingDelete) {
        setConfirmingDelete(false);
        return;
      }
      onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [busy, confirmingDelete, onClose]);

  const diag = phase.kind === 'ready' ? phase.diag : null;
  const chip = OUTCOME_CHIP[diag?.runOutcome ?? 'idle'];
  // The branch is only reclaimable when the daemon actually proved it dirty —
  // offering the delete otherwise would invite destroying an unrelated branch. The
  // blocker itself (not a client-side reconstruction of the name) is what the
  // confirmation quotes.
  const dirty = diag?.blockers.find((b) => b.kind === 'branch-dirty') ?? null;

  const endedMs =
    diag !== null && diag.runEndedAt !== null ? Date.parse(diag.runEndedAt) : Number.NaN;
  const duration =
    diag !== null && diag.runStartedAt !== null && !Number.isNaN(endedMs)
      ? fmtElapsed(diag.runStartedAt, endedMs)
      : null;

  function deleteBranch(): void {
    setBusy(true);
    setConfirmingDelete(false);
    setActionErr(null);
    deletePhaseRunBranch(taskId, phaseId)
      .then(() => load())
      .catch((e: unknown) => setActionErr(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(false));
  }

  // The delete reaches `git branch -D`: the commits go, and nothing else holds
  // them. Two steps, in-modal (never window.confirm — a browser modal blocks the
  // extension-driven flows this repo uses), and the armed step names the branch and
  // the exact number of commits it is about to destroy. Same shape as KillButton's
  // confirm: an armed line + Confirm/Cancel replacing the trigger in place.
  const deleteSlot = ((): JSX.Element | null => {
    if (dirty === null) return null;
    const commits = dirty.commitsAhead ?? 0;
    const branch = dirty.branch ?? 'the run branch';
    if (!confirmingDelete) {
      return (
        <button
          type="button"
          disabled={busy || writesDisabled}
          title={
            writesDisabled ? writesDisabledReason : 'delete the run branch so a retry can recreate it'
          }
          onClick={() => setConfirmingDelete(true)}
          className="rounded-lg border border-red/40 bg-red/5 px-3.5 py-1.5 font-mono text-[11.5px] text-red transition-colors hover:bg-red/10 disabled:cursor-not-allowed disabled:opacity-50"
        >
          {busy ? 'deleting…' : 'Delete branch'}
        </button>
      );
    }
    return (
      <span className="flex flex-wrap items-center justify-end gap-2">
        <span className="font-mono text-[10.5px] text-ink-dim">
          Delete {branch}?{' '}
          {commits > 0
            ? `${String(commits)} commit${commits === 1 ? '' : 's'} will be lost.`
            : 'Its commits will be lost.'}
        </span>
        <button
          type="button"
          disabled={busy || writesDisabled}
          onClick={deleteBranch}
          className="rounded-lg border border-red/40 bg-red/10 px-3.5 py-1.5 font-mono text-[11.5px] font-semibold text-red transition-colors hover:bg-red/20 disabled:cursor-not-allowed disabled:opacity-50"
        >
          {busy ? 'deleting…' : 'Delete permanently'}
        </button>
        <button
          type="button"
          disabled={busy}
          onClick={() => setConfirmingDelete(false)}
          className="font-mono text-[11.5px] text-ink-dim transition-colors hover:text-ink disabled:opacity-50"
        >
          Cancel
        </button>
      </span>
    );
  })();

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-bg/70 p-4"
      role="dialog"
      aria-modal="true"
      aria-label="Phase run diagnosis"
      onClick={busy ? undefined : onClose}
    >
      <div
        className="flex max-h-[85vh] w-full max-w-lg flex-col rounded-xl border border-line bg-surface px-4 py-4"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="font-mono text-[10px] text-ink-faint">
              Phase {diag !== null ? diag.seq : '—'}
            </div>
            <div className="font-display truncate text-[14px] font-bold text-ink">
              {diag?.name ?? 'run diagnosis'}
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-1.5">
            {duration !== null && (
              <span className="font-mono text-[10px] text-ink-faint" title="run duration">
                ran {duration}
              </span>
            )}
            {diag !== null && (
              <span
                className={`rounded border px-1.5 py-px font-mono text-[9.5px] ${chip.cls}`}
                title={chip.title}
              >
                {chip.label}
              </span>
            )}
          </div>
        </div>

        {phase.kind === 'loading' && (
          <div className="mt-3 font-mono text-[11.5px] text-ink-dim">loading diagnosis…</div>
        )}

        {phase.kind === 'error' && (
          <div className="mt-3 rounded-lg border border-red/25 bg-red/5 px-2.5 py-2 font-mono text-[11px] text-red">
            {phase.message}
          </div>
        )}

        {actionErr !== null && (
          <div
            role="alert"
            className="mt-3 rounded-lg border border-red/25 bg-red/5 px-2.5 py-2 font-mono text-[11px] text-red"
          >
            {actionErr}
          </div>
        )}

        {diag !== null && (
          <div className="mt-3 min-h-0 flex-1 space-y-4 overflow-y-auto">
            {/* Criteria delta. `criteriaBefore === null` prints "baseline not
                measured" rather than a 0 nobody observed. */}
            <div className="font-mono text-[11.5px] text-ink-2">
              {diag.criteriaAfter} of {diag.criteriaTotal} criteria ticked
              {diag.criteriaBefore !== null
                ? ` · ${String(diag.criteriaBefore)} before this run`
                : ' · baseline not measured'}
            </div>

            {diag.runError !== null && (
              <div className="rounded-lg border border-red/25 bg-red/5 px-2.5 py-2 font-mono text-[10.5px] break-words text-red">
                {diag.runError}
              </div>
            )}

            <div>
              <div className="mb-1.5 font-mono text-[10px] tracking-[0.16em] text-ink-faint uppercase">
                blockers
              </div>
              {diag.blockers.length === 0 ? (
                <div className="font-mono text-[11px] text-ink-faint">
                  No blockers detected — the executor exited without ticking criteria; open the
                  session to see why.
                </div>
              ) : (
                <ul className="space-y-2">
                  {diag.blockers.map((b, i) => (
                    <li
                      key={`${b.kind}-${String(i)}`}
                      className="rounded-lg border border-line bg-bg/40 px-2.5 py-2"
                    >
                      <div className="text-[11.5px] text-ink">{b.summary}</div>
                      <div className="mt-1 font-mono text-[10px] whitespace-pre-line text-ink-faint">
                        {b.detail}
                      </div>
                    </li>
                  ))}
                </ul>
              )}
            </div>

            {diag.agentMessage !== null && (
              <div>
                <div className="mb-1.5 font-mono text-[10px] tracking-[0.16em] text-ink-faint uppercase">
                  agent said
                </div>
                <div className="rounded-lg border border-line bg-bg/40 px-2.5 py-2 font-mono text-[10.5px] leading-relaxed whitespace-pre-wrap text-ink-2">
                  {diag.agentMessage.text}
                  {diag.agentMessage.truncated ? '…' : ''}
                </div>
                <Link
                  to={`/sessions/${diag.agentMessage.sessionUuid}`}
                  className="mt-1 inline-block font-mono text-[10px] text-ink-dim underline-offset-2 transition-colors hover:text-brand hover:underline"
                >
                  open session
                </Link>
              </div>
            )}
          </div>
        )}

        <div className="mt-4 flex flex-wrap items-center justify-end gap-2">
          {deleteSlot}
          <button
            type="button"
            disabled={busy || writesDisabled}
            title={writesDisabled ? writesDisabledReason : 'run this phase again'}
            onClick={() => {
              onRetry();
              onClose();
            }}
            className="rounded-lg border border-brand/40 bg-brand/10 px-3.5 py-1.5 font-mono text-[11.5px] font-semibold text-brand transition-colors hover:bg-brand/20 disabled:cursor-not-allowed disabled:opacity-50"
          >
            Retry run
          </button>
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            className="rounded-lg border border-line bg-surface px-3.5 py-1.5 font-mono text-[11.5px] text-ink-2 transition-colors hover:bg-surface2 disabled:opacity-50"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  );
}
