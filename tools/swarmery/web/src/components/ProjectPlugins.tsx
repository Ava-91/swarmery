// Plugins section (/projects/:id, managed projects): the swarmery marketplace
// catalog with per-project enable/disable toggles. Writes edit the project's
// .claude/settings.json via the fenced PUT endpoint and take effect in the
// NEXT Claude Code session; core is locked (attach/detach owns its lifecycle).
//
// Each row also carries the daemon's drift verdict: a plugin can be enabled
// here and still not be loadable, which is invisible from settings.json alone.

import { useCallback, useEffect, useRef, useState } from 'react';
import type { PluginDriftStatus, ProjectPluginRow, ProjectPluginsResponse } from '../api/types';
import { fetchProjectPlugins, repairProjectPlugin, toggleProjectPlugin } from '../api';
import { Card, ErrorBox, Loading, SectionTitle } from './ui';

// red = the plugin is not loaded at all; amber = it loads, but stale or from a
// reclaimed cache dir. Both tokens are defined in light and dark themes.
const STATUS_STYLES: Record<Exclude<PluginDriftStatus, 'ok' | 'unknown'>, string> = {
  missing: 'border-red/40 bg-red/10 text-red',
  orphaned: 'border-amber/40 bg-amber/10 text-amber',
  behind: 'border-amber/40 bg-amber/10 text-amber',
};

function StatusChip({ row }: { row: ProjectPluginRow }): JSX.Element | null {
  if (row.status === 'ok' || row.status === 'unknown') return null;
  return (
    <span
      title={row.detail}
      className={`shrink-0 rounded-full border px-2 py-0.5 font-mono text-[10px] ${STATUS_STYLES[row.status]}`}
    >
      {row.status}
    </span>
  );
}

function RepairButton({
  projectId,
  row,
  marketplace,
  disabled,
  onDone,
}: {
  projectId: number;
  row: ProjectPluginRow;
  marketplace: string;
  disabled: boolean;
  onDone: () => void;
}): JSX.Element {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  // A repair can succeed and still need the operator's attention — the
  // user-scope fallback reverts a global enable, and that revert can fail.
  const [warning, setWarning] = useState<string | null>(null);
  const run = (): void => {
    setBusy(true);
    setErr(null);
    setWarning(null);
    repairProjectPlugin(projectId, `${row.name}@${marketplace}`)
      .then((res) => {
        setWarning(res.warning ?? null);
        onDone();
      })
      .catch((e: unknown) => {
        setErr(e instanceof Error ? e.message : String(e));
      })
      .finally(() => {
        setBusy(false);
      });
  };
  const label = busy ? '…' : err !== null ? 'failed' : warning !== null ? 'check' : 'repair';
  return (
    <button
      type="button"
      disabled={disabled || busy}
      onClick={run}
      title={
        err ??
        warning ??
        (disabled
          ? 'read-only — daemon started without SWARMERY_ONBOARD_ROOTS'
          : 'run claude plugin install/update for this project')
      }
      className={`shrink-0 rounded-full border border-line px-2 py-0.5 font-mono text-[10px] transition-colors hover:text-ink disabled:cursor-not-allowed disabled:opacity-50 ${
        warning !== null ? 'text-amber' : 'text-ink-dim'
      }`}
    >
      {label}
    </button>
  );
}

function ToggleButton({
  row,
  disabled,
  busy,
  onToggle,
}: {
  row: ProjectPluginRow;
  disabled: boolean;
  busy: boolean;
  onToggle: () => void;
}): JSX.Element {
  if (row.locked) {
    return (
      <span
        className="font-mono text-[10px] text-ink-faint"
        title="core is managed via attach/detach"
      >
        via attach/detach
      </span>
    );
  }
  return (
    <button
      type="button"
      disabled={disabled || busy}
      onClick={onToggle}
      aria-pressed={row.enabled}
      aria-label={`${row.name}: ${row.enabled ? 'enabled' : 'disabled'}`}
      title={disabled ? 'read-only — daemon started without SWARMERY_ONBOARD_ROOTS' : undefined}
      className={`rounded-full border px-2.5 py-0.5 font-mono text-[10px] transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
        row.enabled
          ? 'border-brand/40 bg-brand/10 text-brand hover:bg-brand/20'
          : 'border-line text-ink-faint hover:text-ink'
      }`}
    >
      {busy ? '…' : row.enabled ? 'on' : 'off'}
    </button>
  );
}

export function ProjectPlugins({ projectId }: { projectId: number }): JSX.Element {
  const [data, setData] = useState<ProjectPluginsResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  // Unmount guard shared by the initial load, the retry button, and the
  // reload-after-toggle: a ref (not a closure `let`) because `load` outlives
  // any single effect run. Flipped false in the effect cleanup below.
  const aliveRef = useRef(true);

  const load = useCallback((): void => {
    fetchProjectPlugins(projectId)
      .then((d) => {
        if (!aliveRef.current) return;
        setData(d);
        setError(null);
      })
      .catch((e: unknown) => {
        if (!aliveRef.current) return;
        setError(e instanceof Error ? e.message : String(e));
      });
  }, [projectId]);

  useEffect(() => {
    aliveRef.current = true;
    load();
    return () => {
      aliveRef.current = false;
    };
  }, [load]);

  const toggle = (row: ProjectPluginRow): void => {
    setBusy(row.name);
    toggleProjectPlugin(projectId, row.name, !row.enabled)
      .then(() => {
        if (!aliveRef.current) return;
        setError(null);
        load();
      })
      .catch((e: unknown) => {
        if (!aliveRef.current) return;
        setError(e instanceof Error ? e.message : String(e));
      })
      .finally(() => {
        if (aliveRef.current) setBusy(null);
      });
  };

  return (
    <>
      <SectionTitle>plugins</SectionTitle>
      {error !== null && (
        <div className="mb-2">
          <ErrorBox message={error} onRetry={load} />
        </div>
      )}
      {data === null && error === null ? (
        <Loading label="plugins…" />
      ) : data !== null ? (
        <Card>
          {data.plugins.length === 0 ? (
            <div className="rounded-xl border border-dashed border-line px-3.5 py-4 font-mono text-[11.5px] text-ink-dim">
              no plugins in the marketplace clone
            </div>
          ) : (
            <div className="divide-y divide-line-soft">
              {data.plugins.map((row) => (
                <div key={row.name} className="flex flex-col gap-0.5 py-1.5 first:pt-0 last:pb-0">
                  <div className="flex items-center gap-3">
                    <span className="font-mono text-[11px] whitespace-nowrap text-ink-2">
                      {row.name}
                    </span>
                    <span className="min-w-0 flex-1 truncate font-mono text-[10.5px] text-ink-faint">
                      {row.description}
                    </span>
                    <StatusChip row={row} />
                    {row.status === 'missing' || row.status === 'behind' ? (
                      <RepairButton
                        projectId={projectId}
                        row={row}
                        marketplace={data.marketplaceName}
                        disabled={!data.canWrite}
                        onDone={load}
                      />
                    ) : null}
                    <ToggleButton
                      row={row}
                      disabled={!data.canWrite}
                      busy={busy === row.name}
                      onToggle={() => toggle(row)}
                    />
                  </div>
                  {/* The detail is a second line, not only a tooltip: the reader
                      needs to see WHICH project the plugin was installed for. */}
                  {row.detail !== undefined && row.detail !== '' ? (
                    <div className="pl-1 font-mono text-[10px] text-ink-faint">{row.detail}</div>
                  ) : null}
                </div>
              ))}
            </div>
          )}
          <div className="mt-2 font-mono text-[10px] text-ink-faint">
            marketplace v{data.marketplaceVersion} · repairs and toggles take effect in the next
            Claude Code session
          </div>
        </Card>
      ) : null}
    </>
  );
}
