package defaults

import (
	"database/sql"
	_ "embed"
	"log/slog"
	"time"
)

//go:embed whalemesh_best_practices.md
var whalemeshBody string

const (
	WhalemeshTitle   = "whalemesh best practices"
	whalemeshSummary = "In-chat playbook: use only injected tools (primarily manage_user_docker), staged read-then-mutate flows, remove temporary containers when done, respect plan/safety behavior."
	whalemeshTags    = "whalemesh,chat-agent,tools,userdocker,react"
)

// EnsureSeed inserts the default whalemesh skill when the skills table is empty.
func EnsureSeed(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM skills`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := db.Exec(
		`INSERT INTO skills (title, summary, body_md, tags, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		WhalemeshTitle, whalemeshSummary, whalemeshBody, whalemeshTags, now, now,
	)
	if err != nil {
		return err
	}
	slog.Info("seeded default skill", "title", WhalemeshTitle)
	return nil
}
