// Plan-doc reader/editor modal (fusion phase 10; upgraded from a narrow side
// drawer to a large centered dialog per reading-comfort feedback): opens one
// plan markdown doc for a workspace epic and lets the user (a) read it in a
// comfortable measure with generous line-height, (b) toggle acceptance
// checkboxes directly in preview — which PATCHes the exact `- [ ]`↔`- [x]` line
// so the rollup follows on the next rescan, and (c) switch to a raw editor and
// Save (PUT, which writes a timestamped backup on the daemon side). Same
// versioned-backup write idiom as the System/Memory surfaces; the daemon
// confines every path to that task's plan/ dir.
//
// Shell follows the app's existing centered-dialog idiom (AttachModal /
// DetachModal / ConfirmDialog: `fixed inset-0 … bg-bg/70` + backdrop-click +
// stopPropagation panel) rather than the side-drawer idiom, just sized for
// prose (max-w-4xl, ~88vh, sticky header, scrollable body capped at ~75ch).
// Adds what none of those dialogs have yet: a focus trap, focus restoration
// to the trigger element on close, and a body-scroll lock — all needed
// because this is the first dialog whose body content is itself scrollable
// and long-form.

import { useCallback, useEffect, useRef, useState } from 'react';
import { fetchPlanDoc, savePlanDoc, togglePlanCheckbox } from '../api';
import { Markdown } from '../lib/markdown';
import { Loading } from '../components/ui';

/** Selector for elements that can hold keyboard focus, for the Tab-trap. */
const FOCUSABLE_SELECTOR =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';

type Mode = 'preview' | 'edit';

/** A checkbox found in the source: its 0-based line index + done state + label. */
interface CheckboxLine {
  line: number;
  done: boolean;
  label: string;
}

const CHECKBOX_RE = /^(\s*[-*]\s+)\[( |x|X)\]\s+(.*)$/;

/** Extract every acceptance checkbox with its source line index. */
function extractCheckboxes(content: string): CheckboxLine[] {
  const out: CheckboxLine[] = [];
  content.split('\n').forEach((raw, i) => {
    const m = CHECKBOX_RE.exec(raw);
    if (m) out.push({ line: i, done: (m[2] ?? '').toLowerCase() === 'x', label: m[3] ?? '' });
  });
  return out;
}

export function PlanDocDrawer({
  taskId,
  path,
  title,
  initialMode = 'preview',
  onClose,
  onChanged,
}: {
  taskId: number;
  path: string;
  title: string;
  /** Starting tab — 'preview' (default) or 'edit' to jump straight to raw mode. */
  initialMode?: Mode;
  onClose: () => void;
  onChanged: () => void;
}): JSX.Element {
  const [content, setContent] = useState<string | null>(null);
  const [draft, setDraft] = useState('');
  const [mode, setMode] = useState<Mode>(initialMode);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [busyLine, setBusyLine] = useState<number | null>(null);
  const [savedNote, setSavedNote] = useState<string | null>(null);
  const dialogRef = useRef<HTMLDivElement>(null);
  const closeBtnRef = useRef<HTMLButtonElement>(null);
  const previouslyFocused = useRef<HTMLElement | null>(null);

  const load = useCallback((): void => {
    setContent(null);
    setError(null);
    fetchPlanDoc(taskId, path)
      .then((doc) => {
        setContent(doc.content);
        setDraft(doc.content);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)));
  }, [taskId, path]);

  useEffect(() => {
    load();
  }, [load]);

  // On open: remember the trigger element (to restore focus on close), move
  // focus into the dialog, and lock page scroll so the backdrop doesn't
  // scroll behind a long doc. All undone on unmount.
  useEffect(() => {
    previouslyFocused.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    closeBtnRef.current?.focus();
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = prevOverflow;
      previouslyFocused.current?.focus();
    };
  }, []);

  // Escape closes; Tab/Shift+Tab is trapped inside the dialog (WCAG 2.2 AA).
  useEffect(() => {
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
        return;
      }
      if (e.key !== 'Tab') return;
      const root = dialogRef.current;
      if (root === null) return;
      const focusable = Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)).filter(
        (el) => el.offsetParent !== null,
      );
      if (focusable.length === 0) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (first === undefined || last === undefined) return;
      const activeInRoot = root.contains(document.activeElement);
      if (e.shiftKey) {
        if (!activeInRoot || document.activeElement === first) {
          e.preventDefault();
          last.focus();
        }
      } else {
        if (!activeInRoot || document.activeElement === last) {
          e.preventDefault();
          first.focus();
        }
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const save = (): void => {
    setSaving(true);
    setError(null);
    savePlanDoc(taskId, path, draft)
      .then((doc) => {
        setContent(doc.content);
        setDraft(doc.content);
        setMode('preview');
        setSavedNote(doc.backup !== undefined ? 'saved · backup written' : 'saved');
        onChanged();
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setSaving(false));
  };

  const toggle = (cb: CheckboxLine): void => {
    setBusyLine(cb.line);
    setError(null);
    togglePlanCheckbox(taskId, path, cb.line, !cb.done)
      .then((doc) => {
        setContent(doc.content);
        setDraft(doc.content);
        onChanged();
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusyLine(null));
  };

  const checkboxes = content !== null ? extractCheckboxes(content) : [];
  const dirty = draft !== content;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-bg/70 p-4"
      onClick={onClose}
      role="presentation"
    >
      <div
        ref={dialogRef}
        className="flex h-[88vh] w-full max-w-4xl flex-col overflow-hidden rounded-xl border border-line bg-surface shadow-2xl"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-labelledby="plan-doc-title"
      >
        {/* Sticky header. */}
        <div className="flex shrink-0 items-center justify-between gap-3 border-b border-line px-5 py-3.5">
          <div className="min-w-0">
            <div id="plan-doc-title" className="truncate text-[14px] font-semibold text-ink">
              {title}
            </div>
            <div className="truncate font-mono text-[10px] text-ink-faint">{path}</div>
          </div>
          <div className="flex shrink-0 items-center gap-1">
            {(['preview', 'edit'] as const).map((m) => (
              <button
                key={m}
                type="button"
                onClick={() => setMode(m)}
                aria-pressed={mode === m}
                className={`rounded-md border px-2.5 py-1.5 font-mono text-[10.5px] capitalize transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand ${
                  mode === m ? 'border-line-strong bg-surface2 text-brand' : 'border-transparent text-ink-dim hover:text-ink'
                }`}
              >
                {m}
              </button>
            ))}
            <button
              ref={closeBtnRef}
              type="button"
              onClick={onClose}
              aria-label="close"
              className="ml-1 flex h-[30px] w-[30px] items-center justify-center rounded-md text-ink-dim transition-colors hover:bg-surface2 hover:text-ink focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand"
            >
              ×
            </button>
          </div>
        </div>

        {error !== null && (
          <div
            role="alert"
            className="shrink-0 border-b border-red/30 bg-red/10 px-5 py-1.5 font-mono text-[11px] text-red"
          >
            {error}
          </div>
        )}
        {savedNote !== null && mode === 'preview' && (
          <div className="shrink-0 border-b border-green/30 bg-green/10 px-5 py-1.5 font-mono text-[11px] text-green">
            {savedNote}
          </div>
        )}

        {/* Scrollable body — prose constrained to a comfortable measure. */}
        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
          {content === null && error === null ? (
            <Loading label="doc…" />
          ) : mode === 'edit' ? (
            <textarea
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              spellCheck={false}
              className="h-full min-h-[400px] w-full resize-none rounded-lg border border-line bg-field px-3.5 py-3 font-mono text-[12.5px] leading-relaxed text-ink outline-none focus:border-ink-dim"
              aria-label="plan doc source"
            />
          ) : (
            <div className="mx-auto max-w-[75ch] space-y-4">
              {/* Interactive acceptance checkboxes (toggling PATCHes the line). */}
              {checkboxes.length > 0 && (
                <div className="rounded-lg border border-line bg-surface/40 p-3.5">
                  <div className="mb-2 font-mono text-[10px] tracking-[0.1em] text-ink-faint uppercase">
                    Acceptance ({checkboxes.filter((c) => c.done).length}/{checkboxes.length})
                  </div>
                  <ul className="space-y-1">
                    {checkboxes.map((cb) => (
                      <li key={cb.line}>
                        <button
                          type="button"
                          disabled={busyLine === cb.line}
                          onClick={() => toggle(cb)}
                          className="flex min-h-[30px] w-full items-start gap-2 rounded px-1 py-1 text-left text-[13px] text-ink transition-colors hover:bg-surface2/50 disabled:opacity-50 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand"
                        >
                          <span
                            aria-hidden="true"
                            className={`mt-px inline-flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded border text-[9px] ${
                              cb.done ? 'border-green bg-green/20 text-green' : 'border-line-strong text-transparent'
                            }`}
                          >
                            ✓
                          </span>
                          <span className={cb.done ? 'text-ink-dim line-through' : ''}>{cb.label}</span>
                        </button>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              {/* Rendered markdown (read-only) — generous line-height for long-form reading. */}
              <div className="text-[14px] leading-[1.75] text-ink-2">
                {content !== null && <Markdown text={content} />}
              </div>
            </div>
          )}
        </div>

        {/* Footer (edit mode only). */}
        {mode === 'edit' && (
          <div className="flex shrink-0 items-center justify-end gap-2 border-t border-line px-5 py-3">
            <button
              type="button"
              onClick={() => {
                setDraft(content ?? '');
                setMode('preview');
              }}
              className="rounded-md border border-line px-3 py-1.5 font-mono text-[11px] text-ink-dim transition-colors hover:text-ink focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand"
            >
              cancel
            </button>
            <button
              type="button"
              onClick={save}
              disabled={saving || !dirty}
              className="rounded-md border border-line-strong bg-surface2 px-3 py-1.5 font-mono text-[11px] text-brand transition-colors hover:bg-surface2/70 disabled:cursor-not-allowed disabled:text-ink-faint focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand"
            >
              {saving ? 'saving…' : 'Save'}
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
