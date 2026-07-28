// Architecture page (/architecture): per-project architecture-map.html served
// through the daemon's jailed static route (architecture-out/). Union-gated:
// the feed lists projects with architecture-pack enabled OR an existing artifact,
// so the dropdown also shows pack-enabled projects that have not yet run
// /architecture-map (hasMap=false). Staleness badge compares analyzedAtCommit
// (baked into the map JSON) against headCommit (current HEAD, no exec).
// Clone of the Graphify page idioms — single load with ErrorBox retry, project
// selection by id, same-origin iframe.

import { useCallback, useEffect, useRef, useState } from 'react';
import type { ProvisionState, ToolsResponse } from '../api/types';
import { fetchTools, rebuildArchitectureMap, toggleProjectPlugin } from '../api';
import { Card, Empty, ErrorBox, Loading, SectionTitle } from '../components/ui';
import { fmtAgo } from '../lib/format';
import { findProject } from '../lib/projectSlug';
import { useTheme } from '../lib/theme';

// Provision states that are still in flight — while any project sits in one of
// these, the page settle-polls /api/tools until it lands on a terminal state.
const ACTIVE_STATES = new Set<ProvisionState['state']>(['pending', 'installing', 'generating']);
const POLL_MS = 3_000;

// architecture-map.html ships its own hardcoded palette + a standalone light/dark
// toggle. Embedded in the dashboard it must follow the app theme instead, so the
// same-origin iframe gets the app's live tokens pushed in as INLINE custom
// properties on its <html> (inline wins over both its `:root` defaults and its
// `html[data-theme]` overrides — the map cannot re-theme itself independently).
// Map-var ← app-token pairs; values are resolved via getComputedStyle so every
// palette re-tints the map, not just the light/dark axis.
const MAP_VARS: ReadonlyArray<readonly [string, string]> = [
  ['--bg', '--color-bg'],
  ['--panel', '--color-surface'],
  ['--card', '--color-surface'],
  ['--card-on', '--color-surface2'],
  ['--line', '--color-line'],
  ['--edge', '--color-line-strong'],
  ['--chip', '--color-field'],
  ['--ink', '--color-ink'],
  ['--ink-dim', '--color-ink-dim'],
  ['--ink-faint', '--color-ink-faint'],
  ['--accent', '--color-brand'],
  ['--flow', '--color-brand'],
  // Text on the accent fill: the page background is the highest-contrast token
  // against the brand amber in both modes (near-black in dark, off-white in light).
  ['--accent-ink', '--color-bg'],
  ['--mono', '--font-mono'],
  ['--sans', '--font-sans'],
];

// Provision pipeline stages for the progress bar. Percentages are stage floors,
// not real progress — the daemon only reports the stage, so the bar shows how
// deep in the pipeline the job is and pulses to signal liveness. `generating`
// (the headless analysis) dominates wall-clock, hence the wide gap before 100.
const STAGE_PROGRESS: Record<ProvisionState['state'], { pct: number; label: string }> = {
  pending: { pct: 8, label: 'queued' },
  installing: { pct: 30, label: 'installing pack' },
  generating: { pct: 70, label: 'analyzing repo & building map' },
  installed: { pct: 100, label: 'installed' },
  done: { pct: 100, label: 'done' },
  skipped: { pct: 100, label: 'already current' },
  failed: { pct: 100, label: 'failed' },
};

/** Indeterminate-ish progress panel for an in-flight provision job — replaces
 * the bare status chip so the wait doesn't look like a dead white area. */
function ProvisionProgress({ provision }: { provision: ProvisionState }): JSX.Element {
  const stage = STAGE_PROGRESS[provision.state];
  return (
    <div className="mt-3 rounded-xl border border-line bg-surface p-4">
      <div className="flex flex-wrap items-center justify-between gap-2 font-mono text-[11px]">
        <span className="inline-flex items-center gap-1.5 text-amber">
          <span aria-hidden>⟳</span>
          {provision.state}
          {provision.lastLine !== '' ? ` — ${provision.lastLine}` : ''}
        </span>
        <span className="text-ink-faint">{stage.label}</span>
      </div>
      <div
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={stage.pct}
        aria-label="architecture map generation progress"
        className="mt-2.5 h-1.5 overflow-hidden rounded-full bg-field"
      >
        <div
          className="h-full animate-pulse rounded-full bg-amber transition-[width] duration-700"
          style={{ width: `${String(stage.pct)}%` }}
        />
      </div>
    </div>
  );
}

/** Push the dashboard theme into the map iframe: resolved mode on `data-theme`,
 * app tokens as inline vars, and the map's own theme toggle hidden (standalone
 * opens of the artifact keep it — this only applies inside the dashboard). */
function syncMapTheme(frame: HTMLIFrameElement, resolved: 'light' | 'dark'): void {
  const doc = frame.contentDocument;
  if (doc === null) return;
  const root = doc.documentElement;
  root.dataset.theme = resolved;
  const cs = getComputedStyle(document.documentElement);
  for (const [mapVar, token] of MAP_VARS) {
    const v = cs.getPropertyValue(token).trim();
    if (v !== '') root.style.setProperty(mapVar, v);
  }
  if (doc.getElementById('swarmery-embed-style') === null) {
    const style = doc.createElement('style');
    style.id = 'swarmery-embed-style';
    style.textContent = '#theme{display:none}';
    doc.head.appendChild(style);
  }
}

export function Architecture({
  scopedSlug,
  scopedId,
}: { scopedSlug?: string; scopedId?: number | null } = {}): JSX.Element {
  const [data, setData] = useState<ToolsResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [full, setFull] = useState(false);
  const [acting, setActing] = useState(false);
  const aliveRef = useRef(true);
  const frameRef = useRef<HTMLIFrameElement | null>(null);
  const { theme, palette } = useTheme();

  // Fullscreen overlay: Esc collapses, page scroll is locked while expanded.
  useEffect(() => {
    if (!full) return;
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') setFull(false);
    };
    window.addEventListener('keydown', onKey);
    const prev = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      window.removeEventListener('keydown', onKey);
      document.body.style.overflow = prev;
    };
  }, [full]);

  const syncTheme = useCallback((): void => {
    // Deferred one frame: this child effect runs BEFORE ThemeProvider's parent
    // effect flips data-mode on <html>, so an immediate getComputedStyle would
    // read the OUTGOING theme's tokens (the same race Analytics notes for its
    // chart tokens). rAF fires after all commit effects, before paint.
    requestAnimationFrame(() => {
      if (frameRef.current !== null) syncMapTheme(frameRef.current, theme);
    });
    // theme + palette are the triggers: the token values are re-read from the
    // live computed style, so the palette id itself is only a dependency signal.
  }, [theme, palette]);

  // Re-push on every mode/palette flip; onLoad below covers the initial load
  // and project switches (the iframe is keyed by project id).
  useEffect(() => {
    syncTheme();
  }, [syncTheme]);

  const load = useCallback((): void => {
    fetchTools()
      .then((d) => {
        if (!aliveRef.current) return;
        setData(d);
        setError(null);
      })
      .catch((e: unknown) => {
        if (!aliveRef.current) return;
        setError(e instanceof Error ? e.message : String(e));
      });
  }, []);

  useEffect(() => {
    aliveRef.current = true;
    load();
    return () => {
      aliveRef.current = false;
    };
  }, [load]);

  const projects = data?.architecture.projects ?? [];
  const scoped = scopedSlug !== undefined;
  // Selection prefers hasMap (a built project) over an unbuilt one; in
  // project-workspace mode it is pinned to the workspace slug.
  const project = scoped
    ? (findProject(projects, scopedSlug ?? null) ?? undefined)
    : (projects.find((p) => p.id === selectedId) ?? projects.find((p) => p.hasMap) ?? projects[0]);

  // Kick off a rebuild (or the scoped enable-and-build) and re-fetch — the
  // provision job lands as 'pending' in the feed, so the settle-poll below
  // takes over the progress display. Failures reuse the top ErrorBox.
  const kick = useCallback(
    (action: () => Promise<unknown>): void => {
      setActing(true);
      action()
        .then(() => {
          if (aliveRef.current) load();
        })
        .catch((e: unknown) => {
          if (aliveRef.current) setError(e instanceof Error ? e.message : String(e));
        })
        .finally(() => {
          if (aliveRef.current) setActing(false);
        });
    },
    [load],
  );

  // While any project has an in-flight provision job, re-fetch every 3s until
  // it settles (mirrors Serena's interval-until-settled; the aliveRef guard in
  // `load` bounds writes to a mounted component). The effect re-runs whenever
  // `projects` changes, so once every job is terminal `active` is false and no
  // new interval is scheduled — it does not loop forever.
  useEffect(() => {
    const active = projects.some((p) => p.provision !== null && ACTIVE_STATES.has(p.provision.state));
    if (!active) return;
    const t = window.setInterval(load, POLL_MS);
    return () => window.clearInterval(t);
  }, [projects, load]);

  return (
    <div className="min-w-0 px-4 pt-6 pb-10 desk:px-10 desk:pt-[34px] desk:pb-[60px]">
      <SectionTitle>architecture</SectionTitle>
      {error !== null && (
        <div className="mb-2">
          <ErrorBox message={error} onRetry={load} />
        </div>
      )}
      {data === null && error === null ? (
        <Loading label="architecture…" />
      ) : data !== null ? (
        project === undefined ? (
          scoped && scopedId !== null && scopedId !== undefined ? (
            // The project has neither the pack nor an artifact: one button does
            // the whole Settings flow in place — enable architecture-pack, which
            // auto-provisions (install → generate); the feed then picks the
            // project up and the settle-poll shows progress.
            <Empty>
              <span className="mr-3">no architecture map for this project yet</span>
              <button
                type="button"
                disabled={acting}
                onClick={() => {
                  kick(() => toggleProjectPlugin(scopedId, 'architecture-pack', true));
                }}
                className="rounded-[9px] border border-line-strong bg-field px-2.5 py-[6px] font-mono text-[12px] text-ink transition-colors hover:border-ink-dim disabled:opacity-50"
              >
                {acting ? 'starting…' : '⚒ enable architecture-pack & build map'}
              </button>
            </Empty>
          ) : (
            <Empty>
              {scoped
                ? 'no architecture map for this project — enable architecture-pack in Settings to generate it'
                : 'no architecture maps yet — run /architecture-map in a project repo'}
            </Empty>
          )
        ) : (
          <>
            <Card>
              <div className="flex flex-wrap items-center gap-3">
                {!scoped && (
                  <select
                    value={String(project.id)}
                    onChange={(e) => setSelectedId(Number(e.target.value))}
                    aria-label="architecture project"
                    className="rounded-[9px] border border-line-strong bg-field px-2.5 py-[6px] font-mono text-[12px] text-ink transition-colors outline-none focus:border-ink-dim"
                  >
                    {projects.map((p) => (
                      <option key={p.id} value={String(p.id)}>
                        {p.name ?? p.slug}
                      </option>
                    ))}
                  </select>
                )}
                {project.builtAt !== null && (
                  <span className="font-mono text-[10.5px] text-ink-faint">
                    map built {fmtAgo(project.builtAt)}
                    {project.analyzedAtCommit !== null && (
                      <> · @ {project.analyzedAtCommit.slice(0, 7)}</>
                    )}
                    {project.analyzedAtCommit !== null && project.headCommit !== null &&
                      (project.headCommit === project.analyzedAtCommit ? (
                        <span className="ml-1 text-green"> · current</span>
                      ) : (
                        <span className="ml-1 text-amber">
                          {' '}
                          · stale (HEAD {project.headCommit.slice(0, 7)})
                        </span>
                      ))}
                  </span>
                )}
                <span className="ml-auto flex items-center gap-2">
                  {(project.provision === null || !ACTIVE_STATES.has(project.provision.state)) && (
                    <button
                      type="button"
                      disabled={acting}
                      onClick={() => {
                        kick(() => rebuildArchitectureMap(project.id));
                      }}
                      className="rounded-[9px] border border-line-strong bg-field px-2.5 py-[6px] font-mono text-[12px] text-ink transition-colors hover:border-ink-dim disabled:opacity-50"
                    >
                      {acting ? 'starting…' : project.hasMap ? '↻ rebuild' : '⚒ build map'}
                    </button>
                  )}
                  {project.hasMap && (
                    <button
                      type="button"
                      onClick={() => setFull(true)}
                      className="rounded-[9px] border border-line-strong bg-field px-2.5 py-[6px] font-mono text-[12px] text-ink transition-colors hover:border-ink-dim"
                    >
                      ⛶ fullscreen
                    </button>
                  )}
                </span>
              </div>
            </Card>
            {project.provision !== null && ACTIVE_STATES.has(project.provision.state) && (
              // Progress replaces the old bare chip; the existing map (when
              // there is one) stays browsable below while the rebuild runs.
              <ProvisionProgress provision={project.provision} />
            )}
            {project.provision?.state === 'failed' && (
              <div className="mt-3">
                <ErrorBox
                  message={`${project.provision.error !== '' ? project.provision.error : 'provision failed'} — press rebuild to retry, or run /architecture-map manually`}
                  onRetry={load}
                />
              </div>
            )}
            {project.hasMap ? (
              // The wrapper/iframe keep their tree position across the toggle —
              // only classes change, so expanding never remounts (= reloads) the map.
              <div className={full ? 'fixed inset-0 z-[60] bg-bg p-3 desk:p-4' : 'mt-3'}>
                <iframe
                  key={project.id}
                  ref={frameRef}
                  // builtAt as a cache-buster: the artifact is regenerated in
                  // place under a stable URL, and browsers heuristic-cached it
                  // before the route sent Cache-Control — a rebuild changes the
                  // query so a stale copy can never be shown.
                  src={
                    project.builtAt !== null
                      ? `${project.mapPath}?v=${encodeURIComponent(project.builtAt)}`
                      : project.mapPath
                  }
                  title="Architecture map"
                  onLoad={syncTheme}
                  className={`w-full rounded-xl border border-line bg-surface ${
                    full ? 'h-full' : 'h-[calc(100vh-180px)]'
                  }`}
                />
                {full && (
                  <button
                    type="button"
                    onClick={() => setFull(false)}
                    aria-label="exit fullscreen"
                    className="absolute top-5 right-6 rounded-[9px] border border-line-strong bg-surface px-2.5 py-[6px] font-mono text-[12px] text-ink shadow-lg transition-colors hover:border-ink-dim desk:top-6 desk:right-7"
                  >
                    ✕ close
                  </button>
                )}
              </div>
            ) : project.provision === null || !ACTIVE_STATES.has(project.provision.state) ? (
              <div className="mt-3">
                <Empty>no map yet — press build map to generate it</Empty>
              </div>
            ) : null}
          </>
        )
      ) : null}
    </div>
  );
}
