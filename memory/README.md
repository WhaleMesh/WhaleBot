# memory

Minimal persistent memory service for the MVP.

- **Not** included in the root `docker-compose.yml` by default; roadmap: [`TODO.md`](TODO.md).
- Local image tag: `whalesbot/memory:latest`
- Build: `docker build -t whalesbot/memory:latest ./memory` (or re-add the service to compose when ready)
- Provides `/health`, `POST /notes`, `GET /notes/{key}`
- Stores notes in SQLite
