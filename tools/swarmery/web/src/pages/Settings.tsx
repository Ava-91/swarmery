// Global settings (/settings — session mode): the app-wide preferences that used
// to live as header popovers (theme + browser notifications), consolidated onto
// one page alongside auto-approve and daemon/health info.
//
//   Appearance    — reuses ThemePickerPanel (mode segments + palette list); the
//                    panel renders its own "appearance" eyebrow, so no extra one.
//   Notifications — reuses NotifySettings, wired to the shared NotifyPrefsContext
//                    (the same state the mounted useBrowserNotifications hook in
//                    AppShell reads, so toggles here drive live background toasts).
//   Auto-approve  — PermissionPresets is per-project (requires a projectId), so
//                    this is an explanatory note pointing at a project's Settings.
//   Daemon        — health + version from the shared useHealth poll.
//   Connectors    — read-only list of the MCP servers Claude Code has configured
//                    on this host (ConnectorsSection self-fetches /api/connectors).
//
// Chrome mirrors ProjectSettings (font-display heading, mono SectionTitle
// eyebrows, hairline cards).

import { AccountsSection } from '../components/AccountsSection';
import { ConnectorsSection } from '../components/ConnectorsSection';
import { NotifySettings } from '../components/NotifySettings';
import { SectionTitle } from '../components/ui';
import { useHealth, versionLabel, versionTitle } from '../lib/health';
import { useNotifyPrefs } from '../lib/notifyPrefsContext';
import { ThemePickerPanel } from '../theme/ThemePicker';
import { WorktreesPanel } from './settings/WorktreesPanel';

export function Settings(): JSX.Element {
  const { prefs, setPrefs } = useNotifyPrefs();
  const { health, unreachable } = useHealth();
  const daemonOk = !unreachable;

  return (
    <div className="px-4 pt-5 pb-10 desk:px-8 desk:pt-7">
      <span className="font-display text-[20px] font-medium tracking-[-0.01em] text-ink">
        settings
      </span>

      {/* Appearance — ThemePickerPanel self-labels with its own mono eyebrow. */}
      <div className="mt-5">
        <ThemePickerPanel />
      </div>

      {/* Notifications — reuse the header popover control inline, wired to the
          shared prefs context so it drives the mounted notifier in AppShell. */}
      <SectionTitle>notifications</SectionTitle>
      <NotifySettings prefs={prefs} onChange={setPrefs} />

      {/* Auto-approve — permission presets are dispatch/approval settings scoped
          to a single project (PermissionPresets needs a projectId), so they are
          configured per project, not globally. */}
      <SectionTitle>auto-approve</SectionTitle>
      <div className="rounded-xl border border-dashed border-line px-3.5 py-4 font-mono text-[11.5px] text-ink-dim">
        auto-approve presets are configured per project — open a project's Settings to set them
      </div>

      {/* Daemon — health + version from the shared poll. */}
      <SectionTitle>daemon</SectionTitle>
      <div className="flex items-center gap-2 rounded-xl border border-line bg-surface px-3.5 py-4 font-mono text-[11.5px] text-ink-dim">
        <span
          aria-hidden="true"
          className={`inline-block h-[7px] w-[7px] rounded-full ${daemonOk ? 'bg-green' : 'bg-red'}`}
        />
        {daemonOk ? 'daemon healthy' : 'daemon unreachable'}
        {health !== null && (
          <span title={versionTitle(health)}>
            {' '}
            · {versionLabel(health)} · :7777
          </span>
        )}
      </div>

      {/* Worktrees — the janitor's inventory + what it decided. Host-level like
          the daemon card above: it sweeps every project on this machine, and it
          does so without being asked, so this is the account of what it did. */}
      <SectionTitle>worktrees</SectionTitle>
      <WorktreesPanel />

      {/* Accounts — the Claude accounts swarmery knows about on THIS host
          (multi-account, phase 7). Host-level like daemon/worktrees above:
          AccountsSection self-fetches /api/accounts. */}
      <SectionTitle>accounts</SectionTitle>
      <AccountsSection />

      {/* Connectors — the MCP servers Claude Code has configured on THIS host,
          read through the daemon. Host-level like the daemon card above, not
          project-scoped: `claude mcp list` reports the user/claudeai/plugin
          scopes, which belong to the machine, not to any one project. */}
      <SectionTitle>connectors</SectionTitle>
      <ConnectorsSection />
    </div>
  );
}
