<script>
  import { onMount, onDestroy } from 'svelte';
  import { api } from '../lib/api.js';

  let components = [];
  let error = '';
  let timer;

  async function refresh() {
    try {
      const c = await api.components();
      components = c.components || [];
      error = '';
    } catch (e) { error = String(e); }
  }

  onMount(() => { refresh(); timer = setInterval(refresh, 3000); });
  onDestroy(() => clearInterval(timer));
</script>

<h1>Components</h1>
{#if error}<div class="err">{error}</div>{/if}

<table>
  <thead>
    <tr>
      <th>Name</th><th>Type</th><th>Endpoint</th>
      <th>Status</th><th>Version</th><th>Failures</th><th>Last Check</th>
    </tr>
  </thead>
  <tbody>
    {#each components as c}
      <tr>
        <td>{c.name}</td>
        <td><span class="type">{c.type}</span></td>
        <td class="mono">{c.endpoint}</td>
        <td><span class="chip {c.status}">{c.status}</span></td>
        <td>{c.version}</td>
        <td>{c.failure_count}</td>
        <td>{c.last_checked_at ? new Date(c.last_checked_at).toLocaleTimeString() : '—'}</td>
      </tr>
    {:else}
      <tr><td colspan="7" class="empty">No components registered yet.</td></tr>
    {/each}
  </tbody>
</table>

<style>
  h1 { margin-top: 0; }
  table { width: 100%; border-collapse: collapse; background: #151923; border: 1px solid #232838; border-radius: 8px; overflow: hidden; }
  th, td { padding: 0.6rem 0.75rem; text-align: left; font-size: 0.9rem; }
  thead th { background: #1b2030; color: #9aa3bb; font-weight: 500; font-size: 0.78rem; text-transform: uppercase; letter-spacing: 0.05em; }
  tbody tr:nth-child(even) { background: #181c27; }
  .mono { font-family: ui-monospace, monospace; font-size: 0.82rem; color: #b9c0d4; }
  .type { background: #1d2638; color: #8ea6ff; border-radius: 4px; padding: 0.1rem 0.45rem; font-size: 0.78rem; }
  .chip { border-radius: 999px; padding: 0.15rem 0.55rem; font-size: 0.75rem; font-weight: 600; text-transform: uppercase; letter-spacing: 0.05em; }
  .chip.healthy { background: rgba(90, 211, 155, 0.15); color: #5ad39b; }
  .chip.unhealthy { background: rgba(245, 196, 105, 0.15); color: #f5c469; }
  .chip.removed { background: rgba(241, 106, 106, 0.15); color: #f16a6a; }
  .empty { text-align: center; color: #6c7389; padding: 1rem; }
  .err { background: #40161a; border: 1px solid #8a2b32; color: #f6c6cb; padding: 0.6rem 0.9rem; border-radius: 6px; margin-bottom: 1rem; }
</style>
