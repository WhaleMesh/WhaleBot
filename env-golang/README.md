# env-golang

## ServiceCard
```yaml
service: env-golang
role: go_code_execution_environment
compose_service: env-golang
image: whalebot/env-golang:latest
build_context: ./env-golang
owner: tbd
runtime: go_http_service
default_port: 8083
health_endpoint: GET /health
component_registration:
  enabled: true
  name: env-golang
  type: environment
  capabilities:
    - run_go
  meta: {}
last_verified_from:
  - docker-compose.yml
  - env-golang/cmd/server/main.go
  - env-golang/internal/runner/runner.go
```

## Purpose
- Deprecated in current compose topology. Runtime now uses `manage_user_docker` + container `exec` for Go/project execution.
- Executes user-provided Go snippets through `go run`.
- Returns stdout/stderr, exit code, duration, and failure details.
- Serves as an environment component routed by orchestrator.

## External API
### Endpoint: GET /health
```yaml
method: GET
path: /health
request: none
response:
  status: ok
  service: env-golang
error_behavior: standard_http_status
```

### Endpoint: POST /run
```yaml
method: POST
path: /run
request:
  content_type: application/json
  body:
    code: string (required)
    timeout_sec: int (optional)
response:
  success: boolean
  stdout: string
  stderr: string
  exit_code: int
  duration_ms: int64
  error: string
error_behavior:
  decode_or_validation_error: http_200_with_success_false
  runtime_error: http_200_with_success_false
  timeout_behavior: timeout_clamped_to_30_seconds_max
```

## Internal Calls
- Calls orchestrator register endpoint via `registerclient`.
- Runs Go code in-process by delegating to internal runner logic.

## Environment Variables
### ENV_GOLANG_PORT
```yaml
name: ENV_GOLANG_PORT
default: "8083"
required: false
effect: bind_port_for_http_server
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
default: env-golang
required: false
effect: advertised_endpoint_host_for_registration
```

## Runtime Contract
- network: `whalebot_net`.
- depends_on: `orchestrator`.
- healthcheck: `wget http://localhost:${ENV_GOLANG_PORT}/health`.
- volumes: none.
- security_notes: executes untrusted code; keep strict timeout and isolated runtime assumptions.

## AI Lookup Hints
```yaml
aliases:
  - go_runner
  - code_execution_env
  - golang_sandbox
query_to_endpoint:
  run_go: POST /run
  env_health: GET /health
orchestrator_proxy_path:
  run_go: POST /api/v1/environments/golang/run
```

## Change Safety
- Keep response fields stable (`stdout`, `stderr`, `exit_code`, `duration_ms`) for UI and tool consumers.
- Maintain timeout guardrails (default 10s, max 30s).
- Avoid widening execution privileges without explicit security review.
