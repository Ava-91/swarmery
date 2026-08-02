// New-task entry: a "+ New task" button at the top of the Triage column that
// opens a create modal. It replaces the inline quick-entry input the column used
// to carry — that input could only ever send {title, prompt=title}, so every
// task needed a second trip through the detail view to get its real prompt,
// priority, model or recipe. The modal sets all of them in one pass.
//
// Landing column is part of the form: Triage parks the task for a human pass,
// Todo makes it immediately dispatchable (POST already accepts boardColumn and
// pokes the dispatcher on a todo create).
//
// The Agent Hub "Run now" deep-link (?compose=@<agent>:) opens this modal on
// mount with the title seeded, so that flow stays one hop.

import { useEffect, useRef, useState } from 'react';
import type { BoardColumn, BoardTask, TaskPriority } from '../api/types';
import { createBoardTask } from '../api';
import { TASK_MODELS, TASK_PRIORITIES } from './boardModel';
import { PlaybookHint, PlaybookSelect, usePlaybooks } from './PlaybookPicker';

/** The two columns a human may create into; the rest are dispatcher-owned. */
const LANDING: { column: BoardColumn; label: string; hint: string }[] = [
  { column: 'triage', label: 'Triage', hint: 'park it for a pass over the queue' },
  { column: 'todo', label: 'Todo', hint: 'dispatchable as soon as a slot frees' },
];

const FIELD =
  'w-full rounded-lg border border-line bg-bg px-2.5 py-1.5 text-[12.5px] text-ink outline-none focus:border-line-strong';

function Label({ children }: { children: React.ReactNode }): JSX.Element {
  return (
    <div className="mb-1 font-mono text-[10.5px] tracking-[0.12em] text-ink-dim uppercase">
      {children}
    </div>
  );
}

export function NewTaskButton({
  projectId,
  onCreated,
  initialTitle = '',
}: {
  projectId: number;
  /** Called with the created row so the board can insert it optimistically. */
  onCreated: (task: BoardTask) => void;
  /** Seed value (Agent Hub "Run now" prefills `@<agent>: `); opens on mount when
   * non-empty. Read once — reopening the modal starts from a blank form. */
  initialTitle?: string;
}): JSX.Element {
  const [open, setOpen] = useState(initialTitle !== '');

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="w-full rounded-lg border border-dashed border-line bg-transparent px-2.5 py-2 text-left text-[12px] text-ink-dim transition-colors hover:border-line-strong hover:bg-surface2/50 hover:text-ink"
      >
        + New task
      </button>
      {open && (
        <NewTaskModal
          projectId={projectId}
          initialTitle={initialTitle}
          onCreated={(t) => {
            onCreated(t);
            setOpen(false);
          }}
          onClose={() => setOpen(false)}
        />
      )}
    </>
  );
}

function NewTaskModal({
  projectId,
  initialTitle,
  onCreated,
  onClose,
}: {
  projectId: number;
  initialTitle: string;
  onCreated: (task: BoardTask) => void;
  onClose: () => void;
}): JSX.Element {
  const [title, setTitle] = useState(initialTitle);
  const [prompt, setPrompt] = useState('');
  const [priority, setPriority] = useState<TaskPriority>('normal');
  const [model, setModel] = useState<string>('default');
  const [playbook, setPlaybook] = useState('');
  const [column, setColumn] = useState<BoardColumn>('triage');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const { playbooks } = usePlaybooks(projectId);
  const titleRef = useRef<HTMLInputElement>(null);

  // Focus the title with the caret at the end, so a seeded "@agent: " reads as
  // a prefix the user types after rather than as selected text they overwrite.
  useEffect(() => {
    const el = titleRef.current;
    if (el === null) return;
    el.focus();
    el.setSelectionRange(el.value.length, el.value.length);
  }, []);

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [onClose]);

  // The prompt is what the agent actually receives; leaving it blank keeps the
  // old quick-entry behaviour (prompt = title) instead of failing validation.
  const effectivePrompt = prompt.trim() !== '' ? prompt.trim() : title.trim();
  const canSubmit = title.trim() !== '' && !busy;

  const submit = (): void => {
    if (!canSubmit) return;
    setBusy(true);
    setError(null);
    createBoardTask({
      projectId,
      title: title.trim(),
      prompt: effectivePrompt,
      priority,
      boardColumn: column,
      ...(model !== 'default' ? { model } : {}),
      ...(playbook !== '' ? { playbook } : {}),
    })
      .then(onCreated)
      .catch((e: unknown) => {
        setError(e instanceof Error ? e.message : String(e));
        setBusy(false);
      });
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-bg/70 p-4"
      role="dialog"
      aria-modal="true"
      aria-label="New task"
      onClick={busy ? undefined : onClose}
    >
      <div
        className="max-h-full w-full max-w-md overflow-y-auto rounded-xl border border-line bg-surface px-4 py-4"
        onClick={(e) => e.stopPropagation()}
        // ⌘/Ctrl+Enter submits from anywhere in the form — the keyboard path the
        // one-line quick entry used to give for free.
        onKeyDown={(e) => {
          if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
            e.preventDefault();
            submit();
          }
        }}
      >
        <div className="font-display text-[14px] font-bold text-ink">New task</div>
        <div className="mt-1 text-[12px] leading-relaxed text-ink-dim">
          Queued for this project&apos;s board. The dispatcher picks it up from Todo.
        </div>

        <div className="mt-3.5">
          <Label>title</Label>
          <input
            ref={titleRef}
            value={title}
            disabled={busy}
            onChange={(e) => setTitle(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.metaKey && !e.ctrlKey) {
                e.preventDefault();
                submit();
              }
            }}
            placeholder="what should be done"
            aria-label="task title"
            className={FIELD}
          />
        </div>

        <div className="mt-3">
          <Label>prompt</Label>
          <textarea
            value={prompt}
            disabled={busy}
            onChange={(e) => setPrompt(e.target.value)}
            rows={4}
            placeholder="blank → the title is used as the prompt"
            aria-label="task prompt"
            className={`${FIELD} resize-y font-mono text-[11.5px] leading-relaxed`}
          />
        </div>

        <div className="mt-3 grid grid-cols-2 gap-3">
          <div>
            <Label>priority</Label>
            <select
              value={priority}
              disabled={busy}
              onChange={(e) => setPriority(e.target.value as TaskPriority)}
              aria-label="priority"
              className={`${FIELD} font-mono text-[11px]`}
            >
              {TASK_PRIORITIES.map((p) => (
                <option key={p} value={p}>
                  {p}
                </option>
              ))}
            </select>
          </div>
          <div>
            <Label>model</Label>
            <select
              value={model}
              disabled={busy}
              onChange={(e) => setModel(e.target.value)}
              aria-label="model"
              className={`${FIELD} font-mono text-[11px]`}
            >
              {TASK_MODELS.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
              ))}
            </select>
          </div>
        </div>

        <div className="mt-3">
          <Label>playbook</Label>
          <PlaybookSelect playbooks={playbooks} value={playbook} onChange={setPlaybook} disabled={busy} />
          <PlaybookHint playbooks={playbooks} value={playbook} />
        </div>

        <div className="mt-3">
          <Label>create into</Label>
          <div className="flex gap-1.5" role="group" aria-label="landing column">
            {LANDING.map(({ column: c, label, hint }) => (
              <button
                key={c}
                type="button"
                disabled={busy}
                onClick={() => setColumn(c)}
                aria-pressed={column === c}
                data-tip={hint}
                className={`rounded-lg border px-2.5 py-1 font-mono text-[11px] transition-colors disabled:opacity-50 ${
                  column === c
                    ? 'border-brand/50 bg-brand/10 text-brand'
                    : 'border-line text-ink-dim hover:bg-surface2'
                }`}
              >
                {label}
              </button>
            ))}
          </div>
          <div className="mt-1 font-mono text-[10.5px] text-ink-faint">
            {LANDING.find((l) => l.column === column)?.hint}
          </div>
        </div>

        {error !== null && (
          <div className="mt-3 rounded-lg border border-red/25 bg-red/5 px-2.5 py-2 font-mono text-[11px] text-red">
            {error}
          </div>
        )}

        <div className="mt-4 flex items-center justify-end gap-2">
          <span className="mr-auto font-mono text-[10px] text-ink-faint">⌘↵ to create</span>
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            className="rounded-lg border border-line bg-surface px-3.5 py-1.5 font-mono text-[11.5px] text-ink-2 transition-colors hover:bg-surface2 disabled:opacity-50"
          >
            cancel
          </button>
          <button
            type="button"
            onClick={submit}
            disabled={!canSubmit}
            className="rounded-lg border border-green/40 bg-green/10 px-3.5 py-1.5 font-mono text-[11.5px] font-semibold text-green transition-colors hover:bg-green/20 disabled:opacity-50"
          >
            {busy ? '…' : 'create'}
          </button>
        </div>
      </div>
    </div>
  );
}
