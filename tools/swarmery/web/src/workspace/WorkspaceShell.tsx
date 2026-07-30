// Workspace shell (fusion phase 4): the top-level chrome for project mode — a
// slim header (wordmark linking back to the fleet, the [Sessions|Projects] mode
// toggle, a theme toggle, daemon health) above the ProjectWorkspaceLayout. It is
// a SIBLING of the fleet <App/> (its own header + rescoped sidebar + status bar),
// not nested inside it, so project mode is a distinct full-screen surface rather
// than a page within the fleet frame. Shared providers (ScopeProvider) live one
// level up in RootProviders so both surfaces read the same project store.
//
// The mode toggle's "Sessions" segment IS the way back to the fleet, so it
// replaces the old `← fleet` text link (the wordmark still links home too).

import { Link } from 'react-router-dom';
import { MOCK } from '../api';
import { ModeToggle } from '../components/ModeToggle';
import { ThemeToggle } from '../components/ThemeToggle';
import { UsageChip } from '../components/usage/UsageChip';
import { useHealth, shortVersion } from '../lib/health';
import { PluginDriftBadge } from '../components/PluginDriftBadge';
import { ProjectWorkspaceLayout } from './ProjectWorkspaceLayout';

export function WorkspaceShell(): JSX.Element {
  const { health, unreachable } = useHealth();
  const daemonOk = !unreachable;
  return (
    <div className="flex h-dvh flex-col">
      <header className="header-hairline relative z-20 flex h-14 shrink-0 items-center gap-4 bg-bg px-4 desk:px-6">
        {/* Same fixed-width wordmark block as the fleet header (desk:w-[208px])
            so the ModeToggle sits at the identical x-position — no shift when
            toggling between Sessions and Projects. */}
        <Link
          to="/"
          aria-label="back to all projects"
          className="flex min-w-0 items-center font-sans text-[16px] leading-none font-extrabold tracking-[0.09em] text-ink uppercase transition-opacity hover:opacity-80 desk:w-[208px] desk:shrink-0"
        >
          SW<span className="text-brand">◆</span>RMERY
        </Link>
        {/* Mode toggle: the "Sessions" segment returns to the fleet (replaces the
            old `← fleet` link); "Projects" stays active across every /p/:slug/*. */}
        <ModeToggle />
        <span className="ml-auto flex items-center gap-3">
          <ThemeToggle />
          <UsageChip />
          <span className="flex items-center gap-1.5 font-mono text-[10.5px] text-ink-dim">
            {MOCK ? (
              <>
                <span className="inline-block h-[7px] w-[7px] rounded-full bg-amber" />
                mock data
              </>
            ) : (
              <>
                <span
                  className={`inline-block h-[7px] w-[7px] rounded-full ${daemonOk ? 'animate-pulse-dot bg-green' : 'bg-red'}`}
                />
                {daemonOk ? 'daemon healthy' : 'daemon unreachable'}
                {health !== null ? ` · ${shortVersion(health.version)}` : ''}
                <PluginDriftBadge health={health} />
              </>
            )}
          </span>
        </span>
      </header>
      <ProjectWorkspaceLayout />
    </div>
  );
}
