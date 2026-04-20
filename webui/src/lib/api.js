function base() {
  const env = window.__WHALESBOT_ENV__ || {};
  return env.ORCHESTRATOR_URL || "http://localhost:8080";
}

async function req(path, opts = {}) {
  const res = await fetch(base() + path, {
    headers: { "Content-Type": "application/json", ...(opts.headers || {}) },
    ...opts,
  });
  if (!res.ok) {
    const txt = await res.text().catch(() => "");
    throw new Error(`${res.status} ${res.statusText}: ${txt}`);
  }
  return res.json();
}

export const api = {
  health: () => req("/health"),
  components: () => req("/api/v1/components"),
  logs: () => req("/api/v1/logs/recent"),
  sessions: () => req("/api/v1/sessions"),
  session: (id) => req("/api/v1/sessions/" + encodeURIComponent(id)),
  chat: (body) =>
    req("/api/v1/chat", { method: "POST", body: JSON.stringify(body) }),
  dockerCreate: (body) =>
    req("/api/v1/tools/docker-create", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  runGo: (body) =>
    req("/api/v1/environments/golang/run", {
      method: "POST",
      body: JSON.stringify(body),
    }),
};
