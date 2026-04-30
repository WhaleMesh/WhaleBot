# memory

Minimal persistent memory service for WhaleBot.

- **Not** included in the root `docker-compose.yml` by default; roadmap: [`TODO.md`](TODO.md).
- Local image tag: `whalebot/memory:latest`
- Build: `docker build -t whalebot/memory:latest ./memory` (or re-add the service to compose when ready)
- Provides `/health`, `POST /notes`, `GET /notes/{key}`
- Stores notes in SQLite
