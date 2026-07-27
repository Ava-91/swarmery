// Refine modal (interactive planning v2 — phase 3): free-form
// course-correction instructions — the plan updates and the next questions
// follow the operator's direction. Same overlay pattern as ImproveModal /
// ConfirmDialog.

import { useState } from 'react';

export function RefineModal({
  open,
  busy,
  onClose,
  onApply,
}: {
  open: boolean;
  busy: boolean;
  onClose: () => void;
  onApply: (instructions: string) => void;
}): JSX.Element | null {
  const [text, setText] = useState('');
  if (!open) return null;

  const apply = (): void => {
    const trimmed = text.trim();
    if (trimmed === '' || busy) return;
    onApply(trimmed);
    setText('');
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-bg/70 p-4"
      role="dialog"
      aria-modal="true"
      aria-label="Уточнити план і наступні питання"
      onClick={busy ? undefined : onClose}
    >
      <div
        className="w-full max-w-lg rounded-xl border border-line bg-surface px-4 py-4"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="font-display text-[14px] font-bold text-ink">
          Уточнити план і наступні питання
        </div>
        <p className="mt-1 text-[12.5px] leading-relaxed text-ink-dim">
          Опишіть, що має змінитися. План оновиться, і наступні питання підуть за вашим напрямом.
        </p>

        <label
          className="mt-3 mb-1 block font-mono text-[10.5px] tracking-[0.1em] text-ink-faint uppercase"
          htmlFor="planning-refine-instructions"
        >
          refinement instructions
        </label>
        <textarea
          id="planning-refine-instructions"
          value={text}
          onChange={(e) => setText(e.target.value)}
          rows={4}
          disabled={busy}
          placeholder="Наприклад: додай поетапний rollout, покрий відновлення після збоїв і спитай про ризики міграції."
          className="w-full resize-y rounded-lg border border-line bg-field px-2.5 py-2 text-[12.5px] leading-relaxed text-ink transition-colors outline-none placeholder:text-ink-faint focus:border-brand/50 disabled:opacity-50"
        />

        <div className="mt-3.5 flex flex-wrap justify-end gap-2">
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            className="rounded-lg border border-line bg-surface px-3.5 py-1.5 font-mono text-[11.5px] text-ink-2 transition-colors hover:bg-surface2 disabled:opacity-50"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={apply}
            disabled={busy || text.trim() === ''}
            className="rounded-lg border border-green/45 bg-green/12 px-3.5 py-1.5 font-mono text-[11.5px] font-semibold text-green transition-colors hover:bg-green/20 disabled:opacity-50"
          >
            {busy ? '…' : 'Apply refinement'}
          </button>
        </div>
      </div>
    </div>
  );
}
