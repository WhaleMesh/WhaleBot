# llm-openai

## ServiceCard
```yaml
service: llm-openai
role: openai_compatible_chat_completion_adapter
compose_service: llm-openai
image: whalebot/llm-openai:latest
build_context: ./llm-openai
owner: tbd
runtime: go_http_service
default_port: 8081
health_endpoint: GET /health
status_endpoint: GET /status
component_registration:
  enabled: true
  name: llm-openai
  type: llm
  capabilities:
    - invoke
    - llm_config
  meta:
    model: string (e.g. default id at registration)
last_verified_from:
  - docker-compose.yml
  - llm-openai/cmd/server/main.go
  - llm-openai/internal/openai/openai.go
```

## Purpose
- Accepts normalized chat requests and forwards them to an OpenAI-compatible `/v1/chat/completions`.
- Supports optional tool definitions and tool-call responses.
- Provides deterministic echo fallback when the active client has no API key.

## External API
### Endpoint: GET /health
```yaml
method: GET
path: /health
request: none
response:
  http_status: 200
  body:
    status: ok
    service: llm-openai
notes: Liveness only; does not reflect model configuration.
```

### Endpoint: GET /status
```yaml
method: GET
path: /status
request: none
response:
  http_status: 200
  body:
    service: llm-openai
    operational_state: normal | no_valid_configuration
notes: English snake_case operational_state for orchestrator + WebUI i18n. Used for chat readiness when registered with orchestrator.
```

### Endpoint: POST /invoke
```yaml
method: POST
path: /invoke
request:
  content_type: application/json
  body:
    messages:
      - role: string
        content: string
        tool_calls: tool_call[]
        tool_call_id: string
    params: object
    tools: tool_definition[]
response:
  success: boolean
  message:
    role: string
    content: string
    tool_calls: tool_call[]
  error: string
error_behavior:
  decode_error: http_200_with_success_false
  upstream_error: http_200_with_success_false
```

### Admin: GET /api/v1/llm/config
Returns `{ success, config }` with masked API keys (`has_api_key`, `api_key_hint`).

### Admin: PUT /api/v1/llm/config
Body `{ models: [{id,name,base_url,model,api_key}], active_model_id }`. Empty `api_key` on an existing `id` keeps the previous secret.

### Admin: POST /api/v1/llm/active
Body `{ id }` — set active model profile (empty string clears).

### Admin: POST /api/v1/llm/test
Body optional `{ model_id }`; if omitted, tests the **active** profile. Response `{ success, error }` with full upstream diagnostic text on failure. Concurrent requests return **409** with `{ success: false, error: "test already in progress" }` (only one test runs at a time).

## Internal Calls
- Outbound call to the configured upstream root + `/v1/chat/completions` (OpenAI-compatible).
- Registers itself to orchestrator every 60 seconds.

## Environment Variables
### LLM_OPENAI_PORT
```yaml
name: LLM_OPENAI_PORT
default: "8081"
required: false
effect: bind_port_for_http_server
```

### LLM_CONFIG_PATH
```yaml
name: LLM_CONFIG_PATH
default: /data/llm-config.json
required: false
effect: JSON file for model profiles and active_model_id (compose mounts llm_openai_data at /data)
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
default: llm-openai
required: false
effect: advertised_endpoint_host_for_registration
```

## Runtime Contract
- network: `whalebot_net`.
- depends_on: `orchestrator`.
- healthcheck: `wget http://localhost:${LLM_OPENAI_PORT}/health`.
- volumes: named volume `llm_openai_data` → `/data` (see compose).
- security_notes: handles model API key; avoid logging secrets.

## AI Lookup Hints
```yaml
aliases:
  - llm_adapter
  - chat_completion_proxy
  - model_gateway
query_to_endpoint:
  invoke_model: POST /invoke
  check_health: GET /health
```

## Change Safety
- Keep `/invoke` schema backward compatible (`messages`, `params`, `tools`) for worker and orchestrator callers.
- Preserve echo fallback behavior for local development without API keys.
- Ensure tool-call message fields remain aligned with OpenAI-compatible format.
