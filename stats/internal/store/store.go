package store

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	sql *sql.DB
}

func Open(dbPath string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	d := &DB{sql: db}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts TEXT NOT NULL,
		kind TEXT NOT NULL,
		prompt_tokens INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0,
		total_tokens INTEGER NOT NULL DEFAULT 0,
		meta_json TEXT NOT NULL DEFAULT '{}'
	)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_events_kind_ts ON events(kind, ts)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) Close() error { return d.sql.Close() }

type IngestEvent struct {
	Kind              string
	Ts                time.Time
	PromptTokens      int64
	CompletionTokens  int64
	TotalTokens       int64
	Meta              map[string]string
}

func (d *DB) InsertEvents(events []IngestEvent) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(`INSERT INTO events (ts, kind, prompt_tokens, completion_tokens, total_tokens, meta_json) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range events {
		ts := e.Ts
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		meta := e.Meta
		if meta == nil {
			meta = map[string]string{}
		}
		raw, _ := json.Marshal(meta)
		if _, err := stmt.Exec(ts.UTC().Format(time.RFC3339Nano), e.Kind, e.PromptTokens, e.CompletionTokens, e.TotalTokens, string(raw)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) Overview() (map[string]any, error) {
	end := time.Now().UTC()
	start := end.Truncate(time.Hour).Add(-24 * time.Hour)
	startStr := start.Format(time.RFC3339Nano)
	endStr := end.Format(time.RFC3339Nano)

	var msgTotal, msgWin int64
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'message'`).Scan(&msgTotal); err != nil {
		return nil, err
	}
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'message' AND ts >= ? AND ts <= ?`, startStr, endStr).Scan(&msgWin); err != nil {
		return nil, err
	}

	var tcTotal, tcWin int64
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'tool_call'`).Scan(&tcTotal); err != nil {
		return nil, err
	}
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'tool_call' AND ts >= ? AND ts <= ?`, startStr, endStr).Scan(&tcWin); err != nil {
		return nil, err
	}

	var pTot, cTot, tTot, pWin, cWin, tWin int64
	qTot := `SELECT COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0), COALESCE(SUM(total_tokens),0) FROM events WHERE kind = 'tokens'`
	if err := d.sql.QueryRow(qTot).Scan(&pTot, &cTot, &tTot); err != nil {
		return nil, err
	}
	qWin := qTot + ` AND ts >= ? AND ts <= ?`
	if err := d.sql.QueryRow(qWin, startStr, endStr).Scan(&pWin, &cWin, &tWin); err != nil {
		return nil, err
	}

	return map[string]any{
		"success": true,
		"window": map[string]any{
			"start": startStr,
			"end":   endStr,
			"label": "rolling_24h_hour_aligned",
		},
		"stats": map[string]any{
			"messages": map[string]int64{"total": msgTotal, "last_24h": msgWin},
			"tool_calls": map[string]int64{
				"total": tcTotal, "last_24h": tcWin,
			},
			"tokens": map[string]any{
				"prompt":     map[string]int64{"total": pTot, "last_24h": pWin},
				"completion": map[string]int64{"total": cTot, "last_24h": cWin},
				"total":      map[string]int64{"total": tTot, "last_24h": tWin},
			},
		},
	}, nil
}
