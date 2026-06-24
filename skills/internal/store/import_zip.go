package store

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const maxZipBytes = 10 * 1024 * 1024

// ImportZip extracts a skill directory package from a zip archive into packages/{slug}/.
// The zip may be a flat package (SKILL.md at root) or wrapped in a single top-level folder.
func (s *Store) ImportZip(r io.ReaderAt, size int64, preferredSlug, zipName string) (string, error) {
	if size <= 0 || size > maxZipBytes {
		return "", fmt.Errorf("%w: zip size out of range", ErrInvalidPackage)
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidPackage, err)
	}
	tmp, err := os.MkdirTemp("", "whalebot-skill-import-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	if err := extractZip(zr, tmp); err != nil {
		return "", err
	}
	root, hintSlug, err := resolvePackageRoot(tmp)
	if err != nil {
		return "", err
	}
	if err := normalizeImportedRoot(root); err != nil {
		return "", err
	}

	slug := strings.TrimSpace(preferredSlug)
	if slug != "" {
		if err := validateSlug(slug); err != nil {
			return "", err
		}
	} else if hintSlug != "" {
		slug = hintSlug
	} else {
		slug = slugFromMetaOrName(root, zipName)
	}
	slug = UniqueSlug(slug, s.slugExists)

	dest := filepath.Join(s.packagesRoot(), slug)
	if err := os.RemoveAll(dest); err != nil {
		return "", err
	}
	if err := copyAllowedPackageFiles(root, dest); err != nil {
		_ = os.RemoveAll(dest)
		return "", err
	}
	if err := s.ensurePackageLayout(slug); err != nil {
		_ = os.RemoveAll(dest)
		return "", err
	}
	if err := s.indexPackage(slug); err != nil {
		_ = os.RemoveAll(dest)
		return "", err
	}
	if err := rebuildSearchIndex(s.index); err != nil {
		return "", err
	}
	return slug, nil
}

func extractZip(zr *zip.Reader, dest string) error {
	dest = filepath.Clean(dest)
	var total int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := path.Clean(strings.ReplaceAll(f.Name, "\\", "/"))
		if name == "." || strings.HasPrefix(name, "../") || strings.Contains(name, "/..") {
			return ErrInvalidPath
		}
		base := path.Base(name)
		if base == ".DS_Store" || strings.HasPrefix(base, "._") {
			continue
		}
		if strings.HasPrefix(name, "__MACOSX/") {
			continue
		}
		rel := filepath.FromSlash(name)
		if !isAllowedRelPath(filepath.ToSlash(rel)) && filepath.Base(rel) != MetaFile {
			continue
		}
		target := filepath.Join(dest, rel)
		if !strings.HasPrefix(filepath.Clean(target), dest+string(os.PathSeparator)) && filepath.Clean(target) != dest {
			return ErrInvalidPath
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxFileBytes+1))
		_ = rc.Close()
		if err != nil {
			return err
		}
		if len(data) > maxFileBytes {
			return ErrFileTooLarge
		}
		total += int64(len(data))
		if total > maxPackageBytes {
			return ErrPackageTooLarge
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func resolvePackageRoot(dir string) (string, string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", err
	}
	var dirs []string
	var files []string
	for _, e := range entries {
		name := e.Name()
		if name == "__MACOSX" || strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, name)
		} else {
			files = append(files, name)
		}
	}
	if len(dirs) == 1 && len(files) == 0 {
		hint := ""
		if validateSlug(dirs[0]) == nil {
			hint = dirs[0]
		}
		return filepath.Join(dir, dirs[0]), hint, nil
	}
	if fileExists(filepath.Join(dir, MainSkillFile)) {
		return dir, "", nil
	}
	// allow single markdown file at root
	if len(dirs) == 0 {
		var mdFiles []string
		for _, f := range files {
			if strings.EqualFold(filepath.Ext(f), ".md") && f != MetaFile {
				mdFiles = append(mdFiles, f)
			}
		}
		if len(mdFiles) == 1 {
			return dir, "", nil
		}
	}
	return "", "", ErrInvalidPackage
}

func normalizeImportedRoot(root string) error {
	skillPath := filepath.Join(root, MainSkillFile)
	if fileExists(skillPath) {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	var mdFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.EqualFold(filepath.Ext(name), ".md") {
			mdFiles = append(mdFiles, name)
		}
	}
	if len(mdFiles) != 1 {
		return ErrInvalidPackage
	}
	data, err := os.ReadFile(filepath.Join(root, mdFiles[0]))
	if err != nil {
		return err
	}
	return os.WriteFile(skillPath, data, 0o644)
}

func slugFromMetaOrName(root, zipName string) string {
	meta, err := readMetaFile(filepath.Join(root, MetaFile))
	if err == nil && strings.TrimSpace(meta.Title) != "" {
		return Slugify(meta.Title)
	}
	base := strings.TrimSuffix(filepath.Base(zipName), filepath.Ext(zipName))
	if base != "" {
		return Slugify(base)
	}
	return "skill"
}

func copyAllowedPackageFiles(src, dest string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == src {
				return nil
			}
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}
			return os.MkdirAll(filepath.Join(dest, rel), 0o755)
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if !isAllowedRelPath(relSlash) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
