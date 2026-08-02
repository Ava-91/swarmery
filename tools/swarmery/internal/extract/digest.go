package extract

import (
	"database/sql"
	"fmt"
	"strings"
)

const (
	// digestLimit is the hard byte ceiling on what reaches the model. The point
	// of this feature is to be CHEAPER than the session it reads: an unbounded
	// digest of a marathon session would cost more than the cards are worth
	// (the same reasoning internal/handoff applies to its own digest).
	digestLimit = 16 << 10 // 16KB
	// maxAssistantTurns / assistantTextCap bound the "what the session actually
	// did" half of the digest. Assistant prose carries the work; the newest
	// turns carry the leftovers, which is what we are asking the model to name.
	maxAssistantTurns = 20
	assistantTextCap  = 1_500
	// firstPromptCap bounds the "what the session was FOR" half. The opening
	// prompt is the intent every extracted task has to stay inside.
	firstPromptCap = 4_000
)

// Digest builds the extraction input from the DB — never from the transcript
// file. Same discipline as internal/handoff.Digest: the daemon already parsed
// this session into turns, and re-reading a 400k-token JSONL to ask a question
// about it would make the answer cost as much as the session did.
//
// Layout is deliberately flat (a header, the opening prompt, the recent
// assistant prose) so the ≤16KB truncation below can cut the TAIL without
// losing the intent — the opening prompt is written first and always survives.
func Digest(db *sql.DB, sessionID int64) (string, error) {
	var b strings.Builder

	var title, cwd, branch sql.NullString
	err := db.QueryRow(`
		SELECT COALESCE(custom_title, title, ''), COALESCE(cwd, ''), COALESCE(git_branch, '')
		  FROM sessions WHERE id = ?`, sessionID).Scan(&title, &cwd, &branch)
	if err != nil {
		return "", err
	}
	b.WriteString("## Session\n")
	fmt.Fprintf(&b, "- Title: %s\n", nz(title.String, "unknown"))
	fmt.Fprintf(&b, "- Working dir: %s\n", nz(cwd.String, "unknown"))
	fmt.Fprintf(&b, "- Git branch: %s\n\n", nz(branch.String, "unknown"))

	// The opening prompt is read from the user_prompt EVENT, not from turns:
	// ingest stores the operator's first message there verbatim, and it is the
	// one piece of the digest that must never be truncated away.
	b.WriteString("## The session opened with\n")
	if p := firstUserPrompt(db, sessionID, firstPromptCap); p != "" {
		b.WriteString(p)
		b.WriteString("\n\n")
	} else {
		b.WriteString("(no opening prompt recorded)\n\n")
	}

	asst, err := recentTurns(db, sessionID, "assistant", maxAssistantTurns, assistantTextCap)
	if err != nil {
		return "", err
	}
	b.WriteString("## Recent assistant messages (oldest first)\n")
	if len(asst) == 0 {
		b.WriteString("- (none)\n")
	}
	for _, t := range asst {
		fmt.Fprintf(&b, "- %s\n", t)
	}

	return truncate(b.String(), digestLimit), nil
}

// firstUserPrompt returns the session's opening prompt from the stored
// user_prompt event, ordered by event id — for the FIRST prompt that is file
// order, since the opener is written before anything else and nothing
// renumbers rows. Returns "" when there is no prompt or the read fails: a
// missing opener degrades the digest, it does not fail the extraction.
func firstUserPrompt(db *sql.DB, sessionID int64, limit int) string {
	var content sql.NullString
	if err := db.QueryRow(`
		SELECT json_extract(payload, '$.content') FROM events
		 WHERE session_id = ? AND type = 'user_prompt'
		 ORDER BY id LIMIT 1`, sessionID).Scan(&content); err != nil {
		return ""
	}
	return truncate(strings.TrimSpace(content.String), limit)
}

// recentTurns returns up to `limit` non-empty turns of the given role, each
// truncated to `cap` chars, in ascending seq order — the newest `limit` turns
// selected by a subquery, then re-sorted oldest→newest so the digest reads
// forward.
func recentTurns(db *sql.DB, sessionID int64, role string, limit, cap int) ([]string, error) {
	rows, err := db.Query(`
		SELECT text FROM (
			SELECT seq, text FROM turns
			 WHERE session_id = ? AND role = ? AND text IS NOT NULL AND TRIM(text) != ''
			 ORDER BY seq DESC
			 LIMIT ?
		) ORDER BY seq ASC`, sessionID, role, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		// Collapse newlines: each turn is one "- " bullet, and a multi-line body
		// would break the list shape the prompt tells the model to read.
		out = append(out, truncate(strings.Join(strings.Fields(text), " "), cap))
	}
	return out, rows.Err()
}

// buildPrompt wraps a digest in the extraction contract. The contract is stated
// twice on purpose — as a rule list and as a literal shape — because the parse
// on the other side accepts exactly one form (parseTasks) and a near-miss
// costs a whole paid run.
func buildPrompt(digest string) string {
	return `You are triaging ONE Claude Code session into follow-up work for a Kanban board.

Below is a digest of that session: what it was asked to do, and the most recent
things the assistant said while doing it.

Name the concrete follow-up tasks the session LEFT BEHIND — work that is still
outstanding, was explicitly deferred, or was discovered and not done. Do not
restate work the digest shows as finished.

Answer with a fenced JSON array and NOTHING else:

` + "```json" + `
[{"title": "short imperative title", "prompt": "a self-contained instruction a coding agent could execute without seeing this session"}]
` + "```" + `

Rules:
- At most 10 items, ordered most important first. Fewer is better.
- Return [] when the session left nothing actionable. An empty array is a valid,
  expected answer — do not invent work to fill it.
- "title" is one line, at most ` + fmt.Sprint(titleLimit) + ` characters.
- "prompt" must stand alone: name the files, symbols and commands from the
  digest rather than referring to "the session" or "the above".
- No meta-tasks ("review this session", "write a summary", "verify the changes").

--- SESSION DIGEST ---

` + digest
}

// nz returns s, or def when s is empty.
func nz(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// truncate caps s at n RUNES (not bytes): the operator writes prompts in
// languages where a byte split lands mid-rune and renders as a replacement
// character. Mirrors internal/ingest.clip for the same reason.
//
// The ellipsis is counted INSIDE the budget, unlike clip's — here n is a real
// ceiling (digestLimit is a cost bound and titleLimit is a column width), not
// an approximate one, so "≤ n runes" has to hold for the returned string rather
// than for the part before the marker.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimRight(string(r[:n-1]), " ") + "…"
}
