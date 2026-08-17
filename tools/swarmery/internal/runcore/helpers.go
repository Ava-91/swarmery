package runcore

import (
	"crypto/rand"
	"fmt"
	"strings"
)

// NewUUID returns a random RFC-4122 v4 UUID string — the --session-id passed to a
// headless run, which IS the explicit run↔session link the whole dashboard reads.
// Uses crypto/rand directly (the codebase's convention — see api/tasks_board.go,
// approvals/approvals.go) rather than promoting the indirect google/uuid dep.
//
// Every engine's Service exposes this as a `UUID func() string` test seam and
// defaults it here, so a stubbed generator still reaches exactly one
// implementation.
func NewUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read never fails on supported platforms; if it somehow does,
		// degrade to a zero-filled variant-tagged uuid rather than crash a single
		// run's goroutine.
		b = [16]byte{}
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Tail returns the last <= n bytes of s, trimmed. Exported because the engines
// also tail values they capture themselves (not just this package's stderr).
func Tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return s
}
