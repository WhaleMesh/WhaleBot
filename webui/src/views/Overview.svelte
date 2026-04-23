<script>
  import { onMount, onDestroy } from 'svelte';
  import { api } from '../lib/api.js';

  let components = [];
  let logs = [];
  let error = '';
  let timer;

  async function refresh() {
    try {
      const [c, l] = await Promise.all([api.components(), api.logs()]);
      components = c.components || [];
      logs = (l.logs || []).slice().reverse();
      error = '';
    } catch (e) {
      error = String(e);
    }
  }

  onMount(() => {
    refresh();
    timer = setInterval(refresh, 3000);
  });
  onDestroy(() => clearInterval(timer));

  $: total = components.length;
  $: healthy = components.filter(c => c.status === 'healthy').length;
  $: unhealthy = components.filter(c => c.status === 'unhealthy').length;
  $: removed = components.filter(c => c.status === 'removed').length;
  $: alertLogs = logs.filter((e) => e.level === 'error' || e.level === 'warn').slice(0, 10);
</script>

<h1>Overview</h1>
{#if error}<div class="err">{error}</div>{/if}

<div class="cards">
  <div class="card"><div class="k">Registered</div><div class="v">{total}</div></div>
  <div class="card"><div class="k">Healthy</div><div class="v ok">{healthy}</div></div>
  <div class="card"><div class="k">Unhealthy</div><div class="v warn">{unhealthy}</div></div>
  <div class="card"><div class="k">Removed</div><div class="v bad">{removed}</div></div>
</div>

<h2>Recent Alerts (warn/error)</h2>
<div class="logs">
  {#each alertLogs as e}
    <div class="log {e.level}">
      <span class="t">{new Date(e.time).toLocaleTimeString()}</span>
      <span class="lvl">{e.level}</span>
      <span class="msg">{e.message}</span>
    </div>
  {:else}
    <div class="empty">No recent alerts.</div>
  {/each}
</div>

<style>
  h1 { margin-top: 0; }
  .cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: 1rem; margin-bottom: 1.5rem; }
  .card { background: #151923; border: 1px solid #232838; border-radius: 8px; padding: 1rem; }
  .k { font-size: 0.8rem; color: #8b93a8; text-transform: uppercase; letter-spacing: 0.05em; }
  .v { font-size: 2rem; font-weight: 600; margin-top: 0.25rem; }
  .v.ok { color: #5ad39b; }
  .v.warn { color: #f5c469; }
  .v.bad { color: #f16a6a; }
  .logs { background: #0c0f15; border: 1px solid #232838; border-radius: 8px; padding: 0.5rem; font-family: ui-monospace, monospace; font-size: 0.82rem; max-height: 260px; overflow: auto; }
  .log { padding: 0.3rem 0.5rem; border-bottom: 1px dashed #1b2030; display: flex; gap: 0.5rem; flex-wrap: wrap; }
  .log:last-child { border-bottom: none; }
  .t { color: #6c7389; }
  .lvl { color: #8ea6ff; text-transform: uppercase; font-weight: 600; font-size: 0.75rem; padding-top: 0.1rem; }
  .log.error .lvl { color: #f16a6a; }
  .log.warn .lvl { color: #f5c469; }
  .msg { color: #dfe3ee; }
  .empty { padding: 1rem; color: #6c7389; }
  .err { background: #40161a; border: 1px solid #8a2b32; color: #f6c6cb; padding: 0.6rem 0.9rem; border-radius: 6px; margin-bottom: 1rem; }
</style>
