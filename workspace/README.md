# workspace

Minimal workspace service for WhaleBot.

- Local image tag: `whalebot/workspace:latest`
- Built locally via Docker Compose
- Provides `/health`, `GET /workspaces`, `POST /workspaces`
- Persists workspace directories under `WORKSPACE_ROOT`
