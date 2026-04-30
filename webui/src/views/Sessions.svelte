<script>
  import { onMount, onDestroy } from 'svelte';
  import { api } from '../lib/api.js';
  import { goto } from '../lib/route.js';
  import { _, t, locale, translate } from '../lib/i18n.js';
  import { formatDateTime24 } from '../lib/datetime.js';

  let sessions = [];
  let error = '';
  /** @type {ReturnType<typeof setInterval> | undefined} */
  let timer;
  let deletingId = '';
  let initialLoad = true;

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
    } finally {
      initialLoad = false;
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
  onDestroy(() => {
    if (timer) clearInterval(timer);
  });

  $: loc = $locale;

  /** @param {string} id */
  function shortSessionId(id) {
    const s = String(id || '');
    if (s.length <= 12) return s;
    return `${s.slice(0, 4)}...${s.slice(-4)}`;
  }

  /** @param {string} loc */
  function fmtRemaining(s, loc) {
    if (s == null) return translate(loc, 'common.emDash');
    if (s.expired) return translate(loc, 'sessions.expired');
    const sec = s.seconds_remaining;
    if (sec == null) return translate(loc, 'common.emDash');
    if (sec > 0 && sec < 60) return translate(loc, 'sessions.idleExpiryLessThanOne');
    const n = Math.ceil(sec / 60);
    return translate(loc, 'sessions.idleExpiryMinutes', { n: String(n) });
  }
</script>

<h1 class="wb-page-title">{$_('sessions.title')}</h1>
{#if error}
  <div role="alert" class="alert alert-soft alert-error mt-3 text-base">{error}</div>
{/if}

<div class="min-w-0 w-full max-w-full overflow-x-auto rounded-lg border border-base-300">
  {#if initialLoad}
    <div class="p-4">
      {#each [1, 2, 3, 4, 5] as _}
        <div class="skeleton mb-3 h-10 w-full"></div>
      {/each}
    </div>
  {:else}
    <table class="table wb-table table-list text-base">
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
            class="cursor-pointer hover:bg-base-300/15"
            on:click={() => goto('session', { id: s.id })}
            on:keydown={(e) => e.key === 'Enter' && goto('session', { id: s.id })}
            role="button"
            tabindex="0"
          >
            <td class="w-0 max-w-[9rem] whitespace-nowrap">
              <div
                class="tooltip tooltip-top before:max-w-[min(100vw-2rem,36rem)] before:break-all before:text-left"
                data-tip={s.id}
              >
                <span class="wb-mono cursor-default text-sm" title={s.id}>{shortSessionId(s.id)}</span>
              </div>
            </td>
            <td class="whitespace-nowrap text-xs text-base-content/70">
              <span class="font-mono tabular-nums">
                {s.updated_at ? formatDateTime24(s.updated_at) : $_('common.emDash')}
              </span>
            </td>
            <td class="wb-mono text-sm">{fmtRemaining(s, loc)}</td>
            <td>{s.length}</td>
            <td class="max-w-md truncate text-base text-base-content/70">{s.last_snippet || ''}</td>
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
  {/if}
</div>
