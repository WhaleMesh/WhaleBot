# adapter-webui

ChatGPT-like web chat interface for WhaleBot. Registers with orchestrator as `type=adapter` (capabilities `webui_chat`).

## Architecture

```
Browser → External reverse proxy (nginx/Traefik/etc.) → Go backend (:18083)
                                                        ├─ Static SPA files (/srv)
                                                        ├─ API endpoints (/api/adapter-webui/*)
                                                        ├─ Config endpoints (/api/v1/adapter/*)
                                                        └─ Dynamic env.js
```

No Caddy in container — the Go binary serves everything directly. Use an external reverse proxy for TLS/domain routing.

## Features

- JWT-based authentication for chat login (default: `admin` / `whalebot`)
- Chat with orchestrator via synchronous HTTP proxy
- Session management (list, view, delete)
- Real-time progress polling during long-running tasks
- Markdown rendering with code blocks
- Thought trace display (collapsed by default)
- File attachment download support
- i18n (English, Chinese, Japanese)
- Dark theme matching WhaleBot WebUI

## Endpoints

### Auth

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/adapter-webui/auth/health` | Health check |
| POST | `/api/adapter-webui/auth/login` | Login (sets HttpOnly JWT cookie) |
| POST | `/api/adapter-webui/auth/logout` | Logout (clears cookie) |
| GET | `/api/adapter-webui/auth/me` | Current user info |
| PUT | `/api/adapter-webui/auth/credentials` | Change username/password |

### Chat

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/adapter-webui/chat` | Send message to orchestrator |
| GET | `/api/adapter-webui/sessions` | List sessions (webui_* filtered) |
| GET | `/api/adapter-webui/sessions/:id` | Get session detail |
| DELETE | `/api/adapter-webui/sessions/:id` | Delete session |
| GET | `/api/adapter-webui/logger/events` | Logger events (progress polling) |

### Config (for orchestrator Adapters page proxy)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/adapter/config` | Read current admin-set login config (`username`, `has_password`) |
| PUT | `/api/v1/adapter/config` | Directly set login `username` / `password` (no auth required; used by dashboard admin page) |

### Static

| Method | Path | Description |
|--------|------|-------------|
| GET | `/env.js` | Runtime env injection (dynamic, no-cache) |
| GET | `/*` | SPA with fallback to index.html |

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `ADAPTER_WEBUI_PORT` | `18083` | Go backend listen port |
| `ADAPTER_DATA_DIR` | `/data` | Data directory for credentials/config |
| `STATIC_DIR` | `/srv` | Static files directory |
| `ORCHESTRATOR_URL` | `http://orchestrator:18080` | Orchestrator URL |
| `SESSION_URL` | `http://session:18090` | Session service URL |
| `ADAPTER_WEBUI_CHAT_TIMEOUT_SEC` | `240` | Chat proxy timeout (seconds) |
| `SERVICE_HOST` | `adapter-webui` | Service hostname for registration |

## Development

```bash
# Frontend dev server (with API proxy to local backend)
cd web && npm install && npm run dev

# Go backend
go run ./cmd/server
```

## Docker Build

```bash
docker build -t whalebot/adapter-webui .
docker run -p 18083:18083 -e ORCHESTRATOR_URL=http://host.docker.internal:18080 whalebot/adapter-webui
```
