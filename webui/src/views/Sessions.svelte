<script>
  import { onMount, onDestroy } from 'svelte';
  import { api } from '../lib/api.js';
  import { goto } from '../lib/route.js';
  import { _, t, locale, translate } from '../lib/i18n.js';

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
    } catch (e) {
      error = String(e);
    }
  }

  async function removeSession(id, event) {
    event?.stopPropagation();
    if (!id) return;
    if (!window.confirm(t('sessions.confirmDelete', { id }))) return;
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

  onMount(() => {
    refresh();
    timer = setInterval(refresh, 3000);
  });
  onDestroy(() => clearInterval(timer));

  $: loc = $locale;

  /** @param {string} loc */
  function fmtRemaining(s, loc) {
    if (s == null) return translate(loc, 'common.emDash');
    if (s.expired) return translate(loc, 'sessions.expired');
    const sec = s.seconds_remaining;
    if (sec == null) return translate(loc, 'common.emDash');
    const h = Math.floor(sec / 3600);
    const m = Math.floor((sec % 3600) / 60);
    const rs = sec % 60;
    if (h > 0) return `${h}h${m}m`;
    if (m > 0) return `${m}m${rs}s`;
    return `${rs}s`;
  }
</script>

<h1 class="font-semibold tracking-tight">{$_('sessions.title')}</h1>
{#if error}
  <div role="alert" class="alert alert-soft alert-error mt-3 text-sm">{error}</div>
{/if}

<div class="mt-4 overflow-x-auto rounded-lg border border-base-300">
  <table class="table table-zebra table-list text-base">
    <thead>
      <tr>
        <th>{$_('sessions.thId')}</th>
        <th>{$_('sessions.thUpdated')}</th>
        <th>{$_('sessions.thIdleExpiry')}</th>
        <th>{$_('sessions.thLength')}</th>
        <th>{$_('sessions.thLastMsg')}</th>
        <th class="w-1 whitespace-nowrap">{$_('sessions.thActions')}</th>
      </tr>
    </thead>
    <tbody>
      {#each sessions as s}
        <tr
          class="cursor-pointer hover:bg-base-300/40"
          on:click={() => goto('session', { id: s.id })}
          on:keydown={(e) => e.key === 'Enter' && goto('session', { id: s.id })}
          role="button"
          tabindex="0"
        >
          <td class="max-w-[14rem] truncate font-mono text-xs">{s.id}</td>
          <td class="whitespace-nowrap text-xs">
            {s.updated_at ? new Date(s.updated_at).toLocaleString() : $_('common.emDash')}
          </td>
          <td class="font-mono text-xs">{fmtRemaining(s, loc)}</td>
          <td>{s.length}</td>
          <td class="max-w-md truncate text-sm text-base-content/70">{s.last_snippet || ''}</td>
          <td>
            <button
              type="button"
              class="btn btn-xs btn-outline btn-error"
              disabled={deletingId === s.id}
              on:click={(event) => removeSession(s.id, event)}
            >
              {deletingId === s.id ? $_('sessions.deleting') : $_('sessions.delete')}
            </button>
          </td>
        </tr>
      {:else}
        <tr>
          <td colspan="6" class="text-center text-base-content/60">{$_('sessions.empty')}</td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>
