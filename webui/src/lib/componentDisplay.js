/**
 * @param {Record<string, unknown> | null | undefined} c
 */
export function primaryOperationalState(c) {
  const op = String(c?.operational_state ?? '').trim();
  return op;
}

/**
 * Badge tone for Components table: success | warning | error | neutral | ghost
 * @param {Record<string, unknown> | null | undefined} c
 */
export function componentBadgeTone(c) {
  const op = primaryOperationalState(c);
  if (op === 'normal') return 'success';
  if (op === 'no_valid_configuration') return 'warning';
  if (op === 'status_check_error') return 'error';
  if (op === 'unknown') return 'neutral';
  const s = String(c?.status || '').toLowerCase();
  if (s.includes('healthy')) return 'success';
  if (s.includes('unhealthy') || s.includes('error')) return 'error';
  if (s.includes('removed') || s.includes('stopped') || s.includes('exited')) return 'neutral';
  if (s.includes('warn') || s.includes('restarting')) return 'warning';
  return 'ghost';
}

/**
 * @param {'healthy' | 'warn' | 'bad' | 'unknown'} n
 */
export function overviewToneFromOperational(c) {
  const op = primaryOperationalState(c);
  if (op === 'normal') return 'healthy';
  if (op === 'no_valid_configuration') return 'warn';
  if (op === 'status_check_error') return 'bad';
  if (op === 'unknown') return 'unknown';
  return null;
}
