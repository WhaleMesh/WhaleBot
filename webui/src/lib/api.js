function base() {
  const env = window.__WHALESBOT_ENV__ || {};
  return env.ORCHESTRATOR_URL || "http://localhost:8080";
}

/** Same URL as internal `base()` — for custom fetch (e.g. LLM test 409 body). */
export function getOrchestratorBase() {
  return base();
}

async function req(path, opts = {}) {
  const res = await fetch(base() + path, {
    cache: "no-store",
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
  statsOverview: async () => {
    const res = await fetch(base() + "/api/v1/stats/overview", {
      cache: "no-store",
      headers: { "Content-Type": "application/json" },
    });
    const data = await res.json().catch(() => ({}));
    if (
      res.status === 503 &&
      (data.code === "stats_disabled" || data.success === false)
    ) {
      return {
        success: false,
        disabled: true,
        error: data.error || "stats service not enabled",
      };
    }
    if (!res.ok) {
      throw new Error(
        `${res.status} ${res.statusText}: ${JSON.stringify(data)}`,
      );
    }
    return data;
  },
  logs: () => req("/api/v1/logs/recent"),
  loggerEvents: (limit = 200) =>
    req(`/api/v1/logger/events/recent?limit=${encodeURIComponent(limit)}`),
  sessions: () => req("/api/v1/sessions"),
  session: (id) => req("/api/v1/sessions/" + encodeURIComponent(id)),
  deleteSession: (id) =>
    req("/api/v1/sessions/" + encodeURIComponent(id), { method: "DELETE" }),
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
  llmConfigGet: (name) =>
    req(`/api/v1/llm-components/${encodeURIComponent(name)}/config`),
  llmConfigPut: (name, body) =>
    req(`/api/v1/llm-components/${encodeURIComponent(name)}/config`, {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  llmActivePost: (name, body) =>
    req(`/api/v1/llm-components/${encodeURIComponent(name)}/active`, {
      method: "POST",
      body: JSON.stringify(body),
    }),
  llmTestPost: (name, body = {}) =>
    req(`/api/v1/llm-components/${encodeURIComponent(name)}/test`, {
      method: "POST",
      body: JSON.stringify(body),
    }),
  skillsList: (opts = {}) => {
    const q = new URLSearchParams();
    if (opts.limit != null) q.set("limit", String(opts.limit));
    if (opts.offset != null) q.set("offset", String(opts.offset));
    const s = q.toString();
    return req("/api/v1/skills" + (s ? "?" + s : ""));
  },
  skillsSearch: (q, limit = 10) =>
    req(
      "/api/v1/skills/search?" +
        new URLSearchParams({ q, limit: String(limit) }).toString(),
    ),
  skillsGet: (id) => req("/api/v1/skills/" + encodeURIComponent(id)),
  skillsCreate: (body) =>
    req("/api/v1/skills", { method: "POST", body: JSON.stringify(body) }),
  skillsUpdate: (id, body) =>
    req("/api/v1/skills/" + encodeURIComponent(id), {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  skillsDelete: (id) =>
    req("/api/v1/skills/" + encodeURIComponent(id), { method: "DELETE" }),
};
