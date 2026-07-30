// One provider card in the Usage modal: glyph + name + plan chip + status
// badge, an error block for a provider that failed, and its window rows.
//
// A provider whose fetch failed degrades to THIS card (red block, no rows)
// rather than taking the whole modal down — that is why UsageProvider carries a
// per-provider `status`/`error` instead of one top-level error.
//
// NOT PORTED from the reference implementation: provider drag-reorder (and its
// persisted order). With two providers — the live Claude card and the optional
// telemetry-estimate card — reordering is cost without benefit, so windows and
// providers both render in server order.

import type { UsageProvider } from '../../api/types';
import { UsageWindowRow } from './UsageWindowRow';

function StatusBadge({ status }: { status: UsageProvider['status'] }): JSX.Element | null {
  if (status === 'ok') return null;
  const error = status === 'error';
  return (
    <span
      className={`shrink-0 rounded-full px-1.5 py-0.5 font-mono text-[9px] tracking-[0.12em] whitespace-nowrap uppercase ${
        error ? 'bg-red/10 text-red' : 'bg-field text-ink-dim'
      }`}
    >
      {error ? 'error' : 'not connected'}
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
          <StatusBadge status={p.status} />
        </span>
      </div>

      {p.error !== undefined && (
        <div className="mt-2 border-l-2 border-red bg-red/8 px-2 py-1.5 font-mono text-[10.5px] leading-snug break-words text-red">
          {p.error}
        </div>
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
