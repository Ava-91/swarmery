package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// No long poll may outlive its test.
//
// The rule is one line — open a permission-request poll under hookCtx(t), never
// under a context nothing cancels — and the cost of breaking it is not a failing
// assertion but a TEN-MINUTE hang: the handler blocks until the approval window
// closes, httptest.Server.Close() waits for it, and the package dies on Go's own
// test timeout with a goroutine dump that points at the blocked handler rather
// than at the test that stranded it. It cost this repo one red CI run, and it did
// not reproduce locally, because whether Close() has to wait at all depends on
// how fast the runner got through the test body.
//
// So the rule is enforced here instead of being remembered.
func TestNoUncancelledLongPolls(t *testing.T) {
	const file = "approvals_test.go"
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}

	// A postHook call whose context is anything the test does not cancel. The
	// legitimate spellings are hookCtx(t) and a ctx the test itself cancels (the
	// client-disconnect test builds one with context.WithCancel).
	bad := regexp.MustCompile(`postHook\([^,]+,\s*context\.(Background|TODO)\(\)`)
	for i, line := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue // a comment naming the hazard is not the hazard
		}
		if bad.MatchString(line) {
			t.Errorf("%s:%d opens a long poll under a context nothing cancels — use hookCtx(t):\n  %s",
				file, i+1, strings.TrimSpace(line))
		}
	}

	// And the helper itself has to keep doing what its name promises.
	if !strings.Contains(string(src), "t.Cleanup(cancel)") {
		t.Error("hookCtx no longer cancels on cleanup — every long poll in this file now outlives its test")
	}
}
