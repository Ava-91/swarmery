package wtjanitor

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Defaults. Every one is overridable by env, none by a config file — this
// module has no YAML at all (see internal/dispatch/config.go and
// internal/routines/config.go, whose idioms this mirrors).
const (
	DefaultTickInterval  = 15 * time.Minute
	DefaultRetentionDays = 30
	// DefaultMinIdle lives in service.go, next to the classifier that enforces
	// it — one declaration, referenced here.
)

// Config is one janitor's runtime settings.
type Config struct {
	Enabled       bool          // SWARMERY_WTJANITOR=0|false|off disables the ticker
	TickInterval  time.Duration // SWARMERY_WTJANITOR_INTERVAL_MIN
	MinIdle       time.Duration // SWARMERY_WTJANITOR_MIN_IDLE_MIN
	RetentionDays int           // SWARMERY_WTJANITOR_RETENTION_DAYS
}

// ConfigFromEnv builds a Config from the SWARMERY_* env vars, falling back to
// the defaults above.
//
// A non-positive or unparsable value keeps the default rather than taking the
// zero: MinIdle=0 would remove the "don't sweep a worktree someone just paused
// in" guarantee, so a typo in an env var must never be able to disarm a safety
// floor. Disabling the janitor is a separate, explicit switch.
func ConfigFromEnv() Config {
	c := Config{
		Enabled:       Enabled(),
		TickInterval:  DefaultTickInterval,
		MinIdle:       DefaultMinIdle,
		RetentionDays: DefaultRetentionDays,
	}
	if v := envPositiveInt("SWARMERY_WTJANITOR_INTERVAL_MIN"); v > 0 {
		c.TickInterval = time.Duration(v) * time.Minute
	}
	if v := envPositiveInt("SWARMERY_WTJANITOR_MIN_IDLE_MIN"); v > 0 {
		c.MinIdle = time.Duration(v) * time.Minute
	}
	if v := envPositiveInt("SWARMERY_WTJANITOR_RETENTION_DAYS"); v > 0 {
		c.RetentionDays = v
	}
	return c
}

// Enabled reports the kill-switch state: SWARMERY_WTJANITOR=0/false/off
// disables the sweeper entirely. Default (unset) is enabled. Parsed exactly
// like routines.Enabled / dispatchEnabled.
func Enabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("SWARMERY_WTJANITOR")))
	return v != "0" && v != "false" && v != "off"
}

// envPositiveInt is a local copy of the dispatch helper of the same name, which
// is unexported there. Two lines of parsing beats exporting a config detail of
// another package purely to share them.
func envPositiveInt(key string) int {
	s := strings.TrimSpace(os.Getenv(key))
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}
