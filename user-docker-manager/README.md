# user-docker-manager

## ServiceCard
```yaml
service: user-docker-manager
role: user_docker_manager_system_tool
compose_service: user-docker-manager
image: whalesbot/user-docker-manager:latest
build_context: ./user-docker-manager
owner: tbd
runtime: go_http_service_with_docker_socket_access
default_port: 8082
health_endpoint: GET /health
component_registration:
  enabled: true
  name: user-docker-manager
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
    - userdocker_artifact_export
    - userdocker_interface_contract
    - userdocker_images
    - userdocker_interface_discovery
  meta:
    default_image: whalesbot/userdocker-base:latest
    go_build_image: whalesbot/userdocker-golang:latest
    default_network: mvp_net
last_verified_from:
  - docker-compose.yml
  - user-docker-manager/cmd/server/main.go
  - user-docker-manager/internal/creator/creator.go
```

## Purpose
- Full user docker lifecycle management via Docker Engine API.
- Supports dual-scope containers (`session_scoped` / `global_service`) with scope switching.
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
      recommended_image: whalesbot/userdocker-golang:latest
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
files_list: GET /api/v1/user-dockers/{name}/files?path=.
file_read: GET /api/v1/user-dockers/{name}/file?path=...
file_write: PUT /api/v1/user-dockers/{name}/file
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
- `POST ${ORCHESTRATOR_URL}/api/v1/components/register` for service registration.
- Pulls image when not detected as local image tag.
- Calls spawned container `GET /api/v1/userdocker/interface` to enforce interface contract.

## Environment Variables
### USER_DOCKER_MANAGER_PORT
```yaml
name: USER_DOCKER_MANAGER_PORT
default: "8082"
required: false
effect: bind_port_for_http_server
```

### ORCHESTRATOR_URL
```yaml
name: ORCHESTRATOR_URL
default: http://orchestrator:8080
required: false
effect: registration_target_and_env_injection_source_for_spawned_containers
```

### SERVICE_HOST
```yaml
name: SERVICE_HOST
default: user-docker-manager
required: false
effect: advertised_endpoint_host_for_registration
```

### USERDOCKER_DEFAULT_IMAGE
```yaml
name: USERDOCKER_DEFAULT_IMAGE
default: whalesbot/userdocker-base:latest
required: false
effect: fallback_image_when_request_image_is_empty
```

### DOCKER_NETWORK
```yaml
name: DOCKER_NETWORK
default: mvp_net
required: false
effect: fallback_network_for_spawned_containers
```

### USERDOCKER_ALLOWED_IMAGES
```yaml
name: USERDOCKER_ALLOWED_IMAGES
default: whalesbot/userdocker-base:latest,whalesbot/userdocker-golang:latest
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
- network: `mvp_net`.
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
  list_userdockers: GET /api/v1/user-dockers
  create_userdocker: POST /api/v1/user-dockers
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
- Preserve default labels (`mvp.component`, `mvp.type`, `mvp.userdocker.interface_version`) for downstream discovery.
- Preserve scope/session/workspace labels (`mvp.userdocker.scope`, `mvp.userdocker.session_id`, `mvp.userdocker.workspace`) for access control and sweeper logic.
- Never skip userdocker contract validation for create requests.
- Do not allow unapproved external images in create requests.
- Any docker socket mount change impacts core feature availability.
