/** FNV-1a 32-bit — stable hue per string for badge colors */
function hashString32(s) {
  let h = 2166136261;
  const str = String(s || '');
  for (let i = 0; i < str.length; i++) {
    h ^= str.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return h >>> 0;
}

/**
 * CSS custom properties for a type badge (dark UI).
 * @param {string} type
 * @returns {string}
 */
export function typeBadgeStyle(type) {
  const h = hashString32(type);
  const hue = h % 360;
  const sat = 46 + (h % 24);
  const bgL = 14 + (h % 8);
  const fgL = 78 + (h % 14);
  const brL = 28 + (h % 12);
  const bg = `hsl(${hue},${sat}%,${bgL}%)`;
  const fg = `hsl(${hue},62%,${fgL}%)`;
  const br = `hsl(${hue},${sat}%,${brL}%)`;
  return `background-color:${bg};color:${fg};border:1px solid ${br};`;
}

/**
 * @param {unknown[]} components
 * @returns {{ ttlSec: number | null, sweepSec: number | null }}
 */
export function parseUserDockerManagerMeta(components) {
  const list = Array.isArray(components) ? components : [];
  const c = list.find((x) => String(x?.name || '') === 'user-docker-manager');
  const m = c?.meta && typeof c.meta === 'object' ? c.meta : {};
  const ttl = parseInt(String(m.userdocker_temp_ttl_sec || ''), 10);
  const tick = parseInt(String(m.userdocker_idle_check_sec || ''), 10);
  return {
    ttlSec: Number.isFinite(ttl) && ttl > 0 ? ttl : null,
    sweepSec: Number.isFinite(tick) && tick > 0 ? tick : null,
  };
}

export function formatDurationSec(sec) {
  if (sec == null || !Number.isFinite(sec) || sec < 0) return '—';
  const s = Math.floor(sec);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const r = s % 60;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${r}s`;
  return `${r}s`;
}

/** Whole minutes (ceil), for idle / policy copy; 0 → "0". */
export function formatDurationMinutesCeil(sec) {
  if (sec == null || !Number.isFinite(sec) || sec < 0) return '—';
  return String(Math.ceil(sec / 60));
}

/**
 * @param {Record<string, unknown>} container — item from userDocker list API
 * @param {number | null} ttlSec
 * @returns {{ kind: 'persistent' } | { kind: 'temp'; seconds: number } | { kind: 'unknown' }}
 */
export function tempRemovalCountdown(container, ttlSec) {
  if (!ttlSec || !container || typeof container !== 'object') return { kind: 'unknown' };
  const scope = String(
    container.scope ||
      (container.labels && container.labels['whalebot.userdocker.scope']) ||
      '',
  ).toLowerCase();
  if (scope === 'global_service') return { kind: 'persistent' };
  const raw = container.last_active_at;
  if (!raw) return { kind: 'unknown' };
  const last = new Date(String(raw)).getTime();
  if (Number.isNaN(last)) return { kind: 'unknown' };
  const deadline = last + ttlSec * 1000;
  const seconds = Math.max(0, Math.floor((deadline - Date.now()) / 1000));
  return { kind: 'temp', seconds };
}
