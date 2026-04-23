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
  - `tool-docker-creator` talks to Docker Engine via `/var/run/docker.sock`.
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
- `im-telegram`
  - purpose: Telegram gateway
  - entry: `im-telegram/cmd/server/main.go`
  - host exposed: no
  - note: outbound replies are converted from standard markdown to Telegram-friendly HTML at send time
- `tool-docker-creator`
  - purpose: create `userdocker` containers
  - entry: `tool-docker-creator/cmd/server/main.go`
  - host exposed: no
- `env-golang`
  - purpose: execute Go code (`go run`)
  - entry: `env-golang/cmd/server/main.go`
  - host exposed: no
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
  - note: `Tools` / `Envs` are selector pages; detailed testers are nested pages
- `userdocker-base`
  - purpose: base image for spawned `userdocker` instances
  - entry: `userdocker-base/main.go`
  - compose behavior: `sleep infinity` placeholder container

## 4) Env Variables (grouped, minimal)

- Telegram:
  - `TELEGRAM_BOT_TOKEN` (empty -> service registers, poll loop disabled)
- Model:
  - `MODEL_PROVIDER`, `MODEL_BASE_URL`, `MODEL_API_KEY`, `MODEL_NAME`
  - localhost model endpoints are rewritten by `chatmodel` to `host.docker.internal`
- Ports:
  - `ORCHESTRATOR_PORT`, `SESSION_PORT`, `CHATMODEL_PORT`, `TOOL_DOCKER_CREATOR_PORT`, `ENV_GOLANG_PORT`, `IM_TELEGRAM_PORT`, `RUNTIME_PORT`, `LOGGER_PORT`, `MEMORY_PORT`, `WORKSPACE_PORT`, `WEBUI_PORT`
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

## 7) Mandatory Update Policy

`AGENT.md` must be updated every time the project is updated.

Update triggers (any one requires update):
- add/remove/rename service or module
- change entrypoint path, API surface, dependencies, or call chain
- change compose wiring (ports, volumes, network, health checks, env injection)
- add/remove/rename env vars in `.env.example` or service env defaults
- change local runbook, bootstrap steps, or operational constraints
- change project status, known drifts, or active roadmap assumptions

## 8) Update Checklist (after each project change)

- service map still matches `docker-compose.yml`
- env groups still match `.env.example`
- runbook still works (`cp .env.example .env` + `docker compose up --build`)
- drift notes still accurate (remove resolved drift, add new drift)
- this file stays concise (high-signal, low-token)
