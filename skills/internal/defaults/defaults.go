package defaults

import (
	"database/sql"
	_ "embed"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/whalebot/skills/internal/store"
)

//go:embed whalemesh_best_practices.md
var whalemeshBody string

const (
	WhalemeshSlug    = "whalemesh-best-practices"
	WhalemeshTitle   = "whalemesh best practices"
	whalemeshSummary = "In-chat playbook: use only injected docker_* tools for user containers (userdocker / 用户容器), staged read-then-mutate flows, remove temporary containers when done, respect plan/safety behavior."
	whalemeshTags    = "whalemesh,chat-agent,tools,userdocker,用户容器,docker_lifecycle,docker_exec,docker_files"
)

// EnsureSeed creates the default whalemesh package when packages/ is empty.
func EnsureSeed(root string, st *store.Store) error {
	packagesRoot := filepath.Join(root, "packages")
	entries, err := os.ReadDir(packagesRoot)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return nil
	}
	slog.Info("seeding default skill package", "slug", WhalemeshSlug)
	_, err = st.Create(store.CreateInput{
		Title:   WhalemeshTitle,
		Summary: whalemeshSummary,
		Tags:    whalemeshTags,
		BodyMd:  whalemeshBody,
	})
	return err
}

// EnsureSeedLegacy is kept for tests that still pass *sql.DB; no-op.
func EnsureSeedLegacy(_ *sql.DB) error { return nil }
