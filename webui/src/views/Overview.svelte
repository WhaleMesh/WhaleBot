<script>
  import { onMount, onDestroy } from 'svelte';
  import { get } from 'svelte/store';
  import { api } from '../lib/api.js';
  import { _, locale, translate } from '../lib/i18n.js';
  import { formatDateTime24 } from '../lib/datetime.js';
  import {
    parseUserDockerManagerMeta,
    tempRemovalCountdown,
    typeBadgeStyle,
  } from '../lib/userdockerPolicy.js';
  import { overviewToneFromOperational } from '../lib/componentDisplay.js';

  let components = [];
  let userdockers = [];
  let stats = null;
  let statsError = '';
  let statsDisabled = false;
  let error = '';
  let chatBlockedMessage = '';
  /** @type {ReturnType<typeof setInterval> | undefined} */
  let timer;
  let tick = 0;
  /** @type {ReturnType<typeof setInterval> | undefined} */
  let tickTimer;
  let initialLoad = true;

  function fmtCount(n) {
    const v = Number(n);
    if (!Number.isFinite(v) || v < 0) return '0';
    if (v >= 1_000_000) return (v / 1_000_000).toFixed(1) + 'M';
    if (v >= 10_000) return (v / 1_000).toFixed(1) + 'k';
    return String(Math.trunc(v));
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

  /** @param {Record<string, unknown>} c @param {string} loc */
  function systemComponentStatusLine(c, loc) {
    const op = String(c?.operational_state ?? '').trim();
    if (op) return translate(loc, `components.operationalState.${op}`);
    return String(c?.status || '') || translate(loc, 'common.unknown');
  }

  function isUserDockerType(v) {
    return String(v || '').toLowerCase().trim() === 'userdocker';
  }

  /** @param {'healthy' | 'warn' | 'bad' | 'unknown'} n */
  function statusTextClass(n) {
    if (n === 'healthy') return 'font-medium text-success max-w-[min(100%,14rem)] truncate';
    if (n === 'warn') return 'font-medium text-warning max-w-[min(100%,14rem)] truncate';
    if (n === 'bad') return 'font-medium text-error max-w-[min(100%,14rem)] truncate';
    return 'text-base-content/70 max-w-[min(100%,14rem)] truncate';
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
    } finally {
      initialLoad = false;
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
    if (timer) clearInterval(timer);
    if (tickTimer != null) clearInterval(tickTimer);
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
      const n = Math.max(1, Math.ceil(r.seconds / 60));
      return translate(loc, 'overview.removalEtaMinutes', { n: String(n) });
    }
    return translate(loc, 'common.emDash');
  }

  $: loc = $locale;
  $: policyLine =
    udPolicy.ttlSec != null && udPolicy.sweepSec != null
      ? translate(loc, 'overview.policyBoth', {
          ttl: String(Math.ceil(udPolicy.ttlSec / 60)),
          sweep: String(Math.ceil(udPolicy.sweepSec / 60)),
        })
      : udPolicy.ttlSec != null
        ? translate(loc, 'overview.policyTtl', { ttl: String(Math.ceil(udPolicy.ttlSec / 60)) })
        : '';
</script>

<h1 class="wb-page-title">{$_('overview.title')}</h1>

{#if chatBlockedMessage}
  <div role="alert" class="alert alert-warning mt-3 text-base">
    {chatBlockedMessage}
  </div>
{/if}
{#if error}
  <div role="alert" class="alert alert-soft alert-error mt-3 text-base">{error}</div>
{/if}

{#if statsDisabled}
  <div role="status" class="alert alert-soft mt-3 text-base">
    {@html $_('overview.statsDisabled')}
  </div>
{:else if statsError}
  <div role="status" class="alert alert-soft alert-warning mt-3 text-base">
    {$_('overview.statsErrPrefix')}{statsError}
  </div>
{/if}

{#if !statsDisabled}
  {#if initialLoad}
    <div
      class="stats stats-vertical mb-6 w-full shadow-sm lg:stats-horizontal rounded-lg border border-base-300 bg-base-200"
    >
      {#each [1, 2, 3] as _}
        <div class="stat place-items-start py-4">
          <div class="skeleton h-3 w-28"></div>
          <div class="skeleton mt-2 h-9 w-24"></div>
          <div class="skeleton mt-2 h-3 w-32"></div>
        </div>
      {/each}
    </div>
  {:else}
    <div
      class="stats stats-vertical mb-6 w-full shadow-sm lg:stats-horizontal rounded-lg border border-base-300 bg-base-200"
    >
      <div class="stat place-items-start py-4">
        <div class="stat-title text-base text-base-content/70">{$_('overview.statMessages')}</div>
        <div class="stat-value font-mono text-primary">{fmtCount(messagesStat.total)}</div>
        <div class="stat-desc text-success">{$_('overview.statDelta', { n: fmtCount(messagesStat.delta) })}</div>
      </div>
      <div class="stat place-items-start py-4">
        <div class="stat-title text-base text-base-content/70">{$_('overview.statToolCalls')}</div>
        <div class="stat-value font-mono text-secondary">{fmtCount(toolCallsStat.total)}</div>
        <div class="stat-desc text-success">{$_('overview.statDelta', { n: fmtCount(toolCallsStat.delta) })}</div>
      </div>
      <div class="stat place-items-start py-4">
        <div class="stat-title text-base text-base-content/70">{$_('overview.statTokens')}</div>
        <div class="stat-value font-mono text-accent">{fmtCount(tokensStat.total)}</div>
        <div class="stat-desc text-success">{$_('overview.statDelta', { n: fmtCount(tokensStat.delta) })}</div>
      </div>
    </div>
  {/if}
{/if}

<h2 class="wb-section-title">{$_('overview.userdocker')}</h2>
{#if policyLine}
  <p class="mb-3 text-base text-base-content/70">{policyLine}</p>
{/if}
<div class="mb-8 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
  {#if initialLoad}
    {#each [1, 2, 3] as _}
      <div class="wb-surface flex flex-col gap-3">
        <div class="flex min-w-0 items-center justify-between gap-2">
          <div class="skeleton h-7 min-w-0 flex-1 max-w-[14rem]"></div>
        </div>
        <div class="skeleton h-4 w-28"></div>
        <div class="grid gap-2 [grid-template-columns:max-content_1fr]">
          {#each [1, 2, 3, 4, 5] as __}
            <div class="skeleton h-4 w-16"></div>
            <div class="skeleton h-4 w-full"></div>
          {/each}
        </div>
      </div>
    {/each}
  {:else}
    {#each userdockers as d}
      {@const ns = normalizeStatus(d.status || d.state)}
      <div class="wb-surface flex flex-col gap-3">
        <div class="flex min-w-0 items-center justify-between gap-2">
          <h3 class="min-w-0 flex-1 text-xl font-bold leading-snug text-base-content">
            {d.name || $_('common.emDash')}
          </h3>
        </div>
        <p class="text-sm text-neutral-content/50 wb-mono">{shortId(d.id)}</p>
        <dl class="grid gap-x-4 gap-y-2 text-base [grid-template-columns:max-content_minmax(0,1fr)]">
          <dt class="text-base-content/60">{$_('overview.rowStatus')}</dt>
          <dd class="min-w-0 flex justify-end">
            <span class={statusTextClass(ns)}>{displayStatus(d.status || d.state)}</span>
          </dd>
          <dt class="text-base-content/60">{$_('overview.rowImage')}</dt>
          <dd class="min-w-0 text-right">
            <span class="wb-mono line-clamp-2 break-all text-sm" title={d.image || ''}>{d.image || $_('common.emDash')}</span>
          </dd>
          <dt class="text-base-content/60">{$_('overview.rowScope')}</dt>
          <dd class="min-w-0 text-right">
            <span class="line-clamp-2 break-all" title={d.scope || ''}>{d.scope || $_('common.emDash')}</span>
          </dd>
          <dt class="text-base-content/60">{$_('overview.rowLastActive')}</dt>
          <dd class="wb-mono min-w-0 text-right text-sm">{formatDateTime24(d.last_active_at)}</dd>
          <dt class="text-base-content/60">{$_('overview.rowIdleRemoval')}</dt>
          <dd class="min-w-0 text-right text-warning">{removalLine(d, loc)}</dd>
          <dt class="text-base-content/60">{$_('overview.rowState')}</dt>
          <dd class="min-w-0 text-right">
            <span class="line-clamp-2 break-all" title={d.state || ''}>{d.state || $_('common.emDash')}</span>
          </dd>
          <dt class="text-base-content/60">{$_('overview.rowRawStatus')}</dt>
          <dd class="min-w-0 text-right">
            <span class="line-clamp-2 break-all" title={d.status || ''}>{d.status || $_('common.emDash')}</span>
          </dd>
        </dl>
      </div>
    {:else}
      <div class="alert alert-soft col-span-full text-base">{$_('overview.emptyUserdocker')}</div>
    {/each}
  {/if}
</div>

<h2 class="wb-section-title">{$_('overview.systemDocker')}</h2>
<div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
  {#if initialLoad}
    {#each [1, 2] as _}
      <div class="wb-surface flex flex-col gap-3">
        <div class="flex min-w-0 items-center justify-between gap-2">
          <div class="skeleton h-7 min-w-0 flex-1 max-w-[14rem]"></div>
          <div class="skeleton h-7 w-14 shrink-0 rounded-md"></div>
        </div>
        <div class="grid gap-2 [grid-template-columns:max-content_1fr]">
          {#each [1, 2, 3, 4] as __}
            <div class="skeleton h-4 w-20"></div>
            <div class="skeleton h-4 w-full"></div>
          {/each}
        </div>
      </div>
    {/each}
  {:else}
    {#each systemComponents as c}
      {@const opTone = overviewToneFromOperational(c)}
      {@const ns = opTone != null ? opTone : normalizeStatus(c.status)}
      <div class="wb-surface flex flex-col gap-3">
        <div class="flex min-w-0 items-center justify-between gap-2">
          <h3 class="min-w-0 flex-1 text-xl font-bold leading-snug text-base-content">
            {c.name || $_('common.emDash')}
          </h3>
          <span
            class="inline-flex max-w-[min(100%,12rem)] shrink-0 items-center truncate rounded-md px-2 py-0.5 font-mono text-xs font-normal"
            style={typeBadgeStyle(c.type || '')}>{c.type || $_('common.emDash')}</span
          >
        </div>
        <dl class="grid gap-x-4 gap-y-2 text-base [grid-template-columns:max-content_minmax(0,1fr)]">
          <dt class="text-base-content/60">{$_('overview.rowStatus')}</dt>
          <dd class="min-w-0 flex justify-end">
            <span
              class={statusTextClass(ns)}
              title={translate(loc, 'components.registryStatusHint', { status: String(c?.status ?? '') })}
              >{systemComponentStatusLine(c, loc)}</span
            >
          </dd>
          <dt class="text-base-content/60">{$_('overview.rowEndpoint')}</dt>
          <dd class="min-w-0 text-right">
            {#if c.endpoint}
              <div
                class="tooltip tooltip-top inline-block max-w-full text-right before:max-w-[min(100vw-2rem,42rem)] before:break-all before:text-left"
                data-tip={c.endpoint}
              >
                <span class="wb-mono block text-sm leading-snug line-clamp-2 break-all" title={c.endpoint}>{c.endpoint}</span>
              </div>
            {:else}
              <span class="text-sm">{$_('common.emDash')}</span>
            {/if}
          </dd>
          <dt class="text-base-content/60">{$_('overview.rowFailures')}</dt>
          <dd class="text-right">{Number.isFinite(c.failure_count) ? c.failure_count : $_('common.emDash')}</dd>
          <dt class="text-base-content/60">{$_('overview.rowLastCheck')}</dt>
          <dd class="wb-mono text-right text-sm">{formatDateTime24(c.last_checked_at)}</dd>
        </dl>
      </div>
    {:else}
      <div class="alert alert-soft col-span-full text-base">{$_('overview.emptySystem')}</div>
    {/each}
  {/if}
</div>

<style>
  :global(.alert :where(code)) {
    font-size: 0.8rem;
  }
</style>
