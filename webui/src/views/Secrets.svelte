<script>
  import { onMount } from 'svelte';
  import { route, goto } from '../lib/route.js';
  import { api } from '../lib/api.js';
  import { _ } from '../lib/i18n.js';

  let list = [];
  let listErr = '';
  let listLoading = false;
  let detailLoading = false;
  let detailErr = '';
  let saving = false;
  let currentKey = '';
  let form = { key: '', value: '', note: '' };
  let maskedValue = '';
  let isEdit = false;
  let loadToken = 0;

  onMount(() => {
    refreshList();
  });

  async function refreshList() {
    listLoading = true;
    listErr = '';
    try {
      const data = await api.secretsList();
      list = data.secrets || [];
    } catch (e) {
      listErr = String(e);
    } finally {
      listLoading = false;
    }
  }

  async function loadDetail(key) {
    if (!key) return;
    const tok = ++loadToken;
    detailLoading = true;
    detailErr = '';
    try {
      const data = await api.secretsGet(key);
      if (tok !== loadToken) return;
      if (data.success && data.found) {
        form = { key: data.key, value: '', note: data.note || '' };
        maskedValue = data.value_masked || '****';
        currentKey = data.key;
        isEdit = true;
      } else {
        detailErr = $_('secrets.placeholder');
      }
    } catch (e) {
      if (tok === loadToken) detailErr = String(e);
    } finally {
      if (tok === loadToken) detailLoading = false;
    }
  }

  function selectItem(key) {
    goto('secrets', { id: key });
  }

  function createNew() {
    form = { key: '', value: '', note: '' };
    maskedValue = '';
    currentKey = '';
    isEdit = false;
    detailErr = '';
    goto('secrets', {});
  }

  async function saveSecret() {
    detailErr = '';
    if (!form.key.trim()) {
      detailErr = $_('secrets.keyRequired');
      return;
    }
    if (!isEdit && !form.value) {
      detailErr = $_('secrets.valueRequired');
      return;
    }
    if (!/^[a-zA-Z0-9_\-]+$/.test(form.key.trim())) {
      detailErr = $_('secrets.keyFormat');
      return;
    }
    saving = true;
    try {
      if (isEdit) {
        const body = { note: form.note };
        if (form.value) body.value = form.value;
        await api.secretsUpdate(currentKey, body);
      } else {
        await api.secretsCreate({ key: form.key.trim(), value: form.value, note: form.note });
        currentKey = form.key.trim();
        isEdit = true;
      }
      await refreshList();
      if (!isEdit) {
        goto('secrets', { id: currentKey });
      } else {
        await loadDetail(currentKey);
      }
    } catch (e) {
      detailErr = String(e);
    } finally {
      saving = false;
    }
  }

  async function deleteSecret() {
    if (!currentKey) return;
    if (!confirm($_('secrets.confirmDelete').replace('{key}', currentKey))) return;
    detailErr = '';
    try {
      await api.secretsDelete(currentKey);
      await refreshList();
      goto('secrets', {});
      form = { key: '', value: '', note: '' };
      maskedValue = '';
      currentKey = '';
      isEdit = false;
    } catch (e) {
      detailErr = String(e);
    }
  }

  $: {
    const r = $route;
    if (r.name === 'secrets') {
      if (r.params.id) {
        if (r.params.id !== currentKey) {
          loadDetail(r.params.id);
        }
      } else if (currentKey) {
        form = { key: '', value: '', note: '' };
        maskedValue = '';
        currentKey = '';
        isEdit = false;
        detailErr = '';
      }
    }
  }
</script>

<div class="flex flex-col gap-4">
  <h1 class="text-xl font-bold">{$_('secrets.title')}</h1>
  <p class="text-sm text-base-content/70">{$_('secrets.hint')}</p>

  <div class="grid min-h-[60vh] gap-4 md:grid-cols-[minmax(0,17rem)_1fr]">
    <!-- Sidebar list -->
    <div class="flex flex-col gap-2">
      <div class="flex gap-2">
        <button class="btn btn-primary btn-sm flex-1" on:click={createNew}>{$_('secrets.create')}</button>
        <button class="btn btn-ghost btn-sm" on:click={refreshList}>{$_('secrets.refresh')}</button>
      </div>
      {#if listErr}
        <div class="alert alert-error text-sm py-2">{listErr}</div>
      {/if}
      <div class="flex flex-col gap-1 overflow-y-auto max-h-[50vh]">
        {#if listLoading}
          <span class="loading loading-spinner loading-sm mx-auto mt-4"></span>
        {:else if list.length === 0}
          <p class="text-sm text-base-content/50 py-4 text-center">{$_('secrets.emptyList')}</p>
        {:else}
          {#each list as item}
            <button
              type="button"
              class="flex min-h-[2.5rem] w-full items-center rounded-lg border border-base-300 bg-base-100 px-3 py-2 text-left text-sm transition-colors hover:border-primary/60 hover:bg-base-300 {currentKey === item.key ? 'border-primary bg-primary text-primary-content hover:border-primary hover:bg-primary hover:text-primary-content' : ''}"
              on:click={() => selectItem(item.key)}
            >
              <span class="min-w-0 flex-1 truncate">{item.key}</span>
            </button>
          {/each}
        {/if}
      </div>
    </div>

    <!-- Detail -->
    <div class="flex flex-col gap-3">
      {#if detailLoading}
        <span class="loading loading-spinner loading-sm mx-auto mt-8"></span>
      {:else if !currentKey && !form.key && list.length > 0}
        <p class="text-sm text-base-content/50 mt-8 text-center">{$_('secrets.placeholder')}</p>
      {:else}
        {#if detailErr}
          <div class="alert alert-error text-sm py-2">{detailErr}</div>
        {/if}

        <label class="form-control w-full">
          <span class="label-text">{$_('secrets.fieldKey')}</span>
          <input
            class="input input-bordered w-full"
            bind:value={form.key}
            placeholder={$_('secrets.keyPlaceholder')}
            disabled={isEdit}
          />
        </label>

        <label class="form-control w-full">
          <span class="label-text">{$_('secrets.fieldNote')}</span>
          <input
            class="input input-bordered w-full"
            bind:value={form.note}
            placeholder={$_('secrets.notePlaceholder')}
          />
        </label>

        {#if isEdit}
          <div class="form-control w-full">
            <span class="label-text">{$_('secrets.fieldMasked')}</span>
            <input class="input input-bordered w-full font-mono" value={maskedValue} disabled />
          </div>
        {/if}

        <label class="form-control w-full">
          <span class="label-text">{isEdit ? $_('secrets.fieldValue') + ' (' + $_('skills.edit') + ')' : $_('secrets.fieldValue')}</span>
          <input
            class="input input-bordered w-full font-mono"
            type="password"
            bind:value={form.value}
            placeholder={isEdit ? $_('secrets.fieldValue') + '…' : $_('secrets.valuePlaceholder')}
          />
        </label>

        <div class="flex gap-2 mt-2">
          <button class="btn btn-primary" on:click={saveSecret} disabled={saving}>
            {#if saving}
              <span class="loading loading-spinner loading-sm"></span>
              {$_('secrets.saving')}
            {:else}
              {$_('secrets.save')}
            {/if}
          </button>
          {#if isEdit}
            <button class="btn btn-error btn-outline" on:click={deleteSecret}>{$_('secrets.delete')}</button>
          {/if}
        </div>
      {/if}
    </div>
  </div>
</div>
