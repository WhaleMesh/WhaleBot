# session

## ServiceCard
```yaml
service: session
role: conversation_context_store
compose_service: session
image: whalebot/session:latest
build_context: ./session
owner: tbd
runtime: go_http_service
default_port: 8090
health_endpoint: GET /health
component_registration:
  enabled: true
  name: session
  type: session
  capabilities:
    - get_context
    - append_messages
    - clear_context
  meta: {}
last_verified_from:
  - docker-compose.yml
  - session/cmd/server/main.go
  - session/internal/store/store.go
```

## Purpose
- Stores per-session message history in memory.
- Provides minimal CRUD for chat context used by orchestrator and worker.
- Limits stored message count per session (`SESSION_MAX_MESSAGES`).

## External API
### Endpoint: GET /health
```yaml
method: GET
path: /health
request: none
response:
  status: ok
  service: session
error_behavior: standard_http_status
```

### Endpoint: POST /get_context
```yaml
method: POST
path: /get_context
request:
  content_type: application/json
  body:
    session_id: string
response:
  success: true
  session_id: string
  messages:
    - role: string
      content: string
error_behavior:
  decode_error: http_400
```

### Endpoint: POST /append_messages
```yaml
method: POST
path: /append_messages
request:
  content_type: application/json
  body:
    session_id: string (required)
    messages:
      - role: string
        content: string
response:
  success: true
error_behavior:
  decode_error: http_400
  validation_error: http_400
```

### Endpoint: POST /clear_context
```yaml
method: POST
path: /clear_context
request:
  content_type: application/json
  body:
    session_id: string
response:
  success: true
error_behavior:
  decode_error: http_400
```

### Endpoint: GET /sessions
```yaml
method: GET
path: /sessions
request: none
response:
  success: true
  sessions:
    - id: string
      updated_at: timestamp
      last_snippet: string
      length: int
error_behavior: standard_http_status
```

### Endpoint: GET /sessions/{id}
```yaml
method: GET
path: /sessions/{id}
request:
  path_params:
    id: string
response:
  success: true
  session:
    id: string
    messages:
      - role: string
        content: string
error_behavior:
  not_found_behavior: returns_empty_session_with_http_200
```

## Internal Calls
- Calls orchestrator register endpoint via `registerclient`.
- No runtime dependencies for business logic; storage is in-process memory.

## Environment Variables
### SESSION_PORT
```yaml
name: SESSION_PORT
default: "8090"
required: false
effect: bind_port_for_http_server
```

### SESSION_MAX_MESSAGES
```yaml
name: SESSION_MAX_MESSAGES
default: "40"
required: false
effect: max_messages_kept_per_session_after_append
```

### ORCHESTRATOR_URL
```yaml
name: ORCHESTRATOR_URL
default: http://orchestrator:8080
required: false
effect: target_for_component_registration
```

### SERVICE_HOST
```yaml
name: SERVICE_HOST
default: session
required: false
effect: advertised_endpoint_host_for_registration
```

## Runtime Contract
- network: `whalebot_net`.
- depends_on: `orchestrator`.
- healthcheck: `wget http://localhost:${SESSION_PORT}/health`.
- volumes: none.
- security_notes: in-memory only; data is ephemeral on container restart.

## AI Lookup Hints
```yaml
aliases:
  - memory_store
  - chat_history
  - context_service
query_to_endpoint:
  read_context: POST /get_context
  append_context: POST /append_messages
  clear_context: POST /clear_context
  list_sessions: GET /sessions
```

## Change Safety
- Preserve `role/content` message shape; upstream services assume this exact structure.
- Keep `GET /sessions/{id}` non-error empty-session behavior for UI compatibility.
- Increasing default retention may impact memory footprint.
