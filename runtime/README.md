# runtime

## ServiceCard
```yaml
service: runtime
role: agent_runtime
compose_service: runtime
image: whalesbot/runtime:latest
build_context: ./runtime
owner: tbd
runtime: go_http_service
default_port: 8085
health_endpoint: GET /health
component_registration:
  enabled: true
  name: runtime
  type: runtime
  capabilities:
    - react_chat
    - run
    - tool_manifest_consumer
  meta: {}
last_verified_from:
  - docker-compose.yml
  - runtime/cmd/server/main.go
```

## Purpose
- Runs the ReAct loop for chat requests.
- Dynamically discovers healthy tool components from orchestrator before each run.
- Calls `chatmodel` with dynamically built tool definitions and executes returned tool calls.
- Persists final user+assistant pair into `session`.
- Emits structured runtime+tool trace events (for example `runtime_run_start`, `runtime_context_loaded`, `react_step_start`, `react_model_response`, `tool_call_start`, `tool_call_end`, `tool_call_error`, `runtime_run_completed`) for diagnosis.
- For execution-oriented requests, first returns an execution plan and asks user confirmation before running tools.
- `export_artifact` tool outputs can be returned as chat attachments (`filename` + base64 payload) for IM delivery.

## External API
### Endpoint: GET /health
```yaml
method: GET
path: /health
request: none
response:
  status: ok
  service: runtime
error_behavior: standard_http_status
```

### Endpoint: POST /run
```yaml
method: POST
path: /run
request:
  content_type: application/json
  body:
    user_id: string
    channel: string
    chat_id: string
    message: string (required)
    trace_id: string
response:
  success: boolean
  session_id: string
  reply: string
  trace_id: string
  error: string
error_behavior:
  decode_or_validation_error: http_200_with_success_false
  react_or_upstream_error: http_200_with_success_false
```

## Internal Calls
- `SESSION_URL`:
  - `POST /get_context`
  - `POST /append_messages` (main `/run` path: user row appended before ReAct, assistant row after completion)
- `CHATMODEL_URL`:
  - `POST /invoke` (with tools + params)
- `ORCHESTRATOR_URL`:
  - `GET /api/v1/components` for runtime capability discovery
  - `GET /api/v1/tools/user-dockers/images` for `manage_user_docker(action=list_images)`
  - `POST /api/v1/tools/user-dockers` for `manage_user_docker(action=create)`
  - `GET /api/v1/tools/user-dockers` for `manage_user_docker(action=list)`
  - `POST /api/v1/tools/user-dockers/{name}/start` for `manage_user_docker(action=start)`
  - `POST /api/v1/tools/user-dockers/{name}/stop` for `manage_user_docker(action=stop)`
  - `POST /api/v1/tools/user-dockers/{name}/touch` for `manage_user_docker(action=touch)`
  - `POST /api/v1/tools/user-dockers/{name}/switch-scope` for `manage_user_docker(action=switch_scope)`
  - `DELETE /api/v1/tools/user-dockers/{name}` for `manage_user_docker(action=remove)`
  - `POST /api/v1/tools/user-dockers/{name}/restart` for `manage_user_docker(action=restart)`
  - `GET /api/v1/tools/user-dockers/{name}/interface` for `manage_user_docker(action=get_interface)`
  - `POST /api/v1/tools/user-dockers/{name}/exec` for `manage_user_docker(action=exec)`
  - `/api/v1/tools/user-dockers/{name}/file(s)*` for file CRUD/mkdir/move
  - `GET /api/v1/tools/user-dockers/{name}/artifacts/export` for `manage_user_docker(action=export_artifact)`
  - `POST /api/v1/components/register` for self-registration
- `logger` (when discovered via capability `events_write`):
  - `POST /events` for structured runtime/tool trace events
- `stats` (optional; when discovered via capability `stats_ingest`):
  - `POST /events` with batched `message` / `tool_call` / `tokens` rows for the Overview metrics service (fire-and-forget; failures are logged only)

## Environment Variables
### RUNTIME_PORT
```yaml
name: RUNTIME_PORT
default: "8085"
required: false
effect: bind_port_for_http_server
```

### REACT_MAX_STEPS
```yaml
name: REACT_MAX_STEPS
default: "16"
required: false
effect: max_iterations_before_runtime_forces_text_finalization
```

### ORCHESTRATOR_URL
```yaml
name: ORCHESTRATOR_URL
default: http://orchestrator:8080
required: false
effect: tool_execution_target_and_component_registration_target
```

### SESSION_URL
```yaml
name: SESSION_URL
default: http://session:8090
required: false
effect: source_of_chat_history_and_target_for_context_persistence
```

### CHATMODEL_URL
```yaml
name: CHATMODEL_URL
default: http://chatmodel:8081
required: false
effect: model_inference_and_tool_call_generation_target
```

### SERVICE_HOST
```yaml
name: SERVICE_HOST
default: runtime
required: false
effect: advertised_endpoint_host_for_registration
```

## Runtime Contract
- network: `mvp_net`.
- depends_on: `orchestrator`, `session`, `chatmodel`, `user-docker-manager`.
- healthcheck: `wget http://localhost:${RUNTIME_PORT}/health`.
- volumes: none.
- security_notes: executes tool side effects indirectly through orchestrator tool APIs.

## AI Lookup Hints
```yaml
aliases:
  - react_engine
  - agent_runtime
  - tool_executor
query_to_endpoint:
  run_agent_chat: POST /run
  runtime_health: GET /health
internal_tool_name_map:
  manage_user_docker:
    list_images: GET /api/v1/tools/user-dockers/images
    list: GET /api/v1/tools/user-dockers
    create: POST /api/v1/tools/user-dockers
    start: POST /api/v1/tools/user-dockers/{name}/start
    stop: POST /api/v1/tools/user-dockers/{name}/stop
    touch: POST /api/v1/tools/user-dockers/{name}/touch
    switch_scope: POST /api/v1/tools/user-dockers/{name}/switch-scope
    remove: DELETE /api/v1/tools/user-dockers/{name}
    restart: POST /api/v1/tools/user-dockers/{name}/restart
    get_interface: GET /api/v1/tools/user-dockers/{name}/interface
    exec: POST /api/v1/tools/user-dockers/{name}/exec
    files: /api/v1/tools/user-dockers/{name}/file(s)*
    export_artifact: GET /api/v1/tools/user-dockers/{name}/artifacts/export
```

## Change Safety
- Keep `POST /run` schema aligned with orchestrator `/api/v1/chat` payload.
- Do not remove session writeback (`append_messages`) or chat history continuity breaks.
- Tool names are runtime-discovered contracts; keep dispatcher and tool schema in sync.
- Tool event fields (`trace_id`, `session_id`, `module`, `phase`, `tool_name`, `tool_call_id`, `step`, `duration_ms`, `args`, `result`) are consumed by Logger diagnostics; keep them stable.
- For `manage_user_docker(action=create)`, prefer framework images by default; external image pull must follow explicit user approval.
