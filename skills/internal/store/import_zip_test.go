package store_test

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/whalebot/skills/internal/store"
)

func TestImportZipFolder(t *testing.T) {
	root := t.TempDir()
	indexPath := filepath.Join(root, ".index", "index.db")
	st, err := store.Open(root, indexPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeZipFile(t, zw, "demo-skill/SKILL.md", "# Demo\n\nAI 行业动态\n")
	writeZipFile(t, zw, "demo-skill/skill.yaml", "title: Demo Skill\nsummary: demo\n")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	slug, err := st.ImportZip(bytes.NewReader(data), int64(len(data)), "", "demo-skill.zip")
	if err != nil {
		t.Fatal(err)
	}
	if slug != "demo-skill" {
		t.Fatalf("slug = %q", slug)
	}
	hits, err := st.Search("行业动态", 3)
	if err != nil || len(hits) == 0 {
		t.Fatalf("search failed: %v hits=%d", err, len(hits))
	}
}

func writeZipFile(t *testing.T, zw *zip.Writer, name, content string) {
	t.Helper()
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
}

func TestImportZipSingleMarkdown(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(root, filepath.Join(root, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeZipFile(t, zw, "playbook.md", "# Playbook\n")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	slug, err := st.ImportZip(bytes.NewReader(data), int64(len(data)), "", "playbook.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "packages", slug, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}
