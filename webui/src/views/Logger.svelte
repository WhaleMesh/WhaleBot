<script>
  import { onMount, onDestroy } from 'svelte';
  import { api } from '../lib/api.js';

  let logs = [];
  let error = '';
  let timer;

  async function refresh() {
    try {
      const r = await api.logs();
      logs = (r.logs || []).slice().reverse();
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
</script>

<h1>Logger</h1>
{#if error}<div class="err">{error}</div>{/if}

<div class="logs">
  {#each logs as e}
    <div class="log {e.level}">
      <span class="t">{new Date(e.time).toLocaleString()}</span>
      <span class="lvl">{e.level}</span>
      <span class="msg">{e.message}</span>
      {#if e.fields}
        <span class="fields">
          {#each Object.entries(e.fields) as [k, v]}
            <span class="f">{k}={v}</span>
          {/each}
        </span>
      {/if}
    </div>
  {:else}
    <div class="empty">No log entries yet.</div>
  {/each}
</div>

<style>
  h1 { margin-top: 0; }
  .logs {
    background: #0c0f15;
    border: 1px solid #232838;
    border-radius: 8px;
    padding: 0.5rem;
    font-family: ui-monospace, monospace;
    font-size: 0.82rem;
    max-height: 75vh;
    overflow: auto;
  }
  .log {
    padding: 0.4rem 0.5rem;
    border-bottom: 1px dashed #1b2030;
    display: flex;
    gap: 0.5rem;
    flex-wrap: wrap;
  }
  .log:last-child { border-bottom: none; }
  .t { color: #6c7389; }
  .lvl {
    color: #8ea6ff;
    text-transform: uppercase;
    font-weight: 600;
    font-size: 0.75rem;
    padding-top: 0.1rem;
  }
  .log.error .lvl { color: #f16a6a; }
  .log.warn .lvl { color: #f5c469; }
  .msg { color: #dfe3ee; }
  .fields { display: inline-flex; flex-wrap: wrap; gap: 0.35rem; }
  .f {
    background: #1a2030;
    color: #a8b0c2;
    border-radius: 4px;
    padding: 0.05rem 0.35rem;
    font-size: 0.75rem;
  }
  .empty { padding: 1rem; color: #6c7389; }
  .err {
    background: #40161a;
    border: 1px solid #8a2b32;
    color: #f6c6cb;
    padding: 0.6rem 0.9rem;
    border-radius: 6px;
    margin-bottom: 1rem;
  }
</style>
