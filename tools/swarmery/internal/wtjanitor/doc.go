// Package wtjanitor removes agent worktrees nobody else will.
//
// Two producers create worktrees in a repository and neither sweeps on a
// schedule: the Claude Code harness makes <repo>/.claude/worktrees/agent-<hex>
// for an isolated subagent (and keeps it whenever it is dirty, because it
// cannot know the work was committed onto some other branch), and this daemon
// makes ~/.swarmery/worktrees/<project>/<task> for dispatch/verify/phaserun
// (internal/worktree), reclaimed only when a LATER task happens to want the
// same path. Residue therefore accumulates until a human notices.
//
// The janitor is the out-of-band sweeper. It discovers worktrees by asking git
// (`git worktree list --porcelain`), so both roots are found by one mechanism,
// and it decides per worktree:
//
//   - skip          — a veto fired; nothing was touched
//   - keep-unmerged — the branch carries commits reachable from no other ref
//   - redundant     — clean, or every dirty path's blob is already in git
//   - salvage       — holds content found nowhere in git
//
// # Veto order is load-bearing
//
// Main checkout, then live process/session, then a fresh index.lock, then the
// idle floor — each is checked before anything about the CONTENT is considered.
// A worktree someone paused in five minutes ago must not be removed just
// because its files happen to be committed elsewhere, and a live one must not
// even be inspected. Reordering these is a safety regression, not a style
// choice.
//
// # The invariant
//
// Removal is unreachable except through VerdictRedundant — proven by blob
// identity, never by name, mtime or similarity — or through a SUCCESSFUL
// salvage commit. A salvage that fails degrades the verdict and leaves the
// worktree alone. Nothing this package does can be the reason work was lost.
package wtjanitor
