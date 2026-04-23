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
  - orchestrator/internal/httpapi/types.go
  - orchestrator/internal/registry/registry.go
```

## Purpose
- Owns the component registry and health lifecycle.
- Exposes the stable northbound API used by `webui` and `im-telegram`.
- Routes chat requests to `worker` when available, otherwise falls back to direct `session + chatmodel`.

## External API
### Endpoint: GET /health
```yaml
method: GET
path: /health
request: none
response:
  status: ok
  service: orchestrator
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
    trace_id: string (optional; injected internally for worker path)
response:
  success: boolean
  session_id: string
  reply: string
  trace_id: string
  error: string
error_behavior:
  worker_path: upstream_worker_status_and_body_passthrough
  fallback_path: often_http_200_with_success_false
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

### Endpoint: POST /api/v1/environments/golang/run
```yaml
method: POST
path: /api/v1/environments/golang/run
request:
  content_type: application/json
  body:
    code: string
    timeout_sec: int
response: proxied_from_environment_service
error_behavior:
  no_environment_component: http_503
  upstream_failure: propagated_or_502
```

## Internal Calls
- `session`: `/get_context`, `/append_messages`, `/sessions`, `/sessions/{id}`.
- `chatmodel`: `/invoke`.
- `worker`: `/run` when a healthy worker exists.
- Generic proxy to `tool` and `environment` components by type lookup.
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
  list_userdockers: GET /api/v1/tools/user-dockers
  create_userdocker: POST /api/v1/tools/user-dockers
  remove_userdocker: DELETE /api/v1/tools/user-dockers/{name}
  restart_userdocker: POST /api/v1/tools/user-dockers/{name}/restart
  userdocker_contract: GET /api/v1/tools/user-dockers/interface-contract
  userdocker_interface: GET /api/v1/tools/user-dockers/{name}/interface
  run_go_code: POST /api/v1/environments/golang/run
```

## Change Safety
- Keep `/api/v1/chat` request/response schema backward compatible for `webui` and `im-telegram`.
- Do not remove fallback chat path unless `worker` becomes mandatory.
- Changes to component `type` strings break discovery (`session`, `chat_model`, `tool`, `environment`, `worker`).
