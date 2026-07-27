# AGENT Context (WhaleBot)

This file is the lowest-token project brief for AI agents.
Read this first, then read only the referenced source-of-truth files.

## 1) Project Snapshot

- Goal: Docker Compose based AI orchestration (`Go + Svelte`). Single host runs the full stack; **userdocker nodes** can additionally run on remote machines (outbound-only, via reverse tunnel to the orchestrator).
- Network: fixed Docker network `whalebot_net`.
- Entry runbook:
 - `cp .env.example .env`
 - `docker compose up --build`
 - WebUI: sign in → LLM page + Adapters → `adapter-telegram` (bot token from @BotFather) for Telegram bot chat
 - optional remote userdocker node: `ORCHESTRATOR_PUBLIC_URL=… NODE_NAME=… NODE_TOKEN=… docker compose -f docker-compose.node.yml up --build`
- Host URLs:
 - WebUI: `http://localhost:18000`
 - Orchestrator API: `http://localhost:18080`
 - Adapter WebUI (chat): `http://localhost:18083`
- Source of truth priority:
 1. `docker-compose.yml` (+ `docker-compose.node.yml` for remote nodes)
 2. `.env.example`
 3. `README.md`

## 1.5) Control Plane (heartbeat model)

- Registration IS the heartbeat: every component POSTs `/api/v1/components/register` every **10s** (`internal/registerclient` copy per service, identical file). Payload: `name/type/version/endpoint/capabilities/meta` + optional `operational_state`.
- Liveness is derived: healthy while last heartbeat is within `HEARTBEAT_TTL_SEC` (default 30); stale → `removed`; entries silent >10x TTL are purged. **The orchestrator never probes components** (no `health_endpoint`/`status_endpoint` in the registry; the old pull health-check loop is gone). `/health` routes still exist per service for compose healthchecks only.
- Readiness: a non-empty `operational_state` other than `normal` keeps the component live but excluded from `FirstReadyByType/Capability` (e.g. `llm-openai` reports `no_valid_configuration` in its heartbeat; its former `GET /status` endpoint is removed).
- `user-docker-manager` is the exception: it does not use registerclient at all — its tunnel connection is its registration and liveness (see 1.6).

## 1.6) Userdocker Nodes (distributed data plane)

- Only `userdocker` supports distributed deployment. Each `user-docker-manager` is a **node**: it dials `POST /api/v1/nodes/connect` on the orchestrator with header `X-Node-Token: $NODE_TOKEN` + JSON hello `{node, version, capabilities, meta}`, the connection upgrades (101) to **yamux** (orchestrator=client, manager=server), and the manager serves its normal HTTP API over tunnel streams. Reconnect with backoff; orchestrator side registers component `user-docker-manager@<node>` (type `tool`, `tunnel:true`) on connect and deletes it on disconnect.
- Container identity everywhere above the manager is the composite name **`"<node>/<container>"`**: orchestrator `list` fans out to all nodes and rewrites names (+`node` field, per-node failures in `node_errors`); `create`/`pull` accept an optional `node` (default: first node by name); pull `job_id` is composite `"<node>/<job>"`; `touch-creator-session` broadcasts to all nodes; container-scoped routes `/{node}/{cname}[/…]` pass through verbatim to that node.
- Remote machines need only outbound access to the orchestrator port; spawned userdockers self-register through the same public URL (`ORCHESTRATOR_URL` env is injected by the manager).
- Key files: `orchestrator/internal/nodes/hub.go` (accept/registry), `orchestrator/internal/httpapi/userdocker.go` (routing), `user-docker-manager/internal/tunnel/tunnel.go` (dialer).

## 2) Architecture At A Glance

- Ingress:
  - `webui` (browser) -> `orchestrator`
  - `adapter-telegram` (long poll) -> `orchestrator`
  - `adapter-webui` (browser chat) -> `orchestrator`
- Core flow:
  - `orchestrator` coordinates `session`, `llm-openai`, `runtime`, `skills`, and tool components.
- Runtime/tooling:
  - `runtime` runs ReAct steps and calls model/tools.
  - `user-docker-manager` talks to Docker Engine via `/var/run/docker.sock`.
  - Go/project build execution is handled through `manage_user_docker` + container `exec`.
- Persistence:
  - `session`, `skills`, `logger`, `stats`, `workspace`, `memory`, `webui` use named volumes (`memory` secrets are AES-256-GCM encrypted at rest); `webui` stores dashboard auth (`credentials.json` bcrypt hash + `jwt-secret.bin`); `adapter-webui` stores the same auth pattern in its own volume.
- Dynamic nodes:
  - `userdocker-base` and `userdocker-golang` images are build placeholders in compose; real `userdocker` containers are created on demand by API.

## 3) Service Map (compose-aligned)

- `orchestrator`
 - purpose: registry (heartbeat liveness) + node tunnel hub + API gateway + chat orchestration
 - entry: `orchestrator/cmd/server/main.go`
 - host exposed: yes (`${ORCHESTRATOR_PORT:-18080}:${ORCHESTRATOR_PORT:-18080}`)
 - note: userdocker API is node-scoped (see §1.6): `GET/POST /api/v1/tools/user-dockers` (list fan-out / create with node pick), `GET …/nodes`, `…/images` (fan-out), `…/images/estimate?node=`, `…/pull` + `…/pull/status` (composite job id), `…/copy` (cross-container file copy; composite names, both containers must be on the same node), `…/touch-creator-session` (broadcast), `…/{node}/{cname}[/action]` generic pass-through
 - note: exposes `GET /api/v1/stats/overview` as a reverse proxy to the healthy `type=stats` component (`GET …/stats/overview`); returns `503` with `code=stats_disabled` when no stats service is registered
 - note: `GET /health` returns `chat_ready` / `chat_error` (HTTP 200): `runtime`, `session`, and `llm` (`llm-openai`) must each be **live** (fresh heartbeat) **and** operationally ready (heartbeat `operational_state` empty or `normal`); `POST /api/v1/chat` rejects with `success=false` and the same English guidance text if not
 - note: `POST /api/v1/chat` only proxies to `runtime` `/run` (no orchestrator-local session+llm-openai fallback)
  - note: reverse-proxies `GET|POST /api/v1/skills`, `GET /api/v1/skills/search`, `GET|PUT|DELETE /api/v1/skills/{id}` to the healthy `type=skills` component (`503` when none)
 - note: reverse-proxies `POST /api/v1/benchmark/run`, `GET /api/v1/benchmark/runs`, `DELETE /api/v1/benchmark/runs` (clear all history, in-progress runs kept), `DELETE /api/v1/benchmark/runs/{id}` to the healthy runtime with capability `benchmark` (`503` when none)
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
 - note: model base URL / API key / upstream model id are stored in **`LLM_CONFIG_PATH`** JSON (default `/data/llm-config.json` on volume `llm_openai_data`), edited through WebUI LLM page (or `PUT /api/v1/llm/config` on the service). No root `.env` `MODEL_*`. Localhost-style upstream URLs are rewritten to `host.docker.internal` in the OpenAI client; trailing overlap with `/v1/chat/completions` is stripped from `base_url` so `…/v1`, `…/api/v1`, and full completions URLs all resolve correctly.
 - note: `GET /health` is **liveness-only** (compose healthcheck). Business readiness rides on the register heartbeat as `operational_state` (`normal` | `no_valid_configuration`); there is no `GET /status` endpoint. Without an active model, `POST /invoke` still returns `success=false` with an explanatory `error`.
- `runtime`
  - purpose: ReAct loop execution engine
  - entry: `runtime/cmd/server/main.go`
  - host exposed: no
  - note: discovers healthy tool/environment components via orchestrator and builds tool list per run
  - note: user-docker capability is exposed to the model as **four split tools** (`docker_lifecycle`, `docker_exec`, `docker_files`, `export_artifact`) with narrow schemas (small-local-model friendly); all normalize via `normalizeDockerToolCall` onto the single internal `manage_user_docker` dispatch/gating/logging path, so logger events keep `tool_name=manage_user_docker`
  - note: defaults `REACT_MAX_STEPS` to 16 and forces a final text-only completion attempt at the last step; per-step completion budget `RUNTIME_MAX_TOKENS` (default 4096, sized to cover thinking/reasoning tokens)
  - note: truncates oversized tool payload fields (for example `content_base64`/large stdout) before feeding tool outputs back to model context
  - note: emits structured runtime + tool trace events (`runtime_run_*`, `react_*`, `tool_call_*`) and writes to `logger` when available; when `stats` (`stats_ingest`) is healthy, also posts batched overview metrics to `stats` `POST /events` (messages on successful session append, `tool_call` per tool start, `tokens` on `runtime_run_completed` when usage is present)
  - note: each `/run` does a structured **plan_gate** call to `llm-openai` (unless `RUNTIME_PLAN_GATE=legacy_keyword`) to set `inject_plan_only` + `restrict_mutating_tools`; the parser tolerates thinking-model output (strips `<think>` blocks, extracts the JSON object from surrounding prose; 512 `max_tokens`, 30s budget). When restriction is on, only **high-risk** docker actions are hard-blocked until plan confirmation (`isPlanConfirmationMessage`): `remove`, `delete_file`, `pull_image`, and `create` with a non-framework image. Routine mutations (framework-image create, exec, writes) stay fluid
 - note: the system prompt is intentionally short (small-model friendly); container-selection process knowledge lives in the tool descriptions — reuse an existing container (matched by its `purpose` from `action=list`) before creating from a framework image, and only pull external images after `estimate_image_pull` + explicit user approval; agents record installed environments in each container's `/workspace/.whalebot/NOTES.md`. Long installs/builds use `action=exec` `async=true` + `action=exec_status` polling
  - note: successful `export_artifact` tool results can be returned as chat attachments (`filename`, `content_base64`)
  - note: at the start of each `/run`, calls `POST /api/v1/tools/user-dockers/touch-creator-session` so temporary userdockers created under that `session_id` have their idle timer reset; refuses run if `get_context` reports expired
  - note: after tool-inventory short path, main chat path appends the user message to `session` before ReAct begins, then appends the assistant message when the run completes (so WebUI shows the user turn while the agent is still working)
  - note: when a healthy `type=skills` with `skills_search` is registered and `RUNTIME_SKILLS_INJECT` is not `0`, each main `/run` calls `GET {skills_endpoint}/skills/search` (top `RUNTIME_SKILLS_TOP_K` hits, FTS5/BM25) and appends an extra **system** block with excerpts for retrieval-first context (failure is non-fatal); all leading system blocks (base prompt, plan gate injection, skills) are merged into a **single** system message before invoke, because strict GGUF chat templates reject system messages beyond index 0
 - note: hosts the **model benchmark harness** (`runtime/cmd/server/benchmark.go`, capability `benchmark`): `POST /benchmark/run {include_e2e}` (async, single-flight, always tests the **active** llm-openai profile — one local model loaded at a time), `GET /benchmark/runs`, `DELETE /benchmark/runs/{id}`. Simulated tier scores plan_gate JSON adherence, docker tool-call format, and scripted ReAct discipline with canned tool results (weights 25/35/40 → total 0-100; includes hard cases: mixed/pushy destructive plan_gate intents, answer-from-context without a redundant tool call, remove-409 stop-then-remove recovery, compile-error fix-and-rebuild, async build polled to completion; destructive-op tool cases grant one scripted-approval retry — cautiously asking first is not penalized; runs are stamped `case_set` — currently `v2.1` — so different case sets are not compared blindly); optional E2E tier drives runtime's own `/run` (session `benchmark_<runid>`) to build a Go binary in `userdocker-golang`, transfer it into `userdocker-base` with `docker_files action=copy_file`, run it, then verifies 5 checkpoints directly via the orchestrator userdocker API (container ownership resolved by nonce + session suffix, so stale `whalebench-*` leftovers cannot cause false negatives) and force-cleans **all** `whalebench-*` containers + session (each chat turn retries once so a transient local-model timeout does not zero the run); E2E results carry `turn_replies` (truncated per-turn assistant replies) and `tool_events` (logger tool_call_start/error events for the benchmark session) so failures are self-explanatory. History persists at `/data/benchmark-runs.json` on volume `runtime_data` (cap 50 runs) so users can swap local models over time and compare; batch automation runbook for agents (llama.cpp swap loop + profile append via PUT config): `docs/benchmark-automation.md`
- `skills`
  - purpose: filesystem skill packages + SQLite FTS search index (`bm25` ranking)
  - entry: `skills/cmd/server/main.go`
  - host exposed: no
  - note: registers `type=skills`, name `skills`, capabilities `skills_list`, `skills_write`, `skills_search`; packages under **`SKILLS_ROOT/packages/{slug}/`** (default volume mount `/data`): required **`SKILL.md`** + **`skill.yaml`** metadata; optional `references/*.md` etc.; search index at `SKILLS_INDEX_PATH` (default `/data/.index/index.db`); legacy single-table SQLite at `SKILLS_LEGACY_DB_PATH` is imported once into packages when `packages/` is empty
  - note: on first start (**empty `packages/`**), seeds **`whalemesh-best-practices/`** from embedded defaults; existing package dirs are not modified
  - note: runtime injects **`SKILL.md`** (truncated) plus up to 3 matched reference excerpts per hit from `GET …/skills/search`
- `adapter-telegram`
  - purpose: Telegram user I/O adapter (`type=adapter` at orchestrator registration)
  - entry: `adapter-telegram/cmd/server/main.go`
  - host exposed: no
  - note: bot token and optional `allowed_user_ids` whitelist live in **`ADAPTER_CONFIG_PATH`** JSON (default `/data/adapter-config.json` on volume `adapter_telegram_data`), edited through WebUI Adapters page (or `PUT /api/v1/adapter-components/{name}/config` via orchestrator). Empty whitelist = no user filter.
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
- `adapter-webui`
  - purpose: ChatGPT-like web chat interface (`type=adapter` at orchestrator registration)
  - entry: `adapter-webui/cmd/server/main.go` (Go backend); `adapter-webui/web/src/main.js` (Svelte SPA)
  - host exposed: yes (`${ADAPTER_WEBUI_PORT:-18083}:${ADAPTER_WEBUI_PORT:-18083}`)
  - note: Go backend serves static SPA files + API + dynamic `env.js` directly; designed for external reverse proxy (nginx/Traefik/etc.)
  - note: Go backend listens on `:18083` inside container
  - note: JWT auth with HttpOnly cookie `adapter_webui_token`; default credentials `admin` / `whalebot` (same as webui); data in `/data/` volume (`credentials.json` + `jwt-secret.bin`)
  - note: registers with orchestrator as `type=adapter`, name `adapter-webui`, capabilities `webui_chat`
  - note: chat proxy: `POST /api/adapter-webui/chat` -> orchestrator `POST /api/v1/chat` with `channel=webui`; session ID format `webui_<key>`
  - note: session list filtered to `webui_*` prefix only
  - note: progress polling via `GET /api/adapter-webui/logger/events` -> orchestrator logger events; frontend polls every 2s during active chat
  - note: i18n (en/zh/ja) with `localStorage` key `adapter_webui_lang`; same whalebot dark theme as webui
 - note: config endpoint `GET|PUT /api/v1/adapter/config` exposed for orchestrator WebUI Adapters page proxy; returns/updates `username` + `has_password` for admin credential management (password is write-only)
  - note: `ADAPTER_WEBUI_CHAT_TIMEOUT_SEC` controls the HTTP client timeout for orchestrator chat calls (default 240s)
- `user-docker-manager`
 - purpose: system-level `userdocker` manager (dual-scope lifecycle + workspace operations); one instance per **node** machine
 - entry: `user-docker-manager/cmd/server/main.go`
 - host exposed: no
 - note: connects to orchestrator via outbound tunnel (`NODE_NAME`/`NODE_TOKEN`, §1.6); appears as component `user-docker-manager@<node>`; the local HTTP listener remains for the compose healthcheck only
 - note: enforces `userdocker.v1` interface contract and only manages containers labeled as manager-owned `userdocker`
  - note: raw language images (for example official `golang:*`) are rejected unless they expose `/api/v1/userdocker/interface`
  - note: pulling non-framework images requires explicit user approval flag (`external_image_approved_by_user=true`)
  - note: supports `session_scoped` and `global_service` container scopes with `switch-scope`
  - note: session-scoped container names append a sanitized `session_id` suffix to reduce naming conflicts across runs
  - note: `session_scoped` containers store `whalebot.userdocker.creator_session_id` (same as create-time `session_id`); **any** request that supplies `session_id` may operate them (no per-container session ownership check); temporary removal TTL from `USERDOCKER_TEMP_TTL_SEC` (or `USERDOCKER_IDLE_HOURS*3600`); `POST /api/v1/user-dockers/touch-creator-session` touches all temp dockers for a creator `session_id`
  - note: exposes `start/stop/touch/exec/files/artifacts/export` APIs and idle sweeper for `session_scoped` containers; `global_service` is not subject to this sweeper
 - note: `create` accepts a `purpose` string stored as label `whalebot.userdocker.purpose` and echoed in `GET /api/v1/user-dockers` (`purpose` field) so agents can decide whether to reuse a container
 - note: `POST /api/v1/user-dockers/pull` starts an **async** image pull (background, 30m cap) returning `job_id`; poll `GET …/pull/status`. `GET …/images/estimate?ref=` returns an upper-bound compressed download size from the registry manifest (docker.io anonymous only; other registries return a note). External refs require `external_image_approved_by_user=true`
 - note: `GET /api/v1/user-dockers/{name}/logs?tail=N` returns demuxed container stdout/stderr; `POST …/exec` accepts `async=true` (returns `job_id`, poll `GET …/exec/status`) for long installs/builds
 - note: `POST /api/v1/user-dockers/copy` (`{from_name, from_path, to_name, to_path, session_id}`) copies a file between two containers on the node, binary-safe (base64 fetch from source file API, PUT to target; 64MB cap) — bytes never enter LLM context
 - note: capabilities add `userdocker_images_estimate`, `userdocker_pull`, `userdocker_logs`, `userdocker_copy`
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
- `memory`
  - purpose: persistent memory notes (KV) + encrypted secrets store (SQLite)
  - entry: `memory/cmd/server/main.go`
  - host exposed: no
  - note: registers `type=memory`, name `memory`, capabilities `notes_get`, `notes_put`, `secrets_get`, `secrets_put`, `secrets_list`, `secrets_delete`; persistence `MEMORY_DB_PATH` (default `/data/memory.db` on volume `memory_data`)
  - note: secrets are AES-256-GCM encrypted at rest; key from `MEMORY_SECRET_KEY` (hex 64 chars) or auto-generated to `/data/.secret-key`
  - note: `GET /secrets` returns masked values (`ghp_****xyz9`); `GET /secrets/{key}` returns full decrypted value (used internally by runtime); orchestrator proxy strips `value` field from the detail response — only runtime accesses memory directly for full values
  - note: runtime discovers `secrets_get` capability and exposes `list_secrets` tool to agent; agent references secrets via `{{secret:key_name}}` placeholders in exec env/command_sh and write_file content; runtime resolves placeholders transparently before forwarding to userdocker-manager, so raw secret values never enter the LLM context
  - note: runtime redacts known secret values from tool results, logger events, and final replies (defense in depth)
- `workspace`
  - purpose: workspace directory manager
  - entry: `workspace/cmd/server/main.go`
  - host exposed: no
- `webui`
  - purpose: Svelte dashboard via Caddy plus small loopback **auth** process (`webui-auth` from `webui/authsrv`)
  - entry: `webui/src/main.js` (UI); `webui/authsrv/main.go` (auth API on `127.0.0.1:8089`, proxied by Caddy as `/api/webui/*`)
  - host exposed: yes (`${WEBUI_PORT:-18000}:80`)
  - note: compose mounts **`webui_data:/data`**: first boot seeds default login **`admin` / `whalebot`** (bcrypt hash in `credentials.json` only); JWT signing key in `jwt-secret.bin`. Session cookie is **HttpOnly** (`webui_token`). SPA shows a sign-in gate until `GET /api/webui/auth/me` succeeds; sidebar account menu opens **account settings** (username + optional new password in one form) and **logout**. **Orchestrator remains directly reachable** at its host port for API calls; this auth gates the dashboard UI only.
  - note: UI stack is **Svelte 4 + Vite + Tailwind CSS v4 + DaisyUI v5**; custom dark theme `whalebot` is defined in `webui/src/styles/global.css` (same pattern as Tailwind `@plugin "daisyui/theme"`).
  - note: **i18n**: default copy is **English**; UI strings also ship **zh** and **ja** via `webui/src/lib/i18n.js` + `webui/src/lib/i18n/messages.js` (deep-merge fallbacks to English). Locale auto-detects from `navigator.language` on first visit; manual override persists in `localStorage` key `whalebot_lang` (`en` | `zh` | `ja`). Left **collapsible sidebar** (when signed in) includes primary routes, a language menu, and account menu; collapsed width shows **icons only** (including a compact brand placeholder icon); collapse state persists in `localStorage` key `whalebot_sidebar_collapsed` (`0`/`1`).
  - note: **brand**: sidebar (and sign-in card) **brand icon** links to `https://github.com/WhaleMesh/WhaleBot` in a **new browser tab** (`webui/src/lib/brandUrls.js`, `WbBrandIcon.svelte`, `App.svelte`).
  - note: sidebar **footer** (below language + account): **Powered by WhaleMesh** with **WhaleMesh** linking to `https://github.com/WhaleMesh` (new tab); copy is i18n-driven (`layout.poweredByBefore` / `whaleMesh` / `layout.poweredByAfter`).
  - note: router is hash-based so refresh keeps current page
  - note: sidebar lists primary routes with icons (`webui/src/lib/NavGlyph.svelte`); nested routes keep parent highlights (e.g. session detail highlights **Sessions**).
  - note: includes dedicated `Logger` page in addition to overview logs
  - note: `Logger` page supports persistent logger events (`/api/v1/logger/events/recent`) + orchestrator recent logs dual-source diagnosis
  - note: session detail auto-scroll follows new messages only when user is near bottom; header/meta stays sticky
  - note: session list/detail support hard-delete via orchestrator `DELETE /api/v1/sessions/{id}`; list shows idle-expiry column; session detail shows expiry countdown
  - note: Overview + Components read `user-docker-manager` registry `meta.userdocker_temp_ttl_sec` / `meta.userdocker_idle_check_sec` and combine with `GET /api/v1/tools/user-dockers` list (`last_active_at`, `scope`) for temporary-container idle-removal countdown; Components type badges use a string-hash palette
  - note: Overview top renders three stat cards (messages / tool calls / tokens) from `GET /api/v1/stats/overview` when the stats service is enabled; shows a stats-disabled banner on `503 stats_disabled`; values use k/M shorthand (>10k -> `Nk`, >1M -> `NM`, one decimal) and a small last-24h delta line (`last_24h` from stats service window)
  - note: session detail keeps thought traces and renders them collapsed by default
  - note: session detail includes runtime timeline panel sourced from logger events (`session_id`-scoped `runtime/react/tool` phases)
  - note: `Tools` / `Envs` are selector pages; detailed testers are nested pages
  - note: sidebar **Skills** opens `#/skills` (CRUD via orchestrator `/api/v1/skills*`), `#/skills/{slug}` edits a **directory package** (metadata + file tree: `SKILL.md`, `references/*`); **Import ZIP** uploads a package archive via `POST /api/v1/skills/import`
  - note: sidebar **Secrets** opens `#/secrets` (CRUD via orchestrator `/api/v1/secrets*`), `#/secrets/{id}` edits one entry; values are masked in the UI, full values only accessible by runtime internally
  - note: sidebar **LLM** opens `#/llm` (lists `type=llm` from `GET /api/v1/components`); `#/llm/{name}` edits persisted model profiles via orchestrator `GET|PUT /api/v1/llm-components/{name}/config`, `POST …/active`, `POST …/test` (proxied to that component’s `/api/v1/llm/*`)
 - note: sidebar **Benchmark** opens `#/benchmark`: runs the model benchmark against the current active model (`POST /api/v1/benchmark/run`, optional E2E toggle), polls `GET /api/v1/benchmark/runs` for live progress, and shows an accumulating run-history comparison table (best completed total highlighted, expandable per-case + E2E checkpoint detail, per-run JSON download, row delete, whole-table CSV export, clear-all-history button via `DELETE /api/v1/benchmark/runs`)
 - note: sidebar **Adapters** opens `#/adapter` (lists `type=adapter`); `#/adapter/{name}` edits adapter-specific config via orchestrator `GET|PUT /api/v1/adapter-components/{name}/config` (proxied to adapter `/api/v1/adapter/config`) — `adapter-telegram`: bot token + whitelist; `adapter-webui`: username/password
- `userdocker-base`
  - purpose: base image for spawned `userdocker` instances
  - entry: `userdocker-base/main.go`
  - compose behavior: `sleep infinity` placeholder container
  - note: exposes public descriptor `GET /api/v1/userdocker/interface` (contract `userdocker.v1`)
  - note: implements workspace APIs (`/exec`, `/exec/status`, `/files`, `/file`, `/files/mkdir`, `/files/move`, `/artifacts/export`); `/exec` supports `async=true` (job id + `/exec/status` polling) and runs `command_sh` via non-login `sh -c` (image PATH preserved); `/file` PUT accepts `content_base64` or plain `content`; paths are workspace-rooted and absolute `/workspace/...` inputs are taken as-is (no doubling)
- `userdocker-golang`
  - purpose: Go toolchain image for spawned `userdocker` compile/build tasks
  - build source: `userdocker-base/Dockerfile` with Go final base image
  - compose behavior: `sleep infinity` placeholder container

## 4) Env Variables (grouped, minimal)

- Telegram adapter:
  - `ADAPTER_CONFIG_PATH` (default `/data/adapter-config.json` in compose; no token -> register only, no long poll)
- Ports:
  - `ORCHESTRATOR_PORT`, `SESSION_PORT`, `LLM_OPENAI_PORT`, `USER_DOCKER_MANAGER_PORT`, `ADAPTER_TELEGRAM_PORT`, `ADAPTER_WEBUI_PORT`, `RUNTIME_PORT`, `SKILLS_PORT`, `SKILLS_ROOT`, `SKILLS_INDEX_PATH`, `SKILLS_LEGACY_DB_PATH`, `LOGGER_PORT`, `STATS_PORT`, `MEMORY_PORT`, `WORKSPACE_PORT`, `WEBUI_PORT`
- Memory secrets:
  - `MEMORY_SECRET_KEY` (optional; hex 64 chars; auto-generated to `/data/.secret-key` if empty)
- Runtime tuning:
  - `REACT_MAX_STEPS`
  - `RUNTIME_MAX_TOKENS` (default `4096`; per-step completion budget incl. thinking tokens)
  - `RUNTIME_SKILLS_INJECT` (default `1`; set `0` to disable skills search injection), `RUNTIME_SKILLS_TOP_K` (default `2`)
- IM/session sync:
  - `SESSION_URL` (for `adapter-telegram` optional artifact append to session)
- Orchestrator request timeout:
 - `ORCHESTRATOR_UPSTREAM_TIMEOUT_SEC`
- LLM invoke timeout:
 - `LLM_INVOKE_TIMEOUT_SEC` (llm-openai per-invoke upstream budget, default 60; raise for slow local models)
- Distributed userdocker nodes:
 - `NODE_TOKEN` (shared secret for `/api/v1/nodes/connect`; empty on orchestrator disables the tunnel endpoint)
 - `NODE_NAME` (unique per machine, lowercase `[a-z0-9_.-]`; default hostname)
 - `ORCHESTRATOR_PUBLIC_URL` (only in `docker-compose.node.yml`; hub URL a remote node dials)
- Telegram adapter chat timeout:
  - `ADAPTER_TELEGRAM_CHAT_TIMEOUT_SEC`
- WebUI adapter chat timeout:
  - `ADAPTER_WEBUI_CHAT_TIMEOUT_SEC`
- Telegram in-chat progress:
  - single placeholder message edited from logger polling every 2s during chat execution (no extra env required); other adapters should follow `docs/adapter-progress-pattern.md`
- Userdocker manager lifecycle:
  - `USERDOCKER_TEMP_TTL_SEC` (optional; temp `session_scoped` removal idle; default `USERDOCKER_IDLE_HOURS*3600`)
  - `USERDOCKER_IDLE_HOURS`, `USERDOCKER_IDLE_CHECK_SEC`, `USERDOCKER_ALLOWED_IMAGES`
- Heartbeat liveness:
 - `HEARTBEAT_TTL_SEC` (component healthy while last heartbeat within TTL; default 30, heartbeats every 10s)
- Session:
  - `SESSION_MAX_MESSAGES`, `SESSION_IDLE_SEC` (idle expiry window in seconds, default 86400)

## 5) Current State / Drift Notes

- `docker-compose.yml` contains 14 services including `runtime`, `skills`, `logger`, `stats`, `workspace`, `memory`, `adapter-webui`; `docker-compose.node.yml` is the standalone remote-node stack (`user-docker-manager` + userdocker images only).
- `README.md` contains broad alignment, but some sections can lag behind compose details; verify against compose first.
- Compose currently exposes `orchestrator`, `webui`, and `adapter-webui` ports to host.
- Named volumes in use: `session_data`, `runtime_data`, `skills_data`, `logger_data`, `stats_data`, `workspace_data`, `llm_openai_data`, `adapter_telegram_data`, `adapter_webui_data`, `webui_data`, `memory_data`.
- Current repository scan does not find a `worker/` directory; if present locally in another branch/untracked state, treat it as non-compose unless compose is updated.

## 6) Rules For Future Agents (must follow)

- Always read `AGENTS.md` first, then only open files needed for the task.
- Use the **graphify knowledge graph** before raw file exploration (see §6.5).
- Treat `docker-compose.yml` + `.env.example` as runtime truth.
- Do not infer service wiring from stale docs without compose confirmation.
- Keep changes minimal and consistent with current compose/network model.
- If you change architecture, service list, env vars, ports, run commands, or status assumptions, you MUST update this file in the same change, and update the relevant per-service `README.md` when that service’s documented behavior or stack changes.

## 6.5) Code Exploration: graphify (mandatory)

- This repo has a code knowledge graph at `graphify-out/` (`graph.json` + manifest). Before exploring the codebase with Read/grep/glob, query the graph first — it surfaces cross-file dependencies that grep cannot:
 - `graphify query "<question>"` — scoped subgraph for any code/architecture question
 - `graphify path "<A>" "<B>"` — dependency path between two symbols
 - `graphify explain "<concept>"` — a node and its neighbors in plain language
- After modifying code files, run `graphify update .` to keep the graph current (AST-only, no LLM/API cost).
- **Availability is a hard requirement**: check `command -v graphify` and that `graphify-out/graph.json` exists. If either is missing, STOP and ask the user to install it — `pipx install graphifyy` (CLI name `graphify`), then `graphify update .` to (re)build the graph. Do not silently fall back to grep-only exploration.
- Include this rule in every subagent prompt that involves code exploration.

## 7) Runtime Capability Injection

- Runtime discovers capabilities per chat run from `GET /api/v1/components` on orchestrator.
- Only components that pass **readiness** are considered: `status=healthy` (fresh heartbeat, or connected tunnel for userdocker nodes) and heartbeat `operational_state` empty or `normal`.
- Tool mapping:
  - `type=tool` + capabilities `userdocker_*` -> model-facing tools `docker_lifecycle` / `docker_exec` / `docker_files` / `export_artifact` (all normalize to internal `manage_user_docker` dispatch, endpoint `/api/v1/tools/user-dockers`)
- Skills retrieval (not a tool call): `type=skills` + `skills_search` -> runtime may `GET {endpoint}/skills/search` before the main ReAct messages and inject a system block (see `RUNTIME_SKILLS_*` in §4).
- Secrets retrieval (not a tool call): `type=memory` + `secrets_get` -> runtime exposes `list_secrets` tool (returns key+note only); agent uses `{{secret:key_name}}` placeholders in exec env/command_sh; runtime resolves transparently before forwarding to userdocker-manager.
- docker tool actions: `docker_lifecycle` covers lifecycle/images (`list/list_images/create/start/stop/restart/remove/touch/switch_scope/get_interface/estimate_image_pull/pull_image/pull_status`), `docker_exec` covers `exec/exec_status/logs`, `docker_files` covers workspace file CRUD plus `copy_file` (cross-container file copy on the same node, via orchestrator `POST …/copy` — the way to move build outputs between containers), `export_artifact` exports artifacts.
- node awareness: container names surfaced to the model are composite `"<node>/<name>"` (from list/create); `create`/`pull_image`/`estimate_image_pull` accept optional `node` (default: first node).
- container-selection ladder: `action=list` (reuse a container matched by its `purpose`) → `action=list_images` + `create` from a framework image → external image only after `estimate_image_pull` + user approval. `create` passes a `purpose`; agents keep `/workspace/.whalebot/NOTES.md` per container to record installed envs.
- for Go compile tasks, prefer `whalebot/userdocker-golang:latest` when listed in `action=list_images`.
- runtime no longer relies on `environment`-type execution capability; build/run flows use the docker tools.
- Degrade behavior:
  - If a capability is not discoverable, runtime should not rely on that tool.
  - Tool calls without healthy backing component must return explicit unavailable errors.
- Quick diagnostics:
  - check chat min stack: `curl -s http://localhost:18080/health` (`chat_ready`, `chat_error`)
  - check components: `curl -s http://localhost:18080/api/v1/components`
  - check persistent logger events: `curl -s http://localhost:18080/api/v1/logger/events/recent?limit=20`
  - check stats overview (when stats service running): `curl -s http://localhost:18080/api/v1/stats/overview`
  - check userdocker manager contract: `curl -s http://localhost:18080/api/v1/tools/user-dockers/interface-contract`
 - check userdocker allowed images: `curl -s http://localhost:18080/api/v1/tools/user-dockers/images`
 - check connected userdocker nodes: `curl -s http://localhost:18080/api/v1/tools/user-dockers/nodes`
 - check userdocker list (composite `"<node>/<name>"`): `curl -s http://localhost:18080/api/v1/tools/user-dockers`
  - check skills list (when skills service running): `curl -s http://localhost:18080/api/v1/skills`
  - check secrets list (when memory service running): `curl -s http://localhost:18080/api/v1/secrets`
 - check model benchmark history: `curl -s http://localhost:18080/api/v1/benchmark/runs`
  - ask runtime via chat to list tool names and confirm the `docker_*` tools are visible.

## 8) Mandatory Update Policy

`AGENTS.md` must be updated every time the project is updated.

Update triggers (any one requires update):
- add/remove/rename service or module
- change entrypoint path, API surface, dependencies, or call chain
- change compose wiring (ports, volumes, network, health checks, env injection)
- add/remove/rename env vars in `.env.example` or service env defaults
- change local runbook, bootstrap steps, or operational constraints
- change project status, known drifts, or active roadmap assumptions
- when a **component’s** behavior, stack, or operational contract changes, also update that component’s **`README.md`** under its directory (same spirit as updating this file), e.g. `webui/README.md`, `orchestrator/README.md`, etc.

## 9) Update Checklist (after each project change)

- service map still matches `docker-compose.yml`
- env groups still match `.env.example`
- runbook still works (`cp .env.example .env` + `docker compose up --build`)
- drift notes still accurate (remove resolved drift, add new drift)
- root `README.md` and any touched service `README.md` under subdirectories stay aligned with reality
- this file stays concise (high-signal, low-token)
