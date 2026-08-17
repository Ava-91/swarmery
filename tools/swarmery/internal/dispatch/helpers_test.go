package dispatch

import (
	"testing"
)

// newUUID now lives in internal/runcore (one copy for all five engines); its
// test moved with it to internal/runcore/spawner_test.go, regexp and all.

func TestDecodeStringList(t *testing.T) {
	cases := []struct {
		in   string
		want int // len
		err  bool
	}{
		{"", 0, false},
		{"  ", 0, false},
		{"[]", 0, false},
		{`["a","b"]`, 2, false},
		{"null", 0, false},
		{"{bad", 0, true},
	}
	for _, tc := range cases {
		got, err := decodeStringList(tc.in)
		if tc.err {
			if err == nil {
				t.Errorf("decodeStringList(%q) expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("decodeStringList(%q) error: %v", tc.in, err)
		}
		if len(got) != tc.want {
			t.Errorf("decodeStringList(%q) len = %d, want %d", tc.in, len(got), tc.want)
		}
	}
}
