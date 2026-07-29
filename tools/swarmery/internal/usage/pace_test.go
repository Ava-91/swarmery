package usage

import (
	"testing"
	"time"
)

// elapsedReset expresses a pace case the way an operator reads it — "N% of the
// window has elapsed" — as the (resetMs, windowMs) pair CalculatePace takes.
func elapsedReset(elapsedPct float64) (resetMs, windowMs int64) {
	const window = 100_000
	return int64(float64(window) * (100 - elapsedPct) / 100), window
}

func TestCalculatePace(t *testing.T) {
	tests := []struct {
		name        string
		percentUsed float64
		elapsedPct  float64
		wantStatus  string
		wantMessage string
		wantElapsed float64
	}{
		// NOTE — deviation from the phase doc's acceptance criteria: the doc
		// claims "28% used at 22% elapsed → on-track". With the ±5-point
		// threshold the doc itself specifies (and Fusion's calculatePace), the
		// delta is +6 points, which is "ahead". The doc's own next case
		// (19% used at 25% elapsed → behind, i.e. a -6 delta) requires the
		// threshold to be < 6, so the two criteria cannot both hold. The
		// algorithm is pinned here; the doc's first clause is the error.
		{"28 used / 22 elapsed is ahead by 6 points", 28, 22, PaceAhead, "6% over pace", 22},
		{"19 used / 25 elapsed", 19, 25, PaceBehind, "6% under pace", 25},
		{"60 used / 20 elapsed", 60, 20, PaceAhead, "40% over pace", 20},
		{"28 used / 25 elapsed is on track", 28, 25, PaceOnTrack, "On pace with time elapsed", 25},
		{"exactly +5 points stays on track", 30, 25, PaceOnTrack, "On pace with time elapsed", 25},
		{"exactly -5 points stays on track", 20, 25, PaceOnTrack, "On pace with time elapsed", 25},
		{"percentUsed clamps at 0", -10, 25, PaceBehind, "25% under pace", 25},
		{"percentUsed clamps at 100", 150, 25, PaceAhead, "75% over pace", 25},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resetMs, windowMs := elapsedReset(tc.elapsedPct)
			got := CalculatePace(tc.percentUsed, resetMs, windowMs)
			if got == nil {
				t.Fatalf("CalculatePace(%v, %d, %d) = nil, want a pace", tc.percentUsed, resetMs, windowMs)
			}
			if got.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", got.Status, tc.wantStatus)
			}
			if got.Message != tc.wantMessage {
				t.Errorf("message = %q, want %q", got.Message, tc.wantMessage)
			}
			if got.PercentElapsed != tc.wantElapsed {
				t.Errorf("percentElapsed = %v, want %v", got.PercentElapsed, tc.wantElapsed)
			}
		})
	}
}

// TestCalculatePaceFusionParity replays Fusion's own calculatePace test vectors
// (packages/dashboard/src/__tests__/usage.test.ts:4505) so a divergence from the
// reference implementation fails here rather than in the UI.
func TestCalculatePaceFusionParity(t *testing.T) {
	const (
		threeDays  = int64(3 * 24 * 60 * 60 * 1000)
		sevenDaysM = int64(7 * 24 * 60 * 60 * 1000)
	)
	if got := CalculatePace(70, threeDays, sevenDaysM); got == nil ||
		got.Status != PaceAhead || got.PercentElapsed != 57 {
		t.Errorf("70%% used with 3d left of 7d = %+v, want ahead at 57%% elapsed", got)
	}
	if got := CalculatePace(20, threeDays, sevenDaysM); got == nil ||
		got.Status != PaceBehind || got.PercentElapsed != 57 {
		t.Errorf("20%% used with 3d left of 7d = %+v, want behind at 57%% elapsed", got)
	}
	if got := CalculatePace(52, sevenDaysM/2, sevenDaysM); got == nil ||
		got.Status != PaceOnTrack || got.PercentElapsed != 50 {
		t.Errorf("52%% used with half of 7d left = %+v, want on-track at 50%% elapsed", got)
	}
}

func TestCalculatePaceNilGuards(t *testing.T) {
	const window = int64(7 * 24 * 60 * 60 * 1000)
	cases := []struct {
		name              string
		resetMs, windowMs int64
	}{
		{"reset already passed", 0, window},
		{"negative reset", -1000, window},
		{"zero window", window / 2, 0},
		{"negative window", window / 2, -1000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CalculatePace(50, tc.resetMs, tc.windowMs); got != nil {
				t.Errorf("CalculatePace(50, %d, %d) = %+v, want nil", tc.resetMs, tc.windowMs, got)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{0, "now"},
		{-time.Second, "now"},
		{500 * time.Millisecond, "0s"},
		{45 * time.Second, "45s"},
		{59 * time.Second, "59s"},
		{time.Minute, "1m"},
		{12*time.Minute + 30*time.Second, "12m 30s"},
		{59*time.Minute + 59*time.Second, "59m 59s"},
		{time.Hour, "1h"},
		{3*time.Hour + 30*time.Minute, "3h 30m"},
		{23*time.Hour + 59*time.Minute, "23h 59m"},
		{24 * time.Hour, "1d"},
		{5*24*time.Hour + 6*time.Hour, "5d 6h"},
		{5 * 24 * time.Hour, "5d"},
	}
	for _, tc := range tests {
		if got := FormatDuration(tc.in); got != tc.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
