const API_PREFIX = "/api/adapter-webui";

async function req(path, opts = {}) {
  const res = await fetch(API_PREFIX + path, {
    credentials: "include",
    cache: "no-store",
    headers: { "Content-Type": "application/json", ...(opts.headers || {}) },
    ...opts,
  });
  const text = await res.text();
  let data = {};
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = {};
    }
  }
  if (!res.ok) {
    throw new Error(typeof data.error === "string" ? data.error : `${res.status} ${res.statusText}`);
  }
  return data;
}

export const api = {
  chat: (body) => req("/chat", { method: "POST", body: JSON.stringify(body) }),
  sessions: () => req("/sessions"),
  session: (id) => req("/sessions/" + encodeURIComponent(id)),
  deleteSession: (id) => req("/sessions/" + encodeURIComponent(id), { method: "DELETE" }),
  loggerEvents: (params = {}) => {
    const q = new URLSearchParams();
    if (params.limit) q.set("limit", String(params.limit));
    if (params.session_id) q.set("session_id", params.session_id);
    if (params.trace_id) q.set("trace_id", params.trace_id);
    const s = q.toString();
    return req("/logger/events" + (s ? "?" + s : ""));
  },
};
