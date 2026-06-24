# skills

Filesystem-backed skill packages for WhaleBot runtime injection.

## Package layout

Each skill is a directory under `$SKILLS_ROOT/packages/{slug}/`:

```text
packages/ai-hot/
  skill.yaml      # title, summary, tags, timestamps
  SKILL.md        # main entry (always injected first)
  references/     # optional supporting docs (injected when search matches)
    api.md
```

Legacy single-markdown skills are imported as this layout automatically (body → `SKILL.md`).

## Storage

- **Packages**: pure filesystem on volume `skills_data` (compose: `/data/packages/…`)
- **Search index**: SQLite FTS5 at `SKILLS_INDEX_PATH` (default `/data/.index/index.db`)
- **Legacy import**: one-time migration from `SKILLS_LEGACY_DB_PATH` (`/data/skills.db`) when `packages/` is empty

## HTTP API

- `GET /skills` — list packages (metadata)
- `GET /skills/{slug}` — metadata + all text files
- `POST /skills` — create package (`body_md` → `SKILL.md`)
- `POST /skills/import` — multipart upload (`file` = `.zip` skill package; optional `slug`); flat or single-folder layout; single `.md` → `SKILL.md`
- `PUT /skills/{slug}` — update metadata + optional `files` map
- `PUT /skills/{slug}/files/{path}` — write one file
- `POST /skills/{slug}/files` — create file `{path, content}`
- `DELETE /skills/{slug}/files/{path}` — remove file (not `SKILL.md`)
- `DELETE /skills/{slug}` — remove package
- `GET /skills/search?q=…&limit=…` — BM25 hits with `skill_md` + `matched_files`

## Env

| Variable | Default | Purpose |
|----------|---------|---------|
| `SKILLS_PORT` | `18093` | HTTP listen port |
| `SKILLS_ROOT` | `/data` | Root containing `packages/` |
| `SKILLS_INDEX_PATH` | `/data/.index/index.db` | FTS index database |
| `SKILLS_LEGACY_DB_PATH` | `/data/skills.db` | Optional legacy SQLite import source |
