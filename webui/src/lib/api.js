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
  loggerEvents: (limit = 200) =>
    req(`/api/v1/logger/events/recent?limit=${encodeURIComponent(limit)}`),
  sessions: () => req("/api/v1/sessions"),
  session: (id) => req("/api/v1/sessions/" + encodeURIComponent(id)),
  chat: (body) =>
    req("/api/v1/chat", { method: "POST", body: JSON.stringify(body) }),
  userDockerContract: () => req("/api/v1/tools/user-dockers/interface-contract"),
  userDockerList: (includeStopped = false) =>
    req(`/api/v1/tools/user-dockers?all=${includeStopped ? "true" : "false"}`),
  userDockerCreate: (body) =>
    req("/api/v1/tools/user-dockers", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  userDockerRemove: (name, force = false) =>
    req(`/api/v1/tools/user-dockers/${encodeURIComponent(name)}?force=${force ? "true" : "false"}`, {
      method: "DELETE",
    }),
  userDockerRestart: (name, timeoutSec = 10) =>
    req(`/api/v1/tools/user-dockers/${encodeURIComponent(name)}/restart?timeout_sec=${timeoutSec}`, {
      method: "POST",
    }),
  userDockerInterface: (name, port = undefined) => {
    const suffix = port ? `?port=${encodeURIComponent(port)}` : "";
    return req(`/api/v1/tools/user-dockers/${encodeURIComponent(name)}/interface${suffix}`);
  },
  runGo: (body) =>
    req("/api/v1/environments/golang/run", {
      method: "POST",
      body: JSON.stringify(body),
    }),
};
