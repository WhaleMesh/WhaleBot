# runtime

## ServiceCard
```yaml
service: runtime
role: agent_runtime
compose_service: runtime
image: whalebot/runtime:latest
build_context: ./runtime
owner: tbd
runtime: go_http_service
default_port: 18085
health_endpoint: GET /health
component_registration:
  enabled: true
  name: runtime
  type: runtime
  capabilities:
    - react_chat
    - run
    - tool_manifest_consumer
    - benchmark
  meta: {}
last_verified_from:
  - docker-compose.yml
  - runtime/cmd/server/main.go
```

## Purpose
- Runs the ReAct loop for chat requests.
- Dynamically discovers healthy tool components from orchestrator before each run.
- Calls `llm-openai` with dynamically built tool definitions and executes returned tool calls.
- The user-docker capability is exposed to the model as four focused tools (`docker_lifecycle`, `docker_exec`, `docker_files`, `export_artifact`) with narrow schemas so small local models select tools reliably; all four normalize to the single internal `manage_user_docker` dispatch path (logger events keep `tool_name=manage_user_docker`).
- Persists final user+assistant pair into `session`.
- Emits structured runtime+tool trace events (for example `runtime_run_start`, `runtime_context_loaded`, `react_step_start`, `react_model_response`, `tool_call_start`, `tool_call_end`, `tool_call_error`, `runtime_run_completed`) for diagnosis.
- For execution-oriented requests, first returns an execution plan and asks user confirmation before running tools.
- `export_artifact` tool outputs can be returned as chat attachments (`filename` + base64 payload) for IM delivery.
- Hosts the **model benchmark harness** (`benchmark.go`): scores the currently active llm-openai model against the framework's real demands — plan_gate JSON adherence, docker tool-call format, scripted ReAct discipline (canned tool results) — plus an optional E2E scenario that drives runtime's own `/run` against real userdockers (build a Go binary in `userdocker-golang`, transfer it into `userdocker-base` with `docker_files action=copy_file`, run it, verify output via orchestrator userdocker API, then clean up; each chat turn retries once on transient failure). E2E results include `turn_replies` (truncated assistant reply per turn) and `tool_events` (logger `tool_call_start`/`tool_call_error` events filtered to the benchmark session) for failure diagnosis. Run history persists to `/data/benchmark-runs.json` (volume `runtime_data`), capped at 50 runs.

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

### Endpoint: POST /benchmark/run
```yaml
method: POST
path: /benchmark/run
request:
  content_type: application/json
  body:
    include_e2e: boolean (optional; run the real-container E2E scenario too)
response:
  success: boolean
  run_id: string
error_behavior:
  run_already_in_progress: http_409_success_false
  no_active_llm_model: http_503_success_false
notes: async; poll GET /benchmark/runs. Always benchmarks the ACTIVE llm-openai profile (one local model loaded at a time); the run record is stamped with the active model name/id.
```

### Endpoint: GET /benchmark/runs
```yaml
method: GET
path: /benchmark/runs
response:
  success: boolean
  runs: array (newest first; status running|completed|failed, scores {plan_gate,tool_call,react,total 0-100}, metrics, per-case results, optional e2e {status,score,checkpoints,...})
```

### Endpoint: DELETE /benchmark/runs/{id}
```yaml
method: DELETE
path: /benchmark/runs/{id}
response: { success: boolean }
error_behavior:
  running_or_unknown_id: http_400_success_false
```

## Internal Calls
- `SESSION_URL`:
  - `POST /get_context`
  - `POST /append_messages` (main `/run` path: user row appended before ReAct, assistant row after completion)
- `LLM_OPENAI_URL`:
  - `POST /invoke` (with tools + params)
- `ORCHESTRATOR_URL`:
  - `GET /api/v1/components` for runtime capability discovery
  - `GET /api/v1/tools/user-dockers/images` for `manage_user_docker(action=list_images)`
  - `GET /api/v1/tools/user-dockers/images/estimate` for `manage_user_docker(action=estimate_image_pull)`
  - `POST /api/v1/tools/user-dockers/pull` for `manage_user_docker(action=pull_image)`
  - `GET /api/v1/tools/user-dockers/pull/status` for `manage_user_docker(action=pull_status)`
  - `POST /api/v1/tools/user-dockers/copy` for `manage_user_docker(action=copy_file)` (cross-container file copy, same node)
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
  - `GET /api/v1/tools/user-dockers/{name}/exec/status` for `manage_user_docker(action=exec_status)`
  - `GET /api/v1/tools/user-dockers/{name}/logs` for `manage_user_docker(action=logs)`
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
default: "18085"
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

### RUNTIME_MAX_TOKENS
```yaml
name: RUNTIME_MAX_TOKENS
default: "4096"
required: false
effect: per_step_completion_token_budget_including_reasoning_tokens
```

### ORCHESTRATOR_URL
```yaml
name: ORCHESTRATOR_URL
default: http://orchestrator:18080
required: false
effect: tool_execution_target_and_component_registration_target
```

### SESSION_URL
```yaml
name: SESSION_URL
default: http://session:18090
required: false
effect: source_of_chat_history_and_target_for_context_persistence
```

### LLM_OPENAI_URL
```yaml
name: LLM_OPENAI_URL
default: http://llm-openai:18081
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
- network: `whalebot_net`.
- depends_on: `orchestrator`, `session`, `llm-openai`, `user-docker-manager`.
- healthcheck: `wget http://localhost:${RUNTIME_PORT}/health`.
- volumes: `runtime_data:/data` (benchmark run history `benchmark-runs.json`).
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
    estimate_image_pull: GET /api/v1/tools/user-dockers/images/estimate
    pull_image: POST /api/v1/tools/user-dockers/pull
    pull_status: GET /api/v1/tools/user-dockers/pull/status
    copy_file: POST /api/v1/tools/user-dockers/copy
    list: GET /api/v1/tools/user-dockers
    create: POST /api/v1/tools/user-dockers
    start: POST /api/v1/tools/user-dockers/{name}/start
    stop: POST /api/v1/tools/user-dockers/{name}/stop
    touch: POST /api/v1/tools/user-dockers/{name}/touch
    switch_scope: POST /api/v1/tools/user-dockers/{name}/switch-scope
    remove: DELETE /api/v1/tools/user-dockers/{name}
    restart: POST /api/v1/tools/user-dockers/{name}/restart
    get_interface: GET /api/v1/tools/user-dockers/{name}/interface
    exec: POST /api/v1/tools/user-dockers/{name}/exec (async=true -> job_id)
    exec_status: GET /api/v1/tools/user-dockers/{name}/exec/status
    logs: GET /api/v1/tools/user-dockers/{name}/logs
    files: /api/v1/tools/user-dockers/{name}/file(s)*
    export_artifact: GET /api/v1/tools/user-dockers/{name}/artifacts/export
```

## Change Safety
- Keep `POST /run` schema aligned with orchestrator `/api/v1/chat` payload.
- Do not remove session writeback (`append_messages`) or chat history continuity breaks.
- Tool names are runtime-discovered contracts; keep dispatcher and tool schema in sync. Model-facing names are the four split docker tools; `normalizeDockerToolCall` maps them (and injects default actions) onto `manage_user_docker` before gating/dispatch/logging.
- Tool event fields (`trace_id`, `session_id`, `module`, `phase`, `tool_name`, `tool_call_id`, `step`, `duration_ms`, `args`, `result`) are consumed by Logger diagnostics; keep them stable.
- For `manage_user_docker(action=create)`, prefer framework images by default; external image pull must follow explicit user approval.
- Container selection ladder: reuse an existing container (`action=list`, matched by its `purpose`) before creating from a framework image, and only pull an external image after `estimate_image_pull` + user approval.
- `action=create` should pass `purpose`; the agent maintains `/workspace/.whalebot/NOTES.md` in each container to record installed environments for future reuse.
- Long installs/builds use `action=exec` with `async=true` + `action=exec_status` polling to avoid HTTP timeouts.
- Plan-gate hard confirmation (when `restrict_mutating_tools` is on) now only blocks high-risk actions: `remove`, `delete_file`, `pull_image`, and `create` with a non-framework image; routine mutations (framework-image create, exec, writes) stay fluid.
