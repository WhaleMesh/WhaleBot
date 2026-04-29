<script>
  import { onMount, onDestroy } from 'svelte';
  import { get } from 'svelte/store';
  import { api } from '../lib/api.js';
  import { _, locale, translate } from '../lib/i18n.js';
  import {
    parseUserDockerManagerMeta,
    tempRemovalCountdown,
    formatDurationSec,
    typeBadgeStyle,
  } from '../lib/userdockerPolicy.js';

  let components = [];
  let userdockers = [];
  let stats = null;
  let statsError = '';
  let statsDisabled = false;
  let error = '';
  /** Non-empty when GET /health reports chat_ready === false or health fetch failed. */
  let chatBlockedMessage = '';
  let timer;
  let tick = 0;
  let tickTimer;

  function fmtCount(n) {
    const v = Number(n);
    if (!Number.isFinite(v) || v < 0) return '0';
    if (v >= 1_000_000) return (v / 1_000_000).toFixed(1) + 'M';
    if (v >= 10_000) return (v / 1_000).toFixed(1) + 'k';
    return String(Math.trunc(v));
  }

  function fmtTs(ts) {
    if (!ts) return '—';
    const d = new Date(ts);
    if (Number.isNaN(d.getTime())) return '—';
    return d.toLocaleString();
  }

  /** yyyy/MM/dd HH:mm:ss (local wall clock) */
  function fmtTsSlash(ts) {
    if (!ts) return '—';
    const d = new Date(ts);
    if (Number.isNaN(d.getTime())) return '—';
    const p = (/** @type {number} */ n) => String(n).padStart(2, '0');
    return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
  }

  function shortId(v) {
    const s = String(v || '');
    if (!s) return '—';
    return s.length > 12 ? s.slice(0, 12) : s;
  }

  function normalizeStatus(v) {
    const s = String(v || '').toLowerCase().trim();
    if (!s) return 'unknown';
    if (s.includes('healthy') || s.includes('running') || s === 'up' || s.startsWith('up ')) return 'healthy';
    if (s.includes('warn') || s.includes('unhealthy') || s.includes('restarting')) return 'warn';
    if (s.includes('removed') || s.includes('stopped') || s.includes('exited') || s.includes('dead')) return 'bad';
    return 'unknown';
  }

  function displayStatus(v) {
    const normalized = normalizeStatus(v);
    if (normalized === 'unknown') {
      const raw = String(v || '').trim();
      return raw || 'unknown';
    }
    return normalized;
  }

  function isUserDockerType(v) {
    return String(v || '').toLowerCase().trim() === 'userdocker';
  }

  /** @param {'healthy' | 'warn' | 'bad' | 'unknown'} n */
  function statusBadgeClass(n) {
    if (n === 'healthy') return 'badge badge-success badge-sm shrink-0 max-w-[min(100%,14rem)] truncate';
    if (n === 'warn') return 'badge badge-warning badge-sm shrink-0 max-w-[min(100%,14rem)] truncate';
    if (n === 'bad') return 'badge badge-error badge-sm shrink-0 max-w-[min(100%,14rem)] truncate';
    return 'badge badge-ghost badge-sm shrink-0 max-w-[min(100%,14rem)] truncate';
  }

  /** Card border color aligned with status badge */
  /** @param {'healthy' | 'warn' | 'bad' | 'unknown'} n */
  function cardStatusBorderClass(n) {
    if (n === 'healthy') return 'border-wb border-success';
    if (n === 'warn') return 'border-wb border-warning';
    if (n === 'bad') return 'border-wb border-error';
    return 'border-wb border-base-300';
  }

  /** @param {Record<string, unknown>} d */
  function userDockerTypeLabel(d) {
    const raw = d && typeof d.labels === 'object' && d.labels != null ? d.labels : {};
    const labels = /** @type {Record<string, unknown>} */ (raw);
    const fromLabel = labels['mvp.type'];
    const t = String(fromLabel != null ? fromLabel : d?.type || '')
      .trim();
    return t || 'userdocker';
  }

  async function refresh() {
    const loc = get(locale);
    try {
      const [c, u, s, h] = await Promise.all([
        api.components(),
        api.userDockerList(true),
        api.statsOverview().catch((e) => ({ _err: String(e) })),
        api.health().catch((e) => ({ _health_err: String(e) })),
      ]);
      components = c.components || [];
      userdockers = u.containers || [];
      if (h && h._health_err) {
        chatBlockedMessage = translate(loc, 'overview.chatHealthErr', { detail: h._health_err });
      } else if (h && h.chat_ready === false) {
        chatBlockedMessage =
          String(h.chat_error || '').trim() || translate(loc, 'overview.chatDepsNotReady');
      } else {
        chatBlockedMessage = '';
      }
      if (s && s.disabled) {
        statsDisabled = true;
        stats = null;
        statsError = '';
      } else if (s && s._err) {
        statsDisabled = false;
        statsError = s._err;
      } else if (s && !s._err && s.success === false) {
        statsDisabled = false;
        stats = null;
        statsError = s.error || translate(loc, 'overview.statsServiceErr');
      } else if (s && !s._err && s.success !== false) {
        statsDisabled = false;
        stats = s.stats || null;
        statsError = '';
      }
      error = '';
    } catch (e) {
      error = String(e);
    }
  }

  onMount(() => {
    refresh();
    timer = setInterval(refresh, 3000);
    tickTimer = setInterval(() => {
      tick += 1;
    }, 1000);
  });
  onDestroy(() => {
    clearInterval(timer);
    if (tickTimer) clearInterval(tickTimer);
  });

  $: systemComponents = components.filter((c) => !isUserDockerType(c?.type));
  $: udPolicy = parseUserDockerManagerMeta(components);
  $: messagesStat = stats
    ? {
        total: Number((stats.messages || {}).total || 0),
        delta: Number((stats.messages || {}).last_24h || 0),
      }
    : { total: 0, delta: 0 };
  $: toolCallsStat = stats
    ? {
        total: Number((stats.tool_calls || {}).total || 0),
        delta: Number((stats.tool_calls || {}).last_24h || 0),
      }
    : { total: 0, delta: 0 };
  $: tokensStat = stats
    ? {
        total: Number(((stats.tokens || {}).total || {}).total || 0),
        delta: Number(((stats.tokens || {}).total || {}).last_24h || 0),
      }
    : { total: 0, delta: 0 };

  /** @param {string} loc */
  function removalLine(d, loc) {
    tick;
    const r = tempRemovalCountdown(d, udPolicy.ttlSec);
    if (r.kind === 'persistent') return translate(loc, 'overview.removalPersistent');
    if (r.kind === 'temp') {
      return translate(loc, 'overview.removalEta', { duration: formatDurationSec(r.seconds) });
    }
    return translate(loc, 'common.emDash');
  }

  $: loc = $locale;
  $: policyLine =
    udPolicy.ttlSec != null && udPolicy.sweepSec != null
      ? translate(loc, 'overview.policyBoth', {
          ttl: formatDurationSec(udPolicy.ttlSec),
          sweep: formatDurationSec(udPolicy.sweepSec),
        })
      : udPolicy.ttlSec != null
        ? translate(loc, 'overview.policyTtl', { ttl: formatDurationSec(udPolicy.ttlSec) })
        : '';
</script>

<h1 class="font-semibold tracking-tight">{$_('overview.title')}</h1>

{#if chatBlockedMessage}
  <div role="alert" class="alert alert-warning mt-3 text-sm">
    {chatBlockedMessage}
  </div>
{/if}
{#if error}
  <div role="alert" class="alert alert-soft alert-error mt-3 text-sm">{error}</div>
{/if}

{#if statsDisabled}
  <div role="status" class="alert alert-soft mt-3 text-sm">
    {@html $_('overview.statsDisabled')}
  </div>
{:else if statsError}
  <div role="status" class="alert alert-soft alert-warning mt-3 text-sm">
    {$_('overview.statsErrPrefix')}{statsError}
  </div>
{/if}

{#if !statsDisabled}
  <div
    class="stats stats-vertical mb-6 w-full rounded-box border border-base-300 bg-base-200 shadow-sm lg:stats-horizontal"
  >
    <div class="stat place-items-center border-base-300 px-4 py-3 lg:border-e">
      <div class="stat-title text-base text-base-content/70">{$_('overview.statMessages')}</div>
      <div class="stat-value font-mono text-primary">{fmtCount(messagesStat.total)}</div>
      <div class="stat-desc text-success">{$_('overview.statDelta', { n: fmtCount(messagesStat.delta) })}</div>
    </div>
    <div class="stat place-items-center border-base-300 px-4 py-3 lg:border-e">
      <div class="stat-title text-base text-base-content/70">{$_('overview.statToolCalls')}</div>
      <div class="stat-value font-mono text-secondary">{fmtCount(toolCallsStat.total)}</div>
      <div class="stat-desc text-success">{$_('overview.statDelta', { n: fmtCount(toolCallsStat.delta) })}</div>
    </div>
    <div class="stat place-items-center px-4 py-3">
      <div class="stat-title text-base text-base-content/70">{$_('overview.statTokens')}</div>
      <div class="stat-value font-mono text-accent">{fmtCount(tokensStat.total)}</div>
      <div class="stat-desc text-success">{$_('overview.statDelta', { n: fmtCount(tokensStat.delta) })}</div>
    </div>
  </div>
{/if}

<h2 class="mt-2 text-base font-semibold text-base-content">{$_('overview.userdocker')}</h2>
{#if policyLine}
  <p class="mb-3 text-base text-base-content/70">{policyLine}</p>
{/if}
<div class="mb-8 grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
  {#each userdockers as d}
    {@const ns = normalizeStatus(d.status || d.state)}
    <div class="card bg-base-200 shadow-sm {cardStatusBorderClass(ns)}">
      <div class="card-body gap-3 p-4">
        <div class="flex min-w-0 items-start justify-between gap-2">
          <h3 class="card-title min-w-0 flex-1 text-lg font-semibold leading-snug">
            {d.name || $_('common.emDash')}
          </h3>
          <span class={statusBadgeClass(ns)}>{displayStatus(d.status || d.state)}</span>
        </div>
        <dl class="space-y-1.5 text-base">
          <div class="flex gap-2">
            <dt class="text-base-content/60 shrink-0">{$_('overview.rowType')}</dt>
            <dd class="min-w-0 text-right">
              <span
                class="inline-flex max-w-full items-center rounded-md px-2 py-0.5 font-mono text-xs font-normal"
                style={typeBadgeStyle(userDockerTypeLabel(d))}>{userDockerTypeLabel(d)}</span
              >
            </dd>
          </div>
          <div class="flex gap-2"><dt class="text-base-content/60 shrink-0">{$_('overview.rowImage')}</dt><dd class="min-w-0 break-all font-mono text-right">{d.image || $_('common.emDash')}</dd></div>
          <div class="flex gap-2"><dt class="text-base-content/60 shrink-0">{$_('overview.rowScope')}</dt><dd class="min-w-0 break-all text-right">{d.scope || $_('common.emDash')}</dd></div>
          <div class="flex gap-2"><dt class="text-base-content/60 shrink-0">{$_('overview.rowLastActive')}</dt><dd class="min-w-0 text-right">{fmtTs(d.last_active_at)}</dd></div>
          <div class="flex gap-2"><dt class="text-base-content/60 shrink-0">{$_('overview.rowIdleRemoval')}</dt><dd class="min-w-0 text-right text-warning">{removalLine(d, loc)}</dd></div>
          <div class="flex gap-2"><dt class="text-base-content/60 shrink-0">{$_('overview.rowState')}</dt><dd class="min-w-0 break-all text-right">{d.state || $_('common.emDash')}</dd></div>
          <div class="flex gap-2"><dt class="text-base-content/60 shrink-0">{$_('overview.rowRawStatus')}</dt><dd class="min-w-0 break-all text-right">{d.status || $_('common.emDash')}</dd></div>
          <div class="flex gap-2"><dt class="text-base-content/60 shrink-0">{$_('overview.rowContainerId')}</dt><dd class="font-mono text-right">{shortId(d.id)}</dd></div>
        </dl>
      </div>
    </div>
  {:else}
    <div class="alert alert-soft col-span-full text-sm">{$_('overview.emptyUserdocker')}</div>
  {/each}
</div>

<h2 class="text-base font-semibold text-base-content">{$_('overview.systemDocker')}</h2>
<div class="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
  {#each systemComponents as c}
    {@const ns = normalizeStatus(c.status)}
    <div class="card bg-base-200 shadow-sm {cardStatusBorderClass(ns)}">
      <div class="card-body gap-3 p-4">
        <div class="flex min-w-0 items-start justify-between gap-2">
          <h3 class="card-title min-w-0 flex-1 text-lg font-semibold leading-snug">
            {c.name || $_('common.emDash')}
          </h3>
          <span class={statusBadgeClass(ns)}>{c.status || $_('common.unknown')}</span>
        </div>
        <dl class="space-y-1.5 text-base">
          <div class="flex gap-2">
            <dt class="text-base-content/60 shrink-0">{$_('overview.rowType')}</dt>
            <dd class="min-w-0 text-right">
              <span
                class="inline-flex max-w-full items-center rounded-md px-2 py-0.5 font-mono text-xs font-normal"
                style={typeBadgeStyle(c.type || '')}>{c.type || $_('common.emDash')}</span
              >
            </dd>
          </div>
          <div class="flex gap-2"><dt class="text-base-content/60 shrink-0">{$_('overview.rowEndpoint')}</dt><dd class="min-w-0 break-all font-mono text-right">{c.endpoint || $_('common.emDash')}</dd></div>
          <div class="flex gap-2"><dt class="text-base-content/60 shrink-0">{$_('overview.rowFailures')}</dt><dd class="text-right">{Number.isFinite(c.failure_count) ? c.failure_count : $_('common.emDash')}</dd></div>
          <div class="flex gap-2"><dt class="text-base-content/60 shrink-0">{$_('overview.rowLastCheck')}</dt><dd class="text-right font-mono">{fmtTsSlash(c.last_checked_at)}</dd></div>
        </dl>
      </div>
    </div>
  {:else}
    <div class="alert alert-soft col-span-full text-sm">{$_('overview.emptySystem')}</div>
  {/each}
</div>

<style>
  :global(.alert :where(code)) {
    font-size: 0.8rem;
  }
</style>
