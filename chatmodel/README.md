# chatmodel

## ServiceCard
```yaml
service: chatmodel
role: openai_compatible_chat_completion_adapter
compose_service: chatmodel
image: whalesbot/chatmodel:latest
build_context: ./chatmodel
owner: tbd
runtime: go_http_service
default_port: 8081
health_endpoint: GET /health
component_registration:
  enabled: true
  name: chatmodel
  type: chat_model
  capabilities:
    - invoke
  meta:
    model: MODEL_NAME
last_verified_from:
  - docker-compose.yml
  - chatmodel/cmd/server/main.go
  - chatmodel/internal/openai/openai.go
```

## Purpose
- Accepts normalized chat requests and forwards them to an OpenAI-compatible `/v1/chat/completions`.
- Supports optional tool definitions and tool-call responses.
- Provides deterministic echo fallback when `MODEL_API_KEY` is empty.

## External API
### Endpoint: GET /health
```yaml
method: GET
path: /health
request: none
response:
  status: ok
  service: chatmodel
error_behavior: standard_http_status
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

## Internal Calls
- Outbound call to `${MODEL_BASE_URL}/v1/chat/completions`.
- Registers itself to orchestrator every 60 seconds.

## Environment Variables
### CHATMODEL_PORT
```yaml
name: CHATMODEL_PORT
default: "8081"
required: false
effect: bind_port_for_http_server
```

### MODEL_PROVIDER
```yaml
name: MODEL_PROVIDER
default: openai_compatible
required: false
effect: informational_in_compose_currently_not_branching_logic
```

### MODEL_BASE_URL
```yaml
name: MODEL_BASE_URL
default: https://api.openai.com
required: false
effect: upstream_chat_completions_base_url_localhost_is_rewritten_for_docker
```

### MODEL_API_KEY
```yaml
name: MODEL_API_KEY
default: ""
required: false
effect: when_empty_service_returns_echo_fallback_instead_of_real_llm_call
```

### MODEL_NAME
```yaml
name: MODEL_NAME
default: gpt-4o-mini
required: false
effect: model_field_sent_to_upstream_chat_completions
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
default: chatmodel
required: false
effect: advertised_endpoint_host_for_registration
```

## Runtime Contract
- network: `mvp_net`.
- depends_on: `orchestrator`.
- healthcheck: `wget http://localhost:${CHATMODEL_PORT}/health`.
- volumes: none.
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
