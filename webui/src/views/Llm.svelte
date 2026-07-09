<script>
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '../lib/route.js';
  import { api, getOrchestratorBase } from '../lib/api.js';
  import { _, t } from '../lib/i18n.js';

  export let llmName = '';

  let components = [];
  let listError = '';
  let timer;

  /** @type {{ id: string, name: string, base_url: string, model: string, has_api_key: boolean, api_key_hint?: string, apiKeyInput: string, persisted: boolean }[]} */
  let editModels = [];
  let activeModelId = '';
  /** Last active id successfully applied on the server (for radio rollback). */
  let lastCommittedActiveId = '';
  let cfgError = '';
  let saveMsg = '';
  let testResult = '';
  let loaded = false;
  let cfgLoading = false;
  let testInProgress = false;

  function isLlm(c) {
    return String(c?.type || '').toLowerCase() === 'llm';
  }

  $: llmComponents = components.filter(isLlm);
  $: detail =
    llmName && llmComponents.length
      ? llmComponents.find((c) => c.name === llmName) || null
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

  function newRow() {
    const id =
      typeof crypto !== 'undefined' && crypto.randomUUID
        ? crypto.randomUUID()
        : 'm_' + Math.random().toString(36).slice(2, 12);
    editModels = [
      ...editModels,
      {
        id,
        name: '',
        base_url: 'https://api.openai.com',
        model: 'gpt-4o-mini',
        has_api_key: false,
        api_key_hint: '',
        apiKeyInput: '',
        persisted: false,
      },
    ];
  }

  function removeRow(i) {
    const removed = editModels[i];
    editModels = editModels.filter((_, j) => j !== i);
    if (activeModelId === removed.id) {
      activeModelId = '';
    }
  }

  async function loadConfig() {
    if (!llmName) return;
    cfgLoading = true;
    cfgError = '';
    try {
      const data = await api.llmConfigGet(llmName);
      const cfg = data.config || { models: [], active_model_id: '' };
      activeModelId = cfg.active_model_id || '';
      lastCommittedActiveId = activeModelId;
      editModels = (cfg.models || []).map((m) => ({
        id: m.id,
        name: m.name,
        base_url: m.base_url,
        model: m.model,
        has_api_key: !!m.has_api_key,
        api_key_hint: m.api_key_hint || '',
        apiKeyInput: '',
        persisted: true,
      }));
    } catch (e) {
      cfgError = String(e);
      editModels = [];
    } finally {
      cfgLoading = false;
    }
  }

  async function persistAll() {
    const models = editModels.map((m) => ({
      id: m.id,
      name: m.name.trim(),
      base_url: m.base_url.trim(),
      model: m.model.trim(),
      api_key: (m.apiKeyInput || '').trim(),
    }));
    await api.llmConfigPut(llmName, {
      models,
      active_model_id: activeModelId || '',
    });
  }

  async function saveAll() {
    saveMsg = '';
    cfgError = '';
    try {
      await persistAll();
      saveMsg = t('llm.saved');
      await loadConfig();
      await refreshList();
    } catch (e) {
      cfgError = String(e);
    }
  }

  function rowValidationError(i) {
    const m = editModels[i];
    if (!m.name.trim() || !m.base_url.trim() || !m.model.trim()) {
      return t('llm.rowRequired');
    }
    if (!m.persisted && !(m.apiKeyInput || '').trim() && !m.has_api_key) {
      return t('llm.newRowKey');
    }
    return '';
  }

  async function saveRow(i) {
    const err = rowValidationError(i);
    if (err) {
      cfgError = err;
      return;
    }
    saveMsg = '';
    cfgError = '';
    try {
      await persistAll();
      saveMsg = t('llm.saved');
      await loadConfig();
      await refreshList();
    } catch (e) {
      cfgError = String(e);
    }
  }

  async function applyActiveChange(newId) {
    cfgError = '';
    saveMsg = '';
    activeModelId = newId;
    try {
      await api.llmActivePost(llmName, { id: newId || '' });
      lastCommittedActiveId = newId;
      saveMsg = t('llm.activeUpdated');
      await loadConfig();
      await refreshList();
    } catch (e) {
      cfgError = String(e);
      activeModelId = lastCommittedActiveId;
    }
  }

  async function runTest(useRowId) {
    if (testInProgress) return;
    testInProgress = true;
    testResult = t('llm.testing');
    cfgError = '';
    const body = useRowId ? { model_id: useRowId } : {};
    const url =
      getOrchestratorBase() +
      '/api/v1/llm-components/' +
      encodeURIComponent(llmName) +
      '/test';
    try {
      const res = await fetch(url, {
        method: 'POST',
        cache: 'no-store',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      const data = await res.json().catch(() => ({}));
      if (res.status === 409) {
        testResult = data.error || t('llm.testInProgress');
        return;
      }
      if (!res.ok) {
        testResult = `${res.status} ${res.statusText}: ${data.error || JSON.stringify(data)}`;
        return;
      }
      if (data.success) {
        testResult = t('llm.testOk');
      } else {
        testResult = data.error || t('llm.testNoErr');
      }
    } catch (e) {
      testResult = String(e);
    } finally {
      testInProgress = false;
    }
  }

  onMount(() => {
    refreshList();
    timer = setInterval(refreshList, 5000);
  });
  onDestroy(() => {
    if (timer) clearInterval(timer);
  });

  $: if (llmName) loadConfig();
</script>

{#if llmName}
  <div class="mb-2">
    <button type="button" class="btn btn-sm btn-ghost gap-1" on:click={() => goto('llm')}>
      {$_('llm.backAll')}
    </button>
  </div>
  <h1 class="wb-page-title">{$_('llm.detailTitle', { name: llmName })}</h1>
  {#if listError}
    <div role="alert" class="alert alert-soft alert-error mt-3 text-base">{listError}</div>
  {/if}
  {#if !loaded}
    <div class="wb-surface mt-3 !py-5">
      <div class="skeleton mb-4 h-5 w-48"></div>
      <div class="skeleton mb-2 h-4 w-full max-w-xl"></div>
      <div class="skeleton h-4 w-2/3 max-w-lg"></div>
    </div>
    <h2 class="wb-section-title">{$_('llm.modelProfiles')}</h2>
    <div class="llm-profiles mt-3">
      {#each [1, 2, 3] as _}
        <div class="llm-profile">
          <div class="llm-profile-inner">
            <div class="skeleton llm-active-toggle rounded"></div>
            <div class="llm-profile-body">
              <div class="llm-profile-row-fields">
                <div class="skeleton h-7 w-full rounded"></div>
                <div class="skeleton h-7 w-full rounded"></div>
              </div>
              <div class="llm-profile-row-fields">
                <div class="skeleton h-7 w-full rounded"></div>
                <div class="skeleton h-7 w-full rounded"></div>
              </div>
            </div>
            <div class="llm-profile-actions">
              <div class="skeleton h-7 w-full rounded"></div>
              <div class="skeleton h-7 w-full rounded"></div>
            </div>
          </div>
        </div>
      {/each}
    </div>
  {:else if !detail}
    <p class="mt-2 text-base-content/70">{$_('llm.notFound', { name: llmName })}</p>
  {:else}
    <div class="wb-surface mt-3">
      <div class="flex flex-wrap items-center gap-x-4 gap-y-1">
        <span class="text-base text-base-content/60">{$_('llm.registryStatus')}</span>
        <span class={statusBadgeClass(detail.status)}>{detail.status || $_('common.emDash')}</span>
      </div>
      <div class="mt-2 flex flex-col gap-1 sm:flex-row sm:items-baseline sm:gap-3">
        <span class="shrink-0 text-base text-base-content/60">{$_('llm.endpoint')}</span>
        <span class="wb-mono break-all text-base text-base-content">{detail.endpoint || $_('common.emDash')}</span>
      </div>
    </div>

    <h2 class="wb-section-title">{$_('llm.modelProfiles')}</h2>
    {#if cfgLoading}<p class="text-base-content/70">{$_('llm.loadingCfg')}</p>{/if}
    {#if cfgError}
      <div role="alert" class="alert alert-soft alert-error mt-2 text-base">{cfgError}</div>
    {/if}
    {#if saveMsg}
      <div role="status" class="alert alert-soft alert-success mt-2 py-2 text-base">{saveMsg}</div>
    {/if}

    <p class="hint-html mt-2 text-base text-base-content/70">{@html $_('llm.hintActive')}</p>
    <details class="llm-hint-local mt-1">
      <summary class="text-sm text-base-content/55">{$_('llm.hintLocalHostSummary')}</summary>
      <p class="hint-html mt-1 text-sm text-base-content/65">{@html $_('llm.hintLocalHost')}</p>
    </details>

    <div class="llm-profiles mt-3">
      <div class="llm-profile llm-profile--none" class:llm-profile--active={activeModelId === ''}>
        <div class="llm-profile-inner">
          <button
            type="button"
            class="llm-active-toggle"
            class:llm-active-toggle--on={activeModelId === ''}
            disabled={testInProgress}
            aria-pressed={activeModelId === ''}
            aria-label={$_('llm.thActive')}
            on:click={() => applyActiveChange('')}
          ></button>
          <div class="llm-profile-body llm-profile-body--single">
            <span class="text-sm text-base-content/60">{$_('llm.noActiveModel')}</span>
          </div>
        </div>
      </div>

      {#each editModels as m, i}
        <div class="llm-profile" class:llm-profile--active={activeModelId === m.id}>
          <div class="llm-profile-inner">
            <button
              type="button"
              class="llm-active-toggle"
              class:llm-active-toggle--on={activeModelId === m.id}
              disabled={!m.persisted || testInProgress}
              aria-pressed={activeModelId === m.id}
              aria-label={$_('llm.thActive')}
              on:click={() => {
                if (m.persisted) applyActiveChange(m.id);
              }}
            ></button>

            <div class="llm-profile-body">
              <div class="llm-profile-row-fields">
                <label class="llm-field">
                  <span class="llm-field-label">{$_('llm.thName')}</span>
                  <input
                    class="input input-bordered input-sm min-w-0 w-full"
                    bind:value={m.name}
                    placeholder={$_('llm.displayNamePh')}
                  />
                </label>
                <label class="llm-field">
                  <span class="llm-field-label">{$_('llm.thBaseUrl')}</span>
                  <input
                    class="input input-bordered input-sm wb-mono min-w-0 w-full"
                    bind:value={m.base_url}
                  />
                </label>
              </div>
              <div class="llm-profile-row-fields">
                <label class="llm-field">
                  <span class="llm-field-label">{$_('llm.thModelId')}</span>
                  <input
                    class="input input-bordered input-sm wb-mono min-w-0 w-full"
                    bind:value={m.model}
                  />
                </label>
                <label class="llm-field">
                  <span class="llm-field-label">{$_('llm.thApiKey')}</span>
                  <div class="flex min-w-0 items-center gap-1">
                    <input
                      type="password"
                      class="input input-bordered input-sm min-w-0 flex-1"
                      bind:value={m.apiKeyInput}
                      placeholder={m.has_api_key ? $_('llm.apiKeyUnchanged') : $_('llm.apiKeyRequired')}
                    />
                    {#if m.has_api_key && m.api_key_hint}
                      <div
                        class="tooltip tooltip-top shrink-0 before:max-w-[min(100vw-2rem,28rem)] before:break-all before:text-left"
                        data-tip={`${$_('llm.storedPrefix')} ${m.api_key_hint}`}
                      >
                        <button
                          type="button"
                          class="btn btn-ghost btn-xs h-7 min-h-7 w-7 shrink-0 px-0 text-xs"
                          aria-label={$_('llm.tipStoredKey')}
                        >ⓘ</button>
                      </div>
                    {/if}
                  </div>
                </label>
              </div>
            </div>

            <div class="llm-profile-actions">
              {#if m.persisted}
                <button
                  type="button"
                  class="btn btn-sm btn-outline btn-error llm-action-btn"
                  disabled={testInProgress}
                  on:click={() => removeRow(i)}
                >
                  {$_('llm.remove')}
                </button>
                <button
                  type="button"
                  class="btn btn-sm btn-outline llm-action-btn"
                  disabled={testInProgress}
                  on:click={() => runTest(m.id)}
                >
                  {$_('llm.test')}
                </button>
              {:else}
                <button
                  type="button"
                  class="btn btn-sm btn-primary llm-action-btn llm-action-btn--solo"
                  disabled={testInProgress}
                  on:click={() => saveRow(i)}
                >
                  {$_('llm.save')}
                </button>
              {/if}
            </div>
          </div>
        </div>
      {/each}
    </div>

    <div class="llm-toolbar">
      <button type="button" class="btn llm-toolbar-btn" disabled={testInProgress} on:click={newRow}>
        {$_('llm.addModel')}
      </button>
      <button type="button" class="btn btn-primary llm-toolbar-btn" disabled={testInProgress} on:click={saveAll}>
        {$_('llm.saveAll')}
      </button>
      <button
        type="button"
        class="btn btn-outline llm-toolbar-btn"
        disabled={testInProgress || !activeModelId}
        on:click={() => runTest(null)}
      >
        {$_('llm.testActive')}
      </button>
    </div>

    <h3 class="mt-4 text-base font-semibold text-base-content/80">{$_('llm.testOutput')}</h3>
    <pre
      class="bg-base-300/40 mt-2 max-h-56 overflow-auto rounded-lg border border-base-300 p-4 font-mono text-base whitespace-pre-wrap break-words text-base-content">{testResult || $_('common.emDash')}</pre>
  {/if}
{:else}
  <h1 class="wb-page-title">{$_('llm.listTitle')}</h1>
  <p class="mb-4 text-base text-base-content/70">{$_('llm.listHint')}</p>
  {#if listError}
    <div role="alert" class="alert alert-soft alert-error mt-3 text-base">{listError}</div>
  {/if}
  {#if !loaded}
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
      {#each [1, 2, 3, 4] as _}
        <div class="wb-surface text-left">
          <div class="skeleton mb-3 h-6 w-2/5"></div>
          <div class="skeleton h-4 w-full"></div>
          <div class="skeleton mt-2 h-4 w-4/5"></div>
        </div>
      {/each}
    </div>
  {:else}
  <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
    {#each llmComponents as c}
      <button
        type="button"
        class="card card-border bg-base-200 text-left shadow-sm transition-colors hover:border-primary/40 hover:shadow-md"
        on:click={() => goto('llm', { id: c.name })}
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
      <p class="text-base-content/60 sm:col-span-2">{$_('llm.emptyRegistry')}</p>
    {/each}
  </div>
  {/if}
{/if}

<style>
  .hint-html :global(b) {
    font-weight: 600;
  }

  .llm-hint-local > summary {
    cursor: pointer;
    list-style: none;
    user-select: none;
  }

  .llm-hint-local > summary::-webkit-details-marker {
    display: none;
  }

  .llm-hint-local > summary::before {
    content: '▸ ';
    display: inline-block;
    transition: transform 0.15s ease;
  }

  .llm-hint-local[open] > summary::before {
    transform: rotate(90deg);
  }

  .llm-profiles {
    overflow: hidden;
    border-radius: var(--radius-box);
    border: 1px solid color-mix(in oklab, var(--color-base-content) 8%, var(--color-base-300));
  }

  .llm-profile {
    border-bottom: 1px solid color-mix(in oklab, var(--color-base-content) 6%, var(--color-base-300));
    padding: 0.5rem 0.75rem;
    transition: background-color 0.15s ease;
  }

  .llm-profile:last-child {
    border-bottom-width: 0;
  }

  .llm-profile--active {
    background: color-mix(in oklab, var(--color-primary) 16%, var(--color-base-200));
  }

  .llm-profile-inner {
    display: grid;
    grid-template-columns: auto 1fr auto;
    gap: 0.625rem;
    align-items: stretch;
  }

  .llm-active-toggle {
    display: flex;
    width: 1.375rem;
    min-width: 1.375rem;
    align-self: stretch;
    align-items: center;
    justify-content: center;
    border-radius: var(--radius-field);
    border: 1px solid color-mix(in oklab, var(--color-base-content) 16%, var(--color-base-300));
    background: color-mix(in oklab, var(--color-base-300) 25%, transparent);
    cursor: pointer;
    padding: 0;
    transition:
      background-color 0.15s ease,
      border-color 0.15s ease,
      box-shadow 0.15s ease;
  }

  .llm-active-toggle:hover:not(:disabled) {
    border-color: color-mix(in oklab, var(--color-primary) 45%, var(--color-base-300));
  }

  .llm-active-toggle--on {
    border-color: var(--color-primary);
    background: var(--color-primary);
    box-shadow: 0 0 0 1px color-mix(in oklab, var(--color-primary) 35%, transparent);
  }

  .llm-active-toggle--on::after {
    content: '';
    width: 0.4rem;
    height: 0.4rem;
    border-radius: 9999px;
    background: var(--color-primary-content);
  }

  .llm-active-toggle:disabled {
    cursor: not-allowed;
    opacity: 0.45;
  }

  .llm-profile-body {
    display: flex;
    min-width: 0;
    flex-direction: column;
    gap: 0.375rem;
  }

  .llm-profile-body--single {
    justify-content: center;
    min-height: 1.75rem;
    padding-top: 0.125rem;
  }

  .llm-profile-row-fields {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1.35fr);
    gap: 0.5rem;
  }

  .llm-field {
    display: flex;
    min-width: 0;
    flex-direction: column;
    gap: 0.125rem;
  }

  .llm-field-label {
    font-size: var(--text-xs);
    line-height: 1.2;
    color: color-mix(in oklab, var(--color-base-content) 55%, transparent);
  }

  .llm-profile-actions {
    display: flex;
    width: 5.25rem;
    flex-direction: column;
    gap: 0.375rem;
    align-self: center;
  }

  .llm-action-btn {
    width: 100%;
    min-height: 1.75rem;
    height: 1.75rem;
    padding-left: 0.5rem;
    padding-right: 0.5rem;
    font-size: var(--text-xs);
  }

  .llm-action-btn--solo {
    min-height: 3.875rem;
    height: 3.875rem;
  }

  .llm-toolbar {
    margin-top: 1rem;
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 0.5rem;
  }

  .llm-toolbar-btn {
    width: 100%;
  }

  @media (max-width: 640px) {
    .llm-profile-inner {
      grid-template-columns: auto 1fr;
      grid-template-rows: auto auto;
    }

    .llm-profile-actions {
      grid-column: 1 / -1;
      width: 100%;
      flex-direction: row;
    }

    .llm-profile-row-fields {
      grid-template-columns: 1fr;
    }

    .llm-action-btn--solo {
      min-height: 1.75rem;
      height: 1.75rem;
    }
  }
</style>
