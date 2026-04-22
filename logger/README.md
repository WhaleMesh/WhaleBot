# logger

Minimal event logging service for the MVP.

- Local image tag: `whalesbot/logger:latest`
- Built locally via `docker compose build/up --build`
- Provides `/health`, `POST /events`, `GET /events/recent`
- Persists events in SQLite at `LOGGER_DB_PATH`
