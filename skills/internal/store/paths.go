package store

import (
	"path"
	"strings"
)

const (
	MainSkillFile   = "SKILL.md"
	MetaFile        = "skill.yaml"
	maxFileBytes    = 512 * 1024
	maxPackageBytes = 2 * 1024 * 1024
)

var allowedExtensions = map[string]struct{}{
	".md":   {},
	".txt":  {},
	".yaml": {},
	".yml":  {},
}

func isAllowedRelPath(rel string) bool {
	rel = path.Clean(strings.ReplaceAll(rel, "\\", "/"))
	if rel == "." || strings.HasPrefix(rel, "..") || strings.Contains(rel, "/..") {
		return false
	}
	if rel == MetaFile {
		return true
	}
	ext := strings.ToLower(path.Ext(rel))
	if _, ok := allowedExtensions[ext]; !ok {
		return false
	}
	return true
}

func normalizeRelPath(rel string) (string, error) {
	rel = strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
	rel = path.Clean(rel)
	if rel == "." || strings.HasPrefix(rel, "..") || strings.Contains(rel, "/..") {
		return "", ErrInvalidPath
	}
	if !isAllowedRelPath(rel) {
		return "", ErrInvalidPath
	}
	return rel, nil
}
