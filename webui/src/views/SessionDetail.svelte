<script>
  import { onMount, onDestroy } from 'svelte';
  import { api } from '../lib/api.js';
  import { goto } from '../lib/route.js';

  export let sessionId;

  let session = null;
  let error = '';
  let timer;

  async function refresh() {
    try {
      const r = await api.session(sessionId);
      session = r.session || { id: sessionId, messages: [] };
      error = '';
    } catch (e) { error = String(e); }
  }

  onMount(() => { refresh(); timer = setInterval(refresh, 3000); });
  onDestroy(() => clearInterval(timer));
</script>

<div class="top">
  <button on:click={() => goto('sessions')}>← Back</button>
  <h1>Session <span class="mono">{sessionId}</span></h1>
</div>

{#if error}<div class="err">{error}</div>{/if}

<div class="thread">
  {#if session}
    {#each session.messages || [] as m}
      <div class="msg {m.role}">
        <div class="role">{m.role}</div>
        <div class="content">{m.content}</div>
      </div>
    {:else}
      <div class="empty">No messages in this session.</div>
    {/each}
  {/if}
</div>

<style>
  .top { display: flex; align-items: center; gap: 0.75rem; }
  h1 { margin: 0; }
  h1 .mono { font-family: ui-monospace, monospace; font-size: 1rem; color: #8ea6ff; }
  button { background: #1c2130; color: #dfe3ee; border: 1px solid #2d3448; border-radius: 6px; padding: 0.35rem 0.7rem; cursor: pointer; }
  .thread { margin-top: 1rem; display: flex; flex-direction: column; gap: 0.75rem; }
  .msg { padding: 0.75rem 1rem; border-radius: 8px; border: 1px solid #232838; max-width: 80%; }
  .msg.user { background: #172232; align-self: flex-end; }
  .msg.assistant { background: #151923; align-self: flex-start; }
  .msg.system { background: #1b1d2a; align-self: center; font-style: italic; color: #9aa3bb; }
  .role { font-size: 0.72rem; color: #8ea6ff; text-transform: uppercase; letter-spacing: 0.05em; margin-bottom: 0.25rem; }
  .content { white-space: pre-wrap; font-size: 0.95rem; }
  .empty { color: #6c7389; padding: 1rem; text-align: center; }
  .err { background: #40161a; border: 1px solid #8a2b32; color: #f6c6cb; padding: 0.6rem 0.9rem; border-radius: 6px; }
</style>
