<script>
  import { onMount, onDestroy } from 'svelte';
  import { api } from '../lib/api.js';
  import { _, locale, translate } from '../lib/i18n.js';
  import { formatDateTime24 } from '../lib/datetime.js';
  import {
    typeBadgeStyle,
    parseUserDockerManagerMeta,
    tempRemovalCountdown,
  } from '../lib/userdockerPolicy.js';

  let components = [];
  let containers = [];
  let error = '';
  /** @type {ReturnType<typeof setInterval> | undefined} */
  let timer;
  let tick = 0;
  /** @type {ReturnType<typeof setInterval> | undefined} */
  let tickTimer;
  let initialLoad = true;

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
    if (r.kind === 'temp') {
      const n = Math.max(1, Math.ceil(r.seconds / 60));
      return translate(loc, 'common.minutesN', { n: String(n) });
    }
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
          ttl: String(Math.ceil(udPolicy.ttlSec / 60)),
          sweep: String(Math.ceil(udPolicy.sweepSec / 60)),
        })
      : udPolicy.ttlSec != null
        ? translate(loc, 'components.policyTtl', { ttl: String(Math.ceil(udPolicy.ttlSec / 60)) })
        : '';

  async function refresh() {
    try {
      const [c, u] = await Promise.all([api.components(), api.userDockerList(true)]);
      components = c.components || [];
      containers = u.containers || [];
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
</script>

<h1 class="wb-page-title">{$_('components.title')}</h1>
{#if error}
  <div role="alert" class="alert alert-soft alert-error mt-3 text-base">{error}</div>
{/if}

{#if policyLine}
  <p class="mt-3 text-base text-base-content/70">{policyLine}</p>
{/if}

<h2 class="wb-section-title">{$_('components.udHeading')}</h2>
<div class="overflow-x-auto rounded-lg border border-base-300">
  {#if initialLoad}
    <div class="p-4">
      {#each [1, 2, 3, 4] as _}
        <div class="skeleton mb-3 h-10 w-full"></div>
      {/each}
    </div>
  {:else}
    <table class="table wb-table table-list text-base">
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
          <tr class="hover:bg-base-300/15">
            <td class="font-medium">{c.name}</td>
            <td>
              <span
                class="inline-flex max-w-full shrink-0 items-center rounded-md px-2 py-0.5 font-mono text-xs font-normal"
                style={typeBadgeStyle(c.type)}>{c.type}</span
              >
            </td>
            <td class="wb-mono text-sm text-base-content/80">{scopeCell(c, loc)}</td>
            <td class="wb-mono text-sm text-warning">{idleRemovalCell(c, loc)}</td>
            <td class="wb-mono max-w-xs break-all text-sm">{c.endpoint}</td>
            <td><span class={rowStatusBadgeClass(c.status)}>{c.status}</span></td>
            <td>{c.version}</td>
            <td>{c.failure_count}</td>
            <td class="wb-mono whitespace-nowrap text-sm">
              {c.last_checked_at ? formatDateTime24(c.last_checked_at) : $_('common.emDash')}
            </td>
          </tr>
        {:else}
          <tr>
            <td colspan="9" class="text-center text-base-content/60">{$_('components.emptyUd')}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>

<h2 class="wb-section-title">{$_('components.otherHeading')}</h2>
<div class="overflow-x-auto rounded-lg border border-base-300">
  {#if initialLoad}
    <div class="p-4">
      {#each [1, 2, 3] as _}
        <div class="skeleton mb-3 h-10 w-full"></div>
      {/each}
    </div>
  {:else}
    <table class="table wb-table table-list text-base">
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
          <tr class="hover:bg-base-300/15">
            <td class="font-medium">{c.name}</td>
            <td>
              <span
                class="inline-flex max-w-full shrink-0 items-center rounded-md px-2 py-0.5 font-mono text-xs font-normal"
                style={typeBadgeStyle(c.type)}>{c.type}</span
              >
            </td>
            <td class="wb-mono max-w-md break-all text-sm">{c.endpoint}</td>
            <td><span class={rowStatusBadgeClass(c.status)}>{c.status}</span></td>
            <td>{c.version}</td>
            <td>{c.failure_count}</td>
            <td class="wb-mono whitespace-nowrap text-sm">
              {c.last_checked_at ? formatDateTime24(c.last_checked_at) : $_('common.emDash')}
            </td>
          </tr>
        {:else}
          <tr>
            <td colspan="7" class="text-center text-base-content/60">{$_('components.emptyOther')}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>
