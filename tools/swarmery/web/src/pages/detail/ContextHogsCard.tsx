// Detail-rail "Context hogs" section: per-tool attribution of a session's
// context growth, parsed on demand from the transcript (no schema, no ingest).
// Collapsed by default; expanding fetches GET /api/sessions/{id}/context-hogs.
// Token figures are ESTIMATES (~4 bytes/token from tool-result sizes) — the
// caveat is rendered, not implied.

import { useCallback, useMemo, useState } from 'react';
import { fetchSessionContextHogs } from '../../api';
import type { ContextHogsReport } from '../../api/types';

const TOP_COLLAPSED = 10;

function fmtTokens(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(n >= 10000 ? 0 : 1)}k`;
  return String(n);
}

export function ContextHogsCard({ sessionId }: { sessionId: number }): JSX.Element {
  const [open, setOpen] = useState(false);
  const [showAll, setShowAll] = useState(false);
  const [data, setData] = useState<ContextHogsReport | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const toggle = useCallback(() => {
    const next = !open;
    setOpen(next);
    if (next && data === null && !loading) {
      setLoading(true);
      setError(null);
      fetchSessionContextHogs(sessionId)
        .then((d) => setData(d))
        .catch((e: unknown) =>
          setError(e instanceof Error ? e.message : 'failed to analyze transcript'),
        )
        .finally(() => setLoading(false));
    }
  }, [open, data, loading, sessionId]);

  const rows = useMemo(() => {
    if (data === null) return [];
    return showAll ? data.tools : data.tools.slice(0, TOP_COLLAPSED);
  }, [data, showAll]);

  const maxWrite = useMemo(
    () => (data === null ? 0 : Math.max(1, ...data.turns.map((t) => t.cacheWrite))),
    [data],
  );

  return (
    <div className="rounded-xl border border-edge bg-surface px-4 py-3.5">
      <button
        type="button"
        onClick={toggle}
        className="flex w-full items-baseline justify-between text-left"
      >
        <span className="font-mono text-[10.5px] tracking-[0.08em] text-amber/70 uppercase">
          context hogs
        </span>
        <span className="font-mono text-[11px] text-ink-dim">{open ? 'hide' : 'show'}</span>
      </button>

      {open && (
        <div className="mt-3">
          {loading && <p className="font-mono text-[11px] text-ink-dim">analyzing transcript…</p>}
          {error !== null && <p className="font-mono text-[11px] text-red">{error}</p>}

          {data !== null && (
            <>
              <table className="w-full border-collapse">
                <thead>
                  <tr className="text-left font-mono text-[10px] text-ink-dim uppercase">
                    <th className="pb-1 font-normal">tool</th>
                    <th className="pb-1 text-right font-normal">calls</th>
                    <th className="pb-1 text-right font-normal">~tokens</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((t) => (
                    <tr key={t.name} className="font-mono text-[11px] text-ink-2">
                      <td className="max-w-[160px] truncate py-0.5 pr-2" title={t.name}>
                        {t.name}
                      </td>
                      <td className="py-0.5 text-right tabular-nums">{t.calls}</td>
                      <td className="py-0.5 text-right tabular-nums">{fmtTokens(t.estTokens)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>

              {data.tools.length > TOP_COLLAPSED && (
                <button
                  type="button"
                  onClick={() => setShowAll((v) => !v)}
                  className="mt-1.5 font-mono text-[10.5px] text-ink-dim hover:text-ink-2"
                >
                  {showAll ? 'show top 10' : `show all ${data.tools.length}`}
                </button>
              )}

              {data.turns.length > 1 && (
                <div className="mt-3">
                  <div className="mb-1 font-mono text-[10px] text-ink-dim uppercase">
                    cache-write per turn
                  </div>
                  <div className="flex h-8 items-end gap-px">
                    {data.turns.map((t) => (
                      <div
                        key={t.seq}
                        title={`turn ${t.seq}: ${fmtTokens(t.cacheWrite)}`}
                        className="min-w-[2px] flex-1 rounded-t-[1px] bg-amber/50"
                        style={{ height: `${Math.max(6, (t.cacheWrite / maxWrite) * 100)}%` }}
                      />
                    ))}
                  </div>
                </div>
              )}

              <p className="mt-2.5 font-mono text-[10px] leading-snug text-ink-dim">
                ~{fmtTokens(data.totalEst)} total, estimated at ~4 bytes/token from tool-result
                sizes
                {data.uninspected > 0 && ` · ${data.uninspected} unattributed result(s)`}
                {data.malformed > 0 && ` · ${data.malformed} malformed line(s) skipped`}
              </p>
            </>
          )}
        </div>
      )}
    </div>
  );
}
