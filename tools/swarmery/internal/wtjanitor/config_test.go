package wtjanitor

import (
	"testing"
	"time"
)

func TestConfigFromEnv_Defaults(t *testing.T) {
	c := ConfigFromEnv()
	if !c.Enabled {
		t.Error("Enabled = false with no env set; the janitor is on by default")
	}
	if c.TickInterval != DefaultTickInterval {
		t.Errorf("TickInterval = %v, want %v", c.TickInterval, DefaultTickInterval)
	}
	if c.MinIdle != DefaultMinIdle {
		t.Errorf("MinIdle = %v, want %v", c.MinIdle, DefaultMinIdle)
	}
	if c.RetentionDays != DefaultRetentionDays {
		t.Errorf("RetentionDays = %d, want %d", c.RetentionDays, DefaultRetentionDays)
	}
}

func TestConfigFromEnv_Overrides(t *testing.T) {
	t.Setenv("SWARMERY_WTJANITOR_INTERVAL_MIN", "5")
	t.Setenv("SWARMERY_WTJANITOR_MIN_IDLE_MIN", "90")
	t.Setenv("SWARMERY_WTJANITOR_RETENTION_DAYS", "7")
	c := ConfigFromEnv()
	if c.TickInterval != 5*time.Minute {
		t.Errorf("TickInterval = %v, want 5m", c.TickInterval)
	}
	if c.MinIdle != 90*time.Minute {
		t.Errorf("MinIdle = %v, want 90m", c.MinIdle)
	}
	if c.RetentionDays != 7 {
		t.Errorf("RetentionDays = %d, want 7", c.RetentionDays)
	}
}

func TestEnabled_KillSwitchSpellings(t *testing.T) {
	for _, v := range []string{"0", "false", "off", "FALSE", "Off"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("SWARMERY_WTJANITOR", v)
			if Enabled() {
				t.Errorf("Enabled() = true for SWARMERY_WTJANITOR=%q", v)
			}
		})
	}
	for _, v := range []string{"", "1", "true", "on", "anything-else"} {
		t.Run("on:"+v, func(t *testing.T) {
			t.Setenv("SWARMERY_WTJANITOR", v)
			if !Enabled() {
				t.Errorf("Enabled() = false for SWARMERY_WTJANITOR=%q; only 0/false/off disable", v)
			}
		})
	}
}

// A garbage or zero value must keep the DEFAULT, never fall through to zero: a
// MinIdle of 0 would silently remove the "don't sweep something someone just
// paused in" guarantee, which is the opposite of a safe failure mode.
func TestConfigFromEnv_GarbageAndZeroKeepDefaults(t *testing.T) {
	for _, v := range []string{"abc", "0", "-5", " "} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("SWARMERY_WTJANITOR_MIN_IDLE_MIN", v)
			t.Setenv("SWARMERY_WTJANITOR_INTERVAL_MIN", v)
			c := ConfigFromEnv()
			if c.MinIdle != DefaultMinIdle {
				t.Errorf("MinIdle = %v for %q, want the default %v", c.MinIdle, v, DefaultMinIdle)
			}
			if c.TickInterval != DefaultTickInterval {
				t.Errorf("TickInterval = %v for %q, want the default %v", c.TickInterval, v, DefaultTickInterval)
			}
		})
	}
}
