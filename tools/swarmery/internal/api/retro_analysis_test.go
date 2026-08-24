package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/retroanalysis"
)

// analysisRunner is a deterministic stand-in for the headless claude run. When
// block is non-nil, Run waits on it — that is how the single-flight test gets
// a request to arrive mid-run.
type analysisRunner struct {
	out    string
	err    error
	block  chan struct{}
	mu     sync.Mutex
	prompt string
	calls  int
}

func (a *analysisRunner) Run(_ context.Context, prompt string) (string, error) {
	a.mu.Lock()
	a.prompt = prompt
	a.calls++
	a.mu.Unlock()
	if a.block != nil {
		<-a.block
	}
	return a.out, a.err
}

func (a *analysisRunner) seen() (string, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.prompt, a.calls
}

// waitStarted blocks until the runner has been entered at least once.
func (a *analysisRunner) waitStarted(t *testing.T) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if _, calls := a.seen(); calls > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the analysis run never started")
}

// analysisValidOut cites ids the seeded report's digest actually offers
// (seedRetroReportDB seeds agent tech-lead, recommendation 1, session
// sess-alpha) — a fabricated id would be rejected, which is the point.
const analysisValidOut = `## Що болить
tech-lead провалює прогони [E:agent:tech-lead]

## Чому
Рекомендація вже назвала причину [E:rec:1]

## Що я б змінив
Додати правило схвалення для Write [E:session:sess-alpha]
`

// analysisAPIServer wires the seeded report database to a handler whose
// analysis service runs INLINE, so a 202 means the row has already resolved.
func analysisAPIServer(t *testing.T, r *analysisRunner) (*httptest.Server, *sql.DB, *retroanalysis.Service) {
	return analysisServerWith(t, r, func(fn func()) { fn() })
}

// analysisAPIServerAsync keeps real goroutine dispatch — needed only when the
// test is about concurrency.
func analysisAPIServerAsync(t *testing.T, r *analysisRunner) (*httptest.Server, *sql.DB, *retroanalysis.Service) {
	return analysisServerWith(t, r, nil)
}

func analysisServerWith(t *testing.T, r *analysisRunner, dispatch func(func())) (*httptest.Server, *sql.DB, *retroanalysis.Service) {
	t.Helper()
	db := seedRetroReportDB(t)
	svc := &retroanalysis.Service{
		DB: db, Runner: r, Go: dispatch,
		Now: func() time.Time { return time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC) },
	}
	h := &Handler{DB: db, RetroAnalysis: svc}
	mux := http.NewServeMux()
	Routes(mux, h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, db, svc
}

// analysisReq issues one request and returns the status and raw body.
func analysisReq(t *testing.T, srv *httptest.Server, method, path, body string) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewBufferString(body)
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func decodeAnalysis(t *testing.T, body string) retroanalysis.Analysis {
	t.Helper()
	var a retroanalysis.Analysis
	if err := json.Unmarshal([]byte(body), &a); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return a
}

func TestStartAnalysisReturns202AndAProposedRow(t *testing.T) {
	srv, _, _ := analysisAPIServer(t, &analysisRunner{out: analysisValidOut})
	code, body := analysisReq(t, srv, "POST", "/api/retro/analysis?"+retroRange(7), `{}`)
	if code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", code, body)
	}
	got := decodeAnalysis(t, body)
	if got.ID == 0 {
		t.Error("no analysis id returned")
	}
	if got.Status != "proposed" {
		t.Errorf("status = %q (error %q), want proposed", got.Status, got.Error)
	}
	if got.Citations != 3 {
		t.Errorf("citations = %d, want 3", got.Citations)
	}
	if got.DigestSHA256 == "" {
		t.Error("the row is not pinned to the digest it was built from")
	}
}

// The prompt must carry the real digest: that is what makes the model's
// citations checkable instead of decorative.
func TestStartAnalysisFeedsTheRealDigest(t *testing.T) {
	runner := &analysisRunner{out: analysisValidOut}
	srv, _, _ := analysisAPIServer(t, runner)
	if code, body := analysisReq(t, srv, "POST", "/api/retro/analysis?"+retroRange(7), `{}`); code != 202 {
		t.Fatalf("start = %d %s", code, body)
	}
	prompt, calls := runner.seen()
	if calls != 1 {
		t.Fatalf("runner calls = %d, want 1", calls)
	}
	for _, want := range []string{"CITATION IS MANDATORY", "[E:agent:tech-lead]", "[E:rec:1]"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
}

func TestStartAnalysisIsSingleFlight(t *testing.T) {
	block := make(chan struct{})
	runner := &analysisRunner{out: analysisValidOut, block: block}
	srv, db, _ := analysisAPIServerAsync(t, runner)

	if code, body := analysisReq(t, srv, "POST", "/api/retro/analysis?"+retroRange(7), `{}`); code != 202 {
		t.Fatalf("first start = %d %s, want 202", code, body)
	}
	runner.waitStarted(t)
	code, body := analysisReq(t, srv, "POST", "/api/retro/analysis?"+retroRange(7), `{}`)
	if code != http.StatusConflict {
		t.Fatalf("second start = %d %s, want 409", code, body)
	}
	if !strings.Contains(body, "already running") {
		t.Errorf("409 body = %s, want a human reason rather than a bare code", body)
	}
	close(block)
	if _, calls := runner.seen(); calls != 1 {
		t.Errorf("runner calls = %d, want 1 — the refused start must not spawn", calls)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM retro_analyses`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
}

func TestGetLatestAnalysis(t *testing.T) {
	srv, _, _ := analysisAPIServer(t, &analysisRunner{out: analysisValidOut})
	var envelope struct {
		Analysis *retroanalysis.Analysis `json:"analysis"`
	}
	getJSON(t, srv.URL+"/api/retro/analysis", &envelope)
	if envelope.Analysis != nil {
		t.Fatalf("Latest before any run = %+v, want null", envelope.Analysis)
	}
	if code, body := analysisReq(t, srv, "POST", "/api/retro/analysis?"+retroRange(7), `{}`); code != 202 {
		t.Fatalf("start = %d %s", code, body)
	}
	getJSON(t, srv.URL+"/api/retro/analysis", &envelope)
	if envelope.Analysis == nil || envelope.Analysis.Status != "proposed" {
		t.Fatalf("Latest = %+v, want the proposed row", envelope.Analysis)
	}
}

func TestPatchAnalysisDecides(t *testing.T) {
	srv, _, _ := analysisAPIServer(t, &analysisRunner{out: analysisValidOut})
	_, body := analysisReq(t, srv, "POST", "/api/retro/analysis?"+retroRange(7), `{}`)
	id := decodeAnalysis(t, body).ID
	path := "/api/retro/analysis/" + strconv.FormatInt(id, 10)

	code, body := analysisReq(t, srv, "PATCH", path, `{"status":"accepted"}`)
	if code != http.StatusOK {
		t.Fatalf("patch = %d %s, want 200", code, body)
	}
	got := decodeAnalysis(t, body)
	if got.Status != "accepted" || got.DecidedAt == nil {
		t.Errorf("row = %+v, want accepted with decided_at stamped", got)
	}
	if code, body = analysisReq(t, srv, "PATCH", path, `{"status":"dismissed"}`); code != http.StatusConflict {
		t.Errorf("re-deciding = %d %s, want 409", code, body)
	}
	// `planned` is the handoff's to set, never a bare PATCH: a planned row
	// with no session uuid would be a lie.
	if code, body = analysisReq(t, srv, "PATCH", path, `{"status":"planned"}`); code != http.StatusBadRequest {
		t.Errorf("PATCH to planned = %d %s, want 400", code, body)
	}
	if code, _ = analysisReq(t, srv, "PATCH", "/api/retro/analysis/9999", `{"status":"accepted"}`); code != http.StatusNotFound {
		t.Errorf("patch of a missing row = %d, want 404", code)
	}
	if code, _ = analysisReq(t, srv, "PATCH", "/api/retro/analysis/abc", `{"status":"accepted"}`); code != http.StatusBadRequest {
		t.Errorf("patch with a non-numeric id = %d, want 400", code)
	}
}

func TestFailedAnalysisSurfacesItsReason(t *testing.T) {
	srv, _, _ := analysisAPIServer(t, &analysisRunner{err: errors.New("claude -p: exit 1; stderr: overloaded")})
	code, body := analysisReq(t, srv, "POST", "/api/retro/analysis?"+retroRange(7), `{}`)
	if code != http.StatusAccepted {
		t.Fatalf("start = %d %s", code, body)
	}
	got := decodeAnalysis(t, body)
	if got.Status != "failed" {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if !strings.Contains(got.Error, "overloaded") {
		t.Errorf("error = %q, want the runner's stderr tail verbatim", got.Error)
	}
}

// SC-4 end to end through HTTP: an uncited analysis never reaches the operator
// wearing a valid badge.
func TestUncitedAnalysisIsFailedNotProposed(t *testing.T) {
	srv, _, _ := analysisAPIServer(t, &analysisRunner{
		out: "## Що болить\nвсе погано\n\n## Чому\nтому що\n\n## Що я б змінив\nпокращити промпти\n",
	})
	_, body := analysisReq(t, srv, "POST", "/api/retro/analysis?"+retroRange(7), `{}`)
	got := decodeAnalysis(t, body)
	if got.Status != "failed" {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if !strings.Contains(got.Error, "cites no evidence") {
		t.Errorf("error = %q, want a reason naming the missing citations", got.Error)
	}
}

func TestAnalysisWritesRejectACrossOriginPost(t *testing.T) {
	srv, _, _ := analysisAPIServer(t, &analysisRunner{out: analysisValidOut})
	req, err := http.NewRequest("POST", srv.URL+"/api/retro/analysis", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://evil.example")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin POST = %d, want 403", resp.StatusCode)
	}
}

func TestAnalysisEndpointsWithoutTheService(t *testing.T) {
	db := seedRetroReportDB(t)
	mux := http.NewServeMux()
	Routes(mux, &Handler{DB: db})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/retro/analysis", `{}`},
		{"GET", "/api/retro/analysis", ""},
		{"PATCH", "/api/retro/analysis/1", `{"status":"accepted"}`},
	} {
		if code, _ := analysisReq(t, srv, c.method, c.path, c.body); code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d, want 503 when the service is not attached", c.method, c.path, code)
		}
	}
}

func TestStartAnalysisRejectsABadWindow(t *testing.T) {
	srv, _, _ := analysisAPIServer(t, &analysisRunner{out: analysisValidOut})
	if code, _ := analysisReq(t, srv, "POST", "/api/retro/analysis?from=nonsense", `{}`); code != http.StatusBadRequest {
		t.Errorf("bad window = %d, want 400", code)
	}
}
