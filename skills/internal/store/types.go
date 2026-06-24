package store

import (
	"time"

	"gopkg.in/yaml.v3"
)

type Meta struct {
	Title     string `json:"title" yaml:"title"`
	Summary   string `json:"summary" yaml:"summary"`
	Tags      string `json:"tags" yaml:"tags"`
	CreatedAt string `json:"created_at" yaml:"created_at"`
	UpdatedAt string `json:"updated_at" yaml:"updated_at"`
}

type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type Package struct {
	Slug      string `json:"slug"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	Summary   string `json:"summary"`
	Tags      string `json:"tags"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type PackageDetail struct {
	Package
	Files []File `json:"files"`
}

type MatchedFile struct {
	Path    string `json:"path"`
	Excerpt string `json:"excerpt"`
}

type SearchHit struct {
	Package
	Rank         float64       `json:"rank"`
	MatchedFiles []MatchedFile `json:"matched_files"`
	SkillMD      string        `json:"skill_md"`
	// BodyMd mirrors SkillMD for legacy runtime/clients.
	BodyMd string `json:"body_md"`
}

func nowRFC3339Nano() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func decodeMeta(data []byte) (Meta, error) {
	var m Meta
	if len(data) == 0 {
		return m, nil
	}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return Meta{}, err
	}
	return m, nil
}

func encodeMeta(m Meta) ([]byte, error) {
	return yaml.Marshal(m)
}
