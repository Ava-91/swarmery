// System shell — the single top-level "System" destination that hosts the four
// sections (Agents · Toolkit · Hooks · Insights) as TABS, restoring the earlier
// one-item-in-the-sidebar shape. It is a thin OUTER tab bar over the existing
// hub pages: the Agents tab embeds <AgentHub>, the other three embed <SystemHub>
// (Toolkit → the skills/commands/templates catalog, Hooks, Insights). Nothing is
// reimplemented — both hubs gained an `embedded` mode that suppresses their own
// heading/role-nav and hands URL ownership to this shell.
//
// Routing: /system/:tab (fleet) and /p/:slug/system/:tab (workspace), where tab ∈
// agents|toolkit|hooks|insights. The active tab is the first path segment after
// /system; each embedded hub keeps its own detail sub-tabs on ?tab= and (for
// Agents) its selection in the /system/agents/:id path via the routeBase we pass.
// Bare /system (or /p/:slug/system) redirects to the Agents tab.

import { useEffect, useMemo, useState } from 'react';
import { Navigate, useNavigate, useParams } from 'react-router-dom';
import type { SystemHubSummary } from '../api/types';
import { fetchSystemHubSummary } from '../api/systemHub';
import { useScope } from '../lib/scope';
import { useLiveUpdates } from '../lib/ws';
import { AgentHub } from './AgentHub';
import { SystemHub } from './SystemHub';

type SystemTab = 'agents' | 'toolkit' | 'hooks' | 'insights';
const TABS: SystemTab[] = ['agents', 'toolkit', 'hooks', 'insights'];
const TAB_LABELS: Record<SystemTab, string> = {
  agents: 'Agents',
  toolkit: 'Toolkit',
  hooks: 'Hooks',
  insights: 'Insights',
};

function parseTab(value: string | undefined): SystemTab | null {
  return (TABS as string[]).includes(value ?? '') ? (value as SystemTab) : null;
}

export function SystemShell(): JSX.Element {
  const params = useParams();
  const navigate = useNavigate();
  const { scope } = useScope();

  // Workspace mount (/p/:slug/system/*) carries the slug; fleet mode uses the
  // global scope switcher. Either narrows the rollup window + template scope.
  const slug = params.slug;
  const scopeSlug = slug ?? scope;
  const base = slug !== undefined ? `/p/${slug}/system` : '/system';

  // Active tab = first path segment after the base (params['*'] is the splat).
  const splat = params['*'] ?? '';
  const firstSeg = splat.split('/')[0];
  const tab = parseTab(firstSeg);

  // Tab-bar count badges (agents · toolkit total · hooks · insights) from the
  // hub summary; live-refreshed on registry edits like the hubs themselves.
  const [summary, setSummary] = useState<SystemHubSummary | null>(null);
  useEffect(() => {
    let cancelled = false;
    const load = (): void => {
      fetchSystemHubSummary(scopeSlug ?? undefined)
        .then((s) => {
          if (!cancelled) setSummary(s);
        })
        .catch(() => {
          if (!cancelled) setSummary(null);
        });
    };
    load();
    return () => {
      cancelled = true;
    };
  }, [scopeSlug]);
  useLiveUpdates(
    (msg) => {
      if (msg.type === 'system_item_updated') {
        fetchSystemHubSummary(scopeSlug ?? undefined)
          .then(setSummary)
          .catch(() => setSummary(null));
      }
    },
    () => {
      fetchSystemHubSummary(scopeSlug ?? undefined)
        .then(setSummary)
        .catch(() => setSummary(null));
    },
  );

  const badges: Record<SystemTab, number | undefined> = useMemo(
    () =>
      summary === null
        ? { agents: undefined, toolkit: undefined, hooks: undefined, insights: undefined }
        : {
            agents: summary.agents,
            toolkit: summary.skills + summary.commands + summary.templates,
            hooks: summary.hooks,
            insights: summary.insights,
          },
    [summary],
  );

  // Bare /system → Agents tab (default). `replace` so back doesn't loop.
  if (tab === null) {
    return <Navigate to={`${base}/agents`} replace />;
  }

  const goTab = (next: SystemTab): void => {
    if (next !== tab) navigate(`${base}/${next}`);
  };

  return (
    <div className="flex h-full min-h-0 flex-col px-4 pt-6 desk:px-10 desk:pt-[34px]">
      <h1 className="mb-4 font-display text-[30px] leading-tight font-medium tracking-[-0.01em]">
        System
      </h1>
      <div
        className="mb-4 flex gap-1 overflow-x-auto border-b border-line [-webkit-overflow-scrolling:touch]"
        role="tablist"
        aria-label="System sections"
      >
        {TABS.map((t) => {
          const active = t === tab;
          const badge = badges[t];
          return (
            <button
              key={t}
              type="button"
              role="tab"
              aria-selected={active}
              onClick={() => goTab(t)}
              className={`-mb-px flex shrink-0 items-center gap-1.5 border-b-2 px-3.5 py-[8px] text-[13px] font-medium whitespace-nowrap transition-colors ${
                active ? 'border-brand text-brand' : 'border-transparent text-ink-dim hover:text-ink'
              }`}
            >
              {TAB_LABELS[t]}
              {badge !== undefined && badge > 0 && (
                <span className="inline-flex h-[16px] min-w-[16px] items-center justify-center rounded-full bg-line-strong px-1 font-mono text-[9.5px] font-bold text-ink-dim">
                  {badge}
                </span>
              )}
            </button>
          );
        })}
      </div>

      {/* The active tab's embedded hub owns the pane below (its own roster,
          search, detail rail and detail sub-tabs). Each hub manages its own
          internal selection/sub-tab state; the outer tab lives in the URL. */}
      <div className="min-h-0 flex-1">
        <SystemTabPanel tab={tab} base={base} scopeSlug={scopeSlug} />
      </div>
    </div>
  );
}

/** Mounts the embedded hub for the active tab. Agents → AgentHub (roster +
 * selection-gated detail); Toolkit/Hooks/Insights → SystemHub pinned to the
 * matching category. Each receives an explicit routeBase so its internal
 * navigation stays on the /system tree. */
function SystemTabPanel({
  tab,
  base,
  scopeSlug,
}: {
  tab: SystemTab;
  base: string;
  scopeSlug: string | null;
}): JSX.Element {
  if (tab === 'agents') {
    return <AgentHub embedded routeBase={`${base}/agents`} scopeSlug={scopeSlug} />;
  }
  // Toolkit → skills (with the skills/commands/templates sub-pills); Hooks and
  // Insights map straight to their SystemHub categories.
  const forceCategory = tab === 'toolkit' ? 'skills' : tab; // 'hooks' | 'insights'
  return (
    <SystemHub
      embedded
      forceCategory={forceCategory}
      routeBase={`${base}/${tab}`}
      scopeSlug={scopeSlug}
    />
  );
}
