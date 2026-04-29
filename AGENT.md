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
  - `adapter-telegram` (long poll) -> `orchestrator`
- Core flow:
  - `orchestrator` coordinates `session`, `llm-openai`, `runtime`, and tool components.
- Runtime/tooling:
  - `runtime` runs ReAct steps and calls model/tools.
  - `user-docker-manager` talks to Docker Engine via `/var/run/docker.sock`.
  - Go/project build execution is handled through `manage_user_docker` + container `exec`.
- Persistence:
  - `session`, `logger`, `stats`, `workspace` use named volumes (`memory` is deferred; see `memory/TODO.md`).
- Dynamic nodes:
  - `userdocker-base` and `userdocker-golang` images are build placeholders in compose; real `userdocker` containers are created on demand by API.

## 3) Service Map (compose-aligned)

- `orchestrator`
  - purpose: registry + health loop + API gateway + chat orchestration
  - entry: `orchestrator/cmd/server/main.go`
  - host exposed: yes (`${ORCHESTRATOR_PORT:-8080}:8080`)
  - note: proxies `POST /api/v1/tools/user-dockers/touch-creator-session` to user-docker-manager (capability `userdocker_touch_creator`)
  - note: exposes `GET /api/v1/stats/overview` as a reverse proxy to the healthy `type=stats` component (`GET …/stats/overview`); returns `503` with `code=stats_disabled` when no stats service is registered
  - note: `GET /health` returns `chat_ready` / `chat_error` (HTTP 200): all of `runtime`, `session`, `llm` (registered component `llm-openai`) must be healthy to chat; `POST /api/v1/chat` rejects with `success=false` and the same English guidance text if not
  - note: `POST /api/v1/chat` only proxies to `runtime` `/run` (no orchestrator-local session+llm-openai fallback)
- `session`
  - purpose: SQLite conversation store
  - entry: `session/cmd/server/main.go`
  - host exposed: no
  - note: supports `DELETE /sessions/{id}` hard-delete in addition to legacy `POST /clear_context`
  - note: per-session **idle expiry** via `SESSION_IDLE_SEC` (extends on each `append_messages`; `get_context` returns `expired` + `expires_at`; append on expired id returns 409)
  - note: message metadata may include real `prompt_tokens` / `completion_tokens` / `total_tokens` and `reply_latency_ms` when upstream provides usage
- `llm-openai`
  - purpose: OpenAI-compatible chat completions client
  - entry: `llm-openai/cmd/server/main.go`
  - host exposed: no
- `runtime`
  - purpose: ReAct loop execution engine
  - entry: `runtime/cmd/server/main.go`
  - host exposed: no
  - note: discovers healthy tool/environment components via orchestrator and builds tool list per run
  - note: defaults `REACT_MAX_STEPS` to 16 and forces a final text-only completion attempt at the last step
  - note: truncates oversized tool payload fields (for example `content_base64`/large stdout) before feeding tool outputs back to model context
  - note: emits structured runtime + tool trace events (`runtime_run_*`, `react_*`, `tool_call_*`) and writes to `logger` when available; when `stats` (`stats_ingest`) is healthy, also posts batched overview metrics to `stats` `POST /events` (messages on successful session append, `tool_call` per tool start, `tokens` on `runtime_run_completed` when usage is present)
  - note: each `/run` does a low-`max_tokens` structured **plan_gate** call to `llm-openai` (unless `RUNTIME_PLAN_GATE=legacy_keyword`) to set `inject_plan_only` + `restrict_mutating_tools`; mutating `manage_user_docker` actions are blocked until the user message matches plan confirmation (`isPlanConfirmationMessage`) when restriction is on
  - note: successful `export_artifact` tool results can be returned as chat attachments (`filename`, `content_base64`)
  - note: at the start of each `/run`, calls `POST /api/v1/tools/user-dockers/touch-creator-session` so temporary userdockers created under that `session_id` have their idle timer reset; refuses run if `get_context` reports expired
  - note: after tool-inventory short path, main chat path appends the user message to `session` before ReAct begins, then appends the assistant message when the run completes (so WebUI shows the user turn while the agent is still working)
- `adapter-telegram`
  - purpose: Telegram user I/O adapter (`type=adapter` at orchestrator registration)
  - entry: `adapter-telegram/cmd/server/main.go`
  - host exposed: no
  - note: outbound replies are converted from standard markdown to Telegram-friendly HTML at send time
  - note: outbound send path strips internal thought/channel markers (for example `<|channel|>...`) before Telegram delivery
  - note: send flow includes retry + format-fallback (HTML -> plain text) and best-effort failure notice to avoid silent drops
  - note: during long runs, polls logger every **2s** and edits one Telegram placeholder in place from logger events (`trace_id` + full `session_id` `telegram_<chatKey>`); text reflects current step/tool action; see `docs/adapter-progress-pattern.md` for the reusable adapter progress pattern (incl. no-edit IM variants)
  - note: can upload binary artifacts to Telegram as documents when runtime returns chat attachments
  - note: may append brief artifact lines to `session` via `SESSION_URL` when uploads succeed (not used for per-step progress)
  - note: fenced code blocks are preserved as `<pre><code>` during Telegram markdown-to-HTML conversion
  - note: supports basic Telegram commands `/new`, `/end`, `/status`, `/help` for session lifecycle control
  - note: first contact uses an auto-generated session id (same key shape as `/new`, not a bare `chat_id` string); when a local chat is `/end`ed, the next plain message auto-starts a new session; background poll notifies IM when the **server** marks a session idle-expired and rotates to a new id
  - note: `/new` still generates a fresh `chatID-…` key for manual resets
- `user-docker-manager`
  - purpose: system-level `userdocker` manager (dual-scope lifecycle + workspace operations)
  - entry: `user-docker-manager/cmd/server/main.go`
  - host exposed: no
  - note: registers to orchestrator as component name `user-docker-manager`
  - note: enforces `userdocker.v1` interface contract and only manages containers labeled as manager-owned `userdocker`
  - note: raw language images (for example official `golang:*`) are rejected unless they expose `/api/v1/userdocker/interface`
  - note: pulling non-framework images requires explicit user approval flag (`external_image_approved_by_user=true`)
  - note: supports `session_scoped` and `global_service` container scopes with `switch-scope`
  - note: session-scoped container names append a sanitized `session_id` suffix to reduce naming conflicts across runs
  - note: `session_scoped` containers store `mvp.userdocker.creator_session_id` (same as create-time `session_id`); **any** request that supplies `session_id` may operate them (no per-container session ownership check); temporary removal TTL from `USERDOCKER_TEMP_TTL_SEC` (or `USERDOCKER_IDLE_HOURS*3600`); `POST /api/v1/user-dockers/touch-creator-session` touches all temp dockers for a creator `session_id`
  - note: exposes `start/stop/touch/exec/files/artifacts/export` APIs and idle sweeper for `session_scoped` containers; `global_service` is not subject to this sweeper
- `logger`
  - purpose: event logs (SQLite)
  - entry: `logger/cmd/server/main.go`
  - host exposed: no
  - note: registers capabilities `events_write`, `events_recent` only
- `stats`
  - purpose: optional Overview metrics (SQLite); ingests batched events from `runtime` / `orchestrator` and serves `GET /stats/overview` with rolling 24h window (hour-aligned)
  - entry: `stats/cmd/server/main.go`
  - host exposed: no
  - note: registers `type=stats` with capabilities `stats_overview`, `stats_ingest`; compose includes the service; omit or stop the container if you do not want metrics
- `memory` (code only; not in default `docker-compose.yml`)
  - purpose: lightweight memory KV/notes (SQLite) — roadmap in `memory/TODO.md`
  - entry: `memory/cmd/server/main.go`
  - host exposed: no (re-add service to compose or run container manually to enable)
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
  - note: session list/detail support hard-delete via orchestrator `DELETE /api/v1/sessions/{id}`; list shows idle-expiry column; session detail shows expiry countdown
  - note: Overview + Components read `user-docker-manager` registry `meta.userdocker_temp_ttl_sec` / `meta.userdocker_idle_check_sec` and combine with `GET /api/v1/tools/user-dockers` list (`last_active_at`, `scope`) for temporary-container idle-removal countdown; Components type badges use a string-hash palette
  - note: Overview top renders three stat cards (对话数量 / 工具调用次数 / Token 消耗) from `GET /api/v1/stats/overview` when the stats service is enabled; shows an explicit "未启用统计服务" banner on `503 stats_disabled`; values use k/M shorthand (>10k -> `Nk`, >1M -> `NM`, one decimal) and a small `近24小时 +N` delta line (`last_24h` from stats service window)
  - note: session detail keeps thought traces and renders them collapsed by default
  - note: session detail includes runtime timeline panel sourced from logger events (`session_id`-scoped `runtime/react/tool` phases)
  - note: `Tools` / `Envs` are selector pages; detailed testers are nested pages
- `userdocker-base`
  - purpose: base image for spawned `userdocker` instances
  - entry: `userdocker-base/main.go`
  - compose behavior: `sleep infinity` placeholder container
  - note: exposes public descriptor `GET /api/v1/userdocker/interface` (contract `userdocker.v1`)
  - note: implements workspace APIs (`/exec`, `/files`, `/file`, `/files/mkdir`, `/files/move`, `/artifacts/export`)
- `userdocker-golang`
  - purpose: Go toolchain image for spawned `userdocker` compile/build tasks
  - build source: `userdocker-base/Dockerfile` with Go final base image
  - compose behavior: `sleep infinity` placeholder container

## 4) Env Variables (grouped, minimal)

- Telegram:
  - `TELEGRAM_BOT_TOKEN` (empty -> service registers, poll loop disabled)
- Model:
  - `MODEL_PROVIDER`, `MODEL_BASE_URL`, `MODEL_API_KEY`, `MODEL_NAME`
  - localhost model endpoints are rewritten by `llm-openai` to `host.docker.internal`
- Ports:
  - `ORCHESTRATOR_PORT`, `SESSION_PORT`, `LLM_OPENAI_PORT`, `USER_DOCKER_MANAGER_PORT`, `ADAPTER_TELEGRAM_PORT`, `RUNTIME_PORT`, `LOGGER_PORT`, `STATS_PORT`, `MEMORY_PORT`, `WORKSPACE_PORT`, `WEBUI_PORT`
- Runtime tuning:
  - `REACT_MAX_STEPS`
- IM/session sync:
  - `SESSION_URL` (for `adapter-telegram` optional artifact append to session)
- Orchestrator request timeout:
  - `ORCHESTRATOR_UPSTREAM_TIMEOUT_SEC`
- Telegram adapter chat timeout:
  - `ADAPTER_TELEGRAM_CHAT_TIMEOUT_SEC`
- Telegram in-chat progress:
  - single placeholder message edited from logger polling every 2s during chat execution (no extra env required); other adapters should follow `docs/adapter-progress-pattern.md`
- Userdocker manager lifecycle:
  - `USERDOCKER_TEMP_TTL_SEC` (optional; temp `session_scoped` removal idle; default `USERDOCKER_IDLE_HOURS*3600`)
  - `USERDOCKER_IDLE_HOURS`, `USERDOCKER_IDLE_CHECK_SEC`, `USERDOCKER_ALLOWED_IMAGES`
- Health loop:
  - `HEALTHCHECK_INTERVAL_SEC`, `HEALTHCHECK_FAIL_THRESHOLD`
- Session:
  - `SESSION_MAX_MESSAGES`, `SESSION_IDLE_SEC` (idle expiry window in seconds, default 86400)

## 5) Current State / Drift Notes

- `docker-compose.yml` contains 12 services including `runtime`, `logger`, `stats`, `workspace` (no `memory` service until roadmap is implemented).
- `README.md` contains broad alignment, but some sections can lag behind compose details; verify against compose first.
- Compose currently exposes only `orchestrator` and `webui` ports to host.
- Named volumes in use: `session_data`, `logger_data`, `stats_data`, `workspace_data`.
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
- `manage_user_docker` runtime actions include lifecycle (`start/stop/touch/switch_scope`), workspace commands/files, and artifact export.
- `manage_user_docker` should query available framework images via `action=list_images` before `action=create`.
- for Go compile tasks, prefer `whalesbot/userdocker-golang:latest` when listed in `action=list_images`.
- runtime no longer relies on `environment`-type execution capability; build/run flows use `manage_user_docker`.
- Degrade behavior:
  - If a capability is not discoverable, runtime should not rely on that tool.
  - Tool calls without healthy backing component must return explicit unavailable errors.
- Quick diagnostics:
  - check chat min stack: `curl -s http://localhost:8080/health` (`chat_ready`, `chat_error`)
  - check components: `curl -s http://localhost:8080/api/v1/components`
  - check persistent logger events: `curl -s http://localhost:8080/api/v1/logger/events/recent?limit=20`
  - check stats overview (when stats service running): `curl -s http://localhost:8080/api/v1/stats/overview`
  - check userdocker manager contract: `curl -s http://localhost:8080/api/v1/tools/user-dockers/interface-contract`
  - check userdocker allowed images: `curl -s http://localhost:8080/api/v1/tools/user-dockers/images`
  - check userdocker list: `curl -s http://localhost:8080/api/v1/tools/user-dockers`
  - ask runtime via chat to list tool names and confirm `manage_user_docker` is visible.

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
