package retroanalysis

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/store"
)

// mockRunner returns canned output (or an error) and records the prompt.
type mockRunner struct {
	out    string
	err    error
	block  chan struct{} // when non-nil, Run waits on it before returning
	mu     sync.Mutex
	prompt string
	calls  int
}

func (m *mockRunner) Run(_ context.Context, prompt string) (string, error) {
	m.mu.Lock()
	m.prompt = prompt
	m.calls++
	m.mu.Unlock()
	if m.block != nil {
		<-m.block
	}
	return m.out, m.err
}

func (m *mockRunner) seen() (string, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.prompt, m.calls
}

// digest is a minimal digest offering three citable ids.
const digest = "# Retro digest\n- `tech-lead` [E:agent:tech-lead]\n- R2 [E:rec:7]\n- boom [E:error_group:boom]\n"

const validOut = `## Що болить
tech-lead завалює третину прогонів [E:agent:tech-lead]

## Чому
Один і той самий збій повторюється [E:error_group:boom]

## Що я б змінив
Додати правило схвалення [E:rec:7]
`

// newSvc builds a service over a fresh database whose async run executes
// inline, so every test is deterministic.
func newSvc(t *testing.T, r Runner) (*Service, *sql.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return &Service{
		DB: db, Runner: r,
		Go:  func(fn func()) { fn() },
		Now: func() time.Time { return time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC) },
	}, db
}

func start(t *testing.T, s *Service) *Analysis {
	t.Helper()
	id, err := s.Start("2026-08-10", "2026-08-24", "", digest, "sha-1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	got, err := s.Get(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return got
}

func TestValidOutputBecomesProposedWithCountedCitations(t *testing.T) {
	s, _ := newSvc(t, &mockRunner{out: validOut})
	got := start(t, s)
	if got.Status != "proposed" {
		t.Fatalf("status = %q (error %q), want proposed", got.Status, got.Error)
	}
	if got.Citations != 3 {
		t.Errorf("citations = %d, want 3 distinct", got.Citations)
	}
	if !strings.Contains(got.Markdown, "## Що я б змінив") {
		t.Error("the analysis body was not stored")
	}
	if got.DigestSHA256 != "sha-1" {
		t.Errorf("digest sha = %q, want the one it was built from", got.DigestSHA256)
	}
}

// SC-4: an uncited analysis is a failure with a reason, never a valid row.
func TestZeroCitationsFails(t *testing.T) {
	s, _ := newSvc(t, &mockRunner{out: "## Що болить\nвсе погано\n\n## Чому\nтому що\n\n## Що я б змінив\nпокращити промпти\n"})
	got := start(t, s)
	if got.Status != "failed" {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if !strings.Contains(got.Error, "cites no evidence") {
		t.Errorf("error = %q, want a human reason naming the missing citations", got.Error)
	}
	if got.Citations != 0 {
		t.Errorf("citations = %d on a failed row, want 0", got.Citations)
	}
	// The rejected text is kept: a refusal you cannot inspect is one you
	// cannot learn from.
	if !strings.Contains(got.Markdown, "все погано") {
		t.Error("the rejected body was discarded")
	}
}

func TestFabricatedCitationFails(t *testing.T) {
	out := strings.Replace(validOut, "[E:rec:7]", "[E:rec:999]", 1)
	s, _ := newSvc(t, &mockRunner{out: out})
	got := start(t, s)
	if got.Status != "failed" {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if !strings.Contains(got.Error, "rec:999") {
		t.Errorf("error = %q, want it to name the invented identifier", got.Error)
	}
}

func TestMissingSectionFails(t *testing.T) {
	out := "## Що болить\nпогано [E:agent:tech-lead]\n\n## Що я б змінив\nзмінити [E:rec:7]\n"
	s, _ := newSvc(t, &mockRunner{out: out})
	got := start(t, s)
	if got.Status != "failed" {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if !strings.Contains(got.Error, "## Чому") {
		t.Errorf("error = %q, want it to name the missing section", got.Error)
	}
}

func TestSectionsOutOfOrderFail(t *testing.T) {
	out := "## Що болить\nx [E:agent:tech-lead]\n\n## Що я б змінив\ny [E:rec:7]\n\n## Чому\nz [E:error_group:boom]\n"
	s, _ := newSvc(t, &mockRunner{out: out})
	got := start(t, s)
	if got.Status != "failed" || !strings.Contains(got.Error, "order") {
		t.Fatalf("status=%q error=%q, want a failure naming the section order", got.Status, got.Error)
	}
}

func TestOversizedChangeSectionFails(t *testing.T) {
	out := "## Що болить\nx [E:agent:tech-lead]\n\n## Чому\ny [E:error_group:boom]\n\n## Що я б змінив\n" +
		strings.Repeat("змінити щось. ", 500) + " [E:rec:7]\n"
	s, _ := newSvc(t, &mockRunner{out: out})
	got := start(t, s)
	if got.Status != "failed" {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if !strings.Contains(got.Error, "6000-byte budget") {
		t.Errorf("error = %q, want it to name the budget it broke", got.Error)
	}
}

func TestRunnerFailureKeepsTheStderrTail(t *testing.T) {
	s, _ := newSvc(t, &mockRunner{err: errors.New("claude -p: exit 1; stderr: model overloaded")})
	got := start(t, s)
	if got.Status != "failed" {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if !strings.Contains(got.Error, "model overloaded") {
		t.Errorf("error = %q, want the runner's stderr tail verbatim", got.Error)
	}
}

func TestSecondStartWhileRunningIsRefused(t *testing.T) {
	block := make(chan struct{})
	r := &mockRunner{out: validOut, block: block}
	// Real goroutine dispatch here: the point is concurrency, so the inline
	// seam would defeat the test.
	db, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	s := &Service{DB: db, Runner: r}

	if _, err := s.Start("2026-08-10", "2026-08-24", "", digest, "sha-1"); err != nil {
		t.Fatalf("first start: %v", err)
	}
	// Wait until the runner is actually in flight before racing it.
	for i := 0; i < 200; i++ {
		if _, calls := r.seen(); calls > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, err = s.Start("2026-08-10", "2026-08-24", "", digest, "sha-2")
	if !errors.Is(err, ErrAnalysisRunning) {
		t.Fatalf("second start err = %v, want ErrAnalysisRunning", err)
	}
	close(block)
	// Only one process was ever started.
	if _, calls := r.seen(); calls != 1 {
		t.Errorf("runner calls = %d, want exactly 1", calls)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM retro_analyses`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("rows = %d, want 1 — the refused start must not insert", n)
	}
}

func TestDecideMovesProposedAndStampsDecidedAt(t *testing.T) {
	for _, to := range []string{"accepted", "dismissed"} {
		t.Run(to, func(t *testing.T) {
			s, _ := newSvc(t, &mockRunner{out: validOut})
			got := start(t, s)
			after, err := s.Decide(got.ID, to)
			if err != nil {
				t.Fatalf("decide: %v", err)
			}
			if after.Status != to {
				t.Errorf("status = %q, want %q", after.Status, to)
			}
			if after.DecidedAt == nil || *after.DecidedAt == "" {
				t.Error("decided_at was not stamped")
			}
		})
	}
}

func TestDecideRejectsEveryOtherTransition(t *testing.T) {
	s, _ := newSvc(t, &mockRunner{err: errors.New("boom")})
	failed := start(t, s)
	if _, err := s.Decide(failed.ID, "accepted"); !errors.Is(err, ErrBadTransition) {
		t.Errorf("accepting a failed analysis: err = %v, want ErrBadTransition", err)
	}

	s2, _ := newSvc(t, &mockRunner{out: validOut})
	ok := start(t, s2)
	if _, err := s2.Decide(ok.ID, "accepted"); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Decide(ok.ID, "dismissed"); !errors.Is(err, ErrBadTransition) {
		t.Errorf("re-deciding an accepted analysis: err = %v, want ErrBadTransition", err)
	}
	if _, err := s2.Decide(9999, "accepted"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deciding a missing row: err = %v, want ErrNotFound", err)
	}
}

// SC-5 in code: only `accepted` may reach `planned`.
func TestMarkPlannedOnlyFromAccepted(t *testing.T) {
	s, _ := newSvc(t, &mockRunner{out: validOut})
	got := start(t, s)
	if _, err := s.MarkPlanned(got.ID, "uuid-1"); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("planning a proposed analysis: err = %v, want ErrBadTransition", err)
	}
	if _, err := s.Decide(got.ID, "accepted"); err != nil {
		t.Fatal(err)
	}
	after, err := s.MarkPlanned(got.ID, "uuid-1")
	if err != nil {
		t.Fatalf("mark planned: %v", err)
	}
	if after.Status != "planned" || after.PlanningSessionUUID != "uuid-1" {
		t.Errorf("row = %+v, want planned with the session uuid", after)
	}
	if _, err := s.MarkPlanned(got.ID, "uuid-2"); !errors.Is(err, ErrBadTransition) {
		t.Error("a planned analysis was allowed to start a second planning session")
	}
}

func TestLatestIsScopedAndNilWhenEmpty(t *testing.T) {
	s, _ := newSvc(t, &mockRunner{out: validOut})
	got, err := s.Latest("")
	if err != nil || got != nil {
		t.Fatalf("Latest on an empty table = (%v, %v), want (nil, nil)", got, err)
	}
	if _, err := s.Start("2026-08-10", "2026-08-24", "alpha", digest, "sha-a"); err != nil {
		t.Fatal(err)
	}
	if got, err = s.Latest(""); err != nil || got != nil {
		t.Errorf("fleet-wide Latest returned a project-scoped row: %v", got)
	}
	if got, err = s.Latest("alpha"); err != nil || got == nil || got.Scope != "alpha" {
		t.Errorf("scoped Latest = (%v, %v), want the alpha row", got, err)
	}
	if _, err := s.Start("2026-08-10", "2026-08-24", "", digest, "sha-f"); err != nil {
		t.Fatal(err)
	}
	if got, err = s.Latest(""); err != nil || got == nil || got.Scope != "" {
		t.Errorf("fleet-wide Latest = (%v, %v), want the unscoped row", got, err)
	}
}

func TestPromptCarriesTheContractAndTheDigest(t *testing.T) {
	r := &mockRunner{out: validOut}
	s, _ := newSvc(t, r)
	start(t, s)
	prompt, _ := r.seen()
	for _, want := range []string{"## Що болить", "## Чому", "## Що я б змінив",
		"CITATION IS MANDATORY", "6000 MAXIMUM", "[E:agent:tech-lead]"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
}

// A daemon restart leaves rows nothing will ever finish; they must not look
// like work still in progress.
func TestHealStaleResolvesOrphanedRuns(t *testing.T) {
	s, db := newSvc(t, &mockRunner{out: validOut})
	if _, err := db.Exec(
		`INSERT INTO retro_analyses (window_from, window_to, digest_sha256, status, created_at)
		 VALUES ('2026-08-10','2026-08-24','sha','running','2026-08-24T08:00:00.000Z')`); err != nil {
		t.Fatal(err)
	}
	if err := s.HealStale(); err != nil {
		t.Fatalf("heal: %v", err)
	}
	got, err := s.Latest("")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" || !strings.Contains(got.Error, "restarted") {
		t.Errorf("row = %+v, want failed with a restart reason", got)
	}
}

func TestChangeIdeaStripsTheHeading(t *testing.T) {
	got := ChangeIdea(validOut)
	if got != "Додати правило схвалення [E:rec:7]" {
		t.Errorf("ChangeIdea = %q", got)
	}
	if ChangeIdea("## Що болить\nx") != "" {
		t.Error("ChangeIdea on a document without the section should be empty")
	}
}

func TestValidateWithoutAnAllowedSetStillRequiresCitations(t *testing.T) {
	if _, err := Validate(validOut, nil); err != nil {
		t.Errorf("valid output rejected with a nil allow-set: %v", err)
	}
	if _, err := Validate("", nil); err == nil {
		t.Error("an empty analysis was accepted")
	}
}
