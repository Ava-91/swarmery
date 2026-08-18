package runcore

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSlotKey(t *testing.T) {
	if got := SlotKey("phaserun", 42); got != "phaserun:42" {
		t.Errorf("SlotKey = %q, want phaserun:42", got)
	}
}

func TestMaxRunsFromEnv(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		want      int
	}{
		{"unset", "", DefaultMaxRuns},
		{"override", "7", 7},
		{"unparseable falls back", "lots", DefaultMaxRuns},
		{"zero falls back", "0", DefaultMaxRuns},
		{"negative falls back", "-2", DefaultMaxRuns},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(MaxRunsEnv, tc.env)
			if got := MaxRunsFromEnv(); got != tc.want {
				t.Errorf("MaxRunsFromEnv() = %d, want %d", got, tc.want)
			}
		})
	}
}

// The same key twice is a DUPLICATE run, which is a different refusal from a
// full pool: the caller must say "already running", not "try later".
func TestSlots_DuplicateKeyIsBusy(t *testing.T) {
	s := NewSlots(4)
	release, err := s.TryAcquire("phaserun:1", "u1", nil)
	if err != nil {
		t.Fatalf("first TryAcquire: %v", err)
	}
	if _, err := s.TryAcquire("phaserun:1", "u2", nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("second TryAcquire err = %v, want ErrBusy", err)
	}
	release()
	if _, err := s.TryAcquire("phaserun:1", "u3", nil); err != nil {
		t.Fatalf("TryAcquire after release: %v", err)
	}
}

// A full pool is RETRIABLE and must name its holders: "no free run slot" with
// nothing else in it is an operator dead end, and the whole point of one shared
// budget is being able to see what is holding it.
func TestSlots_BudgetExhaustionNamesHolders(t *testing.T) {
	s := NewSlots(2)
	if _, err := s.TryAcquire("dispatch:7", "ud", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TryAcquire("planrun:3", "up", nil); err != nil {
		t.Fatal(err)
	}

	_, err := s.TryAcquire("phaserun:9", "uf", nil)
	if !errors.Is(err, ErrNoSlot) {
		t.Fatalf("err = %v, want ErrNoSlot", err)
	}
	var noSlot *NoSlotError
	if !errors.As(err, &noSlot) {
		t.Fatalf("err = %v, want a *NoSlotError carrying the holders", err)
	}
	if noSlot.Max != 2 {
		t.Errorf("Max = %d, want 2", noSlot.Max)
	}
	gotKeys := make([]string, 0, len(noSlot.Holders))
	for _, h := range noSlot.Holders {
		gotKeys = append(gotKeys, h.Key)
	}
	if want := []string{"dispatch:7", "planrun:3"}; !reflect.DeepEqual(gotKeys, want) {
		t.Errorf("holders = %v, want %v", gotKeys, want)
	}
	// The message an operator reads must contain the holders, not just a count.
	if msg := err.Error(); !strings.Contains(msg, "dispatch:7") || !strings.Contains(msg, "planrun:3") {
		t.Errorf("error message %q does not name the holders", msg)
	}
}

// Three engines, one pool — the defect this phase closes. phaserun and planrun
// used to be bounded by nothing at all.
func TestSlots_ThreeEngineContention(t *testing.T) {
	s := NewSlots(3)
	for _, k := range []string{"dispatch:1", "phaserun:2", "planrun:3"} {
		if _, err := s.TryAcquire(k, "u", nil); err != nil {
			t.Fatalf("TryAcquire(%s): %v", k, err)
		}
	}
	if s.Count() != 3 {
		t.Fatalf("Count = %d, want 3", s.Count())
	}
	// A fourth run of ANY engine is refused — the budget counts runs, not runs
	// per engine.
	for _, k := range []string{"dispatch:4", "phaserun:5", "planrun:6"} {
		if _, err := s.TryAcquire(k, "u", nil); !errors.Is(err, ErrNoSlot) {
			t.Errorf("TryAcquire(%s) err = %v, want ErrNoSlot", k, err)
		}
	}
	if got := s.CountEngine("dispatch"); got != 1 {
		t.Errorf("CountEngine(dispatch) = %d, want 1", got)
	}
}

// Release is called on every admission-failure path AND from the run goroutine's
// defer, so a double call must not free a slot twice. Pointer identity, not the
// key, is what makes that safe: the second call must also not evict a LATER run
// that has since taken the same key.
func TestSlots_ReleaseIsIdempotentAndKeyGenerationSafe(t *testing.T) {
	s := NewSlots(2)
	release, err := s.TryAcquire("phaserun:1", "first", nil)
	if err != nil {
		t.Fatal(err)
	}
	release()
	release() // idempotent

	if _, err := s.TryAcquire("phaserun:1", "second", nil); err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	release() // the FIRST run's release must not evict the second run
	if !s.IsActive("phaserun:1") {
		t.Error("a stale release() evicted the run that took the key after it")
	}
	if got, _ := s.UUID("phaserun:1"); got != "second" {
		t.Errorf("uuid = %q, want the second run's", got)
	}
}

// Release(key) is the shape dispatch needs: its clearActive sites hold an id,
// not a closure.
func TestSlots_ReleaseByKey(t *testing.T) {
	s := NewSlots(2)
	if _, err := s.TryAcquire("dispatch:5", "u", nil); err != nil {
		t.Fatal(err)
	}
	if !s.Release("dispatch:5") {
		t.Error("Release reported nothing released")
	}
	if s.Release("dispatch:5") {
		t.Error("Release reported a second release of the same key")
	}
	if s.IsActive("dispatch:5") {
		t.Error("key still active after Release")
	}
}

// Adoption is not admission: the run is ALREADY executing, so refusing it
// because the pool is full would free the slot and let a rival start in the same
// worktree — the exact defect adoption exists to prevent. Adopt therefore
// over-subscribes deliberately; reality wins over the cap.
func TestSlots_AdoptIgnoresBudgetButNotDuplicates(t *testing.T) {
	s := NewSlots(1)
	if _, err := s.TryAcquire("dispatch:1", "u1", nil); err != nil {
		t.Fatal(err)
	}
	release, ok := s.Adopt("phaserun:2", "u2", nil)
	if !ok {
		t.Fatal("Adopt refused a run that is already executing")
	}
	if s.Count() != 2 {
		t.Errorf("Count = %d, want 2 — an adopted run may exceed the budget", s.Count())
	}
	if _, ok := s.Adopt("phaserun:2", "u3", nil); ok {
		t.Error("Adopt accepted a duplicate key — a run must never be adopted twice")
	}
	release()
	if s.IsActive("phaserun:2") {
		t.Error("adopted slot not released")
	}
}

// A full pool must still refuse new admissions while over-subscribed.
func TestSlots_OverSubscribedPoolStillRefuses(t *testing.T) {
	s := NewSlots(1)
	if _, ok := s.Adopt("planrun:1", "u1", nil); !ok {
		t.Fatal("Adopt failed")
	}
	if _, ok := s.Adopt("planrun:2", "u2", nil); !ok {
		t.Fatal("Adopt failed")
	}
	if _, err := s.TryAcquire("dispatch:3", "u3", nil); !errors.Is(err, ErrNoSlot) {
		t.Errorf("err = %v, want ErrNoSlot", err)
	}
}

func TestSlots_CancelInvokesTheRunsCancel(t *testing.T) {
	s := NewSlots(2)
	cancelled := false
	if _, err := s.TryAcquire("phaserun:1", "u", func() { cancelled = true }); err != nil {
		t.Fatal(err)
	}
	if !s.Cancel("phaserun:1") {
		t.Fatal("Cancel reported no live run")
	}
	if !cancelled {
		t.Error("the run's cancel was not called")
	}
	// Cancel does NOT release: the run goroutine's own exit path does, exactly as
	// it did when each engine kept its own map.
	if !s.IsActive("phaserun:1") {
		t.Error("Cancel released the slot; the exit path owns that")
	}
	if s.Cancel("phaserun:404") {
		t.Error("Cancel reported a live run for an unknown key")
	}
}

// A nil cancel (dispatch never stored one) must not panic.
func TestSlots_CancelToleratesNilCancel(t *testing.T) {
	s := NewSlots(1)
	if _, err := s.TryAcquire("dispatch:1", "u", nil); err != nil {
		t.Fatal(err)
	}
	if !s.Cancel("dispatch:1") {
		t.Error("Cancel reported no live run")
	}
}

func TestSlots_SnapshotIsSortedAndCarriesIdentity(t *testing.T) {
	s := NewSlots(4)
	for _, k := range []string{"planrun:3", "dispatch:1", "phaserun:2"} {
		if _, err := s.TryAcquire(k, "uuid-"+k, nil); err != nil {
			t.Fatal(err)
		}
	}
	snap := s.Snapshot()
	gotKeys := make([]string, 0, len(snap))
	for _, si := range snap {
		gotKeys = append(gotKeys, si.Key)
	}
	if want := []string{"dispatch:1", "phaserun:2", "planrun:3"}; !reflect.DeepEqual(gotKeys, want) {
		t.Errorf("Snapshot keys = %v, want %v (sorted, so a UI never flickers)", gotKeys, want)
	}
	for _, si := range snap {
		if si.UUID != "uuid-"+si.Key {
			t.Errorf("slot %s carries uuid %q", si.Key, si.UUID)
		}
		if si.Engine == "" || si.ID == 0 {
			t.Errorf("slot %s did not split into engine+id: %+v", si.Key, si)
		}
		if si.Since.IsZero() {
			t.Errorf("slot %s has no start time", si.Key)
		}
	}
}

// NewSlots(0) takes the env default: the daemon wires it that way so one knob
// governs the whole pool.
func TestNewSlots_ZeroMaxTakesTheEnvDefault(t *testing.T) {
	t.Setenv(MaxRunsEnv, "3")
	if got := NewSlots(0).Max(); got != 3 {
		t.Errorf("Max = %d, want 3", got)
	}
}

// The pool is hit from the dispatch loop, two service goroutines and the API at
// once; -race would find a missing lock here.
func TestSlots_ConcurrentAcquireRespectsTheBudget(t *testing.T) {
	s := NewSlots(4)
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		acquired int
	)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.TryAcquire(SlotKey("dispatch", int64(i)), "u", nil); err == nil {
				mu.Lock()
				acquired++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if acquired != 4 {
		t.Errorf("%d goroutines got a slot, want exactly 4", acquired)
	}
	if s.Count() != 4 {
		t.Errorf("Count = %d, want 4", s.Count())
	}
}

// Go is what keeps a panicking run goroutine from taking the daemon with it — the
// one behaviour five engines each had their own copy of.
func TestGo_RecoversAPanicAndUsesTheSpawnSeam(t *testing.T) {
	ran := false
	Go("test", func(fn func()) { fn() }, func() { ran = true })
	if !ran {
		t.Error("fn did not run through the spawn seam")
	}

	// A panic must be contained, not propagated: this call returning at all IS the
	// assertion (an escaping panic fails the test binary, not just this test).
	Go("test", func(fn func()) { fn() }, func() { panic("boom") })

	// With no seam wired it really is a goroutine, so wait for the effect.
	done := make(chan struct{})
	Go("test", nil, func() { close(done) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Go(nil seam) never ran fn")
	}
}

// A malformed key must degrade rather than fail: the key is an internal
// convention, and losing the numeric id in a status snapshot must never break an
// admission.
func TestSlots_MalformedKeyStillAdmits(t *testing.T) {
	s := NewSlots(2)
	if _, err := s.TryAcquire("no-colon", "u", nil); err != nil {
		t.Fatalf("TryAcquire: %v", err)
	}
	if _, err := s.TryAcquire("phaserun:not-a-number", "u", nil); err != nil {
		t.Fatalf("TryAcquire: %v", err)
	}
	snap := s.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("Snapshot = %+v, want both slots", snap)
	}
	for _, si := range snap {
		if si.Engine == "" {
			t.Errorf("slot %q lost its engine label entirely: %+v", si.Key, si)
		}
		if si.ID != 0 {
			t.Errorf("slot %q parsed an id it does not have: %+v", si.Key, si)
		}
	}
}
