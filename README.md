# WhalesBot MVP

Single-host, Docker Compose–based AI orchestration MVP written in Go + Svelte.
All services run on the same host and share a fixed Docker network named `mvp_net`.
Services self-register to the orchestrator, which health-checks them every 5 s
and evicts any that fail 3 checks in a row.

## Quick start

```bash
cp .env.example .env
# (optional) edit .env to set TELEGRAM_BOT_TOKEN / MODEL_API_KEY
docker compose up --build
```

Then open:

- WebUI: http://localhost:3000
- Orchestrator API: http://localhost:8080

If `MODEL_API_KEY` is empty, `chatmodel` runs in **echo mode** — it replies with
a canned response so you can still exercise the full pipeline end-to-end.
If `TELEGRAM_BOT_TOKEN` is empty, `im-telegram` still starts and registers but
skips the long-poll loop.

## Using a local LLM (Ollama, LM Studio, vLLM, …)

If your model server runs on the host machine, just point `MODEL_BASE_URL` at
`localhost` — the `chatmodel` container automatically rewrites `localhost` /
`127.0.0.1` / `::1` to `host.docker.internal`, and `docker-compose.yml` wires
that name to the host gateway on Linux.

Example with [Ollama](https://ollama.com):

```bash
# on the host
ollama pull qwen2.5:7b
ollama serve   # listens on 127.0.0.1:11434
```

```env
# .env
MODEL_BASE_URL=http://localhost:11434
MODEL_API_KEY=ollama          # any non-empty value disables echo mode
MODEL_NAME=qwen2.5:7b
```

`docker compose logs chatmodel` will show a line like
`rewrote localhost base URL to host.docker.internal original=http://localhost:11434 rewritten=http://host.docker.internal:11434`
confirming the rewrite.

## Architecture

```mermaid
flowchart LR
  U["Telegram User"] --> IM["im-telegram"]
  IM -->|"POST /api/v1/chat"| O["orchestrator (:8080)"]
  WUI["webui (:3000)"] -->|REST| O
  O --> S["session (:8090)"]
  O --> M["chatmodel (:8081)"]
  O --> T["tool-docker-creator (:8082)"]
  O --> E["env-golang (:8083)"]
  T -. docker.sock .-> D[(Docker Engine)]
  D -->|"creates"| UD["userdocker (new container)"]
  UD -->|"self-register"| O
  IM -->|"self-register"| O
  S -->|"self-register"| O
  M -->|"self-register"| O
  T -->|"self-register"| O
  E -->|"self-register"| O
```

## Services

| Service | Type | Port | Purpose |
|---|---|---|---|
| `orchestrator` | — | 8080 (host-exposed) | Registry + health-check loop + chat orchestration + API gateway for WebUI |
| `session` | `session` | 8090 | In-memory conversation store (last 40 msgs per session) |
| `chatmodel` | `chat_model` | 8081 | OpenAI-compatible Chat Completions client |
| `im-telegram` | `im_gateway` | 8084 | Telegram Bot long-poll → `orchestrator /api/v1/chat` |
| `tool-docker-creator` | `tool` | 8082 | Creates `userdocker` containers via the Docker Engine API |
| `env-golang` | `environment` | 8083 | Runs arbitrary Go code with `go run` and returns stdout/stderr/exit_code |
| `userdocker-base` | `userdocker` | 9000 (per container) | Minimal self-registering image used by the creator; new instances are spawned on demand |
| `webui` | `webui` | 3000 (host-exposed) | Svelte dashboard (served by nginx) |

## Component registration

Every non-orchestrator service POSTs to
`http://orchestrator:8080/api/v1/components/register` on boot (and every 60 s
thereafter to survive orchestrator restarts) with a body like:

```json
{
  "name": "session",
  "type": "session",
  "version": "0.1.0",
  "endpoint": "http://session:8090",
  "health_endpoint": "http://session:8090/health",
  "capabilities": ["get_context", "append_messages", "clear_context"],
  "meta": {}
}
```

The orchestrator pings each component's `health_endpoint` every
`HEALTHCHECK_INTERVAL_SEC` seconds. On `HEALTHCHECK_FAIL_THRESHOLD` consecutive
failures the component transitions `healthy → unhealthy → removed`.

## Orchestrator API

```
GET  /health
POST /api/v1/components/register
GET  /api/v1/components
POST /api/v1/chat
GET  /api/v1/logs/recent
GET  /api/v1/sessions
GET  /api/v1/sessions/{id}
POST /api/v1/tools/docker-create
POST /api/v1/environments/golang/run
```

See the spec in this repo for request/response shapes. Every response includes
`"success": true|false`.

## Testing the acceptance criteria

### 1. Auto-register

```bash
curl http://localhost:8080/api/v1/components | jq '.components[] | {name,type,status}'
```

Expect 6 components: `session`, `chatmodel`, `im-telegram`, `tool-docker-creator`, `env-golang` — all `healthy`.
(`userdocker-base` is built but its compose container is a sleep placeholder.)

### 2. Telegram dialog

Set `TELEGRAM_BOT_TOKEN` in `.env`, `docker compose up --build`, then message
your bot. You should see two messages exchanged and a session created in the
WebUI **Sessions** tab.

### 3. Context persistence

```bash
curl -s -X POST http://localhost:8080/api/v1/chat \
  -H 'content-type: application/json' \
  -d '{"user_id":"u1","channel":"web","chat_id":"demo","message":"我叫小明"}'
curl -s -X POST http://localhost:8080/api/v1/chat \
  -H 'content-type: application/json' \
  -d '{"user_id":"u1","channel":"web","chat_id":"demo","message":"我叫什么名字？"}'
```

The second reply should reference "小明" (requires a real `MODEL_API_KEY`).

### 4. WebUI

Open http://localhost:3000 → Overview / Components / Sessions / Tools / Env·Go.

### 5. ~15 s removal

```bash
docker stop chatmodel
sleep 16
curl -s http://localhost:8080/api/v1/components | jq '.components[] | select(.name=="chatmodel")'
```

Expect `"status": "removed"` with `failure_count >= 3`.

### 6. Run Go code

```bash
curl -s -X POST http://localhost:8080/api/v1/environments/golang/run \
  -H 'content-type: application/json' \
  -d '{"code":"package main\nimport \"fmt\"\nfunc main(){fmt.Println(\"hello\")}"}' | jq
```

Expect `"stdout": "hello\n"` and `"exit_code": 0`.

### 7. Create a userdocker container

```bash
curl -s -X POST http://localhost:8080/api/v1/tools/docker-create \
  -H 'content-type: application/json' \
  -d '{"name":"user-task-001","network":"mvp_net","auto_register":true,"labels":{"mvp.type":"userdocker"}}' | jq
```

(`image` left blank defaults to `whalesbot/userdocker-base:latest`.)

### 8. New userdocker is visible

```bash
docker ps --filter label=mvp.type=userdocker
curl -s http://localhost:8080/api/v1/components | jq '.components[] | select(.name=="user-task-001")'
```

The new container should appear on `mvp_net` and register itself.

## Troubleshooting

- **`docker.sock` permission denied from `tool-docker-creator`**: on the host,
  either run compose as root, add yourself to the `docker` group, or adjust
  socket permissions. The service needs read+write on `/var/run/docker.sock`.
- **Network name conflict**: `mvp_net` is declared with `name: mvp_net`; if you
  already have a different network by that name, `docker network rm mvp_net`.
- **Model errors**: check the orchestrator logs or `/api/v1/logs/recent`.
  Without a valid `MODEL_API_KEY`, `chatmodel` runs in echo mode — replies are
  deterministic but not from the LLM.
- **WebUI cannot reach orchestrator**: the WebUI calls
  `http://localhost:${ORCHESTRATOR_PORT}` from the browser; that port must be
  host-exposed (it is by default). If you access the WebUI from another host,
  override `ORCHESTRATOR_URL` in the `webui` service's environment.

## Repo layout

```
.
├── docker-compose.yml
├── .env.example
├── README.md
├── orchestrator/          Go
├── session/               Go
├── chatmodel/             Go
├── im-telegram/           Go
├── tool-docker-creator/   Go (talks to Docker Engine API via unix socket)
├── env-golang/            Go (has Go toolchain in its runtime image)
├── userdocker-base/       Go (minimal self-registering server)
└── webui/                 Svelte + Vite, served by nginx
```
