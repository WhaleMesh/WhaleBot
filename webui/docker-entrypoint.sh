#!/bin/sh
set -e
: "${ORCHESTRATOR_URL:=http://localhost:8080}"
cat > /srv/env.js <<EOF
window.__WHALESBOT_ENV__ = {
  ORCHESTRATOR_URL: "${ORCHESTRATOR_URL}"
};
EOF
exec caddy run --config /etc/caddy/Caddyfile --adapter caddyfile
