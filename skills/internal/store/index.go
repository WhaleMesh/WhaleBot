package store

import (
	"database/sql"
	"strings"
	"unicode"

	_ "modernc.org/sqlite"
)

func openIndex(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS skill_index (
		slug TEXT PRIMARY KEY,
		title TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL DEFAULT '',
		tags TEXT NOT NULL DEFAULT '',
		files_text TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='skill_fts'`).Scan(&n)
	if n == 0 {
		if _, err := db.Exec(`CREATE VIRTUAL TABLE skill_fts USING fts5(
			slug UNINDEXED,
			title,
			summary,
			tags,
			files_text,
			tokenize='unicode61'
		)`); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	_, _ = db.Exec(`PRAGMA journal_mode=WAL`)
	return db, nil
}

func rebuildSearchIndex(db *sql.DB) error {
	if _, err := db.Exec(`DELETE FROM skill_fts`); err != nil {
		return err
	}
	rows, err := db.Query(`SELECT slug, title, summary, tags, files_text FROM skill_index ORDER BY slug`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var slug, title, summary, tags, filesText string
		if err := rows.Scan(&slug, &title, &summary, &tags, &filesText); err != nil {
			return err
		}
		if _, err := db.Exec(`INSERT INTO skill_fts(slug, title, summary, tags, files_text) VALUES (?, ?, ?, ?, ?)`,
			slug, title, summary, tags, filesText); err != nil {
			return err
		}
	}
	return nil
}

func upsertIndexRow(db *sql.DB, pkg Package, filesText string) error {
	_, err := db.Exec(`INSERT INTO skill_index(slug, title, summary, tags, files_text, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(slug) DO UPDATE SET
			title=excluded.title,
			summary=excluded.summary,
			tags=excluded.tags,
			files_text=excluded.files_text,
			created_at=excluded.created_at,
			updated_at=excluded.updated_at`,
		pkg.Slug, pkg.Title, pkg.Summary, pkg.Tags, filesText, pkg.CreatedAt, pkg.UpdatedAt)
	return err
}

func deleteIndexRow(db *sql.DB, slug string) error {
	_, err := db.Exec(`DELETE FROM skill_index WHERE slug=?`, slug)
	return err
}

func searchIndex(db *sql.DB, rawQuery string, limit int) ([]SearchHit, error) {
	match := ftsMatchQuery(rawQuery)
	if match == "" {
		return nil, nil
	}
	rows, err := db.Query(`
		SELECT slug, title, summary, tags, files_text, bm25(skill_fts) AS rnk
		FROM skill_fts
		WHERE skill_fts MATCH ?
		ORDER BY rnk
		LIMIT ?`, match, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []SearchHit
	for rows.Next() {
		var h SearchHit
		var filesText string
		if err := rows.Scan(&h.Slug, &h.Title, &h.Summary, &h.Tags, &filesText, &h.Rank); err != nil {
			return nil, err
		}
		h.ID = h.Slug
		h.MatchedFiles = matchedFilesFromText(filesText, rawQuery)
		hits = append(hits, h)
	}
	return hits, nil
}

func ftsMatchQuery(raw string) string {
	var tokens []string
	var cur strings.Builder
	flush := func() {
		t := strings.TrimSpace(cur.String())
		cur.Reset()
		if t == "" {
			return
		}
		low := strings.ToLower(t)
		if low == "or" || low == "and" || low == "not" || low == "near" {
			t = low + "_"
		}
		t = strings.ReplaceAll(t, `"`, " ")
		if strings.TrimSpace(t) == "" {
			return
		}
		tokens = append(tokens, `"`+strings.ReplaceAll(t, `"`, "")+`"`)
	}
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			cur.WriteRune(r)
		} else if unicode.IsSpace(r) || r == '_' || r == '-' || r == '/' || r == '.' {
			flush()
		} else {
			flush()
		}
	}
	flush()
	if len(tokens) == 0 {
		return ""
	}
	if len(tokens) == 1 {
		return tokens[0]
	}
	return "(" + strings.Join(tokens, " OR ") + ")"
}

func matchedFilesFromText(filesText, rawQuery string) []MatchedFile {
	if strings.TrimSpace(filesText) == "" {
		return nil
	}
	q := strings.ToLower(rawQuery)
	var out []MatchedFile
	for _, block := range strings.Split(filesText, "\n--- file: ") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		pathEnd := strings.Index(block, " ---\n")
		if pathEnd <= 0 {
			continue
		}
		p := block[:pathEnd]
		content := block[pathEnd+len(" ---\n"):]
		if q != "" && !strings.Contains(strings.ToLower(p+"\n"+content), q) {
			// keep if any token matches loosely
			if !fileMatchesQuery(content, rawQuery) {
				continue
			}
		}
		out = append(out, MatchedFile{Path: p, Excerpt: excerpt(content, 600)})
	}
	return out
}

func fileMatchesQuery(content, rawQuery string) bool {
	for _, tok := range strings.FieldsFunc(rawQuery, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsNumber(r))
	}) {
		if len(tok) < 2 {
			continue
		}
		if strings.Contains(strings.ToLower(content), strings.ToLower(tok)) {
			return true
		}
	}
	return false
}

func excerpt(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func buildFilesText(files []File) string {
	var b strings.Builder
	for _, f := range files {
		if f.Path == MetaFile {
			continue
		}
		b.WriteString("\n--- file: ")
		b.WriteString(f.Path)
		b.WriteString(" ---\n")
		b.WriteString(f.Content)
	}
	return b.String()
}
