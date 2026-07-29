package usage

import (
	"fmt"
	"math"
	"time"
)

// paceThresholdPoints is the dead band, in percentage points, inside which a
// window counts as "on-track". Ported verbatim from Fusion
// (packages/dashboard/src/usage.ts:110 PACE_THRESHOLD).
const paceThresholdPoints = 5.0

// CalculatePace compares how much of a window's quota is spent against how much
// of the window's time has elapsed, in percentage points:
//
//	delta = percentUsed - percentElapsed
//
// A delta above +5 points is "ahead" (burning faster than linear — the warning
// state), below -5 points is "behind" (under pace — the good state), and
// anything between is "on-track". The vocabulary is Fusion's; see the note on
// the Pace* constants.
//
// It returns nil when the window lacks timing data or has already reset —
// resetMs <= 0 or windowMs <= 0 means there is no forward pace signal to give.
// Port of usage.ts:116 calculatePace.
func CalculatePace(percentUsed float64, resetMs, windowMs int64) *Pace {
	if resetMs <= 0 || windowMs <= 0 {
		return nil
	}
	used := math.Min(100, math.Max(0, percentUsed))
	elapsed := 100 - (float64(resetMs)/float64(windowMs))*100
	delta := used - elapsed
	p := &Pace{PercentElapsed: math.Round(elapsed)}
	switch {
	case delta > paceThresholdPoints:
		p.Status, p.Message = PaceAhead, fmt.Sprintf("%d%% over pace", int(math.Round(math.Abs(delta))))
	case delta < -paceThresholdPoints:
		p.Status, p.Message = PaceBehind, fmt.Sprintf("%d%% under pace", int(math.Round(math.Abs(delta))))
	default:
		p.Status, p.Message = PaceOnTrack, "On pace with time elapsed"
	}
	return p
}

// FormatDuration renders a countdown with at most the two most significant
// units, dropping the smaller unit when it is zero: "now", "45s", "12m 30s",
// "3h 30m", "5d 6h". Port of usage.ts:183 formatDuration.
func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	secs := int64(d / time.Second)
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	mins, remSecs := secs/60, secs%60
	if mins < 60 {
		if remSecs > 0 {
			return fmt.Sprintf("%dm %ds", mins, remSecs)
		}
		return fmt.Sprintf("%dm", mins)
	}
	hours, remMins := mins/60, mins%60
	if hours < 24 {
		if remMins > 0 {
			return fmt.Sprintf("%dh %dm", hours, remMins)
		}
		return fmt.Sprintf("%dh", hours)
	}
	days, remHours := hours/24, hours%24
	if remHours > 0 {
		return fmt.Sprintf("%dd %dh", days, remHours)
	}
	return fmt.Sprintf("%dd", days)
}
