package api

import (
	"context"
	"log"
	"os/exec"
	"syscall"
	"time"
)

// startResume spawns `claude -r <uuid> -p <text>` in cwd with single-flight per
// session uuid (msgInFlight), sessionMessageTimeout, and session_updated edges.
// It is the ONE resume spawner: both the composer (PostSessionMessage) and the
// planning-wizard endpoints go through it, so a wizard answer and a composer
// message can never race the same transcript — they contend on the same map.
//
// onExit (nil ok) runs after the process exits, BEFORE the slot is released, so
// its state change (planning rolls status back to awaiting_answer on error) is
// already visible when the final session_updated frame goes out. Returns
// (false, nil) when a resume is already in flight for uuid; a non-nil err means
// the claude binary could not be resolved (nothing was spawned).
func startResume(sessionID int64, sessionUUID, cwd, text string, onExit func(err error)) (started bool, err error) {
	bin, binErr := claudeBin()
	if binErr != nil {
		return false, binErr
	}

	ctx, cancel := context.WithTimeout(context.Background(), sessionMessageTimeout)
	msgInFlightMu.Lock()
	if _, busy := msgInFlight[sessionUUID]; busy {
		msgInFlightMu.Unlock()
		cancel()
		return false, nil
	}
	msgInFlight[sessionUUID] = resumeRun{cancel: cancel, startedAt: time.Now()}
	msgInFlightMu.Unlock()

	log.Printf("session_message: resume session id=%d uuid=%s cwd=%q (%d chars)", sessionID, sessionUUID, cwd, len(text))
	go runSessionMessage(ctx, cancel, sessionID, bin, sessionUUID, cwd, text, onExit)
	return true, nil
}

// resumeInFlight reports whether a headless resume is currently running for a
// session uuid — the planning service's "process alive" seam (its raw-fallback
// parsing and stale-generating reconcile must not fire mid-resume).
func resumeInFlight(sessionUUID string) bool {
	msgInFlightMu.Lock()
	_, ok := msgInFlight[sessionUUID]
	msgInFlightMu.Unlock()
	return ok
}

// runSessionMessage spawns the detached resume run. It does not parse stdout —
// the ingest watcher is the source of truth for the resulting turns; here we
// only log completion/failure and publish session_updated at the run's edges so
// the composer flips to Stop (and back) while it is in flight. onExit (nil ok)
// observes the process outcome before the slot release (see startResume).
func runSessionMessage(ctx context.Context, cancel context.CancelFunc, id int64, bin, sessionUUID, cwd, text string, onExit func(err error)) {
	var runErr error
	defer func() {
		if onExit != nil {
			onExit(runErr)
		}
		cancel()
		msgInFlightMu.Lock()
		delete(msgInFlight, sessionUUID)
		msgInFlightMu.Unlock()
		publishSessionUpdated(id) // resumeInFlight is now false → composer shows Send
	}()
	publishSessionUpdated(id) // resumeInFlight is now true → composer shows Stop

	cmd := exec.CommandContext(ctx, bin, "-r", sessionUUID, "-p", text, "--output-format", "json")
	cmd.Dir = cwd
	// Own process group: a daemon restart (make install / launchd job stop)
	// SIGKILLs the daemon's process group — without this, every in-flight
	// dashboard-driven session dies mid-turn. Detached children survive as
	// procwatch 'orphaned' and finish their work.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		runErr = err
		log.Printf("session_message: resume uuid=%s ended: %v — output: %s", sessionUUID, err, truncateOutput(string(out), 500))
		return
	}
	log.Printf("session_message: resume uuid=%s completed", sessionUUID)
}
