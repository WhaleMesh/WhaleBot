package store

import (
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Store struct {
	root  string
	index *sql.DB
}

func Open(root, indexPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(root, "packages"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		return nil, err
	}
	db, err := openIndex(indexPath)
	if err != nil {
		return nil, err
	}
	s := &Store{root: root, index: db}
	return s, nil
}

func (s *Store) Close() error {
	if s.index == nil {
		return nil
	}
	return s.index.Close()
}

func (s *Store) packagesRoot() string {
	return filepath.Join(s.root, "packages")
}

func (s *Store) packageDir(slug string) (string, error) {
	if err := validateSlug(slug); err != nil {
		return "", err
	}
	return filepath.Join(s.packagesRoot(), slug), nil
}

func validateSlug(slug string) error {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return ErrInvalidSlug
	}
	if slug == "." || slug == ".." || strings.Contains(slug, "/") || strings.Contains(slug, `\`) {
		return ErrInvalidSlug
	}
	return nil
}

func (s *Store) slugExists(slug string) bool {
	dir, err := s.packageDir(slug)
	if err != nil {
		return false
	}
	st, err := os.Stat(dir)
	return err == nil && st.IsDir()
}

func (s *Store) ReindexAll() error {
	entries, err := os.ReadDir(s.packagesRoot())
	if err != nil {
		return err
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		if err := s.indexPackage(ent.Name()); err != nil {
			return err
		}
	}
	return rebuildSearchIndex(s.index)
}

func (s *Store) indexPackage(slug string) error {
	detail, err := s.loadPackage(slug)
	if err != nil {
		return err
	}
	filesText := buildFilesText(detail.Files)
	return upsertIndexRow(s.index, detail.Package, filesText)
}

func (s *Store) loadPackage(slug string) (*PackageDetail, error) {
	dir, err := s.packageDir(slug)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return nil, ErrNotFound
	}
	metaPath := filepath.Join(dir, MetaFile)
	metaBytes, err := os.ReadFile(metaPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	meta, err := decodeMeta(metaBytes)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(meta.Title) == "" {
		meta.Title = slug
	}
	pkg := Package{
		Slug:      slug,
		ID:        slug,
		Title:     meta.Title,
		Summary:   meta.Summary,
		Tags:      meta.Tags,
		CreatedAt: meta.CreatedAt,
		UpdatedAt: meta.UpdatedAt,
	}
	var files []File
	var total int
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !isAllowedRelPath(rel) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if len(data) > maxFileBytes {
			return ErrFileTooLarge
		}
		total += len(data)
		if total > maxPackageBytes {
			return ErrPackageTooLarge
		}
		files = append(files, File{Path: rel, Content: string(data)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return &PackageDetail{Package: pkg, Files: files}, nil
}

func (s *Store) List(limit, offset int) ([]Package, error) {
	entries, err := os.ReadDir(s.packagesRoot())
	if err != nil {
		return nil, err
	}
	var slugs []string
	for _, ent := range entries {
		if ent.IsDir() {
			slugs = append(slugs, ent.Name())
		}
	}
	sort.Slice(slugs, func(i, j int) bool { return slugs[i] > slugs[j] })
	if offset >= len(slugs) {
		return nil, nil
	}
	slugs = slugs[offset:]
	if limit > 0 && len(slugs) > limit {
		slugs = slugs[:limit]
	}
	var out []Package
	for _, slug := range slugs {
		detail, err := s.loadPackage(slug)
		if err != nil {
			continue
		}
		out = append(out, detail.Package)
	}
	return out, nil
}

func (s *Store) Get(slug string) (*PackageDetail, error) {
	return s.loadPackage(slug)
}

type CreateInput struct {
	Title   string
	Summary string
	Tags    string
	BodyMd  string
	Files   map[string]string
}

func (s *Store) Create(in CreateInput) (string, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "New skill"
	}
	slug := UniqueSlug(Slugify(title), s.slugExists)
	dir := filepath.Join(s.packagesRoot(), slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	now := nowRFC3339Nano()
	meta := Meta{Title: title, Summary: in.Summary, Tags: in.Tags, CreatedAt: now, UpdatedAt: now}
	if err := s.writeMeta(slug, meta); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	skillMD := strings.TrimSpace(in.BodyMd)
	if skillMD == "" {
		skillMD = "# " + title + "\n"
	}
	if err := s.writeFile(slug, MainSkillFile, skillMD); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	for path, content := range in.Files {
		if path == MainSkillFile || path == MetaFile {
			continue
		}
		if err := s.writeFile(slug, path, content); err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
	}
	if err := s.indexPackage(slug); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	if err := rebuildSearchIndex(s.index); err != nil {
		return "", err
	}
	return slug, nil
}

type UpdateInput struct {
	Title   string
	Summary string
	Tags    string
	Files   map[string]string
}

func (s *Store) Update(slug string, in UpdateInput) error {
	if !s.slugExists(slug) {
		return ErrNotFound
	}
	detail, err := s.loadPackage(slug)
	if err != nil {
		return err
	}
	meta := Meta{
		Title:     detail.Title,
		Summary:   detail.Summary,
		Tags:      detail.Tags,
		CreatedAt: detail.CreatedAt,
		UpdatedAt: nowRFC3339Nano(),
	}
	if strings.TrimSpace(in.Title) != "" {
		meta.Title = strings.TrimSpace(in.Title)
	}
	meta.Summary = in.Summary
	meta.Tags = in.Tags
	if err := s.writeMeta(slug, meta); err != nil {
		return err
	}
	for path, content := range in.Files {
		if err := s.writeFile(slug, path, content); err != nil {
			return err
		}
	}
	if err := s.indexPackage(slug); err != nil {
		return err
	}
	return rebuildSearchIndex(s.index)
}

func (s *Store) PutFile(slug, relPath, content string) error {
	if !s.slugExists(slug) {
		return ErrNotFound
	}
	if err := s.writeFile(slug, relPath, content); err != nil {
		return err
	}
	meta, err := s.readMeta(slug)
	if err != nil {
		return err
	}
	meta.UpdatedAt = nowRFC3339Nano()
	if err := s.writeMeta(slug, meta); err != nil {
		return err
	}
	if err := s.indexPackage(slug); err != nil {
		return err
	}
	return rebuildSearchIndex(s.index)
}

func (s *Store) DeleteFile(slug, relPath string) error {
	if !s.slugExists(slug) {
		return ErrNotFound
	}
	rel, err := normalizeRelPath(relPath)
	if err != nil {
		return err
	}
	if rel == MainSkillFile || rel == MetaFile {
		return ErrInvalidPath
	}
	dir, err := s.packageDir(slug)
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, rel)); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	if err := s.indexPackage(slug); err != nil {
		return err
	}
	return rebuildSearchIndex(s.index)
}

func (s *Store) Delete(slug string) error {
	if !s.slugExists(slug) {
		return ErrNotFound
	}
	dir, err := s.packageDir(slug)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := deleteIndexRow(s.index, slug); err != nil {
		return err
	}
	return rebuildSearchIndex(s.index)
}

func (s *Store) Search(q string, limit int) ([]SearchHit, error) {
	hits, err := searchIndex(s.index, q, limit)
	if err != nil {
		return nil, err
	}
	for i := range hits {
		detail, err := s.loadPackage(hits[i].Slug)
		if err != nil {
			continue
		}
		hits[i].CreatedAt = detail.CreatedAt
		hits[i].UpdatedAt = detail.UpdatedAt
		for _, f := range detail.Files {
			if f.Path == MainSkillFile {
				hits[i].SkillMD = f.Content
				hits[i].BodyMd = f.Content
				break
			}
		}
		if len(hits[i].MatchedFiles) == 0 && strings.TrimSpace(hits[i].SkillMD) != "" {
			hits[i].MatchedFiles = []MatchedFile{{Path: MainSkillFile, Excerpt: excerpt(hits[i].SkillMD, 600)}}
		}
	}
	return hits, nil
}

func (s *Store) writeMeta(slug string, meta Meta) error {
	dir, err := s.packageDir(slug)
	if err != nil {
		return err
	}
	data, err := encodeMeta(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, MetaFile), data, 0o644)
}

func (s *Store) readMeta(slug string) (Meta, error) {
	dir, err := s.packageDir(slug)
	if err != nil {
		return Meta{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, MetaFile))
	if err != nil {
		return Meta{}, err
	}
	return decodeMeta(data)
}

func (s *Store) writeFile(slug, relPath, content string) error {
	rel, err := normalizeRelPath(relPath)
	if err != nil {
		return err
	}
	if len(content) > maxFileBytes {
		return ErrFileTooLarge
	}
	dir, err := s.packageDir(slug)
	if err != nil {
		return err
	}
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	detail, err := s.loadPackage(slug)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	total := len(content)
	if detail != nil {
		total = packageBytesWithout(detail.Files, rel) + len(content)
	}
	if total > maxPackageBytes {
		return ErrPackageTooLarge
	}
	return os.WriteFile(full, []byte(content), 0o644)
}

func packageBytesWithout(files []File, skipPath string) int {
	total := 0
	for _, f := range files {
		if f.Path == skipPath {
			continue
		}
		total += len(f.Content)
	}
	return total
}

func (s *Store) ImportLegacyDirectory(slug, srcDir string) error {
	if err := validateSlug(slug); err != nil {
		return err
	}
	dest := filepath.Join(s.packagesRoot(), slug)
	if s.slugExists(slug) {
		return os.ErrExist
	}
	if err := copyTree(srcDir, dest); err != nil {
		return err
	}
	if err := s.ensurePackageLayout(slug); err != nil {
		_ = os.RemoveAll(dest)
		return err
	}
	if err := s.indexPackage(slug); err != nil {
		_ = os.RemoveAll(dest)
		return err
	}
	return rebuildSearchIndex(s.index)
}

func (s *Store) ensurePackageLayout(slug string) error {
	dir, err := s.packageDir(slug)
	if err != nil {
		return err
	}
	metaPath := filepath.Join(dir, MetaFile)
	skillPath := filepath.Join(dir, MainSkillFile)
	if _, err := os.Stat(skillPath); err != nil {
		return err
	}
	meta, err := readMetaFile(metaPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.TrimSpace(meta.Title) == "" {
		meta.Title = slug
	}
	if meta.CreatedAt == "" {
		meta.CreatedAt = nowRFC3339Nano()
	}
	meta.UpdatedAt = nowRFC3339Nano()
	return s.writeMeta(slug, meta)
}

func readMetaFile(path string) (Meta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Meta{}, err
	}
	return decodeMeta(data)
}

func copyTree(src, dest string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
