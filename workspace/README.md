# workspace

Minimal workspace service for the MVP.

- Local image tag: `whalesbot/workspace:latest`
- Built locally via Docker Compose
- Provides `/health`, `GET /workspaces`, `POST /workspaces`
- Persists workspace directories under `WORKSPACE_ROOT`
