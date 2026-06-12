package store

import (
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type legacySkill struct {
	ID      int64
	Title   string
	Summary string
	BodyMd  string
	Tags    string
	Created string
	Updated string
}

// MigrateLegacyDB imports rows from the pre-filesystem skills SQLite DB into
// packages/{slug}/ directories (SKILL.md + skill.yaml).
func MigrateLegacyDB(st *Store, legacyDBPath string) error {
	if legacyDBPath == "" {
		return nil
	}
	if _, err := os.Stat(legacyDBPath); err != nil {
		return nil
	}
	entries, err := os.ReadDir(st.packagesRoot())
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return nil
	}
	db, err := sql.Open("sqlite", legacyDBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	var tableExists int
	_ = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='skills'`).Scan(&tableExists)
	if tableExists == 0 {
		return nil
	}
	rows, err := db.Query(`SELECT id, title, summary, body_md, tags, created_at, updated_at FROM skills ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var legacy []legacySkill
	for rows.Next() {
		var s legacySkill
		if err := rows.Scan(&s.ID, &s.Title, &s.Summary, &s.BodyMd, &s.Tags, &s.Created, &s.Updated); err != nil {
			return err
		}
		legacy = append(legacy, s)
	}
	if len(legacy) == 0 {
		return nil
	}
	slog.Info("migrating legacy sqlite skills to filesystem packages", "count", len(legacy))
	for _, item := range legacy {
		title := item.Title
		if title == "" {
			title = "Skill"
		}
		slug := UniqueSlug(Slugify(title), st.slugExists)
		dir := filepath.Join(st.packagesRoot(), slug)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		meta := Meta{
			Title:     title,
			Summary:   item.Summary,
			Tags:      item.Tags,
			CreatedAt: item.Created,
			UpdatedAt: item.Updated,
		}
		if meta.CreatedAt == "" {
			meta.CreatedAt = nowRFC3339Nano()
		}
		if meta.UpdatedAt == "" {
			meta.UpdatedAt = meta.CreatedAt
		}
		if err := st.writeMeta(slug, meta); err != nil {
			return err
		}
		body := item.BodyMd
		if body == "" {
			body = "# " + title + "\n"
		}
		if err := st.writeFile(slug, MainSkillFile, body); err != nil {
			return err
		}
	}
	return st.ReindexAll()
}

// ImportSingleMarkdown creates a package directory from one markdown document.
func (st *Store) ImportSingleMarkdown(title, summary, tags, body string) (string, error) {
	return st.Create(CreateInput{Title: title, Summary: summary, Tags: tags, BodyMd: body})
}
