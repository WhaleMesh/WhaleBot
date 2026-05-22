const AUTH_PREFIX = "/api/adapter-webui/auth";

const MAX_USERNAME_LEN = 128;
const MAX_PASSWORD_LEN = 256;
const MIN_NEW_PASSWORD_LEN = 8;

export function validateUsername(s) {
  const u = s.trim();
  if (!u) return { ok: false, error: "Username is required." };
  if (u.length > MAX_USERNAME_LEN) return { ok: false, error: "Username must be at most 128 characters." };
  if (!/^[\p{L}\p{N}._-]+$/u.test(u)) return { ok: false, error: "Use only letters, numbers, underscore (_), hyphen (-), or dot (.)." };
  return { ok: true, username: u };
}

export function validateOptionalNewPassword(newP, confirm) {
  const hasA = newP.length > 0;
  const hasB = confirm.length > 0;
  if (!hasA && !hasB) return { ok: true, password: null };
  if (hasA !== hasB) return { ok: false, error: "Fill in both new password fields, or leave both empty." };
  if (newP !== confirm) return { ok: false, error: "New passwords do not match." };
  if (newP.length < MIN_NEW_PASSWORD_LEN) return { ok: false, error: "At least 8 characters." };
  if (newP.length > MAX_PASSWORD_LEN) return { ok: false, error: "Password must be at most 256 characters." };
  return { ok: true, password: newP };
}

async function authFetch(path, opts = {}) {
  const res = await fetch(AUTH_PREFIX + path, {
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
  return { res, data };
}

export async function me() {
  const { res, data } = await authFetch("/me");
  if (res.status === 401) return { ok: false };
  if (!res.ok) throw new Error(String(data.error || res.statusText));
  return { ok: true, username: typeof data.username === "string" ? data.username : "" };
}

export async function login(username, password) {
  const { res, data } = await authFetch("/login", {
    method: "POST",
    body: JSON.stringify({ username, password }),
  });
  if (!res.ok) return { ok: false, error: typeof data.error === "string" ? data.error : "login failed" };
  return { ok: true };
}

export async function logout() {
  await authFetch("/logout", { method: "POST" });
}

export async function updateCredentials(body) {
  const payload = {
    current_password: body.currentPassword,
    new_username: body.newUsername.trim(),
  };
  if (body.newPassword != null && body.newPassword !== "") {
    payload.new_password = body.newPassword;
  }
  const { res, data } = await authFetch("/credentials", {
    method: "PUT",
    body: JSON.stringify(payload),
  });
  if (!res.ok) {
    return { ok: false, error: typeof data.error === "string" ? data.error : "update failed" };
  }
  return { ok: true, username: typeof data.username === "string" ? data.username : "" };
}
