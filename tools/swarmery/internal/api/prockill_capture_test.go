package api_test

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/api"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/ingest"
)

// fakeClaudeEnv guards the helper process below: the copied test binary only
// behaves as a fake "claude" when re-executed with this set.
const fakeClaudeEnv = "SWARMERY_FAKE_CLAUDE_HELPER"

// TestFakeClaudeHelperProcess is not a test — it is the BODY of the fake claude
// process startFakeClaude spawns (the standard os/exec helper-process pattern).
// It sleeps until the test under exercise signals it, or until cleanup kills it.
func TestFakeClaudeHelperProcess(t *testing.T) {
	if os.Getenv(fakeClaudeEnv) != "1" {
		t.Skip("helper process for startFakeClaude; not a standalone test")
	}
	time.Sleep(2 * time.Minute)
}

// startFakeClaude returns the PID of a live process whose `ps -o comm=` output
// contains "claude" — the identity guard KillSession re-checks immediately
// before it signals anything, so no test can reach the handler's success path
// without one.
//
// It copies THIS test binary to a temp file named "claude" and re-executes it.
// A copy is required rather than a symlink because `comm` is the basename of
// the executed file on Linux and the executed path on macOS, and only a real
// file named "claude" satisfies both. Copying a system binary (/bin/sleep) does
// not work: macOS code signing kills a relocated SIP-protected binary
// immediately, whereas a Go-built binary copies and runs fine.
func startFakeClaude(t *testing.T) int {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	src, err := os.Open(self)
	if err != nil {
		t.Fatalf("open test binary: %v", err)
	}
	defer src.Close()

	path := filepath.Join(t.TempDir(), "claude")
	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		t.Fatalf("create fake claude: %v", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		t.Fatalf("copy test binary: %v", err)
	}
	if err := dst.Close(); err != nil {
		t.Fatalf("close fake claude: %v", err)
	}

	cmd := exec.Command(path, "-test.run=^TestFakeClaudeHelperProcess$")
	cmd.Env = append(os.Environ(), fakeClaudeEnv+"=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fake claude: %v", err)
	}
	// Registered AFTER t.TempDir's own cleanup, so it runs BEFORE the directory
	// is removed (cleanups are LIFO).
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd.Process.Pid
}

// TestKillSession_CapturesSessionCard: 'killed' is a terminal status nothing
// else ever revisits — the ingest status ticker's RecomputeStatuses only scans
// `WHERE status IN ('active','idle')`, so a row this handler flipped to
// 'killed' can never re-enter it. That makes the kill path the ONLY place board
// capture hook B can fire for an operator-killed session; without it, killing a
// stuck session that produced no TodoWrite calls loses that session's context
// from the board permanently, with no later pass to retry.
//
// Mirrors ingest.TestCaptureSessionCardOnStatusTicker, which pins the same two
// properties (exactly one card, exactly one frame) on the completed-by-ticker
// path this one closes the hole beside.
func TestKillSession_CapturesSessionCard(t *testing.T) {
	h := openKillTestDB(t)

	bus := ingest.NewBus()
	api.AttachBus(bus)
	t.Cleanup(func() { api.AttachBus(nil) })
	notes, unsubscribe := bus.Subscribe(32)
	t.Cleanup(unsubscribe)

	const (
		sessionID   = 7
		sessionUUID = "kill-capture-uuid"
	)
	mustExecKill(t, h.DB, `INSERT INTO sessions
		(id, project_id, session_uuid, status, started_at, source, pid, proc_state)
		VALUES (?, 1, ?, 'active', '2024-01-01T00:00:00Z', 'jsonl', ?, 'running')`,
		sessionID, sessionUUID, startFakeClaude(t))
	// The fallback card only exists for a session that actually did something:
	// at least one assistant turn, plus the opening prompt the card is built
	// from. proc_started_at stays NULL so the PID-reuse guard is skipped.
	mustExecKill(t, h.DB, `INSERT INTO turns (session_id, seq, role, started_at)
		VALUES (?, 1, 'assistant', '2024-01-01T00:00:01Z')`, sessionID)
	mustExecKill(t, h.DB, `INSERT INTO events (session_id, ts, type, payload, dedup_key)
		VALUES (?, '2024-01-01T00:00:00Z', 'user_prompt', ?, 'kill-capture-prompt')`,
		sessionID, `{"content":"Unstick the nightly digest job\nit has hung for an hour"}`)

	// force=true is SIGKILL, which needs no escalation — the handler leaves no
	// timer goroutine behind and the test stays deterministic.
	if w := postKillBody(t, h, "7", `{"force":true}`); w.Code != http.StatusAccepted {
		t.Fatalf("kill = %d, want 202; body: %s", w.Code, w.Body.String())
	}

	var status string
	if err := h.DB.QueryRow(
		`SELECT status FROM sessions WHERE id = ?`, sessionID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "killed" {
		t.Fatalf("session status = %q, want killed", status)
	}

	// --- Exactly ONE 'sess:<uuid>' card, built from the opening prompt --------
	var (
		cardID        int64
		title, origin string
		originSession sql.NullInt64
	)
	if err := h.DB.QueryRow(
		`SELECT id, title, origin, origin_session_id FROM tasks WHERE capture_key = ?`,
		"sess:"+sessionUUID,
	).Scan(&cardID, &title, &origin, &originSession); err != nil {
		t.Fatalf("killed todo-less session left no session card: %v", err)
	}
	// Title is the FIRST LINE of the opening prompt: a board column shows one line.
	if title != "Unstick the nightly digest job" {
		t.Errorf("card title = %q, want the first line of the opening prompt", title)
	}
	if origin != "session" || !originSession.Valid || originSession.Int64 != sessionID {
		t.Errorf("card provenance = %q/%v, want session/%d", origin, originSession, sessionID)
	}
	if n := countKill(t, h.DB, `SELECT COUNT(*) FROM tasks`); n != 1 {
		t.Fatalf("tasks = %d, want exactly 1", n)
	}

	// --- Exactly ONE task_updated frame --------------------------------------
	frames := drainTaskFrames(t, notes)
	if len(frames) != 1 {
		t.Fatalf("task_updated frames = %d, want exactly 1 per real insert", len(frames))
	}
	if frames[0].TaskID != cardID {
		t.Errorf("frame = %+v, want task_updated for card %d", frames[0], cardID)
	}

	// --- A repeat kill mints nothing and stays off the wire -------------------
	// The first kill left proc_state='dead', which the handler 409s on, so
	// re-arm the row with a fresh live process to reach the capture hook again.
	mustExecKill(t, h.DB,
		`UPDATE sessions SET proc_state = 'running', pid = ? WHERE id = ?`,
		startFakeClaude(t), sessionID)

	if w := postKillBody(t, h, "7", `{"force":true}`); w.Code != http.StatusAccepted {
		t.Fatalf("second kill = %d, want 202; body: %s", w.Code, w.Body.String())
	}
	if n := countKill(t, h.DB,
		`SELECT COUNT(*) FROM tasks WHERE capture_key = ?`, "sess:"+sessionUUID); n != 1 {
		t.Errorf("session cards after a repeat kill = %d, want exactly 1 ('sess:<uuid>' must dedupe)", n)
	}
	if extra := drainTaskFrames(t, notes); len(extra) != 0 {
		t.Errorf("repeat kill published %d task_updated frames, want 0 — a no-op must not reach the wire",
			len(extra))
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func postKillBody(t *testing.T, h *api.Handler, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+id+"/kill", strings.NewReader(body))
	req.SetPathValue("id", id)
	w := httptest.NewRecorder()
	h.KillSession(w, req)
	return w
}

func mustExecKill(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func countKill(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

// drainTaskFrames collects the task_updated notifications currently queued,
// ignoring the session_updated traffic the same bus carries.
func drainTaskFrames(t *testing.T, notes <-chan ingest.Notification) []ingest.Notification {
	t.Helper()
	var out []ingest.Notification
	for {
		select {
		case n, ok := <-notes:
			if !ok {
				return out
			}
			if n.Type == ingest.NoteTaskUpdated {
				out = append(out, n)
			}
		case <-time.After(200 * time.Millisecond):
			return out
		}
	}
}
