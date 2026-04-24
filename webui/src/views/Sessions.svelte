<script>
  import { onMount, onDestroy } from 'svelte';
  import { api } from '../lib/api.js';
  import { goto } from '../lib/route.js';

  let sessions = [];
  let error = '';
  let timer;
  let deletingId = '';

  async function refresh() {
    try {
      const r = await api.sessions();
      if (r && r.success === false) {
        throw new Error(r.error || 'sessions api returned success=false');
      }
      sessions = r.sessions || [];
      error = '';
    } catch (e) { error = String(e); }
  }

  async function removeSession(id, event) {
    event?.stopPropagation();
    if (!id) return;
    if (!window.confirm(`Delete session "${id}"? This cannot be undone.`)) return;
    deletingId = id;
    try {
      const r = await api.deleteSession(id);
      if (r && r.success === false) {
        throw new Error(r.error || 'delete session api returned success=false');
      }
      await refresh();
    } catch (e) {
      error = String(e);
    } finally {
      deletingId = '';
    }
  }

  onMount(() => { refresh(); timer = setInterval(refresh, 3000); });
  onDestroy(() => clearInterval(timer));
</script>

<h1>Sessions</h1>
{#if error}<div class="err">{error}</div>{/if}

<table>
  <thead>
    <tr><th>Session ID</th><th>Updated</th><th>Length</th><th>Last Message</th><th>Actions</th></tr>
  </thead>
  <tbody>
    {#each sessions as s}
      <tr on:click={() => goto('session', { id: s.id })} class="clickable">
        <td class="mono">{s.id}</td>
        <td>{s.updated_at ? new Date(s.updated_at).toLocaleString() : '—'}</td>
        <td>{s.length}</td>
        <td class="snippet">{s.last_snippet || ''}</td>
        <td class="actions">
          <button
            class="danger"
            disabled={deletingId === s.id}
            on:click={(event) => removeSession(s.id, event)}
          >
            {deletingId === s.id ? 'Deleting...' : 'Delete'}
          </button>
        </td>
      </tr>
    {:else}
      <tr><td colspan="5" class="empty">No sessions yet. Send a message to the bot to start one.</td></tr>
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
  .snippet { color: #9aa3bb; max-width: 500px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .clickable { cursor: pointer; }
  .clickable:hover { background: #1d2333; }
  .actions { width: 1%; white-space: nowrap; }
  .actions button { cursor: pointer; }
  .danger {
    background: #33141a;
    border: 1px solid #7f2936;
    color: #ffd8dd;
    border-radius: 6px;
    padding: 0.28rem 0.58rem;
  }
  .danger:disabled {
    opacity: 0.7;
    cursor: not-allowed;
  }
  .empty { text-align: center; color: #6c7389; padding: 1rem; }
  .err { background: #40161a; border: 1px solid #8a2b32; color: #f6c6cb; padding: 0.6rem 0.9rem; border-radius: 6px; margin-bottom: 1rem; }
</style>
