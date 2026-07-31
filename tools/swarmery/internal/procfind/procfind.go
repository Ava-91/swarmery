// Package procfind locates a headless run's process by the session uuid the
// daemon generated for it.
//
// Every headless spawn in this daemon passes `--session-id <uuid>`, so the uuid
// is an exact, daemon-minted key into the process table — no cwd, start-time or
// command-name heuristics needed (contrast internal/procwatch, which has only a
// pid or a directory to go on).
//
// It exists because a run spawned in its own process group (internal/procgroup)
// OUTLIVES a daemon restart. The restarted daemon therefore cannot assume that a
// row left 'running' is dead: it has to look. What each service does with the
// answer differs — phaserun and planrun adopt the survivor, verify kills it
// because its verdict is unrecoverable — but they all ask this one question.
package procfind

import (
	"log"
	"os/exec"
	"strconv"
	"strings"
)

// BySessionUUID returns the pid of a live `claude` process whose argv carries the
// given session uuid.
//
// -ww disables ps's terminal-width truncation: a phase-run argv embeds an entire
// phase document, and a truncated line would hide the --session-id that follows
// it, making every long run look dead.
func BySessionUUID(sessionUUID string) (int, bool) {
	uuid := strings.TrimSpace(sessionUUID)
	if uuid == "" {
		return 0, false // nothing to match on — never guess
	}
	out, err := exec.Command("ps", "-axww", "-o", "pid=,command=").Output()
	if err != nil {
		// A failed scan is "unknown", not "dead". Callers treat false as "no live
		// process", so log loudly enough that a systematically broken ps is visible.
		log.Printf("warning: procfind: ps scan for uuid=%s: %v", uuid, err)
		return 0, false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, uuid) || !strings.Contains(strings.ToLower(line), "claude") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, convErr := strconv.Atoi(fields[0])
		if convErr != nil {
			continue
		}
		return pid, true
	}
	return 0, false
}
