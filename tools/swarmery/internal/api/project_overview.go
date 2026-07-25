package api

// GET /api/projects/{id}/overview — project-scoped aggregate for the
// ProjectOverview editorial page (Canvas v2 parity, phase 1).
//
// Returns three sections:
//   - rightNow  — live session / approval state tiles (no day scope).
//   - thisWeek  — current 7-day window vs previous 7-day window with REAL
//                 deltas; a metric with no data serialises as null/dash, never
//                 a guess (no *0.78 fakes).
//   - attention — actionable items derived from existing signals (blocked
//                 approvals, paused/failed board tasks). Empty array is valid.
//
// Archived projects excluded; unknown id → 404 (matches getProject semantics).

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// ── DTOs ─────────────────────────────────────────────────────────────────────

// overviewTile is one "Right now" live-count card.
type overviewTile struct {
	Label string `json:"label"`
	Value int64  `json:"value"`
	Sub   string `json:"sub"`
	Tone  string `json:"tone"` // green | amber | red | neutral
}

// weekMetric is one "This week" delta card.
type weekMetric struct {
	Label     string  `json:"label"`
	Value     *string `json:"value"`     // nil → "—" in the UI
	Delta     *string `json:"delta"`     // nil when prev window has no data
	DeltaTone *string `json:"deltaTone"` // green | red | neutral; nil when no delta
	Sub       string  `json:"sub"`
}

// attentionItem is one "Needs attention" row.
type attentionItem struct {
	Text   string `json:"text"`
	Action string `json:"action"`
	Href   string `json:"href"`
	Tone   string `json:"tone"` // amber | red
}

type projectOverviewDTO struct {
	RightNow  []overviewTile  `json:"rightNow"`
	ThisWeek  []weekMetric    `json:"thisWeek"`
	Attention []attentionItem `json:"attention"`
}

// ── handler ──────────────────────────────────────────────────────────────────

// projectOverview handles GET /api/projects/{id}/overview.
func (h *Handler) projectOverview(w http.ResponseWriter, r *http.Request) {
	// Parse and validate project id.
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid project id"}`, http.StatusBadRequest)
		return
	}

	// Resolve project — gate on archived = 0 (list semantics). The project-detail
	// endpoint allows archived rows by id, but the overview is an editorial surface
	// that only makes sense for active projects.
	var archived int
	err = h.DB.QueryRow(
		`SELECT archived FROM projects WHERE id = ?`, id,
	).Scan(&archived)
	if err == sql.ErrNoRows {
		http.Error(w, `{"error":"project not found"}`, http.StatusNotFound)
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	if archived != 0 {
		http.Error(w, `{"error":"project not found"}`, http.StatusNotFound)
		return
	}

	out := projectOverviewDTO{
		RightNow:  make([]overviewTile, 0, 3),
		ThisWeek:  make([]weekMetric, 0, 4),
		Attention: make([]attentionItem, 0),
	}

	// ── rightNow ─────────────────────────────────────────────────────────────

	// Running (active) sessions for this project.
	var running int64
	if err := h.DB.QueryRow(
		`SELECT COUNT(*) FROM sessions s WHERE s.status = 'active' AND s.project_id = ?`, id,
	).Scan(&running); err != nil {
		writeErr(w, err)
		return
	}
	runTone := "neutral"
	if running > 0 {
		runTone = "green"
	}
	out.RightNow = append(out.RightNow, overviewTile{
		Label: "running",
		Value: running,
		Sub:   "sessions",
		Tone:  runTone,
	})

	// Sessions blocked on a pending approval (distinct sessions, not approval count).
	var blockedApprovals int64
	if err := h.DB.QueryRow(`
		SELECT COUNT(DISTINCT pr.session_id)
		FROM permission_requests pr
		JOIN sessions s ON s.id = pr.session_id
		WHERE pr.status = 'pending' AND s.project_id = ?`, id,
	).Scan(&blockedApprovals); err != nil {
		writeErr(w, err)
		return
	}
	approvalTone := "neutral"
	if blockedApprovals > 0 {
		approvalTone = "amber"
	}
	out.RightNow = append(out.RightNow, overviewTile{
		Label: "awaiting approval",
		Value: blockedApprovals,
		Sub:   "sessions",
		Tone:  approvalTone,
	})

	// Sessions completed today (local calendar day → UTC bounds, same as dayBounds).
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dayStartStr, dayEndStr := dayBounds(todayStart)
	var doneToday int64
	if err := h.DB.QueryRow(`
		SELECT COUNT(*) FROM sessions s
		WHERE s.status IN ('completed','done')
		  AND s.ended_at >= ? AND s.ended_at < ?
		  AND s.project_id = ?`,
		dayStartStr, dayEndStr, id,
	).Scan(&doneToday); err != nil {
		writeErr(w, err)
		return
	}
	out.RightNow = append(out.RightNow, overviewTile{
		Label: "done today",
		Value: doneToday,
		Sub:   "sessions",
		Tone:  "neutral",
	})

	// ── thisWeek ─────────────────────────────────────────────────────────────
	// Two rolling 7-day windows (UTC, zone-suffix-free like dayBounds/healthBoundFmt):
	//   current  = [now-7d, now)
	//   previous = [now-14d, now-7d)

	const windowFmt = "2006-01-02T15:04:05"
	nowUTC := now.UTC()
	curEnd := nowUTC.Format(windowFmt)
	curStart := nowUTC.AddDate(0, 0, -7).Format(windowFmt)
	prevStart := nowUTC.AddDate(0, 0, -14).Format(windowFmt)
	// prevEnd == curStart (contiguous, non-overlapping windows).

	// signedDelta formats a signed integer delta string and tone.
	signedDelta := func(cur, prev int64) (string, string) {
		d := cur - prev
		switch {
		case d > 0:
			return fmt.Sprintf("+%d", d), "green"
		case d < 0:
			return fmt.Sprintf("%d", d), "red"
		default:
			return "0", "neutral"
		}
	}

	// 1. Tasks shipped: board tasks that reached done/archived in each window.
	//    Source: tasks.column_moved_at in range, scoped by project_id.
	var tasksCur, tasksPrev int64
	_ = h.DB.QueryRow(`
		SELECT COUNT(*) FROM tasks t
		WHERE t.project_id = ? AND t.source = 'queue'
		  AND t.board_column IN ('done','archived')
		  AND t.column_moved_at >= ? AND t.column_moved_at < ?`,
		id, curStart, curEnd,
	).Scan(&tasksCur)
	_ = h.DB.QueryRow(`
		SELECT COUNT(*) FROM tasks t
		WHERE t.project_id = ? AND t.source = 'queue'
		  AND t.board_column IN ('done','archived')
		  AND t.column_moved_at >= ? AND t.column_moved_at < ?`,
		id, prevStart, curStart,
	).Scan(&tasksPrev)
	tasksVal := fmt.Sprintf("%d", tasksCur)
	tm := weekMetric{Label: "tasks shipped", Value: &tasksVal, Sub: "vs prev 7d"}
	if tasksPrev > 0 || tasksCur > 0 {
		dStr, dTone := signedDelta(tasksCur, tasksPrev)
		tm.Delta = &dStr
		tm.DeltaTone = &dTone
	}
	out.ThisWeek = append(out.ThisWeek, tm)

	// 2. Sessions started in each window (hidden excluded for consistency with the
	//    session list surface).
	var sessCur, sessPrev int64
	_ = h.DB.QueryRow(`
		SELECT COUNT(*) FROM sessions s
		WHERE s.project_id = ? AND s.hidden = 0
		  AND s.started_at >= ? AND s.started_at < ?`,
		id, curStart, curEnd,
	).Scan(&sessCur)
	_ = h.DB.QueryRow(`
		SELECT COUNT(*) FROM sessions s
		WHERE s.project_id = ? AND s.hidden = 0
		  AND s.started_at >= ? AND s.started_at < ?`,
		id, prevStart, curStart,
	).Scan(&sessPrev)
	sessVal := fmt.Sprintf("%d", sessCur)
	sm := weekMetric{Label: "sessions", Value: &sessVal, Sub: "vs prev 7d"}
	if sessPrev > 0 || sessCur > 0 {
		dStr, dTone := signedDelta(sessCur, sessPrev)
		sm.Delta = &dStr
		sm.DeltaTone = &dTone
	}
	out.ThisWeek = append(out.ThisWeek, sm)

	// 3. Cost: sum of priced turns in each window. Null when no priced turn —
	//    honesty rule (never a lying zero, matches project_health.go).
	var costCurNull, costPrevNull sql.NullFloat64
	var pricedCur, pricedPrev int64
	_ = h.DB.QueryRow(`
		SELECT COALESCE(SUM(t.cost_usd), 0), COUNT(t.cost_usd)
		FROM turns t
		JOIN sessions s ON s.id = t.session_id
		WHERE s.project_id = ? AND t.cost_usd IS NOT NULL
		  AND t.started_at >= ? AND t.started_at < ?`,
		id, curStart, curEnd,
	).Scan(&costCurNull, &pricedCur)
	_ = h.DB.QueryRow(`
		SELECT COALESCE(SUM(t.cost_usd), 0), COUNT(t.cost_usd)
		FROM turns t
		JOIN sessions s ON s.id = t.session_id
		WHERE s.project_id = ? AND t.cost_usd IS NOT NULL
		  AND t.started_at >= ? AND t.started_at < ?`,
		id, prevStart, curStart,
	).Scan(&costPrevNull, &pricedPrev)

	cm := weekMetric{Label: "cost", Sub: "vs prev 7d"}
	if pricedCur > 0 {
		v := fmt.Sprintf("$%.2f", costCurNull.Float64)
		cm.Value = &v
		if pricedPrev > 0 {
			d := costCurNull.Float64 - costPrevNull.Float64
			var dStr, dTone string
			switch {
			case d > 0.001:
				dStr = fmt.Sprintf("+$%.2f", d)
				dTone = "red" // cost increase is bad
			case d < -0.001:
				dStr = fmt.Sprintf("-$%.2f", -d)
				dTone = "green" // cost decrease is good
			default:
				dStr = "$0.00"
				dTone = "neutral"
			}
			cm.Delta = &dStr
			cm.DeltaTone = &dTone
		}
	}
	out.ThisWeek = append(out.ThisWeek, cm)

	// 4. Approvals asked in each window.
	var approvalsCur, approvalsPrev int64
	_ = h.DB.QueryRow(`
		SELECT COUNT(*) FROM permission_requests pr
		JOIN sessions s ON s.id = pr.session_id
		WHERE s.project_id = ?
		  AND pr.requested_at >= ? AND pr.requested_at < ?`,
		id, curStart, curEnd,
	).Scan(&approvalsCur)
	_ = h.DB.QueryRow(`
		SELECT COUNT(*) FROM permission_requests pr
		JOIN sessions s ON s.id = pr.session_id
		WHERE s.project_id = ?
		  AND pr.requested_at >= ? AND pr.requested_at < ?`,
		id, prevStart, curStart,
	).Scan(&approvalsPrev)
	appVal := fmt.Sprintf("%d", approvalsCur)
	am := weekMetric{Label: "approvals asked", Value: &appVal, Sub: "vs prev 7d"}
	if approvalsPrev > 0 || approvalsCur > 0 {
		dStr, dTone := signedDelta(approvalsCur, approvalsPrev)
		am.Delta = &dStr
		am.DeltaTone = &dTone
	}
	out.ThisWeek = append(out.ThisWeek, am)

	// ── attention ────────────────────────────────────────────────────────────

	// 1. Sessions blocked on a pending approval for > 1 hour.
	threshold := nowUTC.Add(-time.Hour).Format(windowFmt)
	var blockedOld int64
	_ = h.DB.QueryRow(`
		SELECT COUNT(DISTINCT pr.session_id)
		FROM permission_requests pr
		JOIN sessions s ON s.id = pr.session_id
		WHERE pr.status = 'pending' AND s.project_id = ?
		  AND pr.requested_at < ?`, id, threshold,
	).Scan(&blockedOld)
	if blockedOld > 0 {
		noun := "sessions"
		if blockedOld == 1 {
			noun = "session"
		}
		out.Attention = append(out.Attention, attentionItem{
			Text:   fmt.Sprintf("%d %s blocked on approval > 1h", blockedOld, noun),
			Action: "Review",
			Href:   "/approvals",
			Tone:   "amber",
		})
	}

	// 2. Paused board tasks (either dispatcher-paused or user-paused).
	var pausedTasks int64
	_ = h.DB.QueryRow(`
		SELECT COUNT(*) FROM tasks t
		WHERE t.project_id = ? AND t.source = 'queue'
		  AND (t.paused = 1 OR t.user_paused = 1)
		  AND t.board_column NOT IN ('done','archived')`,
		id,
	).Scan(&pausedTasks)
	if pausedTasks > 0 {
		noun := "tasks"
		if pausedTasks == 1 {
			noun = "task"
		}
		out.Attention = append(out.Attention, attentionItem{
			Text:   fmt.Sprintf("%d board %s paused", pausedTasks, noun),
			Action: "Open board",
			Href:   "/board",
			Tone:   "amber",
		})
	}

	// 3. Failed board tasks not yet resolved (moved to done/archived).
	var failedTasks int64
	_ = h.DB.QueryRow(`
		SELECT COUNT(*) FROM tasks t
		WHERE t.project_id = ? AND t.source = 'queue'
		  AND t.status = 'failed'
		  AND t.board_column NOT IN ('done','archived')`,
		id,
	).Scan(&failedTasks)
	if failedTasks > 0 {
		noun := "tasks"
		if failedTasks == 1 {
			noun = "task"
		}
		out.Attention = append(out.Attention, attentionItem{
			Text:   fmt.Sprintf("%d board %s failed", failedTasks, noun),
			Action: "Open board",
			Href:   "/board",
			Tone:   "red",
		})
	}

	writeJSON(w, out, nil)
}
