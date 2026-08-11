// Task detail modal: the card's full editor, centred over the board. It used to
// be a right-side drawer (fusion phase 4); a modal puts the task in the middle
// of attention instead of squeezing it into a 420px rail next to the columns it
// is about — and it matches every other detail surface in the dashboard.
//
// Editable: title, prompt, priority, model, agent, playbook, file scope (chips),
// dependencies (chips of T-ids). Actions: Move to Todo, Pause/Resume
// (user_paused), Archive, Delete. Read-only (dispatcher-owned): branch,
// worktree path, dispatch error, verdict + detail, a link to the linked
// session's list, and — on a captured card — a link to the session it was
// minted from. Every mutation goes through the board's patchTask/deleteTask so
// the card + status bar stay in sync.
//
// Delete vs Archive: Archive parks the task (it stays on the board, in the
// dependency pickers, in the archive column); Delete removes it for good, for
// the task that simply stopped being relevant. It is confirmed, never
// optimistic, and the server refuses it for a running task.

import { useEffect, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import type { BoardTask, TaskPriority } from '../api/types';
import type { PatchBoardTaskInput } from '../api';
import { discardBoardTask, landBoardTask, rerunBoardTask, verifyBoardTask } from '../api';
import { fmtAgo } from '../lib/format';
import { displaySlug, findProject } from '../lib/projectSlug';
import { useScope } from '../lib/scope';
import { ConfirmDialog } from '../components/ui';
import { AgentHint, AgentSelect, useAgentRoster } from './AgentPicker';
import { TASK_MODELS, TASK_PRIORITIES } from './boardModel';
import { PlaybookHint, PlaybookSelect, usePlaybooks } from './PlaybookPicker';
import { useWorkspaceTerminal } from './ProjectWorkspaceLayout';
import { TaskDiff } from './TaskDiff';
import { ChipEditor, FieldLabel } from './TaskFields';

function ReadOnlyRow({ label, value }: { label: string; value: string }): JSX.Element {
  return (
    <div className="flex items-baseline gap-2 py-1 font-mono text-[10.5px]">
      <span className="w-[92px] shrink-0 tracking-[0.08em] text-ink-faint uppercase">{label}</span>
      <span className="min-w-0 flex-1 break-all text-ink-2">{value}</span>
    </div>
  );
}

export function TaskModal({
  task,
  onClose,
  onPatch,
  onDelete,
}: {
  task: BoardTask;
  onClose: () => void;
  /** Returns the patch promise so the modal can surface a save error. */
  onPatch: (patch: PatchBoardTaskInput) => Promise<BoardTask>;
  /** Permanent delete. Rejects with the server's message (e.g. the 409 on a
   * running task) so the confirm dialog can stay open and show it. */
  onDelete: () => Promise<void>;
}): JSX.Element {
  const [title, setTitle] = useState(task.title);
  const [prompt, setPrompt] = useState(task.prompt);
  const [priority, setPriority] = useState<TaskPriority>(task.priority);
  const [model, setModel] = useState<string>(task.model ?? 'default');
  const [playbook, setPlaybook] = useState<string>(task.playbook ?? '');
  const [agent, setAgent] = useState<string>(task.agent ?? '');
  const [fileScope, setFileScope] = useState<string[]>(task.fileScope);
  const [dependencies, setDependencies] = useState<string[]>(task.dependencies);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);
  // Review-loop state (board redesign phase 3). `reviewBusy` is one flag for all
  // four actions: they are mutually exclusive decisions about the same card, and
  // letting a user click Discard while Land is mid-push is not a state worth
  // supporting. `reviewNote` carries the last success line (a PR URL, "re-verify
  // started") since the card itself only updates once the WS frame arrives.
  const [feedback, setFeedback] = useState('');
  const [reviewBusy, setReviewBusy] = useState(false);
  const [reviewError, setReviewError] = useState<string | null>(null);
  const [reviewNote, setReviewNote] = useState<string | null>(null);
  const [confirmLand, setConfirmLand] = useState(false);
  const [confirmDiscard, setConfirmDiscard] = useState(false);
  const closeRef = useRef<HTMLButtonElement>(null);
  const { playbooks } = usePlaybooks(task.projectId);
  const { agents } = useAgentRoster(task.projectId, task.projectSlug);
  const openTerminal = useWorkspaceTerminal();
  const { projects } = useScope();
  // Linked-sessions scope link: the task row carries the DB path slug — use
  // the pretty slug when the project resolves.
  const scopeProject = findProject(projects, task.projectSlug);
  const scopeSlug =
    scopeProject !== null ? displaySlug(scopeProject, projects) : task.projectSlug;

  // Re-seed local edit state when a DIFFERENT task is opened into the modal.
  // Keyed on task.id rather than task: applyBoardTaskMessage (lib/ws.ts)
  // replaces the matched row wholesale, so every task_updated frame mints a new
  // object for the same task — and on [task] this effect would overwrite what
  // the user is typing with the server's copy, mid-keystroke.
  useEffect(() => {
    setTitle(task.title);
    setPrompt(task.prompt);
    setPriority(task.priority);
    setModel(task.model ?? 'default');
    setPlaybook(task.playbook ?? '');
    setAgent(task.agent ?? '');
    setFileScope(task.fileScope);
    setDependencies(task.dependencies);
    setSaveError(null);
    // Review state is per-card too: another card's feedback draft, error or PR
    // link must not survive into this one.
    setFeedback('');
    setReviewError(null);
    setReviewNote(null);
    setConfirmLand(false);
    setConfirmDiscard(false);
  }, [task.id]);

  // Initial focus is MOUNT-ONLY, deliberately split from the Escape listener
  // below. The two used to share one effect keyed on [onClose], and Board.tsx
  // passes an inline arrow for onClose — so every task_updated frame from the
  // board's WS subscription (useBoard.ts) minted a new identity, re-ran the
  // effect and yanked focus back to the close button. Mid-swarm that fought
  // whatever the user was doing: typing in the title or prompt field, or
  // reading the verify-knob explainer, which closes on focusout. Fixed here
  // rather than by memoising one call site, so it holds for every caller.
  // Same split HistoryDrawer.tsx:49-67 uses.
  //
  // Keyed on task.id, not []: Board.tsx mounts this conditionally so every open
  // is a fresh mount today, but if the modal ever stays mounted across a task
  // swap, focus should follow the new task rather than stay where it was.
  useEffect(() => {
    closeRef.current?.focus();
  }, [task.id]);

  // Escape closes the modal — but not while a confirmation is up, or one key
  // would dismiss both layers and the user would lose the dialog they are
  // reading (each dialog owns its own cancel). All three destructive/irreversible
  // confirms count, not just Delete.
  const confirming = confirmDelete || confirmLand || confirmDiscard;
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent): void => {
      if (e.key === 'Escape' && !confirming) onClose();
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [onClose, confirming]);

  const dirty = useMemo(
    () =>
      title.trim() !== task.title ||
      prompt.trim() !== task.prompt ||
      priority !== task.priority ||
      (model === 'default' ? task.model !== null : model !== task.model) ||
      playbook !== (task.playbook ?? '') ||
      agent !== (task.agent ?? '') ||
      JSON.stringify(fileScope) !== JSON.stringify(task.fileScope) ||
      JSON.stringify(dependencies) !== JSON.stringify(task.dependencies),
    [title, prompt, priority, model, playbook, agent, fileScope, dependencies, task],
  );

  const run = (patch: PatchBoardTaskInput): void => {
    setBusy(true);
    setSaveError(null);
    onPatch(patch)
      .catch((e: unknown) => setSaveError(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(false));
  };

  const save = (): void => {
    if (title.trim() === '' || prompt.trim() === '') {
      setSaveError('title and prompt cannot be empty');
      return;
    }
    run({
      title: title.trim(),
      prompt: prompt.trim(),
      priority,
      model: model === 'default' ? null : model,
      playbook, // "" clears back to the default recipe
      agent, // "" clears back to a plain run
      fileScope,
      dependencies,
    });
  };

  const remove = (): void => {
    setDeleting(true);
    setDeleteError(null);
    onDelete()
      // The row is gone — close the whole modal, not just the dialog.
      .then(onClose)
      .catch((e: unknown) => {
        setDeleteError(e instanceof Error ? e.message : String(e));
        setDeleting(false);
      });
  };

  const blocked = task.paused || task.userPaused;

  // The review loop is offered for a card that has actually been through a run:
  // in_review is its home, and done keeps it reachable so a shipped-but-wrong
  // card can still be re-run or its branch discarded.
  const inReview = task.boardColumn === 'in_review' || task.boardColumn === 'done';

  /**
   * Runs one review action, funnelling every server 4xx into `reviewError`. The
   * server's messages are written to be read (a 422 from Land carries the exact
   * commands to finish by hand), so they are shown verbatim rather than
   * re-worded here. The board's WS subscription refreshes the card itself.
   */
  const runReview = (action: () => Promise<string | null>): void => {
    setReviewBusy(true);
    setReviewError(null);
    setReviewNote(null);
    action()
      .then((note) => {
        setReviewNote(note);
        setConfirmLand(false);
        setConfirmDiscard(false);
      })
      .catch((e: unknown) => setReviewError(e instanceof Error ? e.message : String(e)))
      .finally(() => setReviewBusy(false));
  };

  const reverify = (): void => {
    runReview(async () => {
      await verifyBoardTask(task.id);
      // 202: the verdict lands later, on a task_updated frame.
      return 're-verification started — the verdict will appear here when it finishes';
    });
  };

  const rerun = (): void => {
    const text = feedback.trim();
    if (text === '') {
      setReviewError('feedback is required — a re-run with no notes would repeat the same work');
      return;
    }
    runReview(async () => {
      await rerunBoardTask(task.id, text);
      setFeedback('');
      return 'sent back to todo with your feedback appended to the prompt';
    });
  };

  const land = (): void => {
    runReview(async () => {
      const res = await landBoardTask(task.id);
      return `pull request opened: ${res.prUrl}`;
    });
  };

  const discard = (): void => {
    runReview(async () => {
      const res = await discardBoardTask(task.id);
      return res.deleted
        ? `branch ${res.branch} deleted — card archived`
        : `branch ${res.branch} was already gone — card archived`;
    });
  };

  return (
    <div
      className="fixed inset-0 z-40 flex items-center justify-center bg-bg/70 p-4"
      role="dialog"
      aria-modal="true"
      aria-label="task detail"
      onClick={onClose}
    >
      <div
        className="flex max-h-full w-full max-w-3xl flex-col overflow-hidden rounded-xl border border-line bg-bg shadow-[0_0_40px_rgba(0,0,0,0.5)]"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 border-b border-line px-4 py-3">
          <span className="font-mono text-[10.5px] text-ink-faint">{task.externalId}</span>
          {task.verifyVerdict !== null && (
            <span className="font-mono text-[10px] text-ink-dim uppercase">· {task.verifyVerdict}</span>
          )}
          <button
            ref={closeRef}
            type="button"
            onClick={onClose}
            aria-label="close"
            className="ml-auto text-[15px] leading-none text-ink-dim transition-colors hover:text-ink"
          >
            ×
          </button>
        </div>

        <div className="flex flex-col gap-4 overflow-y-auto px-4 py-4">
          <div>
            <FieldLabel>title</FieldLabel>
            <input
              type="text"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              aria-label="title"
              className="w-full rounded-[8px] border border-line bg-field px-2.5 py-1.5 text-[13px] text-ink outline-none focus:border-ink-dim"
            />
          </div>

          <div>
            <FieldLabel>prompt</FieldLabel>
            <textarea
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              rows={14}
              aria-label="prompt"
              className="w-full resize-y rounded-[8px] border border-line bg-field px-2.5 py-1.5 font-mono text-[11.5px] leading-relaxed text-ink outline-none focus:border-ink-dim"
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <FieldLabel>priority</FieldLabel>
              <select
                value={priority}
                onChange={(e) => setPriority(e.target.value as TaskPriority)}
                aria-label="priority"
                className="w-full rounded-[8px] border border-line bg-field px-2 py-1.5 font-mono text-[11px] text-ink outline-none focus:border-ink-dim"
              >
                {TASK_PRIORITIES.map((p) => (
                  <option key={p} value={p}>
                    {p}
                  </option>
                ))}
              </select>
            </div>
            <div>
              <FieldLabel>model</FieldLabel>
              <select
                value={model}
                onChange={(e) => setModel(e.target.value)}
                aria-label="model"
                className="w-full rounded-[8px] border border-line bg-field px-2 py-1.5 font-mono text-[11px] text-ink outline-none focus:border-ink-dim"
              >
                {TASK_MODELS.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </select>
            </div>
          </div>

          <div>
            <FieldLabel>agent</FieldLabel>
            <AgentSelect agents={agents} value={agent} onChange={setAgent} />
            <AgentHint agents={agents} value={agent} />
          </div>

          <div>
            <FieldLabel>playbook</FieldLabel>
            <PlaybookSelect playbooks={playbooks} value={playbook} onChange={setPlaybook} />
            <PlaybookHint playbooks={playbooks} value={playbook} />
          </div>

          <ChipEditor
            label="file scope"
            values={fileScope}
            placeholder="add a path glob + Enter"
            onChange={setFileScope}
          />
          <ChipEditor
            label="dependencies"
            values={dependencies}
            placeholder="add a T-id + Enter"
            onChange={setDependencies}
          />

          {saveError !== null && <div className="font-mono text-[10.5px] text-red">{saveError}</div>}

          <div className="flex flex-wrap gap-2">
            <button
              type="button"
              disabled={!dirty || busy}
              onClick={save}
              className="rounded-lg border border-brand/50 bg-brand/10 px-3 py-1.5 text-[12px] font-semibold text-brand transition-colors hover:bg-brand/20 disabled:cursor-not-allowed disabled:opacity-40"
            >
              Save
            </button>
            {task.boardColumn !== 'todo' && task.boardColumn !== 'done' && (
              <button
                type="button"
                disabled={busy}
                onClick={() => run({ boardColumn: 'todo' })}
                className="rounded-lg border border-line bg-surface px-3 py-1.5 text-[12px] text-ink-2 transition-colors hover:bg-surface2 disabled:opacity-40"
              >
                Move to Todo
              </button>
            )}
            <button
              type="button"
              disabled={busy}
              onClick={() => run({ userPaused: !task.userPaused })}
              className="rounded-lg border border-line bg-surface px-3 py-1.5 text-[12px] text-ink-2 transition-colors hover:bg-surface2 disabled:opacity-40"
            >
              {task.userPaused ? 'Resume' : 'Pause'}
            </button>
            {task.boardColumn !== 'archived' && (
              <button
                type="button"
                disabled={busy}
                onClick={() => run({ boardColumn: 'archived' })}
                className="rounded-lg border border-line bg-surface px-3 py-1.5 text-[12px] text-ink-dim transition-colors hover:bg-surface2 disabled:opacity-40"
              >
                Archive
              </button>
            )}
            {/* Sits apart from the reversible actions: this one has no undo. */}
            <button
              type="button"
              disabled={busy || deleting}
              onClick={() => {
                setDeleteError(null);
                setConfirmDelete(true);
              }}
              className="ml-auto rounded-lg border border-red/40 bg-red/5 px-3 py-1.5 text-[12px] text-red transition-colors hover:bg-red/15 disabled:opacity-40"
            >
              Delete
            </button>
          </div>

          <ConfirmDialog
            open={confirmDelete}
            title={`Delete ${task.externalId}?`}
            confirmLabel="delete"
            danger
            busy={deleting}
            onConfirm={remove}
            onCancel={() => {
              setConfirmDelete(false);
              setDeleteError(null);
            }}
          >
            <span className="font-mono text-[12px] text-ink">{task.title}</span> is removed
            permanently — this cannot be undone. To keep it out of the way without losing it, use{' '}
            <span className="font-mono">Archive</span> instead.
            {deleteError !== null && (
              <div className="mt-2.5 rounded-lg border border-red/25 bg-red/5 px-2.5 py-2 font-mono text-[11px] text-red">
                {deleteError}
              </div>
            )}
          </ConfirmDialog>

          {/* The review loop (board redesign phase 3). Shown only for a card that
            * has been through a run: the verdict it was given, the diff it
            * produced, and the three exits that make `done` a consequence of a
            * decision rather than a drag. */}
          {inReview && (
            <div className="mt-1 flex flex-col gap-2 border-t border-line pt-3">
              <FieldLabel>review</FieldLabel>

              {task.verifyVerdict !== null && (
                <div>
                  <ReadOnlyRow label="verdict" value={task.verifyVerdict} />
                  {task.verifyDetail !== null && (
                    <ReadOnlyRow label="detail" value={task.verifyDetail} />
                  )}
                </div>
              )}

              {/* A card whose worktree was reclaimed cannot be re-graded — the
                * verifier has nothing to run against. Disabled WITH the reason,
                * rather than hidden: the button's absence would read as a bug. */}
              <div className="flex flex-wrap items-center gap-2">
                <button
                  type="button"
                  disabled={reviewBusy || task.worktreePath === null}
                  onClick={reverify}
                  title={
                    task.worktreePath === null
                      ? 'the worktree was reclaimed, so there is nothing left to grade — re-run the card instead'
                      : 'run verification again against the worktree'
                  }
                  className="rounded-lg border border-line bg-surface px-3 py-1.5 text-[12px] text-ink-2 transition-colors hover:bg-surface2 disabled:cursor-not-allowed disabled:opacity-40"
                >
                  Re-verify
                </button>
                {task.worktreePath === null && (
                  <span className="font-mono text-[10px] text-ink-faint">
                    worktree reclaimed — nothing to re-grade
                  </span>
                )}
              </div>

              <TaskDiff taskId={task.id} />

              {/* Re-run needs its notes before it can do anything, so the textarea
                * sits with the button rather than behind a dialog. */}
              <div>
                <FieldLabel>reviewer feedback</FieldLabel>
                <textarea
                  value={feedback}
                  onChange={(e) => setFeedback(e.target.value)}
                  rows={3}
                  aria-label="reviewer feedback"
                  placeholder="what to fix on the next pass — appended to the prompt"
                  className="w-full resize-y rounded-[8px] border border-line bg-field px-2.5 py-1.5 font-mono text-[11.5px] leading-relaxed text-ink outline-none focus:border-ink-dim"
                />
              </div>

              {reviewError !== null && (
                <div className="rounded-lg border border-red/25 bg-red/5 px-2.5 py-2 font-mono text-[11px] whitespace-pre-wrap text-red">
                  {reviewError}
                </div>
              )}
              {reviewNote !== null && (
                <div className="rounded-lg border border-green/25 bg-green/5 px-2.5 py-2 font-mono text-[11px] break-all text-green">
                  {reviewNote}
                </div>
              )}

              <div className="flex flex-wrap gap-2">
                <button
                  type="button"
                  disabled={reviewBusy || task.branch === null}
                  onClick={() => setConfirmLand(true)}
                  title={
                    task.branch === null
                      ? 'this card has no run branch — there is nothing to push'
                      : 'push the branch and open a pull request'
                  }
                  className="rounded-lg border border-brand/50 bg-brand/10 px-3 py-1.5 text-[12px] font-semibold text-brand transition-colors hover:bg-brand/20 disabled:cursor-not-allowed disabled:opacity-40"
                >
                  Land
                </button>
                <button
                  type="button"
                  disabled={reviewBusy || feedback.trim() === ''}
                  onClick={rerun}
                  title={
                    feedback.trim() === ''
                      ? 'write the feedback first — a re-run with no notes repeats the same work'
                      : 'append the feedback to the prompt and send the card back to todo'
                  }
                  className="rounded-lg border border-line bg-surface px-3 py-1.5 text-[12px] text-ink-2 transition-colors hover:bg-surface2 disabled:cursor-not-allowed disabled:opacity-40"
                >
                  Re-run with feedback
                </button>
                <button
                  type="button"
                  disabled={reviewBusy}
                  onClick={() => setConfirmDiscard(true)}
                  className="ml-auto rounded-lg border border-red/40 bg-red/5 px-3 py-1.5 text-[12px] text-red transition-colors hover:bg-red/15 disabled:opacity-40"
                >
                  Discard
                </button>
              </div>

              {/* The landed PR. `result_note` also carries a dispatcher sentinel
                * line on a no-op exit, so it is only linked when it IS a URL. */}
              {task.resultNote !== null && task.resultNote !== '' && (
                <div className="font-mono text-[10.5px] break-all text-ink-dim">
                  {task.resultNote.startsWith('http') ? (
                    <a
                      href={task.resultNote}
                      target="_blank"
                      rel="noreferrer"
                      className="underline transition-colors hover:text-ink"
                    >
                      ❯ {task.resultNote}
                    </a>
                  ) : (
                    task.resultNote
                  )}
                </div>
              )}

              <ConfirmDialog
                open={confirmLand}
                title={`Land ${task.externalId}?`}
                confirmLabel="land"
                busy={reviewBusy}
                onConfirm={land}
                onCancel={() => setConfirmLand(false)}
              >
                Pushes <span className="font-mono text-[12px] text-ink">{task.branch ?? '—'}</span>{' '}
                to <span className="font-mono text-[12px] text-ink">origin</span> and opens a pull
                request with <span className="font-mono">gh</span>, then moves the card to done. The
                branch is kept.
                {reviewError !== null && (
                  <div className="mt-2.5 rounded-lg border border-red/25 bg-red/5 px-2.5 py-2 font-mono text-[11px] whitespace-pre-wrap text-red">
                    {reviewError}
                  </div>
                )}
              </ConfirmDialog>

              <ConfirmDialog
                open={confirmDiscard}
                title={`Discard ${task.externalId}?`}
                confirmLabel="discard"
                danger
                busy={reviewBusy}
                onConfirm={discard}
                onCancel={() => setConfirmDiscard(false)}
              >
                Deletes the branch{' '}
                <span className="font-mono text-[12px] text-ink">{task.branch ?? '—'}</span> and
                every commit on it, reclaims the worktree, and archives the card. The work is not
                recoverable.
                {reviewError !== null && (
                  <div className="mt-2.5 rounded-lg border border-red/25 bg-red/5 px-2.5 py-2 font-mono text-[11px] whitespace-pre-wrap text-red">
                    {reviewError}
                  </div>
                )}
              </ConfirmDialog>
            </div>
          )}

          {/* Read-only dispatcher-owned state. */}
          <div className="mt-1 border-t border-line pt-3">
            <FieldLabel>dispatcher</FieldLabel>
            <ReadOnlyRow label="status" value={task.status} />
            {blocked && <ReadOnlyRow label="paused" value={task.userPaused ? 'by user' : 'by system'} />}
            <ReadOnlyRow label="branch" value={task.branch ?? '—'} />
            <ReadOnlyRow label="worktree" value={task.worktreePath ?? '—'} />
            {openTerminal !== null && task.worktreePath !== null && (
              <button
                type="button"
                onClick={() => openTerminal(task.externalId, task.worktreePath as string)}
                className="mt-1 flex items-center gap-1.5 rounded-md border border-line bg-surface px-2.5 py-1 font-mono text-[10.5px] text-ink-2 transition-colors hover:border-line-strong hover:bg-surface2 hover:text-ink"
              >
                <span aria-hidden="true">❯_</span>
                Open terminal in worktree
              </button>
            )}
            {task.startPoint !== null && <ReadOnlyRow label="start point" value={task.startPoint} />}
            {/* Two budgets, two labels. A bare "retries: 3" could not say whether
             * the dispatcher healed a dead process three times or verification
             * spawned three fix cards — opposite situations needing opposite
             * responses. They are separate columns since 0051; name them. */}
            {task.retryCount > 0 && (
              <ReadOnlyRow label="dispatch retries" value={String(task.retryCount)} />
            )}
            {task.verifyRetryCount > 0 && (
              <ReadOnlyRow label="verify retries" value={String(task.verifyRetryCount)} />
            )}
            {task.dispatchError !== null && (
              <div className="mt-1 rounded-md border border-red/30 bg-red/5 px-2 py-1.5 font-mono text-[10.5px] text-red">
                {task.dispatchError}
              </div>
            )}
            {/* Not repeated when the Review section above is showing it — one
              * card should state its verdict once. */}
            {!inReview && task.verifyVerdict !== null && (
              <div className="mt-1.5">
                <ReadOnlyRow label="verdict" value={task.verifyVerdict} />
                {task.verifyDetail !== null && <ReadOnlyRow label="detail" value={task.verifyDetail} />}
              </div>
            )}
            {task.branch !== null && scopeSlug !== null && (
              <a
                href={`/sessions?scope=${scopeSlug}`}
                className="mt-1.5 inline-block font-mono text-[10.5px] text-ink-dim underline transition-colors hover:text-ink"
              >
                ❯ linked sessions →
              </a>
            )}
            {/* Capture provenance: the session this card was minted from. The
             * detail route takes a numeric session id as well as a uuid. */}
            {task.origin !== 'manual' && task.originSessionId !== null && (
              <Link
                to={`/sessions/${String(task.originSessionId)}`}
                className="mt-1.5 block font-mono text-[10.5px] text-ink-dim underline transition-colors hover:text-ink"
              >
                ❯ source session →
              </Link>
            )}
            <div className="mt-2 font-mono text-[10px] text-ink-faint">created {fmtAgo(task.createdAt)}</div>
          </div>
        </div>
      </div>
    </div>
  );
}
