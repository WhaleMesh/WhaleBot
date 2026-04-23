# im-telegram

## ServiceCard
```yaml
service: im-telegram
role: telegram_ingress_gateway
compose_service: im-telegram
image: whalesbot/im-telegram:latest
build_context: ./im-telegram
owner: tbd
runtime: go_http_service_plus_telegram_long_poll
default_port: 8084
health_endpoint: GET /health
component_registration:
  enabled: true
  name: im-telegram
  type: im_gateway
  capabilities:
    - telegram_text
  meta: {}
last_verified_from:
  - docker-compose.yml
  - im-telegram/cmd/server/main.go
```

## Purpose
- Receives Telegram user messages via long-poll and forwards them to orchestrator chat API.
- Sends orchestrator replies back to Telegram chats.
- Converts standard Markdown replies into Telegram-friendly HTML before sending (IM-specific render adapter).
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
  service: im-telegram
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
- Session switching is managed inside `im-telegram` (in-memory state per Telegram chat).
- Session history content stays as standard Markdown in backend storage; only Telegram egress rendering is converted.

## Environment Variables
### IM_TELEGRAM_PORT
```yaml
name: IM_TELEGRAM_PORT
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

### SERVICE_HOST
```yaml
name: SERVICE_HOST
default: im-telegram
required: false
effect: advertised_endpoint_host_for_registration
```

## Runtime Contract
- network: `mvp_net`.
- depends_on: `orchestrator`.
- healthcheck: `wget http://localhost:${IM_TELEGRAM_PORT}/health`.
- volumes: none.
- security_notes: bot token is sensitive; keep out of logs and commits.

## AI Lookup Hints
```yaml
aliases:
  - telegram_gateway
  - telegram_ingress
  - im_adapter
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
