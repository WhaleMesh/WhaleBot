# user-docker-manager

## ServiceCard
```yaml
service: user-docker-manager
role: user_docker_manager_system_tool
compose_service: user-docker-manager
image: whalebot/user-docker-manager:latest
build_context: ./user-docker-manager
owner: tbd
runtime: go_http_service_with_docker_socket_access
default_port: 18082
health_endpoint: GET /health (local listener; compose healthcheck + node-side debugging only)
component_registration:
  enabled: true
  mechanism: outbound node tunnel (POST {ORCHESTRATOR_URL}/api/v1/nodes/connect + yamux); no registerclient heartbeat
  name: user-docker-manager@<NODE_NAME> (assigned by orchestrator nodes hub)
  type: tool
  capabilities:
    - userdocker_list
    - userdocker_create
    - userdocker_start
    - userdocker_stop
    - userdocker_touch
    - userdocker_switch_scope
    - userdocker_remove
    - userdocker_restart
    - userdocker_exec
    - userdocker_files
    - userdocker_copy
    - userdocker_artifact_export
    - userdocker_interface_contract
    - userdocker_images
    - userdocker_images_estimate
    - userdocker_pull
    - userdocker_logs
    - userdocker_interface_discovery
  meta:
    default_image: whalebot/userdocker-base:latest
    go_build_image: whalebot/userdocker-golang:latest
    default_network: whalebot_net
last_verified_from:
  - docker-compose.yml
  - user-docker-manager/cmd/server/main.go
  - user-docker-manager/internal/creator/creator.go
```

## Purpose
- Full user docker lifecycle management via Docker Engine API.
- Runs as a **node**: dials the orchestrator (`/api/v1/nodes/connect`, header `X-Node-Token`) and serves this whole HTTP API back over the yamux tunnel, reconnecting with backoff. Only outbound connectivity is required, so the node machine needs no public address (see root `docker-compose.node.yml` for standalone node deployment). Tunnel presence is the manager's liveness.
- Multiple nodes may connect; the orchestrator addresses containers as `"<node>/<name>"` and routes each request to its node (this service itself only ever sees bare container names).
- Supports dual-scope containers (`session_scoped` / `global_service`) with scope switching.
- For `session_scoped` creation, container runtime name appends a sanitized `session_id` suffix to reduce naming conflicts across sessions.
- Supports list/create/start/stop/touch/remove/restart and interface-discovery operations.
- Proxies command execution, file CRUD and artifact export to managed userdocker containers.
- Enforces `userdocker.v1` public interface contract on newly created userdocker containers.
- Enforces manager boundary: only containers labeled as manager-owned `userdocker` are operable.
- Enforces image policy: prefer framework images; pulling external images requires explicit user approval.

## External API
### Endpoint: GET /health
```yaml
method: GET
path: /health
request: none
response:
  status: ok
  service: user-docker-manager
error_behavior: standard_http_status
```

### Endpoint: POST /api/v1/user-dockers
```yaml
method: POST
path: /api/v1/user-dockers
request:
  content_type: application/json
  body:
    name: string (required)
    image: string
    cmd: string[]
    env: object<string,string>
    labels: object<string,string>
    network: string
    auto_register: boolean
    port: int (default 9000)
    scope: string (session_scoped|global_service, default session_scoped)
    session_id: string (required when scope=session_scoped)
    workspace: string (optional volume name; default derived from scope)
    purpose: string (optional; one-line description stored as label whalebot.userdocker.purpose and echoed in list)
    external_image_approved_by_user: boolean (required for non-framework images)
response:
  success: boolean
  container_id: string
  name: string
  port: int
  interface: userdocker_public_interface_descriptor
  error: string
error_behavior:
  decode_error: http_200_with_success_false
  docker_or_contract_validation_error: http_200_with_success_false
```

### Endpoint: GET /api/v1/user-dockers
```yaml
method: GET
path: /api/v1/user-dockers?all=true|false
request: none
response:
  success: boolean
  containers: userdocker_container_summary[]
  error: string
```

### Endpoint: GET /api/v1/user-dockers/images
```yaml
method: GET
path: /api/v1/user-dockers/images
request: none
response:
  success: boolean
  default_image: string
  allowed_images: string[]
  profiles:
    go_build:
      recommended_image: whalebot/userdocker-golang:latest
```

### Endpoint: GET /api/v1/user-dockers/images/estimate
```yaml
method: GET
path: /api/v1/user-dockers/images/estimate?ref=<image>
request: none
response:
  success: boolean
  estimate:
    ref: string
    already_local: boolean
    compressed_bytes: int   # upper bound; cached layers not subtracted
    layer_count: int
    note: string            # explains estimate limits / unsupported registries
note: only docker.io anonymous manifests are sized; other registries return a note only.
```

### Endpoint: POST /api/v1/user-dockers/pull
```yaml
method: POST
path: /api/v1/user-dockers/pull
request:
  content_type: application/json
  body:
    ref: string (required)
    external_image_approved_by_user: boolean (required for non whalebot/* refs)
response:
  success: boolean
  job_id: string   # poll via pull/status
  ref: string
  error: string
note: pull runs asynchronously (background, 30m cap) so chat requests do not block on large downloads.
```

### Endpoint: GET /api/v1/user-dockers/pull/status
```yaml
method: GET
path: /api/v1/user-dockers/pull/status?job_id=<id>
response:
  success: boolean
  job:
    ref: string
    done: boolean
    error: string
    started_at: string
```

### Endpoint: POST /api/v1/user-dockers/copy
```yaml
method: POST
path: /api/v1/user-dockers/copy
body:
  from_name: string (source container, required)
  from_path: string (required)
  to_name: string (target container, required)
  to_path: string (required)
  session_id: string
response:
  success: boolean
  size: integer   # bytes copied
  error: string
note: cross-container binary-safe file copy on this node (manager fetches base64 from the source file API and PUTs to the target; 64MB cap). Both containers are session-authorized; file bytes never enter LLM context.
```

### Endpoint: GET /api/v1/user-dockers/{name}/logs
```yaml
method: GET
path: /api/v1/user-dockers/{name}/logs?tail=200
request: none
response:
  success: boolean
  name: string
  logs: string   # demultiplexed stdout+stderr, trailing `tail` lines (default 200, max 2000)
note: session_scoped containers require matching session_id.
```

### Endpoint: GET /api/v1/user-dockers/{name}/exec/status
```yaml
method: GET
path: /api/v1/user-dockers/{name}/exec/status?job_id=<id>
request: none
response:
  success: boolean
  job_id: string
  done: boolean
  exit_code: int
  stdout: string
  stderr: string
  duration_ms: int
note: proxied to userdocker /api/v1/userdocker/exec/status; used to poll async exec jobs.
```

### Endpoint: DELETE /api/v1/user-dockers/{name}
```yaml
method: DELETE
path: /api/v1/user-dockers/{name}?force=true|false
request: none
response:
  success: boolean
  name: string
  error: string
```

### Endpoint: POST /api/v1/user-dockers/{name}/restart
```yaml
method: POST
path: /api/v1/user-dockers/{name}/restart?timeout_sec=10
request: none
response:
  success: boolean
  name: string
  error: string
```

### Endpoint Group: Extended lifecycle
```yaml
start: POST /api/v1/user-dockers/{name}/start
stop: POST /api/v1/user-dockers/{name}/stop?timeout_sec=10
touch: POST /api/v1/user-dockers/{name}/touch
switch_scope: POST /api/v1/user-dockers/{name}/switch-scope
switch_scope_body:
  target_scope: session_scoped|global_service
  session_id: string (required when target_scope=session_scoped)
```

### Endpoint Group: Workspace operations
```yaml
exec: POST /api/v1/user-dockers/{name}/exec
exec_note: body accepts async=true -> returns job_id; poll exec/status. Sync path clamps timeout to 300s.
exec_status: GET /api/v1/user-dockers/{name}/exec/status?job_id=<id>
files_list: GET /api/v1/user-dockers/{name}/files?path=.
file_read: GET /api/v1/user-dockers/{name}/file?path=...
file_write: PUT /api/v1/user-dockers/{name}/file (body: path + content_base64 OR plain content)
file_delete: DELETE /api/v1/user-dockers/{name}/file?path=...
mkdir: POST /api/v1/user-dockers/{name}/files/mkdir
move: POST /api/v1/user-dockers/{name}/files/move
artifact_export: GET /api/v1/user-dockers/{name}/artifacts/export?path=...
note: session_scoped containers require matching session_id (query/header/body).
```

### Endpoint: GET /api/v1/user-dockers/interface-contract
```yaml
method: GET
path: /api/v1/user-dockers/interface-contract
request: none
response:
  success: true
  contract: userdocker_public_interface_descriptor
```

### Endpoint: GET /api/v1/user-dockers/{name}/interface
```yaml
method: GET
path: /api/v1/user-dockers/{name}/interface?port=9000
request: none
response:
  success: boolean
  name: string
  interface: userdocker_public_interface_descriptor
  error: string
```

## Internal Calls
- Docker Engine API over Unix socket `/var/run/docker.sock`.
- `POST ${ORCHESTRATOR_URL}/api/v1/nodes/connect` outbound tunnel (registration + liveness + API transport).
- Pulls image when not detected as local image tag.
- Calls spawned container `GET /api/v1/userdocker/interface` to enforce interface contract.

## Environment Variables
### USER_DOCKER_MANAGER_PORT
```yaml
name: USER_DOCKER_MANAGER_PORT
default: "18082"
required: false
effect: bind_port_for_http_server
```

### ORCHESTRATOR_URL
```yaml
name: ORCHESTRATOR_URL
default: http://orchestrator:18080
required: false
effect: tunnel_dial_target_and_env_injection_source_for_spawned_containers (use the public orchestrator URL on remote nodes)
```

### NODE_NAME
```yaml
name: NODE_NAME
default: container hostname
required: false
effect: node_identity_for_tunnel_and_composite_container_names (unique per machine; lowercase [a-z0-9_.-])
```

### NODE_TOKEN
```yaml
name: NODE_TOKEN
default: ""
required: true_for_tunnel
effect: shared_secret_presented_to_orchestrator_on_connect
```

### USERDOCKER_DEFAULT_IMAGE
```yaml
name: USERDOCKER_DEFAULT_IMAGE
default: whalebot/userdocker-base:latest
required: false
effect: fallback_image_when_request_image_is_empty
```

### DOCKER_NETWORK
```yaml
name: DOCKER_NETWORK
default: whalebot_net
required: false
effect: fallback_network_for_spawned_containers
```

### USERDOCKER_ALLOWED_IMAGES
```yaml
name: USERDOCKER_ALLOWED_IMAGES
default: whalebot/userdocker-base:latest,whalebot/userdocker-golang:latest
required: false
effect: comma_separated_allowlist_for_framework_images
```

### USERDOCKER_IDLE_HOURS
```yaml
name: USERDOCKER_IDLE_HOURS
default: "24"
required: false
effect: idle_ttl_hours_for_session_scoped_containers
```

### USERDOCKER_IDLE_CHECK_SEC
```yaml
name: USERDOCKER_IDLE_CHECK_SEC
default: "300"
required: false
effect: idle_sweeper_poll_interval_seconds
```

## Runtime Contract
- network: `whalebot_net`.
- depends_on: `orchestrator`, `userdocker-base`, `userdocker-golang`.
- healthcheck: `wget http://localhost:${USER_DOCKER_MANAGER_PORT}/health`.
- volumes: `/var/run/docker.sock:/var/run/docker.sock`.
- security_notes: docker socket grants high privileges; treat this container as sensitive.

## AI Lookup Hints
```yaml
aliases:
  - user_docker_manager
  - sandbox_manager
  - container_lifecycle_tool
query_to_endpoint:
  list_userdocker_images: GET /api/v1/user-dockers/images
  estimate_image_pull: GET /api/v1/user-dockers/images/estimate
  pull_image: POST /api/v1/user-dockers/pull
  pull_status: GET /api/v1/user-dockers/pull/status
  copy_file: POST /api/v1/user-dockers/copy
  list_userdockers: GET /api/v1/user-dockers
  create_userdocker: POST /api/v1/user-dockers
  userdocker_logs: GET /api/v1/user-dockers/{name}/logs
  exec_status: GET /api/v1/user-dockers/{name}/exec/status
  start_userdocker: POST /api/v1/user-dockers/{name}/start
  stop_userdocker: POST /api/v1/user-dockers/{name}/stop
  touch_userdocker: POST /api/v1/user-dockers/{name}/touch
  switch_userdocker_scope: POST /api/v1/user-dockers/{name}/switch-scope
  remove_userdocker: DELETE /api/v1/user-dockers/{name}
  restart_userdocker: POST /api/v1/user-dockers/{name}/restart
  exec_userdocker: POST /api/v1/user-dockers/{name}/exec
  file_ops_userdocker: /api/v1/user-dockers/{name}/file(s)*
  export_userdocker_artifact: GET /api/v1/user-dockers/{name}/artifacts/export
  get_interface_contract: GET /api/v1/user-dockers/interface-contract
  get_userdocker_interface: GET /api/v1/user-dockers/{name}/interface
  health: GET /health
compatible_orchestrator_proxy:
  path:
    - GET /api/v1/tools/user-dockers
    - POST /api/v1/tools/user-dockers
    - DELETE /api/v1/tools/user-dockers/{name}
    - POST /api/v1/tools/user-dockers/{name}/restart
    - GET /api/v1/tools/user-dockers/interface-contract
    - GET /api/v1/tools/user-dockers/{name}/interface
```

## Change Safety
- Keep `name` mandatory to avoid ambiguous container identity.
- Preserve default labels (`whalebot.component`, `whalebot.type`, `whalebot.userdocker.interface_version`) for downstream discovery.
- Preserve scope/session/workspace labels (`whalebot.userdocker.scope`, `whalebot.userdocker.session_id`, `whalebot.userdocker.workspace`) for access control and sweeper logic.
- Never skip userdocker contract validation for create requests.
- Do not allow unapproved external images in create requests.
- Any docker socket mount change impacts core feature availability.
