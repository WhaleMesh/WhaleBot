# adapter-telegram

## ServiceCard
```yaml
service: adapter-telegram
role: telegram_user_io_adapter
compose_service: adapter-telegram
image: whalesbot/adapter-telegram:latest
build_context: ./adapter-telegram
owner: tbd
runtime: go_http_service_plus_telegram_long_poll
default_port: 8084
health_endpoint: GET /health
component_registration:
  enabled: true
  name: adapter-telegram
  type: adapter
  capabilities:
    - telegram_text
  meta: {}
last_verified_from:
  - docker-compose.yml
  - adapter-telegram/cmd/server/main.go
```

## Purpose
- Receives Telegram user messages via long-poll and forwards them to orchestrator chat API.
- Sends orchestrator replies back to Telegram chats.
- Converts standard Markdown replies into Telegram-friendly HTML before sending (Telegram-specific render path).
- Provides basic Telegram chat commands (`/new`, `/end`, `/status`, `/help`) for session lifecycle control.
- Exposes only a health endpoint for infrastructure checks.

## External API
### Endpoint: GET /health
```yaml
method: GET
path: /health
request: none
response:
  status: ok
  service: adapter-telegram
error_behavior: standard_http_status
```

## Internal Calls
- `POST ${ORCHESTRATOR_URL}/api/v1/chat` for each incoming Telegram text message.
- `POST ${ORCHESTRATOR_URL}/api/v1/components/register` for service registration.
- Telegram Bot API long-poll and message send operations via token.

## User Commands (Telegram)
- `/new` (`/reset`): starts a new conversation session ID for the current chat.
- `/end` (`/stop`): marks current session as ended; normal text messages are paused until `/new`.
- `/status`: shows current session state (`active`/`ended`) and session ID.
- `/help` (`/commands`): lists supported commands.
- Commands are registered to Telegram via `setMyCommands` at startup; users can select them from bot command UI.

Notes:
- Session switching is managed inside `adapter-telegram` (in-memory state per Telegram chat).
- `/new` now generates a unique logical session key in format `chatID-timestamp-randomhex` to avoid reusing old IDs after service restarts.
- Session history content stays as standard Markdown in backend storage; only Telegram egress rendering is converted.
- On each user text message, adapter sends `sendChatAction(typing)` on a short interval until the orchestrator round trip completes.
- During long-running tasks, adapter polls logger every **2s** by `trace_id` and full `session_id` (`telegram_<chatKey>`, matching runtime) and edits **one** Telegram placeholder message in place (current step / tool action); it does not append per-step lines to `session`.
- Cross-adapter progress **pattern** (Telegram vs no-edit IM, Web, etc.): [docs/adapter-progress-pattern.md](../docs/adapter-progress-pattern.md).
- When backend returns attachments (for example exported artifacts), adapter uploads them as Telegram documents.

## Environment Variables
### ADAPTER_TELEGRAM_PORT
```yaml
name: ADAPTER_TELEGRAM_PORT
default: "8084"
required: false
effect: bind_port_for_health_endpoint
```

### TELEGRAM_BOT_TOKEN
```yaml
name: TELEGRAM_BOT_TOKEN
default: ""
required: false
effect: when_empty_service_skips_poll_loop_but_keeps_health_and_registration
```

### ORCHESTRATOR_URL
```yaml
name: ORCHESTRATOR_URL
default: http://orchestrator:8080
required: false
effect: target_for_chat_forwarding_and_registration
```

### SESSION_URL
```yaml
name: SESSION_URL
default: http://session:8090
required: false
effect: optional_append_artifact_status_lines_to_session_when_telegram_upload_succeeds
```

### ADAPTER_TELEGRAM_CHAT_TIMEOUT_SEC
```yaml
name: ADAPTER_TELEGRAM_CHAT_TIMEOUT_SEC
default: "240"
required: false
effect: timeout_seconds_waiting_for_orchestrator_chat_response_before_returning_timeout_feedback
```

### SERVICE_HOST
```yaml
name: SERVICE_HOST
default: adapter-telegram
required: false
effect: advertised_endpoint_host_for_registration
```

## Runtime Contract
- network: `mvp_net`.
- depends_on: `orchestrator`.
- healthcheck: `wget http://localhost:${ADAPTER_TELEGRAM_PORT}/health`.
- volumes: none.
- security_notes: bot token is sensitive; keep out of logs and commits.

## AI Lookup Hints
```yaml
aliases:
  - telegram_adapter
  - telegram_ingress
query_to_endpoint:
  service_health: GET /health
upstream_chat_target:
  path: POST /api/v1/chat
  host: orchestrator
```

## Change Safety
- Keep Telegram forward payload fields (`user_id`, `channel`, `chat_id`, `message`) unchanged.
- Preserve behavior when `TELEGRAM_BOT_TOKEN` is empty (non-failing degraded mode).
- Do not add blocking logic on health endpoint; infra checks depend on it.
- Keep IM formatting conversion isolated to Telegram egress; do not rewrite session storage content.
