# logger

Minimal event logging service for WhaleBot.

- Local image tag: `whalebot/logger:latest`
- Built locally via `docker compose build/up --build`
- Provides `/health`, `POST /events`, `GET /events/recent`
- Persists events in SQLite at `LOGGER_DB_PATH`
- Registers capabilities `events_write` and `events_recent`
