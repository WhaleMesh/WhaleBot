# worker

## ServiceCard
```yaml
service: worker
role: react_loop_executor
compose_service: worker
image: whalesbot/worker:latest
build_context: ./worker
owner: tbd
runtime: go_http_service
default_port: 8085
health_endpoint: GET /health
component_registration:
  enabled: true
  name: worker
  type: worker
  capabilities:
    - react_chat
    - run
  meta: {}
last_verified_from:
  - docker-compose.yml
  - worker/cmd/server/main.go
```

## Purpose
- Runs the ReAct loop for chat requests.
- Calls `chatmodel` with tool definitions and executes returned tool calls.
- Persists final user+assistant pair into `session`.

## External API
### Endpoint: GET /health
```yaml
method: GET
path: /health
request: none
response:
  status: ok
  service: worker
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
  - `POST /append_messages`
- `CHATMODEL_URL`:
  - `POST /invoke` (with tools + params)
- `ORCHESTRATOR_URL`:
  - `POST /api/v1/tools/docker-create` for `docker_create_userdocker`
  - `POST /api/v1/components/register` for self-registration

## Environment Variables
### WORKER_PORT
```yaml
name: WORKER_PORT
default: "8085"
required: false
effect: bind_port_for_http_server
```

### REACT_MAX_STEPS
```yaml
name: REACT_MAX_STEPS
default: "8"
required: false
effect: max_iterations_before_react_loop_fails
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
default: worker
required: false
effect: advertised_endpoint_host_for_registration
```

## Runtime Contract
- network: `mvp_net`.
- depends_on: `orchestrator`, `session`, `chatmodel`, `tool-docker-creator`.
- healthcheck: `wget http://localhost:${WORKER_PORT}/health`.
- volumes: none.
- security_notes: executes tool side effects indirectly through orchestrator tool APIs.

## AI Lookup Hints
```yaml
aliases:
  - react_engine
  - agent_worker
  - tool_executor
query_to_endpoint:
  run_agent_chat: POST /run
  worker_health: GET /health
internal_tool_name_map:
  docker_create_userdocker: POST /api/v1/tools/docker-create
```

## Change Safety
- Keep `POST /run` schema aligned with orchestrator `/api/v1/chat` payload.
- Do not remove session writeback (`append_messages`) or chat history continuity breaks.
- Tool name `docker_create_userdocker` is contractually coupled to prompt and dispatcher.
