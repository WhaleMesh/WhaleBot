<script>
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '../lib/route.js';
  import { api } from '../lib/api.js';
  import { _, t } from '../lib/i18n.js';

  /** @type {string} */
  export let adapterName = '';

  let components = [];
  let listError = '';
  let timer;

  let cfgError = '';
  let saveMsg = '';
  let loaded = false;
  let cfgLoading = false;

  let hasBotToken = false;
  let botTokenHint = '';
  let botTokenInput = '';
  let whitelistText = '';

  function isAdapter(c) {
    return String(c?.type || '').toLowerCase() === 'adapter';
  }

  $: adapterComponents = components.filter(isAdapter);
  $: detail =
    adapterName && adapterComponents.length
      ? adapterComponents.find((c) => c.name === adapterName) || null
      : null;

  /** @param {string | undefined} status */
  function statusBadgeClass(status) {
    const s = String(status || '').toLowerCase();
    if (!s.trim()) return 'badge badge-ghost';
    if (s.includes('healthy') || s === 'up' || s.startsWith('up '))
      return 'badge badge-success uppercase';
    if (s.includes('unhealthy') || s.includes('error') || s.includes('removed') || s.includes('down'))
      return 'badge badge-error';
    if (s.includes('warn') || s.includes('degraded') || s.includes('restarting'))
      return 'badge badge-warning';
    return 'badge badge-ghost';
  }

  async function refreshList() {
    try {
      const c = await api.components();
      components = c.components || [];
      listError = '';
      loaded = true;
    } catch (e) {
      listError = String(e);
      loaded = true;
    }
  }

  function idsToText(ids) {
    if (!ids || !ids.length) return '';
    return ids.map((n) => String(n)).join('\n');
  }

  /** @param {string} text @returns {number[]} */
  function parseWhitelist(text) {
    const raw = String(text || '')
      .split(/[\s,;]+/)
      .map((s) => s.trim())
      .filter(Boolean);
    const out = [];
    for (const p of raw) {
      if (!/^-?\d+$/.test(p)) {
        throw new Error(t('adapter.badUserId', { value: p }));
      }
      const n = Number(p);
      if (!Number.isSafeInteger(n)) {
        throw new Error(t('adapter.badUserId', { value: p }));
      }
      out.push(n);
    }
    return out;
  }

  async function loadConfig() {
    if (!adapterName) return;
    cfgLoading = true;
    cfgError = '';
    try {
      const data = await api.adapterConfigGet(adapterName);
      const cfg = data.config || {};
      hasBotToken = !!cfg.has_bot_token;
      botTokenHint = cfg.bot_token_hint || '';
      const ids = Array.isArray(cfg.allowed_user_ids) ? cfg.allowed_user_ids : [];
      whitelistText = idsToText(ids);
      botTokenInput = '';
    } catch (e) {
      cfgError = String(e);
      whitelistText = '';
    } finally {
      cfgLoading = false;
    }
  }

  async function saveConfig() {
    saveMsg = '';
    cfgError = '';
    if (!hasBotToken && !(botTokenInput || '').trim()) {
      cfgError = t('adapter.tokenRequiredSave');
      return;
    }
    let ids;
    try {
      ids = parseWhitelist(whitelistText);
    } catch (e) {
      cfgError = String(e);
      return;
    }
    try {
      await api.adapterConfigPut(adapterName, {
        bot_token: (botTokenInput || '').trim(),
        allowed_user_ids: ids,
      });
      saveMsg = t('adapter.saved');
      await loadConfig();
      await refreshList();
    } catch (e) {
      cfgError = String(e);
    }
  }

  onMount(() => {
    refreshList();
    timer = setInterval(refreshList, 5000);
  });
  onDestroy(() => {
    if (timer) clearInterval(timer);
  });

  $: if (adapterName) loadConfig();
</script>

{#if adapterName}
  <div class="mb-2">
    <button type="button" class="btn btn-sm btn-ghost gap-1" on:click={() => goto('adapter')}>
      {$_('adapter.backAll')}
    </button>
  </div>
  <h1 class="wb-page-title">{$_('adapter.detailTitle', { name: adapterName })}</h1>
  {#if listError}
    <div role="alert" class="alert alert-soft alert-error mt-3 text-base">{listError}</div>
  {/if}
  {#if !loaded}
    <div class="wb-surface mt-3 !py-5">
      <div class="skeleton mb-4 h-5 w-48"></div>
      <div class="skeleton mb-2 h-4 w-full max-w-xl"></div>
    </div>
  {:else if !detail}
    <p class="mt-2 text-base-content/70">{$_('adapter.notFound', { name: adapterName })}</p>
  {:else}
    <div class="wb-surface mt-3">
      <div class="flex flex-wrap items-center gap-x-4 gap-y-1">
        <span class="text-base text-base-content/60">{$_('adapter.registryStatus')}</span>
        <span class={statusBadgeClass(detail.status)}>{detail.status || $_('common.emDash')}</span>
      </div>
      <div class="mt-2 flex flex-col gap-1 sm:flex-row sm:items-baseline sm:gap-3">
        <span class="shrink-0 text-base text-base-content/60">{$_('adapter.endpoint')}</span>
        <span class="wb-mono break-all text-base text-base-content">{detail.endpoint || $_('common.emDash')}</span>
      </div>
    </div>

    <h2 class="wb-section-title">{$_('adapter.settings')}</h2>
    {#if cfgLoading}<p class="text-base-content/70">{$_('adapter.loadingCfg')}</p>{/if}
    {#if cfgError}
      <div role="alert" class="alert alert-soft alert-error mt-2 text-base">{cfgError}</div>
    {/if}
    {#if saveMsg}
      <div role="status" class="alert alert-soft alert-success mt-2 py-2 text-base">{saveMsg}</div>
    {/if}

    <p class="mt-2 text-base text-base-content/70">{$_('adapter.hintWhitelist')}</p>

    <div class="mt-4 flex max-w-2xl flex-col gap-4">
      <label class="form-control w-full">
        <span class="label-text text-base font-medium">{$_('adapter.botToken')}</span>
        <input
          type="password"
          class="input input-bordered wb-mono w-full"
          bind:value={botTokenInput}
          placeholder={hasBotToken ? $_('adapter.tokenUnchanged') : $_('adapter.tokenRequired')}
        />
        {#if hasBotToken && botTokenHint}
          <span class="label-text-alt text-base-content/60">{$_('adapter.storedHint', { hint: botTokenHint })}</span>
        {/if}
      </label>

      <label class="form-control w-full">
        <span class="label-text text-base font-medium">{$_('adapter.whitelistLabel')}</span>
        <textarea
          class="textarea textarea-bordered wb-mono min-h-32 w-full text-base"
          bind:value={whitelistText}
          placeholder={$_('adapter.whitelistPlaceholder')}
        ></textarea>
      </label>

      <button type="button" class="btn btn-primary w-fit" on:click={saveConfig}>
        {$_('adapter.save')}
      </button>
    </div>
  {/if}
{:else}
  <h1 class="wb-page-title">{$_('adapter.listTitle')}</h1>
  <p class="mb-4 text-base text-base-content/70">{$_('adapter.listHint')}</p>
  {#if listError}
    <div role="alert" class="alert alert-soft alert-error mt-3 text-base">{listError}</div>
  {/if}
  {#if !loaded}
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
      {#each [1, 2, 3, 4] as _}
        <div class="wb-surface text-left">
          <div class="skeleton mb-3 h-6 w-2/5"></div>
          <div class="skeleton h-4 w-full"></div>
        </div>
      {/each}
    </div>
  {:else}
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
      {#each adapterComponents as c}
        <button
          type="button"
          class="card card-border bg-base-200 text-left shadow-sm transition-colors hover:border-primary/40 hover:shadow-md"
          on:click={() => goto('adapter', { id: c.name })}
        >
          <div class="card-body gap-0 p-4">
            <div class="flex min-w-0 w-full items-center gap-3">
              <h2 class="card-title min-w-0 flex-1 truncate text-lg font-semibold">
                {c.name || $_('common.emDash')}
              </h2>
              <span class={`${statusBadgeClass(c.status)} shrink-0 whitespace-nowrap`}>
                {c.status || $_('common.unknown')}
              </span>
            </div>
            <p class="wb-mono mt-2 break-all text-base text-base-content/70">{c.endpoint || $_('common.emDash')}</p>
          </div>
        </button>
      {:else}
        <p class="text-base-content/60 sm:col-span-2">{$_('adapter.emptyRegistry')}</p>
      {/each}
    </div>
  {/if}
{/if}
