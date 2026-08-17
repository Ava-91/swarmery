package runcore

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MaxRunsEnv bounds how many headless runs the daemon may have in flight AT
// ONCE, across every engine.
const MaxRunsEnv = "SWARMERY_MAX_RUNS"

// DefaultMaxRuns is the budget when MaxRunsEnv says nothing. Four is the number
// dispatch's worktree cap already used, and a run is expensive in a way a
// goroutine is not: each one is a `claude` process editing a git worktree, so the
// ceiling is about the machine and the account, not about scheduling.
const DefaultMaxRuns = 4

// Refusal reasons. They are DIFFERENT answers and must stay distinguishable:
//
//	ErrBusy   this exact run is already in flight → "already running" (a duplicate)
//	ErrNoSlot the pool is full → "try again shortly" (retriable, names its holders)
//
// Collapsing them is how a busy pool ends up reported as a failed run. Neither is
// ever a terminal state: a refused admission must leave no state behind at all.
var (
	ErrBusy   = errors.New("a run is already in flight for this key")
	ErrNoSlot = errors.New("no free run slot")
)

// NoSlotError is ErrNoSlot with the evidence. An operator told only "no free run
// slot" has nowhere to go; the holders are what makes the refusal actionable, and
// they are the whole point of having ONE budget instead of three.
type NoSlotError struct {
	Max     int
	Holders []SlotInfo
}

func (e *NoSlotError) Error() string {
	names := make([]string, 0, len(e.Holders))
	for _, h := range e.Holders {
		names = append(names, h.Key)
	}
	return fmt.Sprintf("%s: %d/%d in flight (%s)", ErrNoSlot, len(e.Holders), e.Max, strings.Join(names, ", "))
}

func (e *NoSlotError) Is(target error) bool { return target == ErrNoSlot }

// SlotInfo is one in-flight run, as the API and the future UI see it.
type SlotInfo struct {
	Key    string    `json:"key"`    // "<engine>:<id>"
	Engine string    `json:"engine"` // dispatch | phaserun | planrun
	ID     int64     `json:"id"`     // task / phase / plan id, as the engine numbers it
	UUID   string    `json:"uuid"`   // the run's session uuid
	Since  time.Time `json:"since"`
}

// SlotKey is the one key format: "<engine>:<id>". One namespace per engine, so
// dispatch task 7 and phase 7 are different runs, and one shared map, so the
// budget is a property of the daemon rather than of each engine.
func SlotKey(engine string, id int64) string {
	return engine + ":" + strconv.FormatInt(id, 10)
}

// MaxRunsFromEnv reads MaxRunsEnv, falling back to DefaultMaxRuns on anything
// unusable — an operator typo must never mean "unbounded", which is the state
// phaserun and planrun were actually in before this existed.
func MaxRunsFromEnv() int {
	raw := strings.TrimSpace(os.Getenv(MaxRunsEnv))
	if raw == "" {
		return DefaultMaxRuns
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		log.Printf("warning: runcore: ignoring invalid %s=%q", MaxRunsEnv, raw)
		return DefaultMaxRuns
	}
	return n
}

// slot is one tracked run. Held by POINTER so a release closure can prove it is
// releasing its own run and not a later one that took the same key.
type slot struct {
	info   SlotInfo
	cancel context.CancelFunc
}

// Slots is the single-flight registry AND the global run budget — one gate every
// engine passes through.
//
// It replaces three in-memory `active` maps that each meant "single-flight for my
// engine" and, in two of the three, nothing else: phaserun and planrun could put
// an unbounded number of `claude` processes on one machine, while dispatch
// enforced a cap of its own against a total it could not see.
//
// The budget counts RUNS, not worktrees: a run is the expensive thing (a process
// and an account's rate limit), and two runs can legitimately share nothing.
// Worktree accounting is a separate gate — see WorktreeCount.
type Slots struct {
	mu     sync.Mutex
	max    int
	active map[string]*slot
}

// NewSlots builds a pool of max runs; max <= 0 takes MaxRunsFromEnv, which is how
// the daemon wires it so one knob governs everything.
func NewSlots(max int) *Slots {
	if max <= 0 {
		max = MaxRunsFromEnv()
	}
	return &Slots{max: max, active: make(map[string]*slot)}
}

// Max is the configured budget (for status endpoints).
func (s *Slots) Max() int { return s.max }

// TryAcquire admits one run. It never blocks: an engine that cannot get a slot
// must tell its caller so (retriable-busy), because the alternative — parking a
// request until a slot frees — turns a 409 an operator can act on into a hung
// HTTP call.
//
// cancel is the run's abort hook (nil when the engine has none); uuid is its
// session uuid. The returned release is idempotent and safe to call from an
// admission-failure path AND from the run goroutine's defer.
func (s *Slots) TryAcquire(key, uuid string, cancel context.CancelFunc) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.active[key]; busy {
		return nil, ErrBusy
	}
	if len(s.active) >= s.max {
		return nil, &NoSlotError{Max: s.max, Holders: s.snapshotLocked()}
	}
	return s.insertLocked(key, uuid, cancel), nil
}

// Adopt tracks a run this process did NOT spawn (adopt.go, after a daemon
// restart) and deliberately ignores the budget: the process is already running,
// so refusing to track it would release the slot and let a rival start in the
// same worktree — the exact failure adoption exists to prevent. Reality wins over
// the cap; the pool stays over-subscribed until the orphan exits, and new
// admissions are refused meanwhile.
//
// A duplicate key is still refused: a run must never be adopted twice.
func (s *Slots) Adopt(key, uuid string, cancel context.CancelFunc) (func(), bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.active[key]; busy {
		return nil, false
	}
	return s.insertLocked(key, uuid, cancel), true
}

// insertLocked records the run and returns its generation-safe release.
func (s *Slots) insertLocked(key, uuid string, cancel context.CancelFunc) func() {
	engine, id := splitKey(key)
	sl := &slot{
		info:   SlotInfo{Key: key, Engine: engine, ID: id, UUID: uuid, Since: time.Now()},
		cancel: cancel,
	}
	s.active[key] = sl
	return func() { s.releaseSlot(key, sl) }
}

// releaseSlot frees the slot only if the key still holds THIS run. The pointer
// check is what makes a double release harmless: the second call finds either
// nothing or a different (later) run under the same key and leaves it alone.
func (s *Slots) releaseSlot(key string, sl *slot) {
	s.mu.Lock()
	if cur, ok := s.active[key]; ok && cur == sl {
		delete(s.active, key)
	}
	s.mu.Unlock()
}

// Release frees a slot by key, reporting whether one was held. This is the shape
// dispatch needs — its release sites hold a task id, not a closure. Prefer the
// closure from TryAcquire where you have it: this cannot tell one generation of a
// key from the next.
func (s *Slots) Release(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.active[key]; !ok {
		return false
	}
	delete(s.active, key)
	return true
}

// IsActive reports whether a run is in flight for this key.
func (s *Slots) IsActive(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.active[key]
	return ok
}

// UUID is the session uuid of the run holding key.
func (s *Slots) UUID(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, ok := s.active[key]
	if !ok {
		return "", false
	}
	return sl.info.UUID, true
}

// Cancel aborts the run holding key and reports whether one was there. It does
// NOT release the slot: the run's own exit path owns that, exactly as it did when
// each engine kept its own map — releasing here would let a Retry in before the
// dying run has let go of its worktree.
func (s *Slots) Cancel(key string) bool {
	s.mu.Lock()
	sl, ok := s.active[key]
	s.mu.Unlock()
	if !ok {
		return false
	}
	if sl.cancel != nil {
		sl.cancel()
	}
	return true
}

// Count is how many runs are in flight across every engine.
func (s *Slots) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.active)
}

// CountEngine is how many runs one engine holds — dispatch's own MaxConcurrent
// gate is expressed against this, so a board cap keeps meaning "board runs".
func (s *Slots) CountEngine(engine string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, sl := range s.active {
		if sl.info.Engine == engine {
			n++
		}
	}
	return n
}

// Snapshot lists the in-flight runs, sorted by key so a status endpoint and the
// UI reading it never flicker on map order.
func (s *Slots) Snapshot() []SlotInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *Slots) snapshotLocked() []SlotInfo {
	out := make([]SlotInfo, 0, len(s.active))
	for _, sl := range s.active {
		out = append(out, sl.info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// splitKey parses "<engine>:<id>". A malformed key degrades to engine-only with
// id 0 rather than failing: the key is an internal convention, and losing the
// numeric id in a status snapshot must never break an admission.
func splitKey(key string) (string, int64) {
	engine, rawID, found := strings.Cut(key, ":")
	if !found {
		return key, 0
	}
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		return engine, 0
	}
	return engine, id
}

// Go runs fn in a goroutine that cannot take the daemon down with it, through the
// caller's spawn seam when one is wired (tests substitute a synchronous one).
// engine only names the log line. This replaces five identical copies that
// differed in that label alone.
func Go(engine string, spawn func(func()), fn func()) {
	wrapped := func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("error: %s: goroutine panic recovered: %v", engine, r)
			}
		}()
		fn()
	}
	if spawn != nil {
		spawn(wrapped)
		return
	}
	go wrapped()
}
