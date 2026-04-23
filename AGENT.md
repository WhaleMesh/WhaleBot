# AGENT Context (WhalesBot MVP)

This file is the lowest-token project brief for AI agents.
Read this first, then read only the referenced source-of-truth files.

## 1) Project Snapshot

- Goal: single-host, Docker Compose based AI orchestration MVP (`Go + Svelte`).
- Network: fixed Docker network `mvp_net`.
- Entry runbook:
  - `cp .env.example .env`
  - optional: fill `TELEGRAM_BOT_TOKEN` / `MODEL_API_KEY`
  - `docker compose up --build`
- Host URLs:
  - WebUI: `http://localhost:3000`
  - Orchestrator API: `http://localhost:8080`
- Source of truth priority:
  1. `docker-compose.yml`
  2. `.env.example`
  3. `README.md`

## 2) Architecture At A Glance

- Ingress:
  - `webui` (browser) -> `orchestrator`
  - `im-telegram` (long poll) -> `orchestrator`
- Core flow:
  - `orchestrator` coordinates `session`, `chatmodel`, `runtime`, tools/environments.
- Runtime/tooling:
  - `runtime` runs ReAct steps and calls model/tools.
  - `user-docker-manager` talks to Docker Engine via `/var/run/docker.sock`.
  - `env-golang` executes user Go code.
- Persistence:
  - `session`, `logger`, `memory`, `workspace` use named volumes.
- Dynamic nodes:
  - `userdocker-base` image is build placeholder in compose; real `userdocker` containers are created on demand by API.

## 3) Service Map (compose-aligned)

- `orchestrator`
  - purpose: registry + health loop + API gateway + chat orchestration
  - entry: `orchestrator/cmd/server/main.go`
  - host exposed: yes (`${ORCHESTRATOR_PORT:-8080}:8080`)
- `session`
  - purpose: SQLite conversation store
  - entry: `session/cmd/server/main.go`
  - host exposed: no
  - note: message metadata may include real `prompt_tokens` / `completion_tokens` / `total_tokens` and `reply_latency_ms` when upstream provides usage
- `chatmodel`
  - purpose: OpenAI-compatible chat completions client
  - entry: `chatmodel/cmd/server/main.go`
  - host exposed: no
- `runtime`
  - purpose: ReAct loop execution engine
  - entry: `runtime/cmd/server/main.go`
  - host exposed: no
  - note: discovers healthy tool/environment components via orchestrator and builds tool list per run
  - note: emits structured tool lifecycle events (`tool_call_start` / `tool_call_end` / `tool_call_error`) and writes to `logger` when available
- `im-telegram`
  - purpose: Telegram gateway
  - entry: `im-telegram/cmd/server/main.go`
  - host exposed: no
  - note: outbound replies are converted from standard markdown to Telegram-friendly HTML at send time
  - note: fenced code blocks are preserved as `<pre><code>` during Telegram markdown-to-HTML conversion
  - note: supports basic Telegram commands `/new`, `/end`, `/status`, `/help` for session lifecycle control
  - note: `/new` generates unique logical session keys (`chatID-timestamp-randomhex`) to avoid historical ID reuse after restart
- `user-docker-manager`
  - purpose: system-level `userdocker` manager (list/create/remove/restart/interface discovery)
  - entry: `user-docker-manager/cmd/server/main.go`
  - host exposed: no
  - note: registers to orchestrator as component name `user-docker-manager`
  - note: enforces `userdocker.v1` interface contract when creating userdocker containers
- `env-golang`
  - purpose: execute Go code (`go run`)
  - entry: `env-golang/cmd/server/main.go`
  - host exposed: no
  - note: callable by runtime as tool `run_go_code` through orchestrator endpoint `/api/v1/environments/golang/run`
- `logger`
  - purpose: event logs (SQLite)
  - entry: `logger/cmd/server/main.go`
  - host exposed: no
- `memory`
  - purpose: lightweight memory KV/notes (SQLite)
  - entry: `memory/cmd/server/main.go`
  - host exposed: no
- `workspace`
  - purpose: workspace directory manager
  - entry: `workspace/cmd/server/main.go`
  - host exposed: no
- `webui`
  - purpose: Svelte dashboard via caddy
  - entry: `webui/src/main.js`
  - host exposed: yes (`${WEBUI_PORT:-3000}:80`)
  - note: router is hash-based so refresh keeps current page
  - note: includes dedicated `Logger` page in addition to overview logs
  - note: `Logger` page supports persistent logger events (`/api/v1/logger/events/recent`) + orchestrator recent logs dual-source diagnosis
  - note: session detail auto-scroll follows new messages only when user is near bottom; header/meta stays sticky
  - note: `Tools` / `Envs` are selector pages; detailed testers are nested pages
- `userdocker-base`
  - purpose: base image for spawned `userdocker` instances
  - entry: `userdocker-base/main.go`
  - compose behavior: `sleep infinity` placeholder container
  - note: exposes public descriptor `GET /api/v1/userdocker/interface` (contract `userdocker.v1`)

## 4) Env Variables (grouped, minimal)

- Telegram:
  - `TELEGRAM_BOT_TOKEN` (empty -> service registers, poll loop disabled)
- Model:
  - `MODEL_PROVIDER`, `MODEL_BASE_URL`, `MODEL_API_KEY`, `MODEL_NAME`
  - localhost model endpoints are rewritten by `chatmodel` to `host.docker.internal`
- Ports:
  - `ORCHESTRATOR_PORT`, `SESSION_PORT`, `CHATMODEL_PORT`, `USER_DOCKER_MANAGER_PORT`, `ENV_GOLANG_PORT`, `IM_TELEGRAM_PORT`, `RUNTIME_PORT`, `LOGGER_PORT`, `MEMORY_PORT`, `WORKSPACE_PORT`, `WEBUI_PORT`
- Runtime tuning:
  - `REACT_MAX_STEPS`
- Health loop:
  - `HEALTHCHECK_INTERVAL_SEC`, `HEALTHCHECK_FAIL_THRESHOLD`
- Session:
  - `SESSION_MAX_MESSAGES`

## 5) Current State / Drift Notes

- `docker-compose.yml` contains 12 services including `runtime`, `logger`, `memory`, `workspace`.
- `README.md` contains broad alignment, but some sections can lag behind compose details; verify against compose first.
- Compose currently exposes only `orchestrator` and `webui` ports to host.
- Named volumes in use: `session_data`, `logger_data`, `memory_data`, `workspace_data`.
- Current repository scan does not find a `worker/` directory; if present locally in another branch/untracked state, treat it as non-compose unless compose is updated.

## 6) Rules For Future Agents (must follow)

- Always read `AGENT.md` first, then only open files needed for the task.
- Treat `docker-compose.yml` + `.env.example` as runtime truth.
- Do not infer service wiring from stale docs without compose confirmation.
- Keep changes minimal and consistent with current compose/network model.
- If you change architecture, service list, env vars, ports, run commands, or status assumptions, you MUST update this file in the same change.

## 7) Runtime Capability Injection

- Runtime discovers capabilities per chat run from `GET /api/v1/components` on orchestrator.
- Only components with `status=healthy` are considered.
- Tool mapping:
  - `type=tool` + capabilities `userdocker_*` -> tool `manage_user_docker` (endpoint `/api/v1/tools/user-dockers`)
  - `type=environment` + capability `run_go` -> tool `run_go_code` (endpoint `/api/v1/environments/golang/run`)
- Degrade behavior:
  - If a capability is not discoverable, runtime should not rely on that tool.
  - Tool calls without healthy backing component must return explicit unavailable errors.
- Quick diagnostics:
  - check components: `curl -s http://localhost:8080/api/v1/components`
  - check persistent logger events: `curl -s http://localhost:8080/api/v1/logger/events/recent?limit=20`
  - check userdocker manager contract: `curl -s http://localhost:8080/api/v1/tools/user-dockers/interface-contract`
  - check userdocker list: `curl -s http://localhost:8080/api/v1/tools/user-dockers`
  - check env route: `curl -s -X POST http://localhost:8080/api/v1/environments/golang/run ...`
  - ask runtime via chat to list tool names and confirm `manage_user_docker` and `run_go_code` are visible.

## 8) Mandatory Update Policy

`AGENT.md` must be updated every time the project is updated.

Update triggers (any one requires update):
- add/remove/rename service or module
- change entrypoint path, API surface, dependencies, or call chain
- change compose wiring (ports, volumes, network, health checks, env injection)
- add/remove/rename env vars in `.env.example` or service env defaults
- change local runbook, bootstrap steps, or operational constraints
- change project status, known drifts, or active roadmap assumptions

## 9) Update Checklist (after each project change)

- service map still matches `docker-compose.yml`
- env groups still match `.env.example`
- runbook still works (`cp .env.example .env` + `docker compose up --build`)
- drift notes still accurate (remove resolved drift, add new drift)
- this file stays concise (high-signal, low-token)
