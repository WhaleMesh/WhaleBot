/** Em dash placeholder when invalid or missing (avoid i18n cycle in pure helpers). */
export const DATE_EMPTY = '—';

/**
 * Fixed local wall time: yyyy/MM/dd HH:mm:ss (24h).
 * @param {string | number | Date | null | undefined} ts
 * @returns {string}
 */
export function formatDateTime24(ts) {
  if (ts == null || ts === '') return DATE_EMPTY;
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return DATE_EMPTY;
  const p = (/** @type {number} */ n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

/**
 * Seconds remaining → minutes for display (ceil), min 1 when sec in (0, 60].
 * @param {number | null | undefined} sec
 * @returns {number | null} null if sec is null/undefined/NaN/<0
 */
export function ceilMinutesFromSeconds(sec) {
  if (sec == null || !Number.isFinite(sec) || sec < 0) return null;
  if (sec === 0) return 0;
  return Math.max(1, Math.ceil(sec / 60));
}
