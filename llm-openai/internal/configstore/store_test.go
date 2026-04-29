package configstore

import (
	"path/filepath"
	"testing"
)

func TestReplaceModels_mergeKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceModels([]ProfileInput{
		{ID: "a1", Name: "m1", BaseURL: "https://api.openai.com", APIKey: "secret1", Model: "gpt-4o-mini"},
	}, "a1"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceModels([]ProfileInput{
		{ID: "a1", Name: "m1", BaseURL: "https://api.openai.com", APIKey: "", Model: "gpt-4o-mini"},
	}, "a1"); err != nil {
		t.Fatal(err)
	}
	got, err := s.ActiveProfile()
	if err != nil {
		t.Fatal(err)
	}
	if got.APIKey != "secret1" {
		t.Fatalf("expected key preserved, got %q", got.APIKey)
	}
}

func TestReplaceModels_duplicateName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "c.json"))
	if err != nil {
		t.Fatal(err)
	}
	err = s.ReplaceModels([]ProfileInput{
		{ID: "a1", Name: "dup", BaseURL: "https://x", APIKey: "k", Model: "m"},
		{ID: "a2", Name: "dup", BaseURL: "https://y", APIKey: "k", Model: "m"},
	}, "")
	if err == nil {
		t.Fatal("expected error")
	}
}
