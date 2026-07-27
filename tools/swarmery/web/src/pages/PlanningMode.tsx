// Planning Mode (interactive planning v2 — phase 3): "describe what you want
// to build" → a structured wizard interview → a plan in the private workspace.
//
// The backend (internal/planning state machine) drives everything through the
// extended GET /api/projects/{id}/planning DTO; this page renders by status:
//  · '' / cancelled      — idle intake (idea textarea + Start).
//  · generating /        — spinner + elapsed + the planner's last reasoning
//    proceeding            snippet, History drawer available.
//  · awaiting_answer     — the two-pane wizard: QuestionCard (left) + sticky
//                          RunningPlanPanel (right) with refine/proceed; when
//                          the reply failed the protocol parse (currentQuestion
//                          null + rawReply set) the raw-text fallback shows the
//                          prose and a free-text box that answers via the SAME
//                          answer endpoint (questionId "" + otherText — the
//                          endpoint owns the resume, NOT sendSessionMessage).
//  · done                — "Plan ready" + link to the Plans page (the plan→board
//                          activation flow was removed in phase 4).
//  · failed              — error card + intake prefilled to start again.
//
// Frozen WS bus: session_updated/task_updated → refetch, plus a 4s reconcile
// poll while the wizard is open and a settle-poll for the workspace task row.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import type { PlanningStatus, TaskSummary, WSMessage } from '../api/types';
import {
  answerPlanning,
  cancelPlanning,
  fetchPlanning,
  fetchTasks,
  proceedPlanning,
  refinePlanning,
  startPlanning,
} from '../api';
import { fmtAgo } from '../lib/format';
import { useLiveUpdates } from '../lib/ws';
import { useProjectWorkspace } from '../workspace/ProjectContext';
import { Card, Empty, ErrorBox, Loading } from '../components/ui';
import { QuestionCard } from './planning/QuestionCard';
import { RunningPlanPanel } from './planning/RunningPlanPanel';
import { HistoryDrawer } from './planning/HistoryDrawer';
import { RefineModal } from './planning/RefineModal';

// How a planning run actually works, step by step — rendered under the idea
// box so the intake screen doubles as the feature's documentation.
const HOW_IT_WORKS: { title: string; body: string }[] = [
  {
    title: 'Describe the idea',
    body: 'A headless planner session starts in this project’s repo — it sees the code, CLAUDE.md, and the core-pack planning agents.',
  },
  {
    title: 'Answer structured questions',
    body: 'The planner interviews you one question at a time — pick an option (or write your own) while the running plan rebuilds beside it after every answer.',
  },
  {
    title: 'Refine or proceed',
    body: '«Уточнити» steers the plan and the next questions; «Продовжуйте за планом» ends the interview and the planner writes the full plan.',
  },
  {
    title: 'The plan lands in the workspace',
    body: 'Phase-N docs with acceptance checkboxes are written to the private workspace — never the repo — and appear on the Plans page within seconds.',
  },
];

// Settle-poll cadence for the "plan ready" workspace task (wsingest rescans on
// a 60s cadence, so we poll a little faster once the wizard is done).
const PLAN_POLL_MS = 15_000;

// Reconcile-poll cadence while the wizard is open (net under the WS refetch).
const STATUS_POLL_MS = 4_000;

/** Compact elapsed string ("3m 12s") since an RFC3339 instant. */
function fmtElapsed(startedAt: string, nowMs: number): string {
  const s = Math.max(0, Math.floor((nowMs - new Date(startedAt).getTime()) / 1000));
  return s < 60 ? `${String(s)}s` : `${String(Math.floor(s / 60))}m ${String(s % 60)}s`;
}

export function PlanningMode(): JSX.Element {
  const { projectId, project, slug, loading } = useProjectWorkspace();

  const [status, setStatus] = useState<PlanningStatus | null>(null);
  const [idea, setIdea] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // The workspace task row that appears once the plan dir is written.
  const [plan, setPlan] = useState<TaskSummary | null>(null);
  // Free-text reply for the raw fallback (no structured question parsed).
  const [rawAnswer, setRawAnswer] = useState('');
  const [historyOpen, setHistoryOpen] = useState(false);
  const [refineOpen, setRefineOpen] = useState(false);
  // 1s tick that drives the elapsed timer while the planner is thinking.
  const [nowMs, setNowMs] = useState(() => Date.now());

  const aliveRef = useRef(true);

  const wstatus = status?.status ?? '';
  const sessionUuid = status?.sessionUuid ?? '';
  const history = useMemo(() => status?.history ?? [], [status]);
  // Legacy net: a pre-wizard in-memory run reports active with no status row.
  const thinking =
    wstatus === 'generating' || wstatus === 'proceeding' || (wstatus === '' && status?.active === true);
  const wizardOpen = thinking || wstatus === 'awaiting_answer';

  const loadStatus = useCallback((): void => {
    if (projectId === null) return;
    fetchPlanning(projectId)
      .then((s) => {
        if (!aliveRef.current) return;
        setStatus(s);
      })
      .catch((e: unknown) => {
        if (!aliveRef.current) return;
        setError(e instanceof Error ? e.message : String(e));
      });
  }, [projectId]);

  useEffect(() => {
    aliveRef.current = true;
    loadStatus();
    return () => {
      aliveRef.current = false;
    };
  }, [loadStatus]);

  // Reconcile poll while the wizard is open — the WS refetch is the fast path,
  // this is the net (missed frames, daemon restarts, stale-status heal).
  useEffect(() => {
    if (!wizardOpen) return undefined;
    const t = window.setInterval(loadStatus, STATUS_POLL_MS);
    return () => {
      window.clearInterval(t);
    };
  }, [wizardOpen, loadStatus]);

  // Elapsed-timer tick while the planner is thinking.
  useEffect(() => {
    if (!thinking) return undefined;
    const t = window.setInterval(() => {
      setNowMs(Date.now());
    }, 1_000);
    return () => {
      window.clearInterval(t);
    };
  }, [thinking]);

  // Settle-poll once the wizard is done: the workspace task row for the plan
  // dir appears on wsingest's next rescan. Scoped to rows that started at/after
  // this wizard run so a pre-existing plan is never matched.
  useEffect(() => {
    if (projectId === null || slug === '' || wstatus !== 'done' || plan !== null) return undefined;
    const runStartedMs = status?.startedAt != null ? new Date(status.startedAt).getTime() : 0;
    let disposed = false;
    const poll = (): void => {
      fetchTasks()
        .then((tasks) => {
          if (disposed) return;
          const mine = tasks
            .filter((t) => t.projectSlug === slug)
            .filter((t) => {
              const started = t.startedAt != null ? new Date(t.startedAt).getTime() : 0;
              return runStartedMs === 0 || started >= runStartedMs - 60_000;
            })
            .sort((a, b) => (b.startedAt ?? '').localeCompare(a.startedAt ?? ''));
          const newest = mine[0];
          if (newest !== undefined) setPlan(newest);
        })
        .catch(() => {
          /* tasks unavailable — retry next tick */
        });
    };
    poll();
    const t = window.setInterval(poll, PLAN_POLL_MS);
    return () => {
      disposed = true;
      window.clearInterval(t);
    };
  }, [projectId, slug, wstatus, plan, status?.startedAt]);

  // Live nudges: session_updated / task_updated → refresh the wizard DTO.
  const onMessage = useCallback(
    (msg: WSMessage): void => {
      if (msg.type === 'session_updated' || msg.type === 'task_updated') {
        loadStatus();
      }
    },
    [loadStatus],
  );
  useLiveUpdates(onMessage, loadStatus);

  /** Shared error sink: surface the message AND refetch — server state wins
   * (a 409 means the wizard moved under us; the forced load re-syncs). */
  const fail = useCallback(
    (e: unknown): void => {
      if (!aliveRef.current) return;
      setError(e instanceof Error ? e.message : String(e));
      loadStatus();
    },
    [loadStatus],
  );

  /** Optimistic status flip after a wizard action is accepted (202). */
  const optimistic = useCallback((next: PlanningStatus['status']): void => {
    setStatus((prev) => (prev === null ? prev : { ...prev, status: next }));
  }, []);

  const start = (): void => {
    if (projectId === null || idea.trim() === '') return;
    setBusy(true);
    setError(null);
    setPlan(null);
    startPlanning(projectId, idea.trim())
      .then(() => {
        if (!aliveRef.current) return;
        loadStatus();
        setStatus((prev) =>
          prev === null
            ? prev
            : { ...prev, active: true, status: 'generating', startedAt: new Date().toISOString() },
        );
      })
      .catch(fail)
      .finally(() => {
        if (aliveRef.current) setBusy(false);
      });
  };

  const cancel = (): void => {
    if (projectId === null) return;
    setBusy(true);
    cancelPlanning(projectId)
      .then(() => aliveRef.current && loadStatus())
      .catch(fail)
      .finally(() => aliveRef.current && setBusy(false));
  };

  const submitAnswer = (selected: string[], otherText?: string): void => {
    if (projectId === null || status?.currentQuestion == null) return;
    setBusy(true);
    setError(null);
    answerPlanning(projectId, {
      questionId: status.currentQuestion.id,
      selectedOptionIds: selected,
      ...(otherText !== undefined ? { otherText } : {}),
    })
      .then(() => aliveRef.current && optimistic('generating'))
      .catch(fail)
      .finally(() => aliveRef.current && setBusy(false));
  };

  // Raw fallback: the whole free-text reply goes through the SAME answer
  // endpoint (questionId "" + otherText) — it owns the status flip + resume.
  const submitRawAnswer = (): void => {
    if (projectId === null || rawAnswer.trim() === '') return;
    setBusy(true);
    setError(null);
    answerPlanning(projectId, { questionId: '', selectedOptionIds: [], otherText: rawAnswer.trim() })
      .then(() => {
        if (!aliveRef.current) return;
        setRawAnswer('');
        optimistic('generating');
      })
      .catch(fail)
      .finally(() => aliveRef.current && setBusy(false));
  };

  const submitRefine = (instructions: string): void => {
    if (projectId === null) return;
    setBusy(true);
    setError(null);
    refinePlanning(projectId, instructions)
      .then(() => {
        if (!aliveRef.current) return;
        setRefineOpen(false);
        optimistic('generating');
      })
      .catch(fail)
      .finally(() => aliveRef.current && setBusy(false));
  };

  const submitProceed = (): void => {
    if (projectId === null) return;
    setBusy(true);
    setError(null);
    proceedPlanning(projectId)
      .then(() => aliveRef.current && optimistic('proceeding'))
      .catch(fail)
      .finally(() => aliveRef.current && setBusy(false));
  };

  const projectLabel = useMemo(() => project?.name ?? project?.slug ?? slug, [project, slug]);
  const lastReasoning = history.length > 0 ? (history[history.length - 1]?.reasoning ?? '') : '';

  if (loading && status === null) {
    return (
      <div className="px-4 pt-6 pb-10 desk:px-8">
        <Loading label="planning…" />
      </div>
    );
  }
  if (projectId === null) {
    return (
      <div className="px-4 pt-6 pb-10 desk:px-8">
        <Empty>unknown project — pick one from the switcher</Empty>
      </div>
    );
  }

  // Idle, cancelled AND failed all land here — failed shows the error card
  // above an intake prefilled with the same idea.
  const showIntake = !wizardOpen && wstatus !== 'done';

  /** Shared run header: pulse dot, started-ago, session link, History, cancel. */
  const runHeader = (label: string, pulse: boolean): JSX.Element => (
    <div className="flex flex-wrap items-center gap-2.5">
      <span
        className={`inline-block h-[7px] w-[7px] shrink-0 rounded-full bg-brand ${pulse ? 'animate-pulse' : ''}`}
        aria-hidden="true"
      />
      <span className="text-[13px] font-semibold text-ink">{label}</span>
      {status?.startedAt != null && (
        <span className="font-mono text-[10.5px] text-ink-faint">started {fmtAgo(status.startedAt)}</span>
      )}
      {sessionUuid !== '' && (
        <Link
          to={`/sessions/${sessionUuid}`}
          className="font-mono text-[11px] text-ink-dim transition-colors hover:text-brand"
        >
          open session →
        </Link>
      )}
      <div className="ml-auto flex items-center gap-2">
        {history.length > 0 && (
          <button
            type="button"
            onClick={() => setHistoryOpen(true)}
            className="rounded-lg border border-line px-3 py-1 font-mono text-[11px] text-ink-dim transition-colors hover:bg-surface2 hover:text-ink"
          >
            History
          </button>
        )}
        <button
          type="button"
          disabled={busy}
          onClick={cancel}
          className="rounded-lg border border-red/40 px-3 py-1 font-mono text-[11px] text-red transition-colors hover:bg-red/10 disabled:opacity-50"
        >
          cancel
        </button>
      </div>
    </div>
  );

  return (
    <div className="min-w-0 px-4 pt-6 pb-10 desk:px-8 desk:pt-8 desk:pb-[60px]">
      <h1 className="font-display text-[26px] font-medium tracking-[-0.01em] desk:text-[30px]">
        Transform your idea into a plan
      </h1>
      <p className="mt-1.5 max-w-[70ch] text-[13px] text-ink-dim">
        Describe what you want to build for{' '}
        <span className="font-mono text-ink">{projectLabel}</span>. A planner session interviews you
        with structured questions, keeps a running plan, and writes the full plan when you tell it to
        proceed.
      </p>

      {error !== null && (
        <div className="mt-3">
          <ErrorBox message={error} onRetry={() => setError(null)} />
        </div>
      )}

      {/* FAILED — the run died; the intake below is prefilled to start again */}
      {wstatus === 'failed' && (
        <Card>
          <div className="flex flex-wrap items-center gap-2.5">
            <span className="inline-block h-[7px] w-[7px] shrink-0 rounded-full bg-red" aria-hidden="true" />
            <span className="text-[13px] font-semibold text-ink">Planning run failed</span>
            {sessionUuid !== '' && (
              <Link
                to={`/sessions/${sessionUuid}`}
                className="font-mono text-[11px] text-ink-dim transition-colors hover:text-brand"
              >
                open session →
              </Link>
            )}
          </div>
          <div className="mt-2 text-[12.5px] leading-relaxed text-ink-2">
            The planner run ended without a reply. Adjust the idea below (it is prefilled) and start
            again — a new run supersedes this one.
          </div>
        </Card>
      )}

      {/* IDLE / FAILED — idea intake */}
      {showIntake && (
        <div className="mt-5 max-w-[80ch]">
          <textarea
            value={idea}
            onChange={(e) => setIdea(e.target.value)}
            rows={5}
            placeholder="e.g. Add a bulk-export button to the reports page that streams a CSV…"
            aria-label="describe what you want to build"
            className="w-full resize-y rounded-xl border border-line bg-field px-3.5 py-3 text-[13.5px] leading-relaxed text-ink transition-colors outline-none placeholder:text-ink-faint focus:border-brand/50"
          />
          <button
            type="button"
            disabled={busy || idea.trim() === ''}
            onClick={start}
            className="mt-3 rounded-lg border border-brand/50 bg-brand/12 px-4 py-2 text-[13px] font-semibold text-brand transition-colors hover:bg-brand/20 disabled:opacity-50"
          >
            {busy ? 'starting…' : wstatus === 'failed' ? 'Start again' : 'Start planning'}
          </button>

          <ol className="mt-6 grid gap-3 border-t border-line pt-5 sm:grid-cols-2">
            {HOW_IT_WORKS.map((step, i) => (
              <li key={step.title} className="flex gap-2.5">
                <span
                  aria-hidden="true"
                  className="mt-[1px] inline-flex h-[18px] w-[18px] shrink-0 items-center justify-center rounded-full border border-line font-mono text-[10px] text-ink-dim"
                >
                  {i + 1}
                </span>
                <div className="min-w-0">
                  <div className="text-[12.5px] font-semibold text-ink">{step.title}</div>
                  <p className="mt-0.5 text-[12px] leading-relaxed text-ink-dim">{step.body}</p>
                </div>
              </li>
            ))}
          </ol>
        </div>
      )}

      {/* GENERATING / PROCEEDING — the planner is thinking */}
      {thinking && (
        <Card>
          {runHeader(wstatus === 'proceeding' ? 'Writing the plan' : 'Planner thinking', true)}
          <div className="mt-3 flex items-center gap-2 font-mono text-[11.5px] text-ink-dim">
            <span
              className="inline-block h-3 w-3 animate-spin rounded-full border border-line border-t-brand"
              aria-hidden="true"
            />
            {wstatus === 'proceeding'
              ? 'interview closed — writing the full plan into the workspace…'
              : 'reading the repo and preparing the next question…'}
            {status?.startedAt != null && <span>· {fmtElapsed(status.startedAt, nowMs)}</span>}
          </div>
          {lastReasoning !== '' && (
            <div className="mt-3">
              <div className="mb-1 font-mono text-[10.5px] tracking-[0.1em] text-ink-faint uppercase">
                latest reasoning
              </div>
              <pre className="max-h-40 overflow-y-auto rounded-lg border border-line bg-bg px-3 py-2.5 font-mono text-[10.5px] leading-relaxed whitespace-pre-wrap text-ink-2">
                {lastReasoning}
              </pre>
            </div>
          )}
        </Card>
      )}

      {/* AWAITING — the two-pane wizard (structured question) */}
      {wstatus === 'awaiting_answer' && status?.currentQuestion != null && (
        <>
          <Card>{runHeader('Planner is asking', false)}</Card>
          <div className="mt-3 grid items-start gap-3 desk:grid-cols-3">
            <div className="min-w-0 desk:col-span-2">
              <QuestionCard
                key={status.currentQuestion.id}
                question={status.currentQuestion}
                busy={busy}
                onSubmit={submitAnswer}
              />
            </div>
            <div className="min-w-0 desk:sticky desk:top-4">
              <RunningPlanPanel
                plan={status.runningPlan}
                status={wstatus}
                busy={busy}
                onRefine={() => setRefineOpen(true)}
                onProceed={submitProceed}
              />
            </div>
          </div>
        </>
      )}

      {/* AWAITING — raw-text fallback (reply failed the protocol parse) */}
      {wstatus === 'awaiting_answer' && status?.currentQuestion == null && (
        <Card>
          {runHeader('Planner replied', false)}
          <div className="mt-3">
            <div className="mb-1.5 font-mono text-[10.5px] tracking-[0.1em] text-ink-faint uppercase">
              planner
            </div>
            <div className="max-h-72 overflow-y-auto rounded-lg border border-line bg-bg px-3 py-2.5 text-[12.5px] leading-relaxed whitespace-pre-wrap text-ink-2">
              {status?.rawReply ?? '(no reply text)'}
            </div>
            <form
              className="mt-2 flex flex-wrap gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                submitRawAnswer();
              }}
            >
              <input
                type="text"
                value={rawAnswer}
                onChange={(e) => setRawAnswer(e.target.value)}
                placeholder="answer the planner…"
                aria-label="answer the planner"
                className="min-w-0 flex-1 basis-[240px] rounded-lg border border-line bg-field px-2.5 py-[7px] font-mono text-[11.5px] text-ink transition-colors outline-none placeholder:text-ink-faint focus:border-brand/50"
              />
              <button
                type="submit"
                disabled={busy || rawAnswer.trim() === ''}
                className="rounded-lg border border-brand/45 bg-brand/12 px-3.5 py-1.5 font-mono text-[11.5px] font-semibold text-brand transition-colors hover:bg-brand/20 disabled:opacity-50"
              >
                reply
              </button>
            </form>
          </div>
        </Card>
      )}

      {/* DONE — plan ready */}
      {wstatus === 'done' && (
        <Card>
          <div className="flex flex-wrap items-center gap-2.5">
            <span className="inline-block h-[7px] w-[7px] shrink-0 rounded-full bg-green" aria-hidden="true" />
            <span className="text-[13px] font-semibold text-ink">Plan ready</span>
            {plan !== null ? (
              <Link
                to={`/p/${slug}/plans`}
                className="font-mono text-[11px] text-ink-dim transition-colors hover:text-brand"
              >
                {plan.title}
              </Link>
            ) : (
              <Link
                to={`/p/${slug}/plans`}
                className="font-mono text-[11px] text-ink-dim transition-colors hover:text-brand"
              >
                open Plans →
              </Link>
            )}
            {history.length > 0 && (
              <button
                type="button"
                onClick={() => setHistoryOpen(true)}
                className="ml-auto rounded-lg border border-line px-3 py-1 font-mono text-[11px] text-ink-dim transition-colors hover:bg-surface2 hover:text-ink"
              >
                History
              </button>
            )}
          </div>
          <div className="mt-2 font-mono text-[11px] text-ink-dim">
            {status?.planDir != null && status.planDir !== '' ? (
              <>
                plan saved at <span className="text-ink">{status.planDir}</span>
              </>
            ) : (
              'the plan directory is being picked up by the workspace scan…'
            )}
            {plan !== null && (
              <>
                {' '}
                — tracked as <span className="text-ink">{plan.externalId}</span>
              </>
            )}
          </div>
          <div className="mt-2 text-[12.5px] leading-relaxed text-ink-2">
            Review it on the{' '}
            <Link to={`/p/${slug}/plans`} className="text-brand hover:underline">
              Plans page
            </Link>{' '}
            — phases run from there.
          </div>
        </Card>
      )}

      <HistoryDrawer turns={history} open={historyOpen} onClose={() => setHistoryOpen(false)} />
      <RefineModal
        open={refineOpen}
        busy={busy}
        onClose={() => setRefineOpen(false)}
        onApply={submitRefine}
      />
    </div>
  );
}
