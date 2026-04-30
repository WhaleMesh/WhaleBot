# orchestrator

## ServiceCard
```yaml
service: orchestrator
role: component_registry_and_api_gateway
compose_service: orchestrator
image: whalesbot/orchestrator:latest
build_context: ./orchestrator
owner: tbd
runtime: go_http_service
default_port: 8080
health_endpoint: GET /health
component_registration:
  enabled: false
  name: null
  type: null
  capabilities: []
  meta: {}
last_verified_from:
  - docker-compose.yml
  - orchestrator/cmd/server/main.go
  - orchestrator/internal/httpapi/api.go
  - orchestrator/internal/httpapi/chat_min_stack.go
  - orchestrator/internal/httpapi/types.go
  - orchestrator/internal/registry/registry.go
```

## Purpose
- Owns the component registry and health lifecycle.
- Exposes the stable northbound API used by `webui` and `adapter-telegram`.
- `POST /api/v1/chat` requires healthy `runtime`, `session`, and `llm` in the registry; if not, returns `success=false` with an English `error`. Otherwise proxies the request to `runtime` `POST /run` (no orchestrator-local session+llm-openai path).

## External API
### Endpoint: GET /health
```yaml
method: GET
path: /health
request: none
response:
  status: ok
  service: orchestrator
  chat_ready: boolean
  chat_error: string
notes:
  - HTTP status is always 200 when the orchestrator process is up (including when chat_ready is false), so container healthchecks that hit /health still pass.
  - chat_ready is true only when registry has healthy components for types runtime, session, and llm (same gate as POST /api/v1/chat).
  - chat_error is empty when chat_ready is true; when false, an English explanation (same text as chat rejection error).
error_behavior: standard_http_status
```

### Endpoint: POST /api/v1/components/register
```yaml
method: POST
path: /api/v1/components/register
request:
  content_type: application/json
  body:
    name: string (required)
    type: string (required)
    version: string
    endpoint: string (required)
    health_endpoint: string (required)
    capabilities: string[]
    meta: object<string,string>
response:
  success: boolean
  component: registry_component
error_behavior:
  transport_errors: http_4xx_or_5xx
  logical_errors: http_400_with_error_field
```

### Endpoint: GET /api/v1/components
```yaml
method: GET
path: /api/v1/components
request: none
response:
  success: true
  components: registry_component[]
error_behavior: standard_http_status
```

### Endpoint: POST /api/v1/chat
```yaml
method: POST
path: /api/v1/chat
request:
  content_type: application/json
  body:
    user_id: string
    channel: string
    chat_id: string
    message: string (required)
    trace_id: string (optional)
response:
  success: boolean
  session_id: string
  reply: string
  trace_id: string
  error: string
error_behavior:
  min_stack_not_ready: http_200_with_success_false_and_error_message
  runtime_proxy: upstream_runtime_status_and_body_passthrough
```

### Endpoint: GET /api/v1/logs/recent
```yaml
method: GET
path: /api/v1/logs/recent
request: none
response:
  success: true
  logs: log_entry[]
error_behavior: standard_http_status
```

### Endpoint: GET /api/v1/logger/events/recent
```yaml
method: GET
path: /api/v1/logger/events/recent?limit=200
request: none
response: proxied_from_logger_events_recent
error_behavior:
  no_logger_component: http_503
  upstream_failure: propagated_or_502
```

### Endpoint: GET /api/v1/sessions
```yaml
method: GET
path: /api/v1/sessions
request: none
response: proxied_from_session_service
error_behavior:
  no_session_component: http_503
  upstream_failure: propagated_or_502
```

### Endpoint: GET /api/v1/sessions/{id}
```yaml
method: GET
path: /api/v1/sessions/{id}
request:
  path_params:
    id: string
response: proxied_from_session_service
error_behavior:
  no_session_component: http_503
  upstream_failure: propagated_or_502
```

### Endpoint: GET /api/v1/stats/overview
```yaml
method: GET
path: /api/v1/stats/overview
request: none
response:
  proxied_from_stats_service: GET {stats_endpoint}/stats/overview
  body_includes:
    success: true
    window: { start: RFC3339Nano, end: RFC3339Nano, label: rolling_24h_hour_aligned }
    stats:
      messages: { total: int, last_24h: int }
      tool_calls: { total: int, last_24h: int }
      tokens:
        prompt: { total: int, last_24h: int }
        completion: { total: int, last_24h: int }
        total: { total: int, last_24h: int }
error_behavior:
  no_healthy_stats_component: http_503 { success: false, error: stats service not enabled, code: stats_disabled }
  upstream_failure: propagated_or_502
```

Implementation: resolves `FirstHealthyByType("stats")` and reverse-proxies the response body and status code.

### Skills API (reverse proxy)

When a healthy `type=skills` component is registered, the orchestrator reverse-proxies JSON to `{skills_endpoint}` (same pattern as session/logger):

- `GET /api/v1/skills` → `GET {endpoint}/skills`
- `GET /api/v1/skills/search?q=...&limit=...` → `GET {endpoint}/skills/search?...`
- `POST /api/v1/skills` → `POST {endpoint}/skills`
- `GET /api/v1/skills/{id}` → `GET {endpoint}/skills/{id}`
- `PUT /api/v1/skills/{id}` → `PUT {endpoint}/skills/{id}`
- `DELETE /api/v1/skills/{id}` → `DELETE {endpoint}/skills/{id}`

If no healthy skills component: **503** with `success: false` and an English `error` message.

### Endpoint: GET /api/v1/tools/user-dockers
```yaml
method: GET
path: /api/v1/tools/user-dockers?all=true|false
request: none
response: proxied_from_user_docker_manager
error_behavior:
  no_userdocker_manager_component: http_503
  upstream_failure: propagated_or_502
```

### Endpoint: GET /api/v1/tools/user-dockers/images
```yaml
method: GET
path: /api/v1/tools/user-dockers/images
request: none
response: proxied_from_user_docker_manager
error_behavior:
  no_userdocker_manager_component: http_503
  upstream_failure: propagated_or_502
```

### Endpoint: POST /api/v1/tools/user-dockers
```yaml
method: POST
path: /api/v1/tools/user-dockers
request:
  content_type: application/json
  body:
    name: string
    image: string
    cmd: string[]
    env: object<string,string>
    labels: object<string,string>
    network: string
    auto_register: boolean
    port: int
response: proxied_from_user_docker_manager
error_behavior:
  no_userdocker_manager_component: http_503
  upstream_failure: propagated_or_502
```

### Endpoint: DELETE /api/v1/tools/user-dockers/{name}
```yaml
method: DELETE
path: /api/v1/tools/user-dockers/{name}?force=true|false
request: none
response: proxied_from_user_docker_manager
error_behavior:
  no_userdocker_manager_component: http_503
  upstream_failure: propagated_or_502
```

### Endpoint: POST /api/v1/tools/user-dockers/{name}/restart
```yaml
method: POST
path: /api/v1/tools/user-dockers/{name}/restart?timeout_sec=10
request: none
response: proxied_from_user_docker_manager
error_behavior:
  no_userdocker_manager_component: http_503
  upstream_failure: propagated_or_502
```

### Endpoint: GET /api/v1/tools/user-dockers/interface-contract
```yaml
method: GET
path: /api/v1/tools/user-dockers/interface-contract
request: none
response: proxied_from_user_docker_manager
error_behavior:
  no_userdocker_manager_component: http_503
  upstream_failure: propagated_or_502
```

### Endpoint: GET /api/v1/tools/user-dockers/{name}/interface
```yaml
method: GET
path: /api/v1/tools/user-dockers/{name}/interface?port=9000
request: none
response: proxied_from_user_docker_manager
error_behavior:
  no_userdocker_manager_component: http_503
  upstream_failure: propagated_or_502
```

## Internal Calls
- `session`: `/get_context`, `/append_messages`, `/sessions`, `/sessions/{id}`.
- `llm` components (by registry `name`): reverse-proxy `GET|PUT /api/v1/llm-components/{name}/config`, `POST /api/v1/llm-components/{name}/active`, `POST /api/v1/llm-components/{name}/test` to `{endpoint}/api/v1/llm/*` (WebUI model admin).
- `adapter` components (by registry `name`): reverse-proxy `GET|PUT /api/v1/adapter-components/{name}/config` to `{endpoint}/api/v1/adapter/config` (WebUI adapter admin, e.g. Telegram token + whitelist).
- `llm-openai` (typical): `/invoke` at the service root (not under `/api/v1/llm`).
- `worker`: `/run` when a healthy worker exists.
- Generic proxy to `tool` components by capability lookup.
- User docker operations route by capability lookup (`userdocker_*`) to the manager component.

## Environment Variables
### ORCHESTRATOR_PORT
```yaml
name: ORCHESTRATOR_PORT
default: "8080"
required: false
effect: bind_port_for_http_server
```

### HEALTHCHECK_INTERVAL_SEC
```yaml
name: HEALTHCHECK_INTERVAL_SEC
default: "5"
required: false
effect: registry_component_health_poll_interval_seconds
```

### HEALTHCHECK_FAIL_THRESHOLD
```yaml
name: HEALTHCHECK_FAIL_THRESHOLD
default: "3"
required: false
effect: consecutive_failures_before_component_removed
```

### ORCHESTRATOR_UPSTREAM_TIMEOUT_SEC
```yaml
name: ORCHESTRATOR_UPSTREAM_TIMEOUT_SEC
default: "240"
required: false
effect: timeout_seconds_for_orchestrator_http_proxy_to_runtime_tool_and_other_upstreams
```

## Runtime Contract
- network: `mvp_net`.
- depends_on: none.
- healthcheck: `wget http://localhost:8080/health`.
- volumes: none.
- security_notes: registry is the trust boundary for service discovery.

## AI Lookup Hints
```yaml
aliases:
  - api_gateway
  - registry
  - chat_router
query_to_endpoint:
  register_component: POST /api/v1/components/register
  chat: POST /api/v1/chat
  list_components: GET /api/v1/components
  list_persistent_logger_events: GET /api/v1/logger/events/recent
  list_userdockers: GET /api/v1/tools/user-dockers
  list_userdocker_images: GET /api/v1/tools/user-dockers/images
  create_userdocker: POST /api/v1/tools/user-dockers
  remove_userdocker: DELETE /api/v1/tools/user-dockers/{name}
  restart_userdocker: POST /api/v1/tools/user-dockers/{name}/restart
  userdocker_contract: GET /api/v1/tools/user-dockers/interface-contract
  userdocker_interface: GET /api/v1/tools/user-dockers/{name}/interface
```

## Change Safety
- Keep `/api/v1/chat` request/response schema backward compatible for `webui` and `adapter-telegram`.
- Do not remove fallback chat path unless `worker` becomes mandatory.
- Changes to component `type` strings break discovery (`session`, `llm`, `tool`, `environment`, `worker`).
