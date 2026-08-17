package dispatch

import (
	"encoding/json"
	"strconv"
	"strings"
)

// decodeStringList parses a stored JSON string array (file_scope, dependencies)
// into []string. Empty/NULL storage ⇒ empty slice, never nil-panic downstream.
// Mirrors api.unmarshalStringList (kept local so dispatch has no api import).
func decodeStringList(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return []string{}, nil
	}
	var xs []string
	if err := json.Unmarshal([]byte(s), &xs); err != nil {
		return nil, err
	}
	if xs == nil {
		xs = []string{}
	}
	return xs, nil
}

// boolToInt maps a bool to SQLite's 0/1 integer boolean.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// itoa / itoa64 are strconv shortcuts for building error strings + scope keys.
func itoa(n int) string     { return strconv.Itoa(n) }
func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
