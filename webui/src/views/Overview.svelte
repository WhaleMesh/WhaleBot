<script>
  import { onMount, onDestroy } from 'svelte';
  import { api } from '../lib/api.js';

  let components = [];
  let userdockers = [];
  let error = '';
  let timer;

  function fmtTs(ts) {
    if (!ts) return '—';
    const d = new Date(ts);
    if (Number.isNaN(d.getTime())) return '—';
    return d.toLocaleString();
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

  async function refresh() {
    try {
      const [c, u] = await Promise.all([api.components(), api.userDockerList(true)]);
      components = c.components || [];
      userdockers = u.containers || [];
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

  $: systemComponents = components.filter((c) => !isUserDockerType(c?.type));
</script>

<h1>Overview</h1>
{#if error}<div class="err">{error}</div>{/if}

<h2>Userdocker</h2>
<div class="grid">
  {#each userdockers as d}
    <div class="card {normalizeStatus(d.status || d.state)}">
      <div class="head">
        <div class="title">{d.name || '—'}</div>
        <span class="chip {normalizeStatus(d.status || d.state)}">{displayStatus(d.status || d.state)}</span>
      </div>
      <div class="rows">
        <div class="row"><span class="k">Image</span><span class="v mono">{d.image || '—'}</span></div>
        <div class="row"><span class="k">State</span><span class="v">{d.state || '—'}</span></div>
        <div class="row"><span class="k">Raw Status</span><span class="v">{d.status || '—'}</span></div>
        <div class="row"><span class="k">Container ID</span><span class="v mono">{shortId(d.id)}</span></div>
      </div>
    </div>
  {:else}
    <div class="empty">No userdocker containers.</div>
  {/each}
</div>

<h2>System Docker</h2>
<div class="grid">
  {#each systemComponents as c}
    <div class="card {normalizeStatus(c.status)}">
      <div class="head">
        <div class="title">{c.name || '—'}</div>
        <span class="chip {normalizeStatus(c.status)}">{c.status || 'unknown'}</span>
      </div>
      <div class="rows">
        <div class="row"><span class="k">Type</span><span class="v">{c.type || '—'}</span></div>
        <div class="row"><span class="k">Endpoint</span><span class="v mono">{c.endpoint || '—'}</span></div>
        <div class="row"><span class="k">Failures</span><span class="v">{Number.isFinite(c.failure_count) ? c.failure_count : '—'}</span></div>
        <div class="row"><span class="k">Last Check</span><span class="v">{fmtTs(c.last_checked_at)}</span></div>
      </div>
    </div>
  {:else}
    <div class="empty">No system components.</div>
  {/each}
</div>

<style>
  h1 { margin-top: 0; }
  h2 {
    margin: 1rem 0 0.6rem;
    font-size: 0.95rem;
    color: #c7d0e6;
    font-weight: 600;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
    gap: 0.8rem;
    margin-bottom: 1.1rem;
  }
  .card {
    background: #151923;
    border: 1px solid #232838;
    border-radius: 10px;
    padding: 0.75rem 0.85rem;
  }
  .card.healthy {
    border-color: #2f6645;
    box-shadow: inset 0 0 0 1px rgba(133, 216, 167, 0.08);
  }
  .card.warn {
    border-color: #6b5422;
    box-shadow: inset 0 0 0 1px rgba(245, 196, 105, 0.08);
  }
  .card.bad {
    border-color: #78323a;
    box-shadow: inset 0 0 0 1px rgba(241, 106, 106, 0.08);
  }
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.6rem;
    margin-bottom: 0.55rem;
  }
  .title {
    font-size: 0.95rem;
    font-weight: 600;
    color: #e5e9f5;
    word-break: break-word;
  }
  .chip {
    border-radius: 999px;
    font-size: 0.72rem;
    letter-spacing: 0.03em;
    text-transform: uppercase;
    padding: 0.14rem 0.5rem;
    border: 1px solid #2d3448;
    color: #a8b2ca;
    white-space: nowrap;
  }
  .chip.healthy {
    color: #85d8a7;
    border-color: #2f6645;
    background: #163222;
  }
  .chip.warn {
    color: #f5c469;
    border-color: #6b5422;
    background: #2f2715;
  }
  .chip.bad {
    color: #f16a6a;
    border-color: #78323a;
    background: #341a1e;
  }
  .rows {
    display: flex;
    flex-direction: column;
    gap: 0.38rem;
  }
  .row {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 0.8rem;
  }
  .k {
    color: #8f98ae;
    font-size: 0.75rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    flex: 0 0 auto;
  }
  .v {
    color: #dfe3ee;
    font-size: 0.86rem;
    text-align: right;
    word-break: break-all;
  }
  .mono {
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
    font-size: 0.8rem;
  }
  .empty {
    background: #111522;
    border: 1px dashed #263049;
    border-radius: 8px;
    color: #8f98ae;
    padding: 0.8rem 0.9rem;
  }
  .err { background: #40161a; border: 1px solid #8a2b32; color: #f6c6cb; padding: 0.6rem 0.9rem; border-radius: 6px; margin-bottom: 1rem; }
</style>
