# tool-docker-creator

## ServiceCard
```yaml
service: tool-docker-creator
role: docker_container_creation_tool
compose_service: tool-docker-creator
image: whalesbot/tool-docker-creator:latest
build_context: ./tool-docker-creator
owner: tbd
runtime: go_http_service_with_docker_socket_access
default_port: 8082
health_endpoint: GET /health
component_registration:
  enabled: true
  name: tool-docker-creator
  type: tool
  capabilities:
    - create_container
  meta:
    default_image: whalesbot/userdocker-base:latest
    default_network: mvp_net
last_verified_from:
  - docker-compose.yml
  - tool-docker-creator/cmd/server/main.go
  - tool-docker-creator/internal/creator/creator.go
```

## Purpose
- Creates and starts Docker containers through Docker Engine HTTP API.
- Supplies default image/network and optional self-registration env injection.
- Exposes tool endpoint used by orchestrator and worker.

## External API
### Endpoint: GET /health
```yaml
method: GET
path: /health
request: none
response:
  status: ok
  service: tool-docker-creator
error_behavior: standard_http_status
```

### Endpoint: POST /create_container
```yaml
method: POST
path: /create_container
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
response:
  success: boolean
  container_id: string
  name: string
  error: string
error_behavior:
  decode_error: http_200_with_success_false
  docker_error: http_200_with_success_false
```

## Internal Calls
- Docker Engine API over Unix socket `/var/run/docker.sock`.
- `POST ${ORCHESTRATOR_URL}/api/v1/components/register` for service registration.
- Pulls image when not detected as local image tag.

## Environment Variables
### TOOL_DOCKER_CREATOR_PORT
```yaml
name: TOOL_DOCKER_CREATOR_PORT
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
default: tool-docker-creator
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

## Runtime Contract
- network: `mvp_net`.
- depends_on: `orchestrator`, `userdocker-base`.
- healthcheck: `wget http://localhost:${TOOL_DOCKER_CREATOR_PORT}/health`.
- volumes: `/var/run/docker.sock:/var/run/docker.sock`.
- security_notes: docker socket grants high privileges; treat this container as sensitive.

## AI Lookup Hints
```yaml
aliases:
  - docker_tool
  - container_creator
  - sandbox_spawner
query_to_endpoint:
  create_container: POST /create_container
  health: GET /health
compatible_orchestrator_proxy:
  path: POST /api/v1/tools/docker-create
```

## Change Safety
- Keep `name` mandatory to avoid ambiguous container identity.
- Preserve default labels (`mvp.component`, `mvp.type`) for downstream discovery.
- Any docker socket mount change impacts core feature availability.
