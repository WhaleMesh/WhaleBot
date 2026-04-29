<script>
  import { onMount } from 'svelte';
  import { route, goto } from '../lib/route.js';
  import { api } from '../lib/api.js';
  import { _, t } from '../lib/i18n.js';
  import { marked } from 'marked';
  import DOMPurify from 'dompurify';

  let list = [];
  let listErr = '';
  let detailErr = '';
  let saving = false;
  /** @type {'preview' | 'edit'} */
  let viewMode = 'preview';
  let form = { title: '', summary: '', body_md: '', tags: '' };
  let currentId = '';
  let loadToken = 0;
  /** @type {string} */
  let prevNavKey = '';

  $: routeId = $route.name === 'skills' ? ($route.params.id || '') : '';

  marked.setOptions({ gfm: true, breaks: true });

  $: renderedHtml =
    viewMode === 'preview'
      ? DOMPurify.sanitize(marked.parse(form.body_md || ''))
      : '';

  async function refreshList() {
    listErr = '';
    try {
      const data = await api.skillsList({ limit: 500 });
      list = data.skills || [];
    } catch (e) {
      listErr = String(e.message || e);
      list = [];
    }
  }

  async function loadDetail(id) {
    const my = ++loadToken;
    detailErr = '';
    try {
      const data = await api.skillsGet(id);
      if (my !== loadToken) return;
      const sk = data.skill;
      form = {
        title: sk.title || '',
        summary: sk.summary || '',
        body_md: sk.body_md || '',
        tags: sk.tags || '',
      };
      currentId = String(sk.id);
      viewMode = 'preview';
    } catch (e) {
      if (my !== loadToken) return;
      detailErr = String(e.message || e);
      form = { title: '', summary: '', body_md: '', tags: '' };
      currentId = '';
    }
  }

  $: {
    const nk =
      $route.name === 'skills'
        ? `skills:${$route.params.id || ''}`
        : `_nav:${$route.name}`;
    if (nk !== prevNavKey) {
      prevNavKey = nk;
      if ($route.name === 'skills') {
        const id = $route.params.id || '';
        if (id) {
          loadDetail(id);
        } else {
          loadToken++;
          currentId = '';
          form = { title: '', summary: '', body_md: '', tags: '' };
          detailErr = '';
          viewMode = 'preview';
        }
      }
    }
  }

  onMount(() => {
    refreshList();
  });

  async function createSkill() {
    listErr = '';
    try {
      const data = await api.skillsCreate({
        title: t('skills.newDefaultTitle'),
        summary: '',
        body_md: '',
        tags: '',
      });
      await refreshList();
      goto('skills', { id: String(data.id) });
    } catch (e) {
      listErr = String(e.message || e);
    }
  }

  async function saveSkill() {
    if (!currentId) return;
    saving = true;
    detailErr = '';
    try {
      await api.skillsUpdate(currentId, {
        title: form.title,
        summary: form.summary,
        body_md: form.body_md,
        tags: form.tags,
      });
      await refreshList();
    } catch (e) {
      detailErr = String(e.message || e);
    } finally {
      saving = false;
    }
  }

  async function removeSkill() {
    if (!currentId) return;
    if (!confirm(t('skills.confirmDelete'))) return;
    detailErr = '';
    try {
      await api.skillsDelete(currentId);
      await refreshList();
      goto('skills', {});
    } catch (e) {
      detailErr = String(e.message || e);
    }
  }

  function selectRow(id) {
    goto('skills', { id: String(id) });
  }
</script>

<h1 class="font-semibold tracking-tight">{$_('skills.title')}</h1>
<p class="mt-1 text-base text-base-content/70">{$_('skills.hint')}</p>

{#if listErr}
  <div role="alert" class="alert alert-soft alert-error mt-3 text-sm">{listErr}</div>
{/if}

<div class="mt-4 grid min-w-0 grid-cols-1 items-start gap-4 md:grid-cols-[minmax(0,17rem)_1fr]">
  <aside
    class="card card-border min-w-0 max-w-full overflow-hidden bg-base-200 shadow-sm md:max-h-[calc(100vh-8rem)] md:overflow-y-auto"
  >
    <div class="card-body min-w-0 max-w-full gap-3 p-4">
      <div class="flex min-w-0 flex-wrap gap-2">
        <button type="button" class="btn btn-primary shrink-0" on:click={createSkill}>{$_('skills.create')}</button>
        <button type="button" class="btn btn-outline shrink-0" on:click={refreshList}>{$_('skills.refresh')}</button>
      </div>
      <ul class="menu skills-sidebar w-full min-w-0 max-w-full rounded-box bg-base-100 p-0">
        {#each list as s (s.id)}
          <li class="min-w-0 max-w-full">
            <button
              type="button"
              class="flex w-full min-w-0 max-w-full items-center gap-2 text-left {String(s.id) === routeId
                ? 'active bg-base-300'
                : ''}"
              on:click={() => selectRow(s.id)}
            >
              <span class="min-w-0 flex-1 truncate text-base">{s.title || $_('skills.noTitle')}</span>
              <span class="badge badge-ghost shrink-0 whitespace-nowrap font-mono text-sm">#{s.id}</span>
            </button>
          </li>
        {:else}
          <li class="px-3 py-4 text-center text-sm text-base-content/60">{$_('skills.emptyList')}</li>
        {/each}
      </ul>
    </div>
  </aside>

  <section class="card card-border min-w-0 bg-base-200 shadow-sm">
    <div class="card-body min-h-[320px] grid gap-4 p-5 md:p-6">
      {#if !routeId}
        <p class="py-12 text-center text-base text-base-content/60">{$_('skills.placeholder')}</p>
      {:else}
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="join">
            <button
              type="button"
              class="btn join-item min-w-[6.5rem] {viewMode === 'preview' ? 'btn-primary' : 'btn-outline'}"
              on:click={() => (viewMode = 'preview')}
            >
              {$_('skills.preview')}
            </button>
            <button
              type="button"
              class="btn join-item min-w-[6.5rem] {viewMode === 'edit' ? 'btn-primary' : 'btn-outline'}"
              on:click={() => (viewMode = 'edit')}
            >
              {$_('skills.edit')}
            </button>
          </div>
          <div class="flex flex-wrap gap-2">
            <button type="button" class="btn btn-primary" disabled={saving || !currentId} on:click={saveSkill}>
              {saving ? $_('skills.saving') : $_('skills.save')}
            </button>
            <button type="button" class="btn btn-outline btn-error" disabled={!currentId} on:click={removeSkill}>
              {$_('skills.delete')}
            </button>
          </div>
        </div>

        {#if detailErr}
          <div role="alert" class="alert alert-soft alert-error text-base">{detailErr}</div>
        {/if}

        <label class="form-control w-full">
          <span class="label label-text text-sm">{$_('skills.fieldTitle')}</span>
          <input type="text" class="input input-bordered w-full" bind:value={form.title} />
        </label>
        <label class="form-control w-full">
          <span class="label label-text text-sm">{$_('skills.fieldSummary')}</span>
          <textarea
            class="textarea textarea-bordered min-h-[6.5rem] w-full text-base leading-relaxed"
            rows="4"
            bind:value={form.summary}
          ></textarea>
        </label>
        <label class="form-control w-full">
          <span class="label label-text text-sm">{$_('skills.fieldTags')}</span>
          <input
            type="text"
            class="input input-bordered w-full"
            bind:value={form.tags}
            placeholder={$_('skills.tagsPlaceholder')}
          />
        </label>

        <div class="form-control w-full">
          <span class="label label-text text-sm">{$_('skills.bodyLabel')}</span>
          {#if viewMode === 'edit'}
            <textarea
              class="textarea textarea-bordered min-h-[280px] w-full font-mono text-base leading-relaxed"
              rows="18"
              bind:value={form.body_md}
            ></textarea>
          {:else}
            <article
              class="md-preview rounded-lg border border-base-300 bg-base-100 p-4 text-base leading-relaxed text-base-content min-h-[280px] max-h-[60vh] overflow-y-auto"
            >
              {@html renderedHtml}
            </article>
          {/if}
        </div>
      {/if}
    </div>
  </section>
</div>

<style>
  .md-preview :global(h1),
  .md-preview :global(h2),
  .md-preview :global(h3) {
    margin: 0.75rem 0 0.4rem;
    font-weight: 600;
  }
  .md-preview :global(p) {
    margin: 0.5rem 0;
  }
  .md-preview :global(ul),
  .md-preview :global(ol) {
    margin: 0.5rem 0;
    padding-left: 1.25rem;
  }
  .md-preview :global(code) {
    background: var(--color-base-300);
    padding: 0.12rem 0.35rem;
    border-radius: 0.25rem;
    font-size: 0.85em;
  }
  .md-preview :global(pre) {
    background: var(--color-base-300);
    border: 1px solid var(--color-base-300);
    border-radius: 0.5rem;
    padding: 0.65rem 0.75rem;
    overflow: auto;
  }
  .md-preview :global(pre code) {
    background: none;
    padding: 0;
  }
  .md-preview :global(a) {
    color: var(--color-primary);
  }
  :global(ul.skills-sidebar.menu li > button),
  :global(ul.skills-sidebar.menu li > a) {
    max-width: 100%;
    overflow: hidden;
  }
</style>
