const AUTH_PREFIX = "/api/webui/auth";

const MAX_USERNAME_LEN = 128;
const MAX_PASSWORD_LEN = 256;
const MIN_NEW_PASSWORD_LEN = 8;

/**
 * @param {string} s
 * @returns {{ ok: true, username: string } | { ok: false, errorKey: string }}
 */
export function validateAccountUsername(s) {
  const u = s.trim();
  if (!u) {
    return { ok: false, errorKey: "auth.usernameRequired" };
  }
  if (u.length > MAX_USERNAME_LEN) {
    return { ok: false, errorKey: "auth.usernameTooLong" };
  }
  if (!/^[\p{L}\p{N}._-]+$/u.test(u)) {
    return { ok: false, errorKey: "auth.usernameInvalidChars" };
  }
  return { ok: true, username: u };
}

/**
 * Optional new password: both empty = no change; both filled = validate; only one filled = error.
 * @param {string} newP
 * @param {string} confirm
 * @returns {{ ok: true, password: string | null } | { ok: false, errorKey: string }}
 */
export function validateOptionalNewPassword(newP, confirm) {
  const a = newP;
  const b = confirm;
  const hasA = a.length > 0;
  const hasB = b.length > 0;
  if (!hasA && !hasB) {
    return { ok: true, password: null };
  }
  if (hasA !== hasB) {
    return { ok: false, errorKey: "auth.passwordBothOrNone" };
  }
  if (a !== b) {
    return { ok: false, errorKey: "auth.passwordMismatch" };
  }
  if (a.length < MIN_NEW_PASSWORD_LEN) {
    return { ok: false, errorKey: "auth.passwordMin" };
  }
  if (a.length > MAX_PASSWORD_LEN) {
    return { ok: false, errorKey: "auth.passwordTooLong" };
  }
  return { ok: true, password: a };
}

/** @param {string} msg from API `error` string */
export function mapAuthServerError(msg) {
  /** @type {Record<string, string>} */
  const m = {
    "no changes": "auth.noChanges",
    "invalid username": "auth.usernameInvalidChars",
    "invalid new password length": "auth.passwordLengthServer",
    "current_password required": "auth.requiredField",
    "invalid current password": "auth.invalidCurrentPassword",
  };
  return m[msg] || null;
}

/**
 * @param {string} path
 * @param {RequestInit} [opts]
 */
async function authFetch(path, opts = {}) {
  const res = await fetch(AUTH_PREFIX + path, {
    credentials: "include",
    cache: "no-store",
    headers: { "Content-Type": "application/json", ...(opts.headers || {}) },
    ...opts,
  });
  const text = await res.text();
  /** @type {Record<string, unknown>} */
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
  if (res.status === 401) {
    return { ok: false };
  }
  if (!res.ok) {
    throw new Error(String(data.error || res.statusText));
  }
  const username =
    typeof data.username === "string" ? data.username : "";
  return { ok: true, username };
}

/**
 * @param {string} username
 * @param {string} password
 */
export async function login(username, password) {
  const { res, data } = await authFetch("/login", {
    method: "POST",
    body: JSON.stringify({ username, password }),
  });
  if (!res.ok) {
    return {
      ok: false,
      error: typeof data.error === "string" ? data.error : "login failed",
    };
  }
  return { ok: true };
}

export async function logout() {
  await authFetch("/logout", { method: "POST" });
}

/**
 * @param {{ currentPassword: string, newUsername: string, newPassword?: string | null }} body
 */
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
    const raw = typeof data.error === "string" ? data.error : "update failed";
    return {
      ok: false,
      error: raw,
      errorKey: mapAuthServerError(raw),
    };
  }
  const username =
    typeof data.username === "string" ? data.username : "";
  return { ok: true, username };
}
