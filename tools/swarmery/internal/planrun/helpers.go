package planrun

import (
	"crypto/rand"
	"fmt"
)

// newUUID returns a random RFC-4122 v4 UUID string — the --session-id passed to
// the headless plan run (the explicit plan↔session link). Uses crypto/rand
// directly (the codebase convention, see planning/helpers.go,
// phaserun/helpers.go) rather than promoting the indirect google/uuid dep.
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		b = [16]byte{}
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
