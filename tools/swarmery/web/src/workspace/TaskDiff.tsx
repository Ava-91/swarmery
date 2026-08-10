// The evidence panel of the review loop (board redesign phase 3, §3.3): what
// the agent actually committed on this card's run branch.
//
// Fetched LAZILY — the request shells out to three git commands in the project
// repo, and the modal opens far more often for editing a prompt than for
// reviewing a result. Nothing loads until the panel is expanded.
//
// The server returns ONE unified patch, not a patch per file; splitting it on
// `diff --git` boundaries client-side keeps the endpoint's contract simple and
// costs nothing here — the text is already in memory and capped at 200 KB.

import { useCallback, useEffect, useState } from 'react';
import { getBoardTaskDiff } from '../api';
import type { TaskDiff as TaskDiffData } from '../api/types';

/** One file's slice of the unified patch. */
interface PatchSection {
  path: string;
  body: string;
}

/**
 * Splits a unified diff into per-file sections. The header line is
 * `diff --git a/<path> b/<path>`; the second path is the one that matters (a
 * rename's `b/` side is where the file ended up). A patch that does not start
 * with a header — the leading slice of a truncated diff, or an unfamiliar
 * format — is kept whole under an empty path rather than silently dropped.
 *
 * Exported so it is reachable from a test; the web app has no test runner
 * configured today, so it currently has none. The Go side owns the parsing that
 * IS covered (commits, numstat, truncation in tasks_diff_test.go).
 */
export function splitPatch(patch: string): PatchSection[] {
  if (patch.trim() === '') return [];
  const lines = patch.split('\n');
  const out: PatchSection[] = [];
  let current: PatchSection | null = null;
  for (const line of lines) {
    if (line.startsWith('diff --git ')) {
      if (current !== null) out.push(current);
      const m = /^diff --git a\/(.+) b\/(.+)$/.exec(line);
      current = { path: m?.[2] ?? line.slice('diff --git '.length), body: line };
      continue;
    }
    if (current === null) {
      current = { path: '', body: line };
      continue;
    }
    current.body += `\n${line}`;
  }
  if (current !== null) out.push(current);
  return out;
}

function StatCount({ additions, deletions }: { additions: number; deletions: number }): JSX.Element {
  return (
    <span className="shrink-0 font-mono text-[10.5px] tabular-nums">
      <span className="text-green">+{additions}</span>{' '}
      <span className="text-red">−{deletions}</span>
    </span>
  );
}

function PatchBlock({ section }: { section: PatchSection }): JSX.Element {
  const [open, setOpen] = useState(false);
  return (
    <div className="border-t border-line/60 first:border-t-0">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="flex w-full items-center gap-1.5 py-1 text-left font-mono text-[10.5px] text-ink-2 transition-colors hover:text-ink"
      >
        <span aria-hidden="true" className="w-2 shrink-0 text-ink-faint">
          {open ? '▾' : '▸'}
        </span>
        <span className="min-w-0 flex-1 truncate">{section.path === '' ? '(patch)' : section.path}</span>
      </button>
      {open && (
        <pre className="mb-1.5 max-h-80 overflow-auto rounded-md border border-line bg-field px-2 py-1.5 font-mono text-[10.5px] leading-relaxed text-ink-2">
          {section.body}
        </pre>
      )}
    </div>
  );
}

export function TaskDiff({ taskId }: { taskId: number }): JSX.Element {
  const [open, setOpen] = useState(false);
  const [diff, setDiff] = useState<TaskDiffData | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const load = useCallback((): void => {
    setLoading(true);
    setError(null);
    getBoardTaskDiff(taskId)
      .then(setDiff)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false));
  }, [taskId]);

  // Fetch on first expand, and again whenever a DIFFERENT card is opened while
  // the panel is already expanded — otherwise the second card would render the
  // first one's commits.
  useEffect(() => {
    if (open) load();
  }, [open, load]);

  // Collapse and drop the previous card's data on a task swap: a stale diff
  // under a new card's title is worse than no diff at all.
  useEffect(() => {
    setOpen(false);
    setDiff(null);
    setError(null);
  }, [taskId]);

  const sections = diff !== null ? splitPatch(diff.patch) : [];

  return (
    <div className="rounded-lg border border-line bg-surface/40">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="flex w-full items-center gap-1.5 px-2.5 py-1.5 text-left font-mono text-[10.5px] tracking-[0.08em] text-ink-dim uppercase transition-colors hover:text-ink"
      >
        <span aria-hidden="true" className="w-2 shrink-0">
          {open ? '▾' : '▸'}
        </span>
        diff
        {diff !== null && (
          <span className="ml-auto normal-case tracking-normal text-ink-faint">
            {diff.commits.length} commit{diff.commits.length === 1 ? '' : 's'} ·{' '}
            {diff.files.length} file{diff.files.length === 1 ? '' : 's'}
          </span>
        )}
      </button>

      {open && (
        <div className="border-t border-line px-2.5 py-2">
          {loading && <div className="font-mono text-[10.5px] text-ink-faint">loading diff…</div>}

          {error !== null && (
            <div className="rounded-md border border-red/30 bg-red/5 px-2 py-1.5 font-mono text-[10.5px] whitespace-pre-wrap text-red">
              {error}
            </div>
          )}

          {diff !== null && !loading && error === null && (
            <>
              <div className="pb-1.5 font-mono text-[10px] text-ink-faint">
                {diff.branch} · base {diff.base.slice(0, 10)}
              </div>

              {diff.commits.length === 0 ? (
                <div className="font-mono text-[10.5px] text-ink-faint">
                  the run branch carries no commits ahead of its start point — the agent
                  changed nothing
                </div>
              ) : (
                <ul className="mb-2 flex flex-col gap-0.5">
                  {diff.commits.map((c) => (
                    <li key={c.sha} className="flex items-baseline gap-2 font-mono text-[10.5px]">
                      <span className="shrink-0 text-ink-faint">{c.sha.slice(0, 8)}</span>
                      <span className="min-w-0 flex-1 truncate text-ink-2">{c.subject}</span>
                    </li>
                  ))}
                </ul>
              )}

              {diff.files.length > 0 && (
                <ul className="mb-2 flex flex-col gap-0.5">
                  {diff.files.map((f) => (
                    <li key={f.path} className="flex items-baseline gap-2 font-mono text-[10.5px]">
                      <span className="min-w-0 flex-1 truncate text-ink-2">{f.path}</span>
                      <StatCount additions={f.additions} deletions={f.deletions} />
                    </li>
                  ))}
                </ul>
              )}

              {sections.length > 0 && <div>{sections.map((s, i) => <PatchBlock key={`${s.path}-${String(i)}`} section={s} />)}</div>}

              {diff.patchTruncated && (
                <div className="mt-1.5 font-mono text-[10px] text-ink-faint">
                  diff truncated at 200 KB — open a terminal in the worktree to read the rest
                </div>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
}
