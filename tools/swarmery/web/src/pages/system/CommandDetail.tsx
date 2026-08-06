// Read-only detail panel for a slash command — the first one commands have
// ever had. They were list-only until now because they carry no write surface:
// there is no PUT, no version history and no base_hash for a command, so this
// panel is a reader, not an editor.
//
// It is fed by the existing GET /api/system/commands/{id}/hub and mirrors the
// agents/skills panel's header, badges and section tablist so the two read the
// same way: Docs (the `# How to use` guide) | Definition (frontmatter + body).

import { useEffect, useState } from 'react';
import type { SystemCommandHub } from '../../api/types';
import { fetchCommandHub } from '../../api/system';
import { ErrorBox, Loading, SectionTitle } from '../../components/ui';
import { Markdown } from '../../lib/markdown';
import { FrontmatterTable, OriginBadge, ScopeBadge } from './shared';
import { DocsPanel } from './DocsPanel';
import { SectionPanel, SectionTabs } from './SectionTabs';
import { defaultSection, guidePath, type DocsSection } from './docsSection';

const SECTION_TABS = ['docs', 'definition'] as const;
const SECTION_LABELS: Record<string, string> = { docs: 'Docs', definition: 'Definition' };

export function CommandDetail({
  id,
  projectNames,
  section,
  onSection,
  onClose,
}: {
  id: number;
  /** slug → short display name lookup (from /api/projects). */
  projectNames?: Record<string, string>;
  /** Active section when the PAGE owns it (?sec=); null = use the default. */
  section?: DocsSection | null;
  onSection?: (section: DocsSection) => void;
  onClose: () => void;
}): JSX.Element {
  const [cmd, setCmd] = useState<SystemCommandHub | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [localSection, setLocalSection] = useState<DocsSection | null>(null);

  useEffect(() => {
    let cancelled = false;
    setError(null);
    setCmd(null);
    setLocalSection(null);
    fetchCommandHub(id)
      .then((c) => {
        if (!cancelled) setCmd(c);
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [id]);

  if (error !== null) return <ErrorBox message={error} />;
  if (cmd === null) return <Loading label="command…" />;

  const panelPrefix = `sys-commands-${String(cmd.id)}`;
  const activeSection: DocsSection = section ?? localSection ?? defaultSection(cmd.docs);
  const selectSection = (next: string): void => {
    const s: DocsSection = next === 'docs' ? 'docs' : 'definition';
    setLocalSection(s);
    onSection?.(s);
  };

  return (
    <div>
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="font-display text-[18px] leading-tight font-semibold">/{cmd.name}</h1>
        <span className="ml-auto flex items-center gap-1.5">
          <ScopeBadge
            scope={cmd.scope}
            projectSlug={cmd.projectSlug}
            projectName={
              cmd.projectSlug !== null ? (projectNames?.[cmd.projectSlug] ?? cmd.projectSlug) : null
            }
          />
          <OriginBadge origin={cmd.origin} pluginName={cmd.pluginName} />
          <button
            type="button"
            onClick={onClose}
            aria-label="close detail"
            className="ml-1 rounded-[7px] border border-line-strong px-2 py-px text-[12px] text-ink-faint transition-colors hover:text-ink"
          >
            ✕
          </button>
        </span>
      </div>
      {cmd.description !== null && (
        <div className="mt-1 text-[12.5px] text-ink-dim">{cmd.description}</div>
      )}
      <div className="mt-1 font-mono text-[10px] break-all text-ink-faint">{cmd.path}</div>

      {/* Usage is INFERRED from prompt text, never an event — the caveat is
          part of the number, so it is rendered with it and never without. */}
      <div className="mt-3 flex gap-[22px] border-b border-line pb-3 font-mono text-[11px] text-ink-dim">
        <span data-tip="slash-command invocations are matched in prompt text — a lower bound, not a count">
          invocations {String(cmd.usage.windowDays)}d{' '}
          <b className="font-medium text-ink">≈{String(cmd.usage.invocations)}</b>
        </span>
        <span>read-only — commands have no editor</span>
      </div>

      <SectionTabs
        tabs={SECTION_TABS}
        labels={SECTION_LABELS}
        active={activeSection}
        onSelect={selectSection}
        idPrefix={panelPrefix}
        ariaLabel={`/${cmd.name} sections`}
      />

      <SectionPanel idPrefix={panelPrefix} tab={activeSection}>
        {activeSection === 'docs' ? (
          <DocsPanel
            docs={cmd.docs}
            path={guidePath(cmd.path, 'commands')}
            name={`/${cmd.name}`}
          />
        ) : (
          <>
            <SectionTitle>Frontmatter</SectionTitle>
            <FrontmatterTable frontmatter={cmd.frontmatter} />

            <SectionTitle>Body</SectionTitle>
            <div className="text-[13px] leading-[1.6] text-ink-2">
              <Markdown text={cmd.content} />
            </div>
          </>
        )}
      </SectionPanel>
    </div>
  );
}
