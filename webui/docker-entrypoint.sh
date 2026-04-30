#!/bin/sh
set -e

/usr/local/bin/webui-auth -listen 127.0.0.1:8089 -data-dir /data &
AUT_PID=$!

term_handler() {
	kill "$AUT_PID" 2>/dev/null || true
	if [ -n "${CADDY_PID:-}" ]; then
		kill "$CADDY_PID" 2>/dev/null || true
	fi
	exit 0
}
trap term_handler TERM INT

i=0
while [ "$i" -lt 50 ]; do
	if wget -qO- http://127.0.0.1:8089/api/webui/auth/health >/dev/null 2>&1; then
		break
	fi
	i=$((i + 1))
	sleep 0.1
done

: "${ORCHESTRATOR_URL:=http://localhost:8080}"
cat > /srv/env.js <<EOF
window.__WHALESBOT_ENV__ = {
  ORCHESTRATOR_URL: "${ORCHESTRATOR_URL}"
};
EOF

caddy run --config /etc/caddy/Caddyfile --adapter caddyfile &
CADDY_PID=$!
wait "$CADDY_PID"
term_handler
