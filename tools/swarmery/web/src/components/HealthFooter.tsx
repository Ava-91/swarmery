// Sidebar-bottom daemon health line (Redesign): "● daemon healthy · v0.3"
// from GET /api/health, polled every 60s; red + "daemon unreachable" when the
// fetch fails.

import { useEffect, useState } from 'react';
import type { HealthResponse } from '../api/types';
import { fetchHealth } from '../api';

const POLL_MS = 60_000;

/** "0.3.0" → "v0.3" (Redesign shows major.minor). */
function shortVersion(version: string): string {
  const parts = version.split('.');
  return `v${parts.slice(0, 2).join('.')}`;
}

export function HealthFooter(): JSX.Element | null {
  const [health, setHealth] = useState<HealthResponse | null>(null);
  const [unreachable, setUnreachable] = useState(false);

  useEffect(() => {
    let disposed = false;
    const poll = (): void => {
      fetchHealth()
        .then((h) => {
          if (disposed) return;
          setHealth(h);
          setUnreachable(false);
        })
        .catch(() => {
          if (!disposed) setUnreachable(true);
        });
    };
    poll();
    const timer = setInterval(poll, POLL_MS);
    return () => {
      disposed = true;
      clearInterval(timer);
    };
  }, []);

  if (health === null && !unreachable) return null;

  return (
    <div className="mt-auto flex items-center gap-1.5 px-3 py-2 font-mono text-[10.5px] text-ink-dim">
      <span
        className={`h-1.5 w-1.5 shrink-0 rounded-full ${unreachable ? 'bg-red' : 'bg-green'}`}
        aria-hidden="true"
      />
      {unreachable
        ? 'daemon unreachable'
        : `daemon healthy${health !== null ? ` · ${shortVersion(health.version)}` : ''}`}
      {/* Plugin drift: enabled plugins the daemon found are not actually
          loadable. Silent at zero — this line is otherwise pure noise. */}
      {health?.pluginDrift !== undefined && health.pluginDrift.error + health.pluginDrift.warn > 0 ? (
        <span
          title={`${String(health.pluginDrift.error)} error / ${String(health.pluginDrift.warn)} warn plugin findings`}
          className={health.pluginDrift.error > 0 ? 'text-red' : 'text-amber'}
        >
          ·{' '}
          {health.pluginDrift.error > 0
            ? `plugins ⚠ ${String(health.pluginDrift.error)}`
            : `plugins ${String(health.pluginDrift.warn)}`}
        </span>
      ) : null}
    </div>
  );
}
