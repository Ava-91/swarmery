// One provider card in the Usage modal: glyph + name + plan chip + status
// badge, an error block for a provider that failed, and its window rows.
//
// A provider whose fetch failed degrades to THIS card (no rows) rather than
// taking the whole modal down — that is why UsageProvider carries a per-provider
// `status`/`error` instead of one top-level error. A card that failed because
// the operator has not connected the provider yet renders the daemon's setup
// hint (UsageSetupHint) instead of a red error line.
//
// NOT PORTED from the reference implementation: provider drag-reorder (and its
// persisted order). With two providers — the live Claude card and the optional
// telemetry-estimate card — reordering is cost without benefit, so windows and
// providers both render in server order.

import type { UsageProvider } from '../../api/types';
import { UsageSetupHint } from './UsageSetupHint';
import { UsageWindowRow } from './UsageWindowRow';

/**
 * The badge distinguishes the three states the operator can act on differently:
 * red `error` = the provider is broken and there is nothing to do but retry;
 * amber `setup needed` = the operator can fix it right now (the hint says how);
 * neutral `disabled`/`not connected` = switched off, or off with no guidance.
 */
function StatusBadge({ p }: { p: UsageProvider }): JSX.Element | null {
  if (p.status === 'ok') return null;

  let label = 'not connected';
  let tone = 'bg-field text-ink-dim';
  if (p.status === 'error') {
    label = 'error';
    tone = 'bg-red/10 text-red';
  } else if (p.hint?.kind === 'opted-out') {
    label = 'disabled';
  } else if (p.hint !== undefined) {
    label = 'setup needed';
    tone = 'bg-amber/10 text-amber';
  }

  return (
    <span
      className={`shrink-0 rounded-full px-1.5 py-0.5 font-mono text-[9px] tracking-[0.12em] whitespace-nowrap uppercase ${tone}`}
    >
      {label}
    </span>
  );
}

export function UsageProviderCard({
  p,
  mode,
  hidden,
  onToggleWindow,
  onShowAllHidden,
  now,
}: {
  p: UsageProvider;
  mode: 'used' | 'remaining';
  /** Window keys the operator collapsed, across all providers. */
  hidden: string[];
  onToggleWindow: (key: string) => void;
  onShowAllHidden: () => void;
  now: number;
}): JSX.Element {
  const hiddenCount = p.windows.filter((w) => hidden.includes(w.key)).length;

  return (
    <div
      className="rounded-xl border border-line bg-surface2/40 px-2.5 py-2.5"
      data-provider={p.name}
      data-status={p.status}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="flex min-w-0 items-center gap-1.5">
          <span aria-hidden="true" className="shrink-0 font-mono text-[11px] text-brand">
            ◆
          </span>
          <span className="min-w-0 truncate font-mono text-[12px] font-semibold text-ink">
            {p.name}
          </span>
          {p.plan !== undefined && (
            <span className="shrink-0 rounded-full border border-line px-1.5 py-0.5 font-mono text-[9.5px] whitespace-nowrap text-ink-dim">
              {p.plan}
            </span>
          )}
        </span>
        <span className="flex shrink-0 items-center gap-1.5">
          {hiddenCount > 0 && (
            <button
              type="button"
              onClick={onShowAllHidden}
              className="rounded-[6px] border border-line px-1.5 py-0.5 font-mono text-[9.5px] whitespace-nowrap text-ink-dim transition-colors hover:bg-surface2 hover:text-ink focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand"
            >
              show hidden ({hiddenCount})
            </button>
          )}
          <StatusBadge p={p} />
        </span>
      </div>

      {/* A hint means "not connected yet", not "broken": it supersedes the raw
          error line, which is the same fact in a form the operator cannot act
          on. The red block is kept for genuine provider failures — a 429, a
          non-200, an unparseable payload. */}
      {p.hint !== undefined ? (
        <UsageSetupHint hint={p.hint} />
      ) : (
        p.error !== undefined && (
          <div className="mt-2 border-l-2 border-red bg-red/8 px-2 py-1.5 font-mono text-[10.5px] leading-snug break-words text-red">
            {p.error}
          </div>
        )
      )}

      {p.windows.length > 0 ? (
        <div className="mt-2 flex flex-col gap-2">
          {p.windows.map((w) => (
            <UsageWindowRow
              key={w.key}
              w={w}
              mode={mode}
              hidden={hidden.includes(w.key)}
              onToggleHidden={() => onToggleWindow(w.key)}
              now={now}
            />
          ))}
        </div>
      ) : (
        p.status === 'ok' && (
          <div className="mt-2 font-mono text-[10.5px] text-ink-dim">No usage data available</div>
        )
      )}
    </div>
  );
}
