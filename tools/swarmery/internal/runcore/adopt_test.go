package runcore

import (
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeTracked is a scripted engine: the candidates it reports, and a record of the
// hooks runcore called.
type fakeTracked struct {
	candidates []Candidate
	scanErr    error
	skip       map[int64]bool // ids whose Adopt refuses (unloadable row)

	mu             sync.Mutex
	adopted        []int64
	ended          []int64
	cancelledOnEnd []bool
	kills          []int64
}

func (f *fakeTracked) Engine() string { return "faketest" }

func (f *fakeTracked) ScanRunning() ([]Candidate, error) { return f.candidates, f.scanErr }

func (f *fakeTracked) Adopt(c Candidate, pid int) (AdoptHooks, bool) {
	if f.skip[c.ID] {
		return AdoptHooks{}, false
	}
	return AdoptHooks{
		Kill: func() {
			f.mu.Lock()
			f.kills = append(f.kills, c.ID)
			f.mu.Unlock()
		},
		Adopted: func() {
			f.mu.Lock()
			f.adopted = append(f.adopted, c.ID)
			f.mu.Unlock()
		},
		Ended: func(cancelled bool) {
			f.mu.Lock()
			f.ended = append(f.ended, c.ID)
			f.cancelledOnEnd = append(f.cancelledOnEnd, cancelled)
			f.mu.Unlock()
		},
	}, true
}

func (f *fakeTracked) snapshot() (adopted, ended []int64, cancelled []bool, kills []int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.adopted...), append([]int64(nil), f.ended...),
		append([]bool(nil), f.cancelledOnEnd...), append([]int64(nil), f.kills...)
}

// The four reasons a 'running' row is NOT adopted, in one pass: no uuid to match a
// process by, no live process, an engine that refuses the row (unloadable), and a
// key already tracked. Each leaves the row to the heal sweep instead.
func TestAdoptSurvivors_WhoGetsAdopted(t *testing.T) {
	tr := &fakeTracked{
		candidates: []Candidate{
			{ID: 1, UUID: "live"},
			{ID: 2, UUID: ""},     // never got a uuid: nothing to match
			{ID: 3, UUID: "gone"}, // no live process
			{ID: 4, UUID: "unloadable"},
		},
		skip: map[int64]bool{4: true},
	}
	slots := NewSlots(1) // deliberately smaller than the number of survivors
	a := Adopter{
		Slots:     slots,
		Tracked:   tr,
		FindRun:   func(uuid string) (int, bool) { return 4242, uuid != "gone" },
		ProcAlive: func(int) bool { return true },
		Poll:      time.Millisecond,
		Go:        func(fn func()) { go fn() },
	}

	adopted, err := a.AdoptSurvivors()
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{1}; !reflect.DeepEqual(adopted, want) {
		t.Errorf("adopted = %v, want %v", adopted, want)
	}
	if !slots.IsActive(SlotKey("faketest", 1)) {
		t.Error("the adopted run is not holding a slot")
	}
	gotAdopted, _, _, _ := tr.snapshot()
	if want := []int64{1}; !reflect.DeepEqual(gotAdopted, want) {
		t.Errorf("Adopted hook fired for %v, want %v", gotAdopted, want)
	}

	// A second pass must not adopt the same run twice.
	again, err := a.AdoptSurvivors()
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Errorf("second pass adopted %v, want nothing", again)
	}
}

// The watcher's whole job: poll until the pid is gone, release the slot, then let
// the engine write the terminal state — once.
func TestAdoptSurvivors_WatcherEndsTheRunOnce(t *testing.T) {
	tr := &fakeTracked{candidates: []Candidate{{ID: 7, UUID: "live"}}}
	slots := NewSlots(2)
	var alive atomic.Bool
	alive.Store(true)
	a := Adopter{
		Slots:     slots,
		Tracked:   tr,
		FindRun:   func(string) (int, bool) { return 99, true },
		ProcAlive: func(int) bool { return alive.Load() },
		Poll:      time.Millisecond,
		Go:        func(fn func()) { go fn() },
	}
	if _, err := a.AdoptSurvivors(); err != nil {
		t.Fatal(err)
	}

	_, ended, _, _ := tr.snapshot()
	if len(ended) != 0 {
		t.Fatal("Ended fired while the process was still alive")
	}

	alive.Store(false)
	waitUntil(t, func() bool {
		_, ended, _, _ := tr.snapshot()
		return len(ended) == 1
	}, "the watcher to end the run")

	if slots.IsActive(SlotKey("faketest", 7)) {
		t.Error("slot still held after the adopted run ended")
	}
	_, ended, cancelled, _ := tr.snapshot()
	if !reflect.DeepEqual(ended, []int64{7}) {
		t.Errorf("ended = %v, want [7]", ended)
	}
	if cancelled[0] {
		t.Error("a run that simply exited was reported as cancelled")
	}
}

// A Stop on an adopted run kills the orphan's group AND is remembered, because
// "the operator stopped it" is the one thing about the outcome adoption can still
// know — everything else about an orphan's exit is unrecoverable.
func TestAdoptSurvivors_CancelIsRememberedAsCancelled(t *testing.T) {
	tr := &fakeTracked{candidates: []Candidate{{ID: 8, UUID: "live"}}}
	slots := NewSlots(2)
	var alive atomic.Bool
	alive.Store(true)
	a := Adopter{
		Slots:     slots,
		Tracked:   tr,
		FindRun:   func(string) (int, bool) { return 99, true },
		ProcAlive: func(int) bool { return alive.Load() },
		Poll:      time.Millisecond,
		Go:        func(fn func()) { go fn() },
	}
	if _, err := a.AdoptSurvivors(); err != nil {
		t.Fatal(err)
	}

	if !slots.Cancel(SlotKey("faketest", 8)) {
		t.Fatal("Cancel found no live run")
	}
	_, _, _, kills := tr.snapshot()
	if !reflect.DeepEqual(kills, []int64{8}) {
		t.Errorf("kills = %v, want [8] — a Stop must reach the orphan's process group", kills)
	}

	alive.Store(false)
	waitUntil(t, func() bool {
		_, ended, _, _ := tr.snapshot()
		return len(ended) == 1
	}, "the watcher to end the cancelled run")
	_, _, cancelled, _ := tr.snapshot()
	if !cancelled[0] {
		t.Error("the terminal write was not told the run had been cancelled")
	}
}

// Something else closing the run out first (a Stop that already stamped, a rescan)
// must stop the watcher stamping over an outcome that is already recorded.
func TestAdoptSurvivors_ReleasedElsewhereSkipsTheTerminalWrite(t *testing.T) {
	tr := &fakeTracked{candidates: []Candidate{{ID: 9, UUID: "live"}}}
	slots := NewSlots(2)
	var alive atomic.Bool
	alive.Store(true)
	a := Adopter{
		Slots:     slots,
		Tracked:   tr,
		FindRun:   func(string) (int, bool) { return 99, true },
		ProcAlive: func(int) bool { return alive.Load() },
		Poll:      time.Millisecond,
		Go:        func(fn func()) { go fn() },
	}
	if _, err := a.AdoptSurvivors(); err != nil {
		t.Fatal(err)
	}

	slots.Release(SlotKey("faketest", 9)) // someone else closed it out
	alive.Store(false)

	// Give the watcher room to do the wrong thing.
	time.Sleep(50 * time.Millisecond)
	_, ended, _, _ := tr.snapshot()
	if len(ended) != 0 {
		t.Errorf("Ended fired %v times over an already-closed run", len(ended))
	}
}

func TestHealExcluding(t *testing.T) {
	db := wtFixture(t)
	taskID := insertWorkspaceTask(t, db, "2026-08-01-epic")
	mustExec(t, db, `INSERT INTO plan_runs (workspace_task_id, run_state) VALUES (?, 'running')`, taskID)

	// With the row excluded, nothing is healed — `NOT IN` must be built only when
	// there is something to exclude, and must actually exclude it.
	n, err := HealExcluding(db,
		`UPDATE plan_runs SET run_state='failed' WHERE run_state='running'`,
		"workspace_task_id", []int64{taskID})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("healed %d rows, want 0 — the adopted row was excluded", n)
	}

	// With nothing to exclude, the clause is absent and the row heals.
	n, err = HealExcluding(db,
		`UPDATE plan_runs SET run_state='failed' WHERE run_state='running'`,
		"workspace_task_id", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("healed %d rows, want 1", n)
	}
}

// A leading placeholder in the UPDATE must survive the exclusion args being
// appended after it — phaserun's heal stamps a timestamp that way.
func TestHealExcluding_OwnArgsComeFirst(t *testing.T) {
	db := wtFixture(t)
	taskID := insertWorkspaceTask(t, db, "2026-08-01-epic")
	mustExec(t, db, `INSERT INTO plan_runs (workspace_task_id, run_state) VALUES (?, 'running')`, taskID)
	other := insertWorkspaceTask(t, db, "2026-08-01-other")
	mustExec(t, db, `INSERT INTO plan_runs (workspace_task_id, run_state) VALUES (?, 'running')`, other)

	n, err := HealExcluding(db,
		`UPDATE plan_runs SET run_state='failed', run_error=? WHERE run_state='running'`,
		"workspace_task_id", []int64{taskID}, "daemon restart")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("healed %d rows, want 1 (only the un-adopted one)", n)
	}
	var note string
	if err := db.QueryRow(`SELECT run_error FROM plan_runs WHERE workspace_task_id=?`, other).Scan(&note); err != nil {
		t.Fatal(err)
	}
	if note != "daemon restart" {
		t.Errorf("run_error = %q, want the UPDATE's own placeholder to have bound", note)
	}
}

func waitUntil(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
