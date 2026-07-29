package api

// Usage modal (fusion phase 14 → live quotas): GET /api/usage serves a PROVIDER
// ARRAY — the live Claude subscription reading first, the telemetry estimate
// second.
//
// # Policy note — the 2026-06 OAuth spike is reversed
//
// This file used to open with a spike note declaring the OAuth path
// "credential exfiltration + a fragile private-API dependency, out of policy",
// and self-estimated every window from our own indexed telemetry. That decision
// is reversed. The reasoning is recorded in full in the internal/usage package
// doc (tools/swarmery/internal/usage/types.go); in short: the daemon reads the
// operator's OWN credential on the operator's OWN machine and sends it ONLY to
// Anthropic — the exact call `claude /usage` makes — never persisting, logging,
// or serializing it. See that package doc before touching this path.
//
// Providers:
//
//   - "Claude" (source:"oauth") — usage.Client.Fetch. Never returns an error;
//     every failure mode (opted out, not logged in, auth rejected, rate limited,
//     malformed payload) degrades to a visible per-provider error card, so one
//     broken provider can never break the endpoint.
//   - "Telemetry estimate" (source:"estimate") — the previous behaviour, demoted
//     to a SECOND card and emitted ONLY when SWARMERY_USAGE_LIMITS is set. It is
//     a self-estimate and must never masquerade as the real reading.
//
// The old top-level `configured` flag is gone: the frontend and the daemon ship
// together (go:embed), so there is no compatibility window to preserve, and the
// estimate card's presence now carries the same information.
//
// Configuration: SWARMERY_USAGE_LIMITS is a JSON object of window quotas, e.g.
//
//	{"session5h":{"label":"5-hour session","tokens":50000000,"windowHours":5},
//	 "weekly":{"label":"Weekly","tokens":300000000,"windowHours":168}}
//
// Unset/blank → no estimate card at all. `used` = indexed input+output tokens in
// the rolling window across ALL projects (archived included — quota is billed
// regardless).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/usage"
)

// claudeFetchTimeout bounds the whole live-provider fetch. Fusion budgets 75s
// only because of its node-pty fallback (usage.ts:2346); without that fallback,
// 3 retries plus backoff fit comfortably inside 20s.
const claudeFetchTimeout = 20 * time.Second

// estimateProviderName is the second card's label. Deliberately explicit: the
// operator must never mistake the estimate for the real subscription reading.
const estimateProviderName = "Telemetry estimate"

// usageClient is the package-level live-quota client. The refreshed-token cache
// lives on it, so it must outlive a single request. Tests swap this for a client
// wired to a stub endpoint through usage.Client's seams.
var usageClient = &usage.Client{}

// errBadUsageLimits marks a malformed SWARMERY_USAGE_LIMITS so the handler can
// answer with the specific 500 body this endpoint has always returned, rather
// than the generic error shape.
var errBadUsageLimits = errors.New("invalid SWARMERY_USAGE_LIMITS JSON")

// usageWindowConfig is one configured quota window from SWARMERY_USAGE_LIMITS.
type usageWindowConfig struct {
	Label       string  `json:"label"`
	Tokens      int64   `json:"tokens"`      // quota for the window
	WindowHours float64 `json:"windowHours"` // rolling window length
}

// usageResp is the /api/usage body. The window/provider shapes are owned by
// internal/usage and serialized verbatim — this package does not redefine or
// re-map their JSON tags.
type usageResp struct {
	GeneratedAt string           `json:"generatedAt"`
	Providers   []usage.Provider `json:"providers"`
}

// parseUsageLimits parses the SWARMERY_USAGE_LIMITS JSON. Blank → (nil, nil):
// not configured, not an error. Invalid JSON or a non-positive quota/window is
// an error the caller surfaces. Pure; unit-tested.
func parseUsageLimits(raw string) (map[string]usageWindowConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var cfg map[string]usageWindowConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, err
	}
	for k, v := range cfg {
		if v.Tokens <= 0 || v.WindowHours <= 0 {
			delete(cfg, k)
		}
	}
	return cfg, nil
}

// usageWindowElapsed models where "now" sits inside the current rolling window.
// For a simple rolling window anchored at the local calendar boundary of its
// length, elapsed is (now - windowStart). We anchor session windows to the
// hour and the weekly window to the local week to give a meaningful "resets in"
// countdown without needing Anthropic's real reset schedule (this is an
// estimate — the card says so). Pure; unit-tested.
func usageWindowElapsed(now time.Time, windowHours float64) (elapsed, window time.Duration, resetsAt time.Time) {
	window = time.Duration(windowHours * float64(time.Hour))
	// Anchor the rolling window to a deterministic grid so "resets in" is stable
	// between polls: number of whole windows since the Unix epoch, in local time.
	epoch := now.Unix()
	winSec := int64(window / time.Second)
	if winSec <= 0 {
		return 0, window, now
	}
	startSec := (epoch / winSec) * winSec
	windowStart := time.Unix(startSec, 0).In(now.Location())
	elapsed = now.Sub(windowStart)
	resetsAt = windowStart.Add(window)
	return elapsed, window, resetsAt
}

// usageCacheEntry is the cached computed response.
type usageCacheEntry struct {
	at   time.Time
	body usageResp
}

var (
	usageCacheMu sync.Mutex
	usageCache   *usageCacheEntry
)

// usageCacheTTL matches Fusion's CACHE_TTL_MS (usage.ts:107). It is deliberately
// short: this now costs a live Anthropic round-trip per miss.
const usageCacheTTL = 30 * time.Second

// resetUsageCache clears the process-wide usage cache. Used by tests to make
// assertions independent of a prior computed body (the ?fresh=1 query bypasses
// the cache read but still repopulates it).
func resetUsageCache() {
	usageCacheMu.Lock()
	usageCache = nil
	usageCacheMu.Unlock()
}

// cachedUsage returns the cached body when it is still within the TTL.
func cachedUsage() (usageResp, bool) {
	usageCacheMu.Lock()
	defer usageCacheMu.Unlock()
	if usageCache != nil && time.Since(usageCache.at) < usageCacheTTL {
		return usageCache.body, true
	}
	return usageResp{}, false
}

// storeUsage replaces the cached body.
func storeUsage(body usageResp, at time.Time) {
	usageCacheMu.Lock()
	usageCache = &usageCacheEntry{at: at, body: body}
	usageCacheMu.Unlock()
}

// usedTokensSince sums indexed input+output tokens across ALL projects since
// the given UTC bound (quota is billed regardless of project archival, so no
// archived filter here — unlike the cost analytics). start is the zone-suffix
// free bound form used elsewhere.
func (h *Handler) usedTokensSince(startUTC string) (int64, error) {
	var n int64
	err := h.DB.QueryRow(`
		SELECT COALESCE(SUM(COALESCE(tokens_in,0) + COALESCE(tokens_out,0)), 0)
		FROM turns
		WHERE started_at >= ?`, startUTC).Scan(&n)
	return n, err
}

// estimateProvider builds the secondary telemetry-estimate card from
// SWARMERY_USAGE_LIMITS and our own indexed token counts.
//
// ok=false (with a nil error) means SWARMERY_USAGE_LIMITS is unset or empty —
// no estimate card is emitted at all. A malformed value is errBadUsageLimits; a
// failed token query is returned as-is so the handler can 500.
//
// Pace is computed by usage.CalculatePace so both cards share ONE definition.
// This is a deliberate behavioural change from the old ratio helper — see the
// pace-semantics table in usage_test.go.
func (h *Handler) estimateProvider(now time.Time) (usage.Provider, bool, error) {
	cfg, err := parseUsageLimits(os.Getenv("SWARMERY_USAGE_LIMITS"))
	if err != nil {
		return usage.Provider{}, false, fmt.Errorf("%w: %v", errBadUsageLimits, err)
	}
	if len(cfg) == 0 {
		return usage.Provider{}, false, nil
	}

	// Deterministic order: shortest window first (session before weekly).
	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if cfg[keys[i]].WindowHours != cfg[keys[j]].WindowHours {
			return cfg[keys[i]].WindowHours < cfg[keys[j]].WindowHours
		}
		return keys[i] < keys[j]
	})

	p := usage.Provider{
		Name:    estimateProviderName,
		Status:  usage.StatusOK,
		Source:  usage.SourceEstimate,
		Windows: make([]usage.Window, 0, len(keys)),
	}

	const bound = "2006-01-02T15:04:05"
	for _, k := range keys {
		c := cfg[k]
		elapsed, window, resetsAt := usageWindowElapsed(now, c.WindowHours)
		windowStart := now.Add(-elapsed)
		used, err := h.usedTokensSince(windowStart.UTC().Format(bound))
		if err != nil {
			return usage.Provider{}, false, err
		}
		label := c.Label
		if label == "" {
			label = k
		}
		// Clamped to 0..100 to match the oauth provider's windows; the raw
		// over-quota signal survives in Used/Limit.
		percentUsed := 0.0
		if c.Tokens > 0 {
			percentUsed = math.Min(100, math.Max(0, float64(used)/float64(c.Tokens)*100))
		}
		resetMs := resetsAt.Sub(now).Milliseconds()
		w := usage.Window{
			Key:         k,
			Label:       label,
			PercentUsed: percentUsed,
			PercentLeft: 100 - percentUsed,
			ResetMs:     resetMs,
			ResetAt:     resetsAt.UTC().Format(time.RFC3339),
			WindowMs:    window.Milliseconds(),
			Pace:        usage.CalculatePace(percentUsed, resetMs, window.Milliseconds()),
			Source:      usage.SourceEstimate,
			Used:        used,
			Limit:       c.Tokens,
		}
		if resetMs > 0 {
			w.ResetText = "resets in " + usage.FormatDuration(time.Duration(resetMs)*time.Millisecond)
		}
		p.Windows = append(p.Windows, w)
	}
	return p, true, nil
}

// GET /api/usage — the live Claude provider plus the optional telemetry-estimate
// provider. See the file header for the policy note. Cached 30s; the Refresh
// button in the modal appends ?fresh=1 to bypass the cache.
//
// The response body NEVER carries credential material — asserted by
// TestUsageResponseCarriesNoSecrets.
func (h *Handler) usage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("fresh") != "1" {
		if body, ok := cachedUsage(); ok {
			writeJSON(w, body, nil)
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), claudeFetchTimeout)
	defer cancel()

	now := time.Now()
	out := usageResp{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Providers:   make([]usage.Provider, 0, 2),
	}

	// The live card always appears, whatever its status — an error card is the
	// honest answer, an absent card would be a silent failure.
	out.Providers = append(out.Providers, usageClient.Fetch(ctx))

	est, ok, err := h.estimateProvider(now)
	switch {
	case errors.Is(err, errBadUsageLimits):
		http.Error(w, `{"error":"invalid SWARMERY_USAGE_LIMITS JSON"}`, http.StatusInternalServerError)
		return
	case err != nil:
		writeErr(w, err)
		return
	case ok:
		out.Providers = append(out.Providers, est)
	}

	storeUsage(out, now)
	writeJSON(w, out, nil)
}
