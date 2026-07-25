// Header mode toggle: the segmented [ Sessions | Projects ] control that
// switches between the two platform shells — the fleet AppShell (routes under
// `/…`) and the project WorkspaceShell (routes under `/p/:slug/…`). It lives in
// BOTH headers, placed before the fleet scope switcher.
//
// It is a NAVIGATION control, not a form value: two react-router NavLinks inside
// a labelled `role="group"` (per W3C ARIA APG, `aria-current="page"` is a link
// property — a radiogroup would model a stored value instead, which is the
// ThemePicker mode-segments idiom, a different thing). The active segment tracks
// MODE (`pathname.startsWith('/p/')`), NOT NavLink's default path matching, so
// "Sessions" is not falsely active on `/sessions` and "Projects" stays active
// across every `/p/:slug/*` sub-page. Tab + Enter is the full keyboard contract.
//
// The Projects segment reopens the last-visited project (`/p/:lastSlug`); with no
// project ever opened it lands on the fleet Projects list (`/projects`).

import { NavLink, useLocation } from 'react-router-dom';
import { loadLastProject } from '../lib/lastProject';

export function ModeToggle(): JSX.Element {
  const { pathname } = useLocation();
  const projectMode = pathname.startsWith('/p/');
  const last = loadLastProject();
  const projectsTo = last !== null ? `/p/${last}` : '/projects';

  const seg = (active: boolean): string =>
    `flex items-center rounded-[7px] px-2.5 py-1 font-mono text-[11px] font-semibold transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-brand ${
      active ? 'bg-surface2 text-ink' : 'text-ink-dim hover:text-ink'
    }`;

  return (
    <div
      role="group"
      aria-label="platform mode"
      className="flex shrink-0 gap-1 rounded-lg border border-line-strong bg-field p-0.5"
    >
      <NavLink
        to="/"
        aria-current={projectMode ? undefined : 'page'}
        className={() => seg(!projectMode)}
      >
        Sessions
      </NavLink>
      <NavLink
        to={projectsTo}
        aria-current={projectMode ? 'page' : undefined}
        className={() => seg(projectMode)}
      >
        Projects
      </NavLink>
    </div>
  );
}
