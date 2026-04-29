<script>
  import { onMount, onDestroy } from 'svelte';
  import { api } from '../lib/api.js';
  import { _, locale, translate } from '../lib/i18n.js';
  import {
    typeBadgeStyle,
    parseUserDockerManagerMeta,
    tempRemovalCountdown,
    formatDurationSec,
  } from '../lib/userdockerPolicy.js';

  let components = [];
  let containers = [];
  let error = '';
  let timer;
  let tick = 0;
  let tickTimer;

  function isUserDockerComponent(c) {
    const type = String(c?.type || '').toLowerCase();
    return type === 'userdocker';
  }

  function containerByName(name) {
    return containers.find((d) => d.name === name) || null;
  }

  /** @param {string | undefined} status */
  function rowStatusBadgeClass(status) {
    const s = String(status || '').toLowerCase();
    if (!s.trim()) return 'badge badge-ghost badge-sm whitespace-nowrap';
    if (s.includes('healthy')) return 'badge badge-success badge-sm whitespace-nowrap';
    if (s.includes('unhealthy') || s.includes('error')) return 'badge badge-error badge-sm whitespace-nowrap';
    if (s.includes('removed') || s.includes('stopped') || s.includes('exited'))
      return 'badge badge-neutral badge-sm whitespace-nowrap';
    if (s.includes('warn') || s.includes('restarting')) return 'badge badge-warning badge-sm whitespace-nowrap';
    return 'badge badge-ghost badge-sm max-w-[12rem] truncate';
  }

  /** @param {string} loc */
  function idleRemovalCell(c, loc) {
    tick;
    const d = containerByName(c.name);
    const r = tempRemovalCountdown(d, udPolicy.ttlSec);
    if (r.kind === 'persistent') return translate(loc, 'components.idlePersistent');
    if (r.kind === 'temp') return formatDurationSec(r.seconds);
    return translate(loc, 'common.emDash');
  }

  /** @param {string} loc */
  function scopeCell(c, loc) {
    const d = containerByName(c.name);
    return d?.scope || translate(loc, 'common.emDash');
  }

  $: userDockerComponents = components.filter((c) => isUserDockerComponent(c));
  $: otherComponents = components.filter((c) => !isUserDockerComponent(c));
  $: udPolicy = parseUserDockerManagerMeta(components);
  $: loc = $locale;
  $: policyLine =
    udPolicy.ttlSec != null && udPolicy.sweepSec != null
      ? translate(loc, 'components.policyBoth', {
          ttl: formatDurationSec(udPolicy.ttlSec),
          sweep: formatDurationSec(udPolicy.sweepSec),
        })
      : udPolicy.ttlSec != null
        ? translate(loc, 'components.policyTtl', { ttl: formatDurationSec(udPolicy.ttlSec) })
        : '';

  async function refresh() {
    try {
      const [c, u] = await Promise.all([api.components(), api.userDockerList(true)]);
      components = c.components || [];
      containers = u.containers || [];
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
</script>

<h1 class="font-semibold tracking-tight">{$_('components.title')}</h1>
{#if error}
  <div role="alert" class="alert alert-soft alert-error mt-3 text-sm">{error}</div>
{/if}

{#if policyLine}
  <p class="mt-3 text-sm text-base-content/70">{policyLine}</p>
{/if}

<h2 class="mt-4 text-base font-semibold text-base-content">{$_('components.udHeading')}</h2>
<div class="mt-2 overflow-x-auto rounded-lg border border-base-300">
  <table class="table table-zebra table-list text-base">
    <thead>
      <tr>
        <th>{$_('components.thName')}</th>
        <th>{$_('components.thType')}</th>
        <th>{$_('components.thScope')}</th>
        <th>{$_('components.thIdleRemoval')}</th>
        <th>{$_('components.thEndpoint')}</th>
        <th>{$_('components.thStatus')}</th>
        <th>{$_('components.thVersion')}</th>
        <th>{$_('components.thFailures')}</th>
        <th>{$_('components.thLastCheck')}</th>
      </tr>
    </thead>
    <tbody>
      {#each userDockerComponents as c}
        <tr>
          <td class="font-medium">{c.name}</td>
          <td>
            <span
              class="inline-flex max-w-full shrink-0 items-center rounded-md px-2 py-0.5 font-mono text-xs font-normal"
              style={typeBadgeStyle(c.type)}>{c.type}</span
            >
          </td>
          <td class="font-mono text-xs text-base-content/80">{scopeCell(c, loc)}</td>
          <td class="font-mono text-xs text-warning">{idleRemovalCell(c, loc)}</td>
          <td class="max-w-xs break-all font-mono text-xs">{c.endpoint}</td>
          <td><span class={rowStatusBadgeClass(c.status)}>{c.status}</span></td>
          <td>{c.version}</td>
          <td>{c.failure_count}</td>
          <td class="whitespace-nowrap text-xs">
            {c.last_checked_at ? new Date(c.last_checked_at).toLocaleTimeString() : $_('common.emDash')}
          </td>
        </tr>
      {:else}
        <tr>
          <td colspan="9" class="text-center text-base-content/60">{$_('components.emptyUd')}</td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>

<h2 class="mt-6 text-base font-semibold text-base-content">{$_('components.otherHeading')}</h2>
<div class="mt-2 overflow-x-auto rounded-lg border border-base-300">
  <table class="table table-zebra table-list text-base">
    <thead>
      <tr>
        <th>{$_('components.thName')}</th>
        <th>{$_('components.thType')}</th>
        <th>{$_('components.thEndpoint')}</th>
        <th>{$_('components.thStatus')}</th>
        <th>{$_('components.thVersion')}</th>
        <th>{$_('components.thFailures')}</th>
        <th>{$_('components.thLastCheck')}</th>
      </tr>
    </thead>
    <tbody>
      {#each otherComponents as c}
        <tr>
          <td class="font-medium">{c.name}</td>
          <td>
            <span
              class="inline-flex max-w-full shrink-0 items-center rounded-md px-2 py-0.5 font-mono text-xs font-normal"
              style={typeBadgeStyle(c.type)}>{c.type}</span
            >
          </td>
          <td class="max-w-md break-all font-mono text-xs">{c.endpoint}</td>
          <td><span class={rowStatusBadgeClass(c.status)}>{c.status}</span></td>
          <td>{c.version}</td>
          <td>{c.failure_count}</td>
          <td class="whitespace-nowrap text-xs">
            {c.last_checked_at ? new Date(c.last_checked_at).toLocaleTimeString() : $_('common.emDash')}
          </td>
        </tr>
      {:else}
        <tr>
          <td colspan="7" class="text-center text-base-content/60">{$_('components.emptyOther')}</td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>
