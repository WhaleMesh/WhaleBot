<script>
  import { onMount, onDestroy } from 'svelte';
  import { api } from '../lib/api.js';
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

  function idleRemovalCell(c) {
    tick;
    const d = containerByName(c.name);
    const r = tempRemovalCountdown(d, udPolicy.ttlSec);
    if (r.kind === 'persistent') return 'Persistent';
    if (r.kind === 'temp') return formatDurationSec(r.seconds);
    return '—';
  }

  function scopeCell(c) {
    const d = containerByName(c.name);
    return d?.scope || '—';
  }

  $: userDockerComponents = components.filter((c) => isUserDockerComponent(c));
  $: otherComponents = components.filter((c) => !isUserDockerComponent(c));
  $: udPolicy = parseUserDockerManagerMeta(components);
  $: policyLine =
    udPolicy.ttlSec != null && udPolicy.sweepSec != null
      ? `Framework policy: remove temporary (session_scoped) userdockers after ${formatDurationSec(udPolicy.ttlSec)} without activity · idle check every ${formatDurationSec(udPolicy.sweepSec)}`
      : udPolicy.ttlSec != null
        ? `Framework policy: temporary userdocker idle removal after ${formatDurationSec(udPolicy.ttlSec)}`
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

<h1>Components</h1>
{#if error}<div class="err">{error}</div>{/if}

{#if policyLine}
  <p class="policy-hint">{policyLine}</p>
{/if}

<h2>Userdocker Components</h2>
<table>
  <thead>
    <tr>
      <th>Name</th><th>Type</th><th>Scope</th><th>Idle removal</th><th>Endpoint</th>
      <th>Status</th><th>Version</th><th>Failures</th><th>Last Check</th>
    </tr>
  </thead>
  <tbody>
    {#each userDockerComponents as c}
      <tr>
        <td>{c.name}</td>
        <td><span class="type-badge" style={typeBadgeStyle(c.type)}>{c.type}</span></td>
        <td class="mono sm">{scopeCell(c)}</td>
        <td class="mono sm warn">{idleRemovalCell(c)}</td>
        <td class="mono">{c.endpoint}</td>
        <td><span class="chip {c.status}">{c.status}</span></td>
        <td>{c.version}</td>
        <td>{c.failure_count}</td>
        <td>{c.last_checked_at ? new Date(c.last_checked_at).toLocaleTimeString() : '—'}</td>
      </tr>
    {:else}
      <tr><td colspan="9" class="empty">No userdocker components registered yet.</td></tr>
    {/each}
  </tbody>
</table>

<h2>Other Components</h2>
<table>
  <thead>
    <tr>
      <th>Name</th><th>Type</th><th>Endpoint</th>
      <th>Status</th><th>Version</th><th>Failures</th><th>Last Check</th>
    </tr>
  </thead>
  <tbody>
    {#each otherComponents as c}
      <tr>
        <td>{c.name}</td>
        <td><span class="type-badge" style={typeBadgeStyle(c.type)}>{c.type}</span></td>
        <td class="mono">{c.endpoint}</td>
        <td><span class="chip {c.status}">{c.status}</span></td>
        <td>{c.version}</td>
        <td>{c.failure_count}</td>
        <td>{c.last_checked_at ? new Date(c.last_checked_at).toLocaleTimeString() : '—'}</td>
      </tr>
    {:else}
      <tr><td colspan="7" class="empty">No non-userdocker components registered yet.</td></tr>
    {/each}
  </tbody>
</table>

<style>
  h1 { margin-top: 0; margin-bottom: 0.6rem; }
  h2 {
    margin: 1rem 0 0.45rem;
    font-size: 0.92rem;
    color: #c7d0e6;
    font-weight: 600;
  }
  table { width: 100%; border-collapse: collapse; background: #151923; border: 1px solid #232838; border-radius: 8px; overflow: hidden; }
  th, td { padding: 0.6rem 0.75rem; text-align: left; font-size: 0.9rem; }
  thead th { background: #1b2030; color: #9aa3bb; font-weight: 500; font-size: 0.78rem; text-transform: uppercase; letter-spacing: 0.05em; }
  tbody tr:nth-child(even) { background: #181c27; }
  .mono { font-family: ui-monospace, monospace; font-size: 0.82rem; color: #b9c0d4; }
  .mono.sm { font-size: 0.78rem; color: #9aa3bb; }
  .type-badge {
    display: inline-block;
    border-radius: 4px;
    padding: 0.1rem 0.45rem;
    font-size: 0.78rem;
    font-weight: 500;
    background: var(--tb-bg);
    color: var(--tb-fg);
    border: 1px solid var(--tb-border);
  }
  .chip { border-radius: 999px; padding: 0.15rem 0.55rem; font-size: 0.75rem; font-weight: 600; text-transform: uppercase; letter-spacing: 0.05em; }
  .chip.healthy { background: rgba(90, 211, 155, 0.15); color: #5ad39b; }
  .chip.unhealthy { background: rgba(245, 196, 105, 0.15); color: #f5c469; }
  .chip.removed { background: rgba(241, 106, 106, 0.15); color: #f16a6a; }
  .empty { text-align: center; color: #6c7389; padding: 1rem; }
  .err { background: #40161a; border: 1px solid #8a2b32; color: #f6c6cb; padding: 0.6rem 0.9rem; border-radius: 6px; margin-bottom: 1rem; }
  .policy-hint {
    margin: 0 0 0.75rem;
    font-size: 0.82rem;
    color: #9aa3bb;
    line-height: 1.4;
  }
  .warn {
    color: #e8a854;
  }
</style>
