package ingest

import (
	"context"
	"database/sql"
	"log"
	"path/filepath"
	"sort"
)

// RebuildTextStats summarizes one RebuildText run.
type RebuildTextStats struct {
	Files  int // main transcripts re-read successfully
	Errors int // per-file failures (logged, never fatal)
}

// RebuildText re-reads every MAIN transcript under every projects root from
// byte 0 so that turns.text is assembled for turns ingested before migration
// 0005 (assistant message text was deliberately deferred at gate 03). A plain
// `backfill` cannot do this: it tails from persisted offsets and the
// already-consumed lines are never re-read.
//
// Safe to run at any time: fileFrom() ingests inside one transaction per
// transcript, event dedup_keys absorb the full replay (no duplicate rows),
// and the turns.text extend rule is a no-op when the text already matches.
// Sidechain companions are re-read as part of their main transcript.
func RebuildText(ctx context.Context, db *sql.DB, roots []string) RebuildTextStats {
	var stats RebuildTextStats
	for _, root := range roots {
		mains, _ := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
		sort.Strings(mains)
		for _, f := range mains {
			if ctx.Err() != nil {
				return stats
			}
			if _, err := fileFrom(db, f, root); err != nil {
				log.Printf("warn: rebuild-text: %s: %v", f, err)
				stats.Errors++
				continue
			}
			stats.Files++
		}
	}
	return stats
}
