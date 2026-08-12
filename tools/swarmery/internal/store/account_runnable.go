package store

// The account_runnable verdict rows (0054): the persisted answer to "can the
// `claude` CLI run under this account", written by the probe endpoint (and, in
// later phases, by run-truth and the PTY login). Package-level functions over
// *sql.DB, like every other query in this module — the store package owns the
// schema, callers own their handles.
//
// The rows carry a status, a fixed-phrase reason, a timestamp and a source —
// NEVER credential material; the API layer's no-secrets test pins that.

import (
	"database/sql"
	"time"
)

// AccountRunnable is one account's stored verdict.
type AccountRunnable struct {
	Status    string    // 'ready' | 'no-login' | 'unknown' (claudeprobe.Status values)
	Reason    string    // short fixed phrase from internal/claudeprobe, "" for ready
	CheckedAt time.Time // when the verdict was taken
	Source    string    // 'probe' | 'run' | 'pty-login'
}

// PutAccountRunnable upserts account's verdict — the latest answer replaces
// the previous one wholesale; there is no history by design (absence = never
// probed, one row = the current truth).
func PutAccountRunnable(db *sql.DB, account, status, reason, source string, checkedAt time.Time) error {
	_, err := db.Exec(`
		INSERT INTO account_runnable (account, status, reason, checked_at, source)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(account) DO UPDATE SET
			status = excluded.status,
			reason = excluded.reason,
			checked_at = excluded.checked_at,
			source = excluded.source`,
		account, status, reason, checkedAt.Unix(), source)
	return err
}

// GetAccountRunnable reads one account's verdict. ok=false means the account
// was never probed — which readers must render as unknown, not as "not ready".
func GetAccountRunnable(db *sql.DB, account string) (AccountRunnable, bool, error) {
	var row AccountRunnable
	var checkedAt int64
	err := db.QueryRow(
		`SELECT status, reason, checked_at, source FROM account_runnable WHERE account = ?`,
		account).Scan(&row.Status, &row.Reason, &checkedAt, &row.Source)
	if err == sql.ErrNoRows {
		return AccountRunnable{}, false, nil
	}
	if err != nil {
		return AccountRunnable{}, false, err
	}
	row.CheckedAt = time.Unix(checkedAt, 0).UTC()
	return row, true, nil
}

// AllAccountRunnable returns every stored verdict keyed by account — one query
// for the accounts list, instead of one per row.
func AllAccountRunnable(db *sql.DB) (map[string]AccountRunnable, error) {
	rows, err := db.Query(`SELECT account, status, reason, checked_at, source FROM account_runnable`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]AccountRunnable{}
	for rows.Next() {
		var account string
		var row AccountRunnable
		var checkedAt int64
		if err := rows.Scan(&account, &row.Status, &row.Reason, &checkedAt, &row.Source); err != nil {
			return nil, err
		}
		row.CheckedAt = time.Unix(checkedAt, 0).UTC()
		out[account] = row
	}
	return out, rows.Err()
}
