# webui

## ServiceCard
```yaml
service: webui
role: browser_dashboard_frontend
compose_service: webui
image: whalesbot/webui:latest
build_context: ./webui
owner: tbd
runtime: static_frontend_served_by_nginx
default_port: 80
health_endpoint: GET /
component_registration:
  enabled: false
  name: null
  type: null
  capabilities: []
  meta: {}
last_verified_from:
  - docker-compose.yml
  - webui/src/lib/api.js
  - webui/Dockerfile
```

## Purpose
- Provides the human-facing dashboard for component status, logs, sessions, and tool operations.
- Calls orchestrator REST API from the browser.
- Is not a backend component in the orchestrator registry.

## External API
### Endpoint: GET /
```yaml
method: GET
path: /
request: none
response:
  content_type: text/html
  body: web_dashboard_app
error_behavior: standard_http_status_from_nginx
```

## Internal Calls
- Browser-side fetch calls to orchestrator:
  - `GET /health`
  - `GET /api/v1/components`
  - `GET /api/v1/logs/recent`
  - `GET /api/v1/sessions`
  - `GET /api/v1/sessions/{id}`
  - `POST /api/v1/chat`
  - `POST /api/v1/tools/docker-create`
  - `POST /api/v1/environments/golang/run`

## Environment Variables
### ORCHESTRATOR_URL
```yaml
name: ORCHESTRATOR_URL
default: http://localhost:8080
required: false
effect: browser_reachable_base_url_for_api_requests_injected_as_runtime_env
```

### WEBUI_PORT
```yaml
name: WEBUI_PORT
default: "3000"
required: false
effect: host_port_mapping_to_container_port_80_in_compose
```

## Runtime Contract
- network: `mvp_net`.
- depends_on: `orchestrator`.
- healthcheck: `wget http://localhost/`.
- volumes: none.
- security_notes: frontend trusts configured orchestrator URL; CORS and host reachability must match deployment.

## AI Lookup Hints
```yaml
aliases:
  - dashboard
  - frontend
  - admin_ui
query_to_endpoint:
  ui_health: GET /
backend_api_base: ORCHESTRATOR_URL
```

## Change Safety
- Keep API base env injection path stable, otherwise browser fetches fail.
- `ORCHESTRATOR_URL` must be host-reachable for browsers (not only Docker-internal DNS).
- UI assumes orchestrator response contracts from `/api/v1/*` endpoints.
