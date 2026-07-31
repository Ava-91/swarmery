// Package procgroup runs a spawned child in its own process group so that
// cancelling or timing out a run takes the child's whole descendant tree with
// it — not just the direct child.
//
// Why this exists: exec.CommandContext's default cancellation SIGKILLs the
// direct child only. A headless `claude` run is a process tree (shells, node,
// browsers, MCP servers), so killing the leader alone leaves that tree running
// as orphans: they keep writing to a worktree the caller is about to delete,
// keep holding the stderr pipe (so Wait blocks past the deadline), and keep
// burning the machine. Isolate makes cancellation group-scoped; Drain closes
// the gap between "the leader is reaped" and "nothing of this run is left".
//
// Unix-only, like the rest of the daemon's process handling.
package procgroup

import (
	"os/exec"
	"syscall"
	"time"
)

// Default bounds. waitDelay stops Wait from hanging on I/O held by a descendant
// that outlived the group kill; drainGrace bounds how long Drain waits for the
// group to actually empty out.
const (
	defaultWaitDelay  = 2 * time.Second
	defaultDrainGrace = 5 * time.Second
	pollInterval      = 50 * time.Millisecond
)

// Isolate puts cmd in a dedicated process group and routes ctx cancellation to
// the entire group. Call it after building cmd and before starting it.
// waitDelay <= 0 uses defaultWaitDelay.
//
// cmd MUST come from exec.CommandContext: os/exec rejects a non-nil Cancel on a
// command built with exec.Command ("command with a non-nil Cancel was not
// created with CommandContext") — and a group with no cancellation wired is the
// bug this package exists to prevent, so the constraint is deliberate.
func Isolate(cmd *exec.Cmd, waitDelay time.Duration) {
	if waitDelay <= 0 {
		waitDelay = defaultWaitDelay
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return signalGroup(cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = waitDelay
}

// Drain SIGKILLs whatever is still in the process group led by pid (the pid of
// a command started under Isolate) and blocks until the group is empty or grace
// elapses. It reports whether anything was still alive — i.e. whether the run
// leaked a tree the caller should know about.
//
// Call it after Wait returns, before touching resources the run was using (a
// worktree, a lock): Wait only guarantees the leader is reaped.
//
// grace <= 0 uses defaultDrainGrace. A descendant that escaped the group
// (setsid, or a double-fork reparented to init) is out of reach by design —
// Drain reports the group, not the whole subtree.
func Drain(pid int, grace time.Duration) bool {
	if pid <= 0 || !groupAlive(pid) {
		return false
	}
	if grace <= 0 {
		grace = defaultDrainGrace
	}
	_ = signalGroup(pid, syscall.SIGKILL)
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !groupAlive(pid) {
			break
		}
		time.Sleep(pollInterval)
	}
	return true
}

// Kill SIGKILLs every member of the group led by pid. Use it on a process this
// daemon started under Isolate but no longer owns as a child (an orphan that
// outlived a restart): Cmd.Cancel is gone with the parent, so the group signal
// is the only handle left on the tree.
func Kill(pid int) error {
	if pid <= 0 {
		return nil
	}
	return signalGroup(pid, syscall.SIGKILL)
}

// groupAlive reports whether the group led by pid still has a member. Signal 0
// performs the existence check without delivering anything (ESRCH ⇒ empty).
//
// The leader's pid is reaped by Wait before Drain runs, so in principle the OS
// could recycle it into an unrelated group; pid reuse is slow enough relative to
// the microseconds between Wait and Drain that this is theoretical, and the
// worst case is one stray signal to a process we just failed to kill anyway.
func groupAlive(pid int) bool {
	return signalGroup(pid, 0) == nil
}

// signalGroup sends sig to every member of the group led by pid (negative pid).
func signalGroup(pid int, sig syscall.Signal) error {
	return syscall.Kill(-pid, sig)
}
