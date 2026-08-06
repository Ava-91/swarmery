// Create-account modal (multi-account, phase 7). Two stages: a form for the
// account key, then — on success — a copy-the-command stage. This modal NEVER
// runs `loginCommand` itself: the server only reserves a config dir, it does
// not (and cannot) drive the operator's own `claude` login flow. Pretending
// success here would be a lie about whether the account is actually usable.

import { useId, useState } from 'react';
import { createAccount } from '../api';
import { ErrorBox } from './ui';

type Stage =
  | { kind: 'form' }
  | { kind: 'saving' }
  | { kind: 'done'; loginCommand: string; hint: string | undefined };

const KEY_HINT = "letters, digits, '-' or '_' (it becomes a directory name)";

export function CreateAccountModal({
  open,
  onClose,
  onCreated,
}: {
  open: boolean;
  onClose: () => void;
  onCreated: () => void;
}): JSX.Element | null {
  const titleId = useId();
  const [key, setKey] = useState('');
  const [stage, setStage] = useState<Stage>({ kind: 'form' });
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  if (!open) return null;
  const busy = stage.kind === 'saving';

  function reset(): void {
    setKey('');
    setStage({ kind: 'form' });
    setError(null);
    setCopied(false);
  }

  function close(): void {
    if (busy) return;
    reset();
    onClose();
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLDivElement>): void {
    if (e.key === 'Escape' && !busy) close();
  }

  async function submit(): Promise<void> {
    if (key.trim() === '') return;
    setStage({ kind: 'saving' });
    setError(null);
    try {
      const resp = await createAccount(key.trim());
      setStage({ kind: 'done', loginCommand: resp.loginCommand, hint: resp.hint });
    } catch (e) {
      setStage({ kind: 'form' });
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  function copy(loginCommand: string): void {
    void navigator.clipboard.writeText(loginCommand);
    setCopied(true);
  }

  function done(): void {
    reset();
    onCreated();
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-bg/70 p-4"
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      onClick={busy ? undefined : close}
      onKeyDown={onKeyDown}
    >
      <div
        className="w-full max-w-md rounded-xl border border-line bg-surface px-4 py-4"
        onClick={(e) => e.stopPropagation()}
      >
        <div id={titleId} className="font-display text-[14px] font-bold text-ink">
          Add Claude account
        </div>

        {stage.kind !== 'done' ? (
          <form
            className="mt-3"
            onSubmit={(e) => {
              e.preventDefault();
              void submit();
            }}
          >
            <label
              htmlFor="account-key"
              className="block font-mono text-[10.5px] tracking-[0.12em] text-ink-dim uppercase"
            >
              account key
            </label>
            <input
              id="account-key"
              type="text"
              autoFocus
              value={key}
              disabled={busy}
              onChange={(e) => setKey(e.target.value)}
              aria-describedby="account-key-hint"
              className="mt-1 w-full rounded-lg border border-line bg-bg px-2.5 py-1.5 font-mono text-[12.5px] text-ink outline-none focus:border-line-strong"
            />
            <p id="account-key-hint" className="mt-1 text-[10.5px] text-ink-faint">
              {KEY_HINT}
            </p>

            {error !== null && <ErrorBox message={error} />}

            <div className="mt-4 flex justify-end gap-2">
              <button
                type="button"
                onClick={close}
                disabled={busy}
                className="rounded-lg border border-line bg-surface px-3.5 py-1.5 font-mono text-[11.5px] text-ink-2 transition-colors hover:bg-surface2 disabled:opacity-50"
              >
                cancel
              </button>
              <button
                type="submit"
                disabled={busy || key.trim() === ''}
                className="rounded-lg border border-green/40 bg-green/10 px-3.5 py-1.5 font-mono text-[11.5px] font-semibold text-green transition-colors hover:bg-green/20 disabled:opacity-50"
              >
                {busy ? '…' : 'create'}
              </button>
            </div>
          </form>
        ) : (
          <div className="mt-3">
            <p className="text-[12px] leading-relaxed text-ink-2">
              Account <span className="font-mono text-ink">{key.trim()}</span> reserved. Run this
              command yourself to log in — swarmery never runs it for you:
            </p>
            <div className="mt-2 flex items-center gap-2 rounded-lg border border-line bg-bg px-2.5 py-1.5">
              <code className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-ink">
                {stage.loginCommand}
              </code>
              <button
                type="button"
                onClick={() => copy(stage.loginCommand)}
                className="shrink-0 rounded border border-line px-1.5 py-0.5 font-mono text-[10px] text-ink-dim transition-colors hover:bg-surface2"
              >
                {copied ? 'copied' : 'copy'}
              </button>
            </div>
            {stage.hint !== undefined && stage.hint !== '' && (
              <p className="mt-2 font-mono text-[10.5px] text-amber">{stage.hint}</p>
            )}
            <div className="mt-4 flex justify-end">
              <button
                type="button"
                onClick={done}
                className="rounded-lg border border-line bg-surface px-3.5 py-1.5 font-mono text-[11.5px] text-ink-2 transition-colors hover:bg-surface2"
              >
                done
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
