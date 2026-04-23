# userdocker-base

## ServiceCard
```yaml
service: userdocker-base
role: minimal_spawnable_user_container_image
compose_service: userdocker-base
image: whalesbot/userdocker-base:latest
build_context: ./userdocker-base
owner: tbd
runtime: go_http_binary_used_as_base_image
default_port: 9000
health_endpoint: GET /health
component_registration:
  enabled: conditional
  name: COMPONENT_NAME
  type: COMPONENT_TYPE
  capabilities:
    - long_running
    - introspection
    - userdocker.v1
  meta:
    origin: user-docker-manager
    interface_version: userdocker.v1
last_verified_from:
  - docker-compose.yml
  - userdocker-base/main.go
```

## Purpose
- Provides a tiny HTTP service image intended to be spawned dynamically by the user-docker-manager.
- Optionally self-registers to orchestrator when `ORCHESTRATOR_URL` is provided.
- In compose, it is kept running as a build/helper container (`sleep infinity`).
- Implements the public `userdocker.v1` interface descriptor endpoint.

## External API
### Endpoint: GET /health
```yaml
method: GET
path: /health
request: none
response:
  status: ok
  service: userdocker
  name: string
error_behavior: standard_http_status
```

### Endpoint: GET /
```yaml
method: GET
path: /
request: none
response:
  content_type: text/plain
  body: "userdocker <name> (type=<type>)"
error_behavior: standard_http_status
```

### Endpoint: GET /api/v1/userdocker/interface
```yaml
method: GET
path: /api/v1/userdocker/interface
request: none
response:
  interface_version: userdocker.v1
  service_name: string
  service_type: userdocker
  description: string
  endpoints: {method,path,description}[]
  capabilities: {name,description}[]
error_behavior: standard_http_status
```

## Internal Calls
- Optional `POST ${ORCHESTRATOR_URL}/api/v1/components/register` in periodic register loop.
- No other upstream dependencies.

## Environment Variables
### COMPONENT_NAME
```yaml
name: COMPONENT_NAME
default: userdocker-anon
required: false
effect: identity_used_in_http_response_and_registration
```

### COMPONENT_TYPE
```yaml
name: COMPONENT_TYPE
default: userdocker
required: false
effect: registered_component_type
```

### PORT
```yaml
name: PORT
default: "9000"
required: false
effect: bind_port_for_http_server
```

### ORCHESTRATOR_URL
```yaml
name: ORCHESTRATOR_URL
default: ""
required: false
effect: when_empty_no_self_registration_when_set_periodic_registration_enabled
```

## Runtime Contract
- network: `mvp_net`.
- depends_on: none.
- healthcheck: none in compose helper mode.
- volumes: none.
- security_notes: spawned instances trust orchestrator URL and exposed component identity from env.

## AI Lookup Hints
```yaml
aliases:
  - user_sandbox_base
  - spawned_container_base
  - userdocker
query_to_endpoint:
  health: GET /health
  root_info: GET /
used_by:
  user-docker-manager: as_default_spawn_image
```

## Change Safety
- Keep self-registration payload keys (`name`, `type`, `endpoint`, `health_endpoint`, `capabilities`, `meta`) stable across all userdocker implementations.
- Compose helper behavior (`sleep infinity`) should not be mistaken for production spawned container behavior.
- Endpoint host is built from `COMPONENT_NAME`; changing this affects discoverability.
