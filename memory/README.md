# memory

Persistent memory + secrets service for WhaleBot.

- **Re-added** to root `docker-compose.yml` (was deferred).
- Local image tag: `whalebot/memory:latest`
- Provides `/health`, notes CRUD, and encrypted secrets CRUD.

## Notes

- `POST /notes` — create/update a note (key/value)
- `GET /notes/{key}` — read a note

## Secrets

- `GET /secrets` — list all secrets (values masked: `ghp_****xyz9`)
- `GET /secrets/{key}` — read full decrypted value (internal use by runtime)
- `POST /secrets` — create a secret (`key`, `value`, `note`)
- `PUT /secrets/{key}` — update (`value` and/or `note`, partial)
- `DELETE /secrets/{key}` — delete

Values are encrypted at rest with AES-256-GCM. Key source:
1. `MEMORY_SECRET_KEY` env var (hex-encoded 32 bytes), or
2. Auto-generated on first start and persisted to `/data/.secret-key`
