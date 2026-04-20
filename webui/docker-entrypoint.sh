#!/bin/sh
set -e
: "${ORCHESTRATOR_URL:=http://localhost:8080}"
cat > /usr/share/nginx/html/env.js <<EOF
window.__WHALESBOT_ENV__ = {
  ORCHESTRATOR_URL: "${ORCHESTRATOR_URL}"
};
EOF
exec nginx -g 'daemon off;'
