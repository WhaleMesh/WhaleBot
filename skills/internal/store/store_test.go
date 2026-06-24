package store_test

import (
	"path/filepath"
	"testing"

	"github.com/whalebot/skills/internal/store"
)

func TestCreateSearchPackage(t *testing.T) {
	root := t.TempDir()
	indexPath := filepath.Join(root, ".index", "index.db")
	st, err := store.Open(root, indexPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	slug, err := st.Create(store.CreateInput{
		Title:   "AI Hot",
		Summary: "AI industry news skill",
		Tags:    "ai,news",
		BodyMd:  "# AI Hot\n\n看一下 AI 行业动态\n",
		Files: map[string]string{
			"references/api.md": "# API\n\nUse curl examples here.\n",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hits, err := st.Search("看一下 AI 行业动态", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("expected search hit")
	}
	if hits[0].Slug != slug {
		t.Fatalf("slug = %q want %q", hits[0].Slug, slug)
	}
	if hits[0].SkillMD == "" {
		t.Fatal("expected skill_md content")
	}
}
