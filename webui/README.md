# webui

## ServiceCard
```yaml
service: webui
role: browser_dashboard_frontend
compose_service: webui
image: whalebot/webui:latest
build_context: ./webui
owner: tbd
runtime: caddy_static_spa_plus_loopback_go_auth
default_port: 80
health_endpoint: GET /
component_registration:
  enabled: false
  name: null
  type: null
  capabilities: []
  meta: {}
last_verified_from:
  - docker-compose.yml
  - webui/src/lib/api.js
  - webui/src/lib/auth.js
  - webui/src/lib/i18n.js
  - webui/src/styles/global.css
  - webui/vite.config.js
  - webui/Dockerfile
  - webui/Caddyfile
  - webui/authsrv/main.go
```

## Purpose
- Provides the human-facing dashboard for component status, logs, sessions, tool operations, LLM profiles, and **adapter** configuration (e.g. Telegram bot token + user ID whitelist via orchestrator `adapter-components` proxy).
- Calls orchestrator REST API from the browser (same origin as the UI for auth only; orchestrator base URL remains `ORCHESTRATOR_URL` in `env.js`).
- Is not a backend component in the orchestrator registry.
- Renders session content as standard Markdown (no IM-specific formatting conversion).

## Dashboard authentication
- **Process**: `webui-auth` (Go, `webui/authsrv`) listens on **`127.0.0.1:8089`** inside the container. **Caddy** reverse-proxies **`/api/webui/*`** to that address; browsers never talk to 8089 directly.
- **Persistence** (Docker volume **`webui_data` → `/data`**):
  - `credentials.json` — `username` and `password_hash` (bcrypt). Created on first start with defaults **`admin` / `whalebot`**.
  - `jwt-secret.bin` — random signing material for session JWTs (created on first start).
- **Cookie**: HttpOnly **`webui_token`**, `SameSite=Lax`, `Path=/` (no `Secure` so local HTTP works).
- **REST API** (all under `/api/webui/auth`, JSON bodies, `Set-Cookie` on login/credential update):
  - `GET /health` — liveness for container entrypoint.
  - `POST /login` — body `{ "username", "password" }`.
  - `POST /logout` — clears session cookie.
  - `GET /me` — returns `{ "username" }` when cookie valid; `401` when not.
  - `PUT /credentials` — body `{ "current_password", "new_username" (trimmed; may match current), "new_password?" }`. Omit `new_password` or send empty to keep the existing password. Request is rejected with **`no changes`** when the username is unchanged and no new password is supplied. Usernames: Unicode letters or numbers plus `_` `-` `.`, length 1–128. New password length **8–256** when set.
- **Scope**: The SPA hides routes until signed in. The **orchestrator host port is not gated** by this mechanism; callers can still use the API without the WebUI.

## Frontend stack
- **Svelte 4** + **Vite 5**.
- **Tailwind CSS v4** via `@tailwindcss/vite` and **DaisyUI v5**; theme tokens and plugin config live in [`src/styles/global.css`](src/styles/global.css) (custom theme name `whalebot`, `data-theme="whalebot"` on `<html>`).
- Build: `npm run build` produces static assets consumed by the container Caddy image.

## Local development (`npm run dev`)
- Vite dev server proxies **`/api/webui`** → **`http://127.0.0.1:8099`** (see [`vite.config.js`](vite.config.js)).
- In a second terminal, from `webui/authsrv`:
  ```bash
  mkdir -p /tmp/whalebot-webui-auth-data
  go run . -listen 127.0.0.1:8099 -data-dir /tmp/whalebot-webui-auth-data
  ```
- Then open the Vite URL (default `http://localhost:5173`). Sign in with `admin` / `whalebot` after the auth process is running.

## Internationalization (i18n)
- Copy defaults to **English**; **Chinese (zh)** and **Japanese (ja)** are provided as overlays merged onto English keys in [`src/lib/i18n/messages.js`](src/lib/i18n/messages.js).
- Runtime: [`src/lib/i18n.js`](src/lib/i18n.js) exposes `locale` store, `setLocale`, `translate`, reactive `$_` for templates, and `t()` for non-reactive script use.
- **Auto-detect**: on first visit (no saved preference), `navigator.language` maps `zh*` → `zh`, `ja*` → `ja`, else `en`.
- **Manual**: sidebar language menu (flyout next to the sidebar); choice persists under `localStorage` key **`whalebot_lang`** (`en` | `zh` | `ja`).
- `document.documentElement.lang` is updated (`en`, `zh-Hans`, `ja`) when the locale changes.

## External API
### Endpoint: GET /
```yaml
method: GET
path: /
request: none
response:
  content_type: text/html
  body: web_dashboard_app
error_behavior: standard_http_status_from_caddy
```

## Internal Calls
- **Same-origin auth** (`credentials: 'include'`) via [`src/lib/auth.js`](src/lib/auth.js):
  - `GET /api/webui/auth/me`, `POST /api/webui/auth/login`, `POST /api/webui/auth/logout`, `PUT /api/webui/auth/credentials`
- Browser-side fetch calls to **orchestrator** (`ORCHESTRATOR_URL`, [`src/lib/api.js`](src/lib/api.js)):
  - `GET /health` (Overview uses `chat_ready` / `chat_error` when `chat_ready` is false)
  - `GET /api/v1/components`
  - `GET /api/v1/logs/recent`
  - `GET /api/v1/logger/events/recent`
  - `GET /api/v1/sessions`
  - `GET /api/v1/sessions/{id}`
  - `POST /api/v1/chat`
  - `GET /api/v1/tools/user-dockers`
  - `POST /api/v1/tools/user-dockers`
  - `DELETE /api/v1/tools/user-dockers/{name}`
  - `POST /api/v1/tools/user-dockers/{name}/restart`
  - `GET /api/v1/tools/user-dockers/interface-contract`
  - `GET /api/v1/tools/user-dockers/{name}/interface`
  - `GET|POST /api/v1/skills`, `GET /api/v1/skills/search`, `GET|PUT|DELETE /api/v1/skills/{id}` (Skills page)

## UI Navigation Model
- Router uses hash-based URLs so browser refresh keeps the current page and detail context.
- `Tools` page is a selector list for tool test pages.
  - Current item: `User Docker Manager`.
- `Skills` page (`#/skills`, `#/skills/{id}`): CRUD for markdown skills; body defaults to preview with an edit toggle.
- `Logger` page provides dual-source diagnostics:
  - persistent logger events from `GET /api/v1/logger/events/recent`
  - recent orchestrator ring logs from `GET /api/v1/logs/recent`
- `Logger` page supports fine-grained filters (`module`, `tool_name`, `trace_id`, `level`, `phase`, time window) and tool-call flow grouping by `trace_id + tool_call_id`.
- `Sessions` detail page renders Markdown and displays extra diagnostics when available:
  - message timestamps
  - real token usage (`prompt_tokens` / `completion_tokens` / `total_tokens`) when available
  - real assistant reply latency
  - sticky top header/meta block for quick back navigation and status visibility
  - auto-scroll to newest message only when user is already near page bottom

## Environment Variables
### ORCHESTRATOR_URL
```yaml
name: ORCHESTRATOR_URL
default: http://localhost:8080
required: false
effect: browser_reachable_base_url_for_api_requests_injected_as_runtime_env
```

### WEBUI_PORT
```yaml
name: WEBUI_PORT
default: "3000"
required: false
effect: host_port_mapping_to_container_port_80_in_compose
```

## Runtime Contract
- network: `whalebot_net`.
- depends_on: `orchestrator`.
- healthcheck: `wget http://localhost/`.
- volumes: **`webui_data:/data`** (auth state; see above).
- security_notes: frontend trusts configured orchestrator URL; CORS and host reachability must match deployment. Dashboard login does not authenticate orchestrator traffic.

## Gateway Layering Notes (Outer Caddy/Nginx)

- Current inner server is Caddy inside the `webui` container, forwarding `/api/webui/*` to the co-located `webui-auth` listener.
- Using an outer gateway (Caddy, nginx, ingress) in front of `webui` is supported and does not conflict.
- Recommended responsibility split:
  - inner Caddy: static files + SPA fallback + runtime `env.js` serving + `/api/webui` reverse proxy to loopback auth.
  - outer gateway: TLS, domain routing, auth, rate limit, access logs, WAF-like policies.
- Keep `ORCHESTRATOR_URL` browser-reachable from end users (do not point to Docker-internal DNS such as `http://orchestrator:8080` in public deployments).
- Keep `/env.js` non-cached (`Cache-Control: no-store`) so runtime endpoint changes can take effect without rebuilding.
- If deploying under a path prefix (for example `/whalebot/`), align:
  - frontend base path build/runtime config
  - outer gateway path rewrite rules
  - API base URL exposed to browsers

## AI Lookup Hints
```yaml
aliases:
  - dashboard
  - frontend
  - admin_ui
query_to_endpoint:
  ui_health: GET /
backend_api_base: ORCHESTRATOR_URL
auth_api_base: /api/webui/auth
```

## Change Safety
- Keep API base env injection path stable, otherwise browser fetches fail.
- `ORCHESTRATOR_URL` must be host-reachable for browsers (not only Docker-internal DNS).
- UI assumes orchestrator response contracts from `/api/v1/*` endpoints.
- Auth paths under `/api/webui/auth` are reserved for the embedded auth service.
