// Plugin-drift badge on the daemon health line: enabled plugins the daemon
// found are not actually loadable in some project. Silent at zero — the health
// line is otherwise pure noise — and rendered by every shell that shows health
// (App, WorkspaceShell), so the signal cannot be present on one screen and
// missing on the next.

import type { HealthResponse } from '../api/types';

export function PluginDriftBadge({
  health,
}: {
  health: HealthResponse | null;
}): JSX.Element | null {
  const drift = health?.pluginDrift;
  // Absent (older daemon) and zero both render nothing, but they are not the
  // same thing: a daemon that cannot scan reports itself as an error finding,
  // so a blind scanner still shows up here rather than reading as healthy.
  if (drift === undefined || drift.error + drift.warn === 0) return null;
  return (
    <span
      title={`${String(drift.error)} error / ${String(drift.warn)} warn plugin findings — see System → Insights`}
      className={drift.error > 0 ? 'text-red' : 'text-amber'}
    >
      {drift.error > 0 ? `· plugins ⚠ ${String(drift.error)}` : `· plugins ${String(drift.warn)}`}
    </span>
  );
}
