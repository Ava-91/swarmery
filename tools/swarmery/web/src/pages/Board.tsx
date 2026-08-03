// Board page (fusion phase 4): the kanban board of a project workspace. Six
// columns Triage→Todo→In Progress→In Review→Done→Archived. Native HTML5
// drag&drop (Fusion's choice — no dnd lib): dragstart serializes the task id,
// a column drop issues an OPTIMISTIC PATCH that reverts on error (the revert +
// toast live in useBoard). Every card also carries a keyboard "move to →" menu
// so drag is never the only path. A "+ New task" button sits at the top of
// Triage and opens NewTaskModal (the full intake form); the
// Done column is sorted by columnMovedAt desc; Archived is lazy — it loads
// on first expand (boardColumn='archived' fetch) to keep the default view light.
//
// The board reads the shared BoardState from the workspace layout so the card,
// the status bar, and the detail modal all reflect one source of truth. Demo
// mode (VITE_MOCK) renders a full board from fixtures.

import { useCallback, useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import type { BoardColumn, BoardTask } from '../api/types';
import { fetchBoardTasks } from '../api';
import { useProjectWorkspace } from '../workspace/ProjectContext';
import { useWorkspaceBoard } from '../workspace/ProjectWorkspaceLayout';
import { BOARD_COLUMNS, COLUMN_LABELS, matchesLabelFilter, uniqueLabels } from '../workspace/boardModel';
import { NewTaskModal } from '../workspace/NewTaskModal';
import { TaskCard } from '../workspace/TaskCard';
import { TaskModal } from '../workspace/TaskModal';
import { TaskGraph } from '../workspace/TaskGraph';
import { Empty, ErrorBox, Loading } from '../components/ui';

/** Board header view mode: the kanban columns or the dependency graph. */
type BoardView = 'board' | 'graph';

/** Live columns render immediately; Archived is fetched only when expanded. */
const EAGER_COLUMNS: BoardColumn[] = ['triage', 'todo', 'in_progress', 'in_review', 'done'];

export function Board(): JSX.Element {
  const { project, projectId, loading: projLoading } = useProjectWorkspace();
  const board = useWorkspaceBoard();
  // Agent Hub "Run now" deep-links here with ?compose=@<agent>: — the modal
  // opens prefilled from it (the agent picker resolves the "@name:" prefix) so a
  // task can be dispatched to that agent in one hop.
  const [searchParams, setSearchParams] = useSearchParams();
  const compose = searchParams.get('compose') ?? '';
  const [composing, setComposing] = useState(compose !== '');
  const [openId, setOpenId] = useState<number | null>(null);
  const [view, setView] = useState<BoardView>('board');
  const [draggingId, setDraggingId] = useState<number | null>(null);
  const [dropCol, setDropCol] = useState<BoardColumn | null>(null);

  // Archived lazy state — separate from the live board query.
  const [archived, setArchived] = useState<BoardTask[] | null>(null);
  const [archiveOpen, setArchiveOpen] = useState(false);
  const [archiveLoading, setArchiveLoading] = useState(false);

  // Label filter — kept in the URL (not local state) so `?label=jira-ticket`
  // is both the write target and the read source: a reload restores it with
  // no separate rehydration step to keep in sync.
  const labelFilter = searchParams.get('label');
  const setLabelFilter = useCallback(
    (label: string | null): void => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          if (label === null || label === '') next.delete('label');
          else next.set('label', label);
          return next;
        },
        { replace: true },
      );
    },
    [setSearchParams],
  );
  const availableLabels = useMemo(() => uniqueLabels(board.tasks), [board.tasks]);
  // Filtered BEFORE the column split so Archived (its own lazy fetch) and the
  // Done sort are untouched — this list only ever feeds byColumn below.
  const labelFilteredTasks = useMemo(
    () => board.tasks.filter((t) => matchesLabelFilter(t, labelFilter)),
    [board.tasks, labelFilter],
  );

  const byColumn = useMemo(() => {
    const map = new Map<BoardColumn, BoardTask[]>();
    for (const c of BOARD_COLUMNS) map.set(c, []);
    for (const t of labelFilteredTasks) map.get(t.boardColumn)?.push(t);
    // Done: most-recently-moved first (spec).
    map.get('done')?.sort((a, b) => (b.columnMovedAt ?? '').localeCompare(a.columnMovedAt ?? ''));
    return map;
  }, [labelFilteredTasks]);

  const openArchive = (): void => {
    setArchiveOpen((v) => !v);
    if (archived === null && projectId !== null) {
      setArchiveLoading(true);
      fetchBoardTasks(projectId, 'archived')
        .then(setArchived)
        .catch(() => setArchived([]))
        .finally(() => setArchiveLoading(false));
    }
  };

  const openTask = openId !== null ? board.tasks.find((t) => t.id === openId) ?? null : null;

  // The Archived column renders from its own lazy fetch, so a deleted row has to
  // be dropped there too — the board list alone would leave the card on screen
  // until the next expand.
  const deleteTask = (id: number): Promise<void> =>
    board.deleteTask(id).then(() => {
      setArchived((prev) => (prev === null ? prev : prev.filter((t) => t.id !== id)));
    });

  if (projLoading) return <Loading label="workspace…" />;
  if (project === null) {
    return (
      <div className="px-4 py-8 desk:px-8">
        <Empty>unknown project — pick one from the switcher</Empty>
      </div>
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col px-3 py-4 desk:px-5">
      {board.error !== null && (
        <div className="mb-2">
          <ErrorBox message={board.error} onRetry={board.reload} />
        </div>
      )}
      {board.actionError !== null && (
        <div
          role="alert"
          className="mb-2 flex items-center gap-2 rounded-lg border border-red/40 bg-red/10 px-3 py-1.5 font-mono text-[11px] text-red"
        >
          <span className="min-w-0 flex-1">{board.actionError}</span>
          <button
            type="button"
            onClick={board.clearActionError}
            aria-label="dismiss"
            className="text-red/70 transition-colors hover:text-red"
          >
            ×
          </button>
        </div>
      )}

      {/* Board ⇄ Graph toggle. */}
      <div className="mb-3 flex items-center gap-2">
        <div className="flex items-center gap-1" role="group" aria-label="board view">
          {(['board', 'graph'] as const).map((v) => (
            <button
              key={v}
              type="button"
              onClick={() => setView(v)}
              aria-pressed={view === v}
              className={`rounded-lg border px-2.5 py-1 font-mono text-[11px] capitalize transition-colors ${
                view === v
                  ? 'border-line-strong bg-surface2 text-brand'
                  : 'border-transparent text-ink-dim hover:bg-surface2/50 hover:text-ink'
              }`}
            >
              {v === 'board' ? '▤ Board' : '⋈ Graph'}
            </button>
          ))}
        </div>

        {(availableLabels.length > 0 || labelFilter !== null) && (
          <div className="flex items-center gap-1">
            <select
              value={labelFilter ?? ''}
              onChange={(e) => setLabelFilter(e.target.value === '' ? null : e.target.value)}
              aria-label="filter by label"
              className={`rounded-lg border px-2 py-1 font-mono text-[11px] outline-none transition-colors ${
                labelFilter !== null
                  ? 'border-brand/50 bg-brand/10 text-brand'
                  : 'border-line bg-surface text-ink-dim hover:bg-surface2/50'
              }`}
            >
              <option value="">label: any</option>
              {availableLabels.map((l) => (
                <option key={l} value={l}>
                  {l}
                </option>
              ))}
            </select>
            {labelFilter !== null && (
              <button
                type="button"
                onClick={() => setLabelFilter(null)}
                aria-label="clear label filter"
                data-tip="clear label filter"
                className="text-[13px] leading-none text-ink-faint transition-colors hover:text-ink"
              >
                ×
              </button>
            )}
          </div>
        )}
      </div>

      {board.loading ? (
        <Loading label="board…" />
      ) : view === 'graph' ? (
        <TaskGraph tasks={board.tasks} onOpen={setOpenId} />
      ) : (
        <div className="flex min-h-0 flex-1 gap-3 overflow-x-auto pb-2">
          {EAGER_COLUMNS.map((col) => {
            const tasks = byColumn.get(col) ?? [];
            const isDropTarget = dropCol === col;
            return (
              <section
                key={col}
                aria-label={`${COLUMN_LABELS[col]} column`}
                onDragOver={(e) => {
                  e.preventDefault();
                  e.dataTransfer.dropEffect = 'move';
                  if (dropCol !== col) setDropCol(col);
                }}
                onDragLeave={(e) => {
                  // Only clear when leaving the column, not moving between its cards.
                  if (!e.currentTarget.contains(e.relatedTarget as Node)) setDropCol((c) => (c === col ? null : c));
                }}
                onDrop={(e) => {
                  e.preventDefault();
                  const raw = e.dataTransfer.getData('text/plain');
                  const id = Number.parseInt(raw, 10);
                  setDropCol(null);
                  setDraggingId(null);
                  if (!Number.isNaN(id)) board.moveTask(id, col);
                }}
                className={`flex w-[248px] shrink-0 flex-col rounded-xl border transition-colors ${
                  isDropTarget ? 'border-brand/50 bg-brand/5' : 'border-line bg-surface/40'
                }`}
              >
                <div className="flex items-center gap-2 px-3 pt-3 pb-2">
                  <span className="font-mono text-[10.5px] tracking-[0.1em] text-ink-dim uppercase">
                    {COLUMN_LABELS[col]}
                  </span>
                  <span className="font-mono text-[10px] text-ink-faint">{tasks.length}</span>
                </div>
                <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto px-2 pb-2">
                  {col === 'triage' && projectId !== null && (
                    <button
                      type="button"
                      onClick={() => setComposing(true)}
                      className="w-full rounded-lg border border-dashed border-line bg-transparent px-2.5 py-2 text-left text-[12px] text-ink-faint transition-colors hover:border-ink-dim hover:bg-field hover:text-ink"
                    >
                      + New task
                    </button>
                  )}
                  {tasks.map((t) => (
                    <TaskCard
                      key={t.id}
                      task={t}
                      dragging={draggingId === t.id}
                      onOpen={() => setOpenId(t.id)}
                      onMove={(to) => board.moveTask(t.id, to)}
                      onDragStart={() => setDraggingId(t.id)}
                      onDragEnd={() => {
                        setDraggingId(null);
                        setDropCol(null);
                      }}
                    />
                  ))}
                  {tasks.length === 0 && col !== 'triage' && (
                    <div className="px-1 py-2 font-mono text-[10px] text-ink-faint">empty</div>
                  )}
                </div>
              </section>
            );
          })}

          {/* Archived — lazy, collapsed by default. */}
          <section
            aria-label="Archived column"
            className="flex w-[248px] shrink-0 flex-col rounded-xl border border-line bg-surface/20"
          >
            <button
              type="button"
              onClick={openArchive}
              aria-expanded={archiveOpen}
              className="flex items-center gap-2 px-3 pt-3 pb-2 text-left"
            >
              <span className="font-mono text-[10.5px] tracking-[0.1em] text-ink-dim uppercase">
                {COLUMN_LABELS.archived}
              </span>
              <span aria-hidden="true" className="font-mono text-[9px] text-ink-faint">
                {archiveOpen ? '▾' : '▸'}
              </span>
              {archived !== null && <span className="font-mono text-[10px] text-ink-faint">{archived.length}</span>}
            </button>
            {archiveOpen && (
              <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto px-2 pb-2">
                {archiveLoading ? (
                  <div className="px-1 py-2 font-mono text-[10px] text-ink-faint">loading…</div>
                ) : archived !== null && archived.length > 0 ? (
                  archived.map((t) => (
                    <TaskCard
                      key={t.id}
                      task={t}
                      dragging={false}
                      onOpen={() => setOpenId(t.id)}
                      onMove={(to) => board.moveTask(t.id, to)}
                      onDragStart={() => undefined}
                      onDragEnd={() => undefined}
                    />
                  ))
                ) : (
                  <div className="px-1 py-2 font-mono text-[10px] text-ink-faint">empty</div>
                )}
              </div>
            )}
          </section>
        </div>
      )}

      {composing && projectId !== null && (
        <NewTaskModal
          projectId={projectId}
          projectSlug={project.slug}
          initialText={compose}
          onCreated={board.addTask}
          onClose={() => setComposing(false)}
        />
      )}

      {openTask !== null && (
        <TaskModal
          task={openTask}
          onClose={() => setOpenId(null)}
          onPatch={(patch) => board.patchTask(openTask.id, patch)}
          onDelete={() => deleteTask(openTask.id)}
        />
      )}
    </div>
  );
}
