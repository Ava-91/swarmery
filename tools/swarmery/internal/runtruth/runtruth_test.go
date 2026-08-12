package runtruth

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/claudeprobe"
	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// newRecorder returns a Recorder on a fresh migrated store with a controllable
// clock (the debounce must be testable without sleeping).
func newRecorder(t *testing.T) (*Recorder, *sql.DB, *time.Time) {
	t.Helper()
	db := openDB(t)
	rec := NewRecorder(db)
	now := time.Unix(1765000000, 0).UTC()
	rec.now = func() time.Time { return now }
	return rec, db, &now
}

func mustGet(t *testing.T, db *sql.DB, account string) (store.AccountRunnable, bool) {
	t.Helper()
	row, ok, err := store.GetAccountRunnable(db, account)
	if err != nil {
		t.Fatalf("get verdict: %v", err)
	}
	return row, ok
}

var noLogin = claudeprobe.Result{Status: claudeprobe.StatusNoLogin, Reason: claudeprobe.ReasonNoLogin}

// The core write rule: a run that died demanding a login demotes the account,
// with source='run' and the fixed no-login reason (SC-8's persistence half).
func TestRecordNoLoginWrites(t *testing.T) {
	rec, db, _ := newRecorder(t)
	rec.Record("nabu-org", noLogin)

	row, ok := mustGet(t, db, "nabu-org")
	if !ok {
		t.Fatal("no verdict written")
	}
	if row.Status != "no-login" || row.Source != "run" || row.Reason != claudeprobe.ReasonNoLogin {
		t.Errorf("verdict = %+v, want no-login/run/%q", row, claudeprobe.ReasonNoLogin)
	}
}

// The runners spell the default account "" — the recorder writes it under its
// registry key, never under an empty one.
func TestRecordEmptyAccountIsDefault(t *testing.T) {
	rec, db, _ := newRecorder(t)
	rec.Record("", noLogin)

	if _, ok := mustGet(t, db, ""); ok {
		t.Error("verdict written under empty account key")
	}
	if row, ok := mustGet(t, db, "default"); !ok || row.Status != "no-login" {
		t.Errorf("default-account verdict = %+v ok=%v, want no-login", row, ok)
	}
}

// unknown is never written from the run path: an ordinary task failure is not
// evidence about the account and must not erase a good verdict.
func TestRecordUnknownIsDropped(t *testing.T) {
	rec, db, _ := newRecorder(t)
	if err := store.PutAccountRunnable(db, "nabu-org", "ready", "", "probe", time.Unix(1764000000, 0)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rec.Record("nabu-org", claudeprobe.Result{Status: claudeprobe.StatusUnknown, Reason: claudeprobe.ReasonUnrecognised})

	row, _ := mustGet(t, db, "nabu-org")
	if row.Status != "ready" || row.Source != "probe" {
		t.Errorf("verdict = %+v, want the seeded ready/probe row untouched", row)
	}
}

// A successful run is weak evidence: it must not overwrite a probe-sourced
// verdict (ready or otherwise), and it must not create a first row either.
func TestRecordReadyDoesNotOverwriteOrCreate(t *testing.T) {
	rec, db, _ := newRecorder(t)
	ready := claudeprobe.Result{Status: claudeprobe.StatusReady}

	rec.Record("never-seen", ready)
	if _, ok := mustGet(t, db, "never-seen"); ok {
		t.Error("ready created a verdict for a never-probed account")
	}

	seeded := time.Unix(1764000000, 0)
	if err := store.PutAccountRunnable(db, "nabu-org", "ready", "", "probe", seeded); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rec.Record("nabu-org", ready)
	row, _ := mustGet(t, db, "nabu-org")
	if row.Source != "probe" || !row.CheckedAt.Equal(seeded.UTC()) {
		t.Errorf("verdict = %+v, want the probe row byte-untouched", row)
	}
}

// The self-healing exception: a run that succeeds while the stored verdict is
// no-login clears it to ready with source='run' — the operator logged in
// outside the dashboard, and a false alarm must come down.
func TestRecordReadyClearsStoredNoLogin(t *testing.T) {
	rec, db, _ := newRecorder(t)
	if err := store.PutAccountRunnable(db, "nabu-org", "no-login", claudeprobe.ReasonNoLogin, "run", time.Unix(1764000000, 0)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rec.Record("nabu-org", claudeprobe.Result{Status: claudeprobe.StatusReady})

	row, _ := mustGet(t, db, "nabu-org")
	if row.Status != "ready" || row.Source != "run" || row.Reason != "" {
		t.Errorf("verdict = %+v, want ready/run with empty reason", row)
	}
}

// At most one run-sourced write per account per minute: a burst of failing
// dispatches lands one row-write; a different account is not throttled by it;
// after the window the next write goes through.
func TestRecordDebounce(t *testing.T) {
	rec, db, now := newRecorder(t)

	rec.Record("nabu-org", noLogin)
	first, _ := mustGet(t, db, "nabu-org")

	*now = now.Add(30 * time.Second)
	rec.Record("nabu-org", noLogin)
	second, _ := mustGet(t, db, "nabu-org")
	if !second.CheckedAt.Equal(first.CheckedAt) {
		t.Errorf("second write inside the window landed: checkedAt %v → %v", first.CheckedAt, second.CheckedAt)
	}

	rec.Record("other-org", noLogin)
	if _, ok := mustGet(t, db, "other-org"); !ok {
		t.Error("debounce throttled a DIFFERENT account")
	}

	*now = now.Add(31 * time.Second) // 61s after the first write
	rec.Record("nabu-org", noLogin)
	third, _ := mustGet(t, db, "nabu-org")
	if third.CheckedAt.Equal(first.CheckedAt) {
		t.Error("write after the debounce window was still throttled")
	}
}

// The debounce also holds across the recovery path — ready-after-no-login is a
// write like any other.
func TestRecordDebounceCoversRecovery(t *testing.T) {
	rec, db, now := newRecorder(t)

	rec.Record("nabu-org", noLogin)
	*now = now.Add(10 * time.Second)
	rec.Record("nabu-org", claudeprobe.Result{Status: claudeprobe.StatusReady})

	row, _ := mustGet(t, db, "nabu-org")
	if row.Status != "no-login" {
		t.Errorf("recovery write inside the window landed: status = %q", row.Status)
	}

	*now = now.Add(51 * time.Second)
	rec.Record("nabu-org", claudeprobe.Result{Status: claudeprobe.StatusReady})
	row, _ = mustGet(t, db, "nabu-org")
	if row.Status != "ready" {
		t.Errorf("recovery after the window did not land: status = %q", row.Status)
	}
}
