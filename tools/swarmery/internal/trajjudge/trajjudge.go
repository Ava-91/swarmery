// Package trajjudge is the advisory LLM-judge (Verification Contour v2, Phase 2).
// It scores real agent trajectories on a 4-dimension rubric via headless
// claude -p and persists verdicts to trajectory_judgments. Advisory only: no
// verdict ever gates a merge. Best-effort by contract — a failed or unparseable
// verdict skips the candidate rather than persisting garbage or panicking.
package trajjudge

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

// Runner executes one judge prompt and returns the model's raw stdout.
// Mocked in tests; production is ClaudeRunner. Twin of internal/improve.Runner.
type Runner interface {
	Run(ctx context.Context, prompt string) (string, error)
}

// judgment is one parsed rubric verdict (scores are 1..5, higher = better).
type judgment struct {
	EndResult             int     `json:"end_result"`
	InstructionCompliance int     `json:"instruction_compliance"`
	Pitfalls              int     `json:"pitfalls"`
	ToolCalls             int     `json:"tool_calls"`
	Review                string  `json:"review"`
	Overall               float64 `json:"-"`
}

func inRange(n int) bool { return n >= 1 && n <= 5 }

// parseJudgment extracts the first JSON object from raw model output and
// validates every dimension is 1..5 and the review is non-empty. Any failure
// is an error — the caller skips the candidate, never persists a partial row.
func parseJudgment(raw string) (judgment, error) {
	var j judgment
	start := strings.IndexByte(raw, '{')
	end := strings.LastIndexByte(raw, '}')
	if start < 0 || end <= start {
		return j, fmt.Errorf("no JSON object in judge output")
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &j); err != nil {
		return j, fmt.Errorf("unmarshal judge output: %w", err)
	}
	if !inRange(j.EndResult) || !inRange(j.InstructionCompliance) ||
		!inRange(j.Pitfalls) || !inRange(j.ToolCalls) {
		return j, fmt.Errorf("judge score out of 1..5 range: %+v", j)
	}
	if strings.TrimSpace(j.Review) == "" {
		return j, fmt.Errorf("judge review is empty")
	}
	j.Overall = float64(j.EndResult+j.InstructionCompliance+j.Pitfalls+j.ToolCalls) / 4.0
	return j, nil
}

const judgeCtxTimeout = 90 * time.Second

// Score judges up to capN un-judged (session, agent) candidates for the given
// judge model, flagged-first then a bounded random sample. Best-effort: any
// candidate failure is logged and skipped; the batch never aborts. Advisory
// only — nothing here can block a merge.
func Score(db *sql.DB, runner Runner, model string, now time.Time, capN int) error {
	if capN <= 0 {
		return nil
	}
	cands, err := selectCandidates(db, model, now, capN)
	if err != nil {
		return err
	}
	for _, c := range cands {
		evs, err := loadAgentEvents(db, c.sessionID, c.agent)
		if err != nil || len(evs) == 0 {
			continue
		}
		prompt := buildRubricPrompt(summarizeTrajectory(evs))
		ctx, cancel := context.WithTimeout(context.Background(), judgeCtxTimeout)
		out, err := runner.Run(ctx, prompt)
		cancel()
		if err != nil {
			log.Printf("trajjudge: runner failed for session=%d agent=%s: %v", c.sessionID, c.agent, err)
			continue
		}
		j, err := parseJudgment(out)
		if err != nil {
			log.Printf("trajjudge: unparseable verdict for session=%d agent=%s: %v", c.sessionID, c.agent, err)
			continue
		}
		if err := persist(db, c.sessionID, c.agent, model, j, now); err != nil {
			log.Printf("trajjudge: persist failed for session=%d agent=%s: %v", c.sessionID, c.agent, err)
			continue
		}
	}
	return nil
}

type candidate struct {
	sessionID int64
	agent     string
}

// selectCandidates returns up to capN (session, agent) rows from
// trajectory_scores that have no judgment for this model, ordered
// flagged-first (has a trajectory_findings row) then by a stable pseudo-random
// key seeded from now so the unflagged sample is reproducible in tests.
func selectCandidates(db *sql.DB, model string, now time.Time, capN int) ([]candidate, error) {
	seed := now.UTC().Format("2006-01-02T15") // hour-stable seed
	rows, err := db.Query(`
		SELECT s.session_id, s.agent
		FROM trajectory_scores s
		LEFT JOIN trajectory_judgments j
		  ON j.session_id = s.session_id AND j.agent = s.agent AND j.model = ?
		WHERE j.id IS NULL
		ORDER BY
		  (SELECT COUNT(*) FROM trajectory_findings f WHERE f.score_id = s.id) DESC,
		  (s.session_id || s.agent || ?)
		LIMIT ?`, model, seed, capN)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.sessionID, &c.agent); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// loadAgentEvents returns one agent's events in a session, ordered, using the
// same normalized-agent fold as trajeval so the join lines up.
func loadAgentEvents(db *sql.DB, sessionID int64, agent string) ([]event, error) {
	rows, err := db.Query(`
		SELECT e.id, e.type, COALESCE(e.tool_name,'')
		FROM events e
		LEFT JOIN turns t ON t.id = e.turn_id
		WHERE e.session_id = ? AND COALESCE(t.agent_name,'main') = ?
		ORDER BY e.id`, sessionID, agent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []event
	for rows.Next() {
		var e event
		if err := rows.Scan(&e.seq, &e.typ, &e.tool); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// persist inserts one verdict; UNIQUE(session,agent,model) makes re-runs no-ops.
func persist(db *sql.DB, sessionID int64, agent, model string, j judgment, now time.Time) error {
	_, err := db.Exec(`
		INSERT INTO trajectory_judgments
		  (session_id, agent, model, judged_at, end_result, instruction_compliance, pitfalls, tool_calls, overall, review)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(session_id, agent, model) DO NOTHING`,
		sessionID, agent, model, now.UTC().Format(time.RFC3339),
		j.EndResult, j.InstructionCompliance, j.Pitfalls, j.ToolCalls, j.Overall, j.Review)
	return err
}

// ClaudeRunner runs `claude -p --model <id> --output-format text` with the
// prompt on stdin. Binary resolution is a plain PATH lookup (same as
// internal/improve.ClaudeRunner and internal/toolproc). Twin — keep in lockstep.
type ClaudeRunner struct {
	Model string
}

func (r ClaudeRunner) Run(ctx context.Context, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, "claude", "-p", "--model", r.Model, "--output-format", "text")
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude -p: %w; stderr: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
