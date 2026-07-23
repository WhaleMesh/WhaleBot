# orchestrator

## ServiceCard
```yaml
service: orchestrator
role: component_registry_and_api_gateway
compose_service: orchestrator
image: whalebot/orchestrator:latest
build_context: ./orchestrator
owner: tbd
runtime: go_http_service
default_port: 18080
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
- Owns the component registry. Liveness is **heartbeat-based**: components re-register every 10s; a component is healthy while its last heartbeat is within `HEARTBEAT_TTL_SEC`. The orchestrator never probes components back (no pull health checks), so components only need outbound connectivity.
- Accepts **userdocker node tunnels** (`POST /api/v1/nodes/connect`): remote `user-docker-manager` instances dial in with `NODE_TOKEN`, get upgraded to a yamux session, and serve their API back over that connection. Connection presence doubles as their liveness. This lets machines without a public address host userdockers while the orchestrator is the only public server.
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
    capabilities: string[]
    meta: object<string,string>
    operational_state: string (optional; "normal" or a snake_case reason like "no_valid_configuration")
response:
  success: boolean
  component: registry_component
notes:
  - registration IS the heartbeat; components must re-register every ~10s
  - liveness = last heartbeat within HEARTBEAT_TTL_SEC (default 30)
  - non-normal operational_state keeps the component live but not ready (excluded from FirstReadyByType/Capability)
error_behavior:
  transport_errors: http_4xx_or_5xx
  logical_errors: http_400_with_error_field
```

### Endpoint: POST /api/v1/nodes/connect
```yaml
method: POST
path: /api/v1/nodes/connect
request:
  headers:
    X-Node-Token: shared secret (orchestrator env NODE_TOKEN)
  content_type: application/json
  body: { node: string, version: string, capabilities: string[], meta: object }
response: HTTP 101 upgrade, then yamux (orchestrator=client, node=server)
notes:
  - the connected manager appears in the registry as component "user-docker-manager@<node>" (type tool, tunnel=true)
  - disconnect removes the component; reconnect replaces a stale session
  - empty NODE_TOKEN disables the endpoint (503)
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

Implementation: resolves `FirstReadyByType("stats")` (liveness + optional `status_endpoint` / `operational_state`) and reverse-proxies the response body and status code.

### Benchmark API (reverse proxy)

Proxied to the healthy `type=runtime` component with capability `benchmark` (runtime hosts the model benchmark harness):

- `POST /api/v1/benchmark/run` → `POST {runtime_endpoint}/benchmark/run` (body `{include_e2e}`; 409 when a run is in progress, 503 when llm-openai has no active model)
- `GET /api/v1/benchmark/runs` → `GET {runtime_endpoint}/benchmark/runs`
- `DELETE /api/v1/benchmark/runs/{id}` → `DELETE {runtime_endpoint}/benchmark/runs/{id}`

If no runtime with the `benchmark` capability: **503** with `success: false`.

### Skills API (reverse proxy)

When a healthy `type=skills` component is registered, the orchestrator reverse-proxies JSON to `{skills_endpoint}` (same pattern as session/logger):

- `GET /api/v1/skills` → `GET {endpoint}/skills`
- `GET /api/v1/skills/search?q=...&limit=...` → `GET {endpoint}/skills/search?...`
- `POST /api/v1/skills` → `POST {endpoint}/skills`
- `GET /api/v1/skills/{id}` → `GET {endpoint}/skills/{id}`
- `PUT /api/v1/skills/{id}` → `PUT {endpoint}/skills/{id}`
- `DELETE /api/v1/skills/{id}` → `DELETE {endpoint}/skills/{id}`

If no healthy skills component: **503** with `success: false` and an English `error` message.

### Userdocker API (node-scoped)

Userdockers are managed per **node** (every connected `user-docker-manager` tunnel). Container identity is the composite name `"<node>/<container>"` everywhere on this API.

```yaml
nodes: GET /api/v1/tools/user-dockers/nodes            # connected nodes (name, version, capabilities, meta, connected_at)
list: GET /api/v1/tools/user-dockers?all=true|false    # fan-out to all nodes; containers get name="<node>/<name>" + node field; per-node failures in node_errors
create: POST /api/v1/tools/user-dockers                # body may carry "node"; empty -> first node; response name rewritten to "<node>/<name>"
images: GET /api/v1/tools/user-dockers/images          # fan-out; response { success, nodes: [{node, default_image, allowed_images, profiles}] }
estimate: GET /api/v1/tools/user-dockers/images/estimate?ref=<image>&node=<node>   # node optional (defaults to first)
pull: POST /api/v1/tools/user-dockers/pull             # body may carry "node"; returned job_id is composite "<node>/<job>"
pull_status: GET /api/v1/tools/user-dockers/pull/status?job_id=<node>/<job>
copy: POST /api/v1/tools/user-dockers/copy             # body {from_name,from_path,to_name,to_path,session_id}; names composite "<node>/<name>"; both must be on the same node (400 otherwise); forwarded to that node's manager
touch_creator_session: POST /api/v1/tools/user-dockers/touch-creator-session       # broadcast to all nodes, sums touched
interface_contract: GET /api/v1/tools/user-dockers/interface-contract              # any node (contract is uniform userdocker.v1)
container_scoped: <METHOD> /api/v1/tools/user-dockers/{node}/{cname}[/action...]   # generic pass-through to that node's manager
error_behavior:
  no_nodes_connected: http_503 "no userdocker nodes connected"
  unknown_or_offline_node: http_503 with guidance to use "<node>/<name>" from list
  node_transport_failure: http_502
```

Container-scoped actions (`start/stop/restart/remove/touch/switch-scope/exec/exec/status/logs/files/file/files/mkdir/files/move/artifacts/export/interface`) are proxied verbatim (method, body, query) to the node's manager, which validates them.

## Internal Calls
- `session`: `/get_context`, `/append_messages`, `/sessions`, `/sessions/{id}`.
- `llm` components (by registry `name`): reverse-proxy `GET|PUT /api/v1/llm-components/{name}/config`, `POST /api/v1/llm-components/{name}/active`, `POST /api/v1/llm-components/{name}/test` to `{endpoint}/api/v1/llm/*` (WebUI model admin).
- `adapter` components (by registry `name`): reverse-proxy `GET|PUT /api/v1/adapter-components/{name}/config` to `{endpoint}/api/v1/adapter/config` (WebUI adapter admin, e.g. Telegram token + whitelist).
- `runtime`: `/run` (chat proxy).
- User docker operations route by node over the yamux tunnels (see Userdocker API above); other proxies dial component endpoints directly.

## Environment Variables
### ORCHESTRATOR_PORT
```yaml
name: ORCHESTRATOR_PORT
default: "18080"
required: false
effect: bind_port_for_http_server
```

### HEARTBEAT_TTL_SEC
```yaml
name: HEARTBEAT_TTL_SEC
default: "30"
required: false
effect: component_is_healthy_while_last_heartbeat_is_within_this_many_seconds
```

### NODE_TOKEN
```yaml
name: NODE_TOKEN
default: "" (tunnel disabled)
required: false
effect: shared_secret_for_userdocker_node_tunnel_connections
```

### ORCHESTRATOR_UPSTREAM_TIMEOUT_SEC
```yaml
name: ORCHESTRATOR_UPSTREAM_TIMEOUT_SEC
default: "240"
required: false
effect: timeout_seconds_for_orchestrator_http_proxy_to_runtime_tool_and_other_upstreams
```

## Runtime Contract
- network: `whalebot_net`.
- depends_on: none.
- healthcheck: `wget http://localhost:18080/health`.
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
  estimate_image_pull: GET /api/v1/tools/user-dockers/images/estimate
  pull_image: POST /api/v1/tools/user-dockers/pull
  pull_status: GET /api/v1/tools/user-dockers/pull/status
  copy_file: POST /api/v1/tools/user-dockers/copy
  userdocker_logs: GET /api/v1/tools/user-dockers/{name}/logs
  exec_status: GET /api/v1/tools/user-dockers/{name}/exec/status
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
