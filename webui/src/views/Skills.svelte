<script>
  import { onMount } from 'svelte';
  import { route, goto } from '../lib/route.js';
  import { api } from '../lib/api.js';
  import { _, t } from '../lib/i18n.js';
  import { marked } from 'marked';
  import DOMPurify from 'dompurify';

  let list = [];
  let listErr = '';
  let listInitialLoad = true;
  let detailLoading = false;
  let detailErr = '';
  let saving = false;
  let importUploading = false;
  /** @type {HTMLInputElement | null} */
  let zipInput = null;
  /** @type {'preview' | 'edit'} */
  let viewMode = 'preview';
  let form = { title: '', summary: '', tags: '' };
  /** @type {Record<string, string>} */
  let fileContents = {};
  /** @type {string[]} */
  let filePaths = [];
  let selectedPath = 'SKILL.md';
  let currentId = '';
  let loadToken = 0;
  /** @type {string} */
  let prevNavKey = '';

  $: routeId = $route.name === 'skills' ? ($route.params.id || '') : '';

  marked.setOptions({ gfm: true, breaks: true });

  $: editablePaths = filePaths.filter((p) => p !== 'skill.yaml');
  $: previewContent = fileContents[selectedPath] || '';
  $: renderedHtml =
    viewMode === 'preview'
      ? DOMPurify.sanitize(marked.parse(previewContent || ''))
      : '';

  function sortPaths(paths) {
    const main = [];
    const refs = [];
    const rest = [];
    for (const p of paths) {
      if (p === 'SKILL.md') main.push(p);
      else if (p.startsWith('references/')) refs.push(p);
      else if (p !== 'skill.yaml') rest.push(p);
    }
    refs.sort();
    rest.sort();
    return [...main, ...refs, ...rest];
  }

  async function refreshList() {
    listErr = '';
    try {
      const data = await api.skillsList({ limit: 500 });
      list = data.skills || [];
    } catch (e) {
      listErr = String(e.message || e);
      list = [];
    } finally {
      listInitialLoad = false;
    }
  }

  async function loadDetail(id) {
    const my = ++loadToken;
    detailErr = '';
    detailLoading = true;
    try {
      const data = await api.skillsGet(id);
      if (my !== loadToken) return;
      const sk = data.skill;
      form = {
        title: sk.title || '',
        summary: sk.summary || '',
        tags: sk.tags || '',
      };
      const files = sk.files || [];
      fileContents = {};
      for (const f of files) {
        if (f.path && f.path !== 'skill.yaml') {
          fileContents[f.path] = f.content || '';
        }
      }
      filePaths = sortPaths(Object.keys(fileContents));
      if (!fileContents['SKILL.md']) {
        fileContents['SKILL.md'] = '';
        filePaths = sortPaths(['SKILL.md', ...filePaths.filter((p) => p !== 'SKILL.md')]);
      }
      selectedPath = 'SKILL.md';
      currentId = String(sk.slug || sk.id);
      viewMode = 'preview';
    } catch (e) {
      if (my !== loadToken) return;
      detailErr = String(e.message || e);
      form = { title: '', summary: '', tags: '' };
      fileContents = {};
      filePaths = [];
      currentId = '';
    } finally {
      if (my === loadToken) detailLoading = false;
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
          form = { title: '', summary: '', tags: '' };
          fileContents = {};
          filePaths = [];
          detailErr = '';
          detailLoading = false;
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
        tags: '',
        body_md: '# ' + t('skills.newDefaultTitle') + '\n',
      });
      await refreshList();
      goto('skills', { id: String(data.slug || data.id) });
    } catch (e) {
      listErr = String(e.message || e);
    }
  }

  function pickZipImport() {
    zipInput?.click();
  }

  /** @param {Event} ev */
  async function onZipSelected(ev) {
    const input = /** @type {HTMLInputElement} */ (ev.currentTarget);
    const file = input.files && input.files[0];
    input.value = '';
    if (!file) return;
    if (!/\.zip$/i.test(file.name)) {
      listErr = t('skills.importZipInvalid');
      return;
    }
    listErr = '';
    importUploading = true;
    try {
      const data = await api.skillsImportZip(file);
      await refreshList();
      goto('skills', { id: String(data.slug || data.id) });
    } catch (e) {
      listErr = String(e.message || e);
    } finally {
      importUploading = false;
    }
  }

  async function saveSkill() {
    if (!currentId) return;
    saving = true;
    detailErr = '';
    try {
      const files = {};
      for (const p of editablePaths) {
        files[p] = fileContents[p] ?? '';
      }
      await api.skillsUpdate(currentId, {
        title: form.title,
        summary: form.summary,
        tags: form.tags,
        files,
      });
      await refreshList();
      await loadDetail(currentId);
    } catch (e) {
      detailErr = String(e.message || e);
    } finally {
      saving = false;
    }
  }

  async function addReferenceFile() {
    if (!currentId) return;
    const name = prompt(t('skills.newFilePrompt'), 'references/notes.md');
    if (!name) return;
    const path = name.trim().replace(/^\/+/, '');
    if (!path || path === 'SKILL.md' || path === 'skill.yaml') return;
    detailErr = '';
    try {
      await api.skillsCreateFile(currentId, path, '# ' + path + '\n');
      await loadDetail(currentId);
      selectedPath = path;
      viewMode = 'edit';
    } catch (e) {
      detailErr = String(e.message || e);
    }
  }

  async function removeCurrentFile() {
    if (!currentId || !selectedPath || selectedPath === 'SKILL.md') return;
    if (!confirm(t('skills.confirmDeleteFile'))) return;
    detailErr = '';
    try {
      await api.skillsDeleteFile(currentId, selectedPath);
      await loadDetail(currentId);
      selectedPath = 'SKILL.md';
    } catch (e) {
      detailErr = String(e.message || e);
    }
  }

  /** @param {string} id @param {MouseEvent} [ev] */
  async function removeSkillFromList(id, ev) {
    ev?.stopPropagation();
    ev?.preventDefault();
    if (!confirm(t('skills.confirmDelete'))) return;
    listErr = '';
    try {
      await api.skillsDelete(id);
      await refreshList();
      if (String(routeId) === String(id)) {
        goto('skills', {});
      }
    } catch (e) {
      listErr = String(e.message || e);
    }
  }

  function selectRow(id) {
    goto('skills', { id: String(id) });
  }
</script>

<h1 class="wb-page-title">{$_('skills.title')}</h1>
<p class="mb-4 text-base text-base-content/70">{$_('skills.hint')}</p>

{#if listErr}
  <div role="alert" class="alert alert-soft alert-error mt-3 text-sm">{listErr}</div>
{/if}

<div class="mt-3 grid min-w-0 grid-cols-1 items-start gap-4 lg:grid-cols-[minmax(15rem,24rem)_minmax(0,1fr)]">
  <aside
    class="skills-aside min-w-0 rounded-xl border border-base-300/40 bg-base-200 p-4 lg:max-h-[calc(100vh-8rem)] lg:overflow-y-auto"
  >
    <div class="flex flex-col gap-3">
      <div class="grid grid-cols-2 gap-2">
        <button type="button" class="btn btn-primary btn-sm" on:click={createSkill}>
          {$_('skills.create')}
        </button>
        <button
          type="button"
          class="btn btn-outline btn-sm"
          disabled={importUploading}
          on:click={pickZipImport}
        >
          {importUploading ? $_('skills.importZipUploading') : $_('skills.importZipShort')}
        </button>
      </div>
      <button type="button" class="btn btn-ghost btn-sm w-full" on:click={refreshList}>
        {$_('skills.refresh')}
      </button>
      <input
        bind:this={zipInput}
        type="file"
        accept=".zip,application/zip"
        class="hidden"
        on:change={onZipSelected}
      />

      <div class="skills-list" role="list">
        {#if listInitialLoad}
          {#each [1, 2, 3, 4, 5] as _}
            <div class="skeleton mb-2 h-[4.25rem] w-full rounded-lg"></div>
          {/each}
        {:else if list.length === 0}
          <p class="px-1 py-6 text-center text-sm text-base-content/60">{$_('skills.emptyList')}</p>
        {:else}
          {#each list as s (s.slug || s.id)}
            {@const sid = String(s.slug || s.id)}
            {@const active = sid === routeId}
            <div class="skill-row {active ? 'skill-row-active' : ''}" role="listitem">
              <button
                type="button"
                class="skill-row-main"
                aria-current={active ? 'true' : undefined}
                on:click={() => selectRow(sid)}
              >
                <span class="skill-row-title">{s.title || $_('skills.noTitle')}</span>
                <span class="skill-row-slug">{sid}</span>
              </button>
              <button
                type="button"
                class="btn btn-ghost btn-square btn-sm skill-row-delete"
                title={$_('skills.deletePackage')}
                aria-label={$_('skills.deletePackage')}
                on:click={(ev) => removeSkillFromList(sid, ev)}
              >
                <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="h-4 w-4" aria-hidden="true">
                  <path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2m3 0v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6h14z" />
                  <path d="M10 11v6M14 11v6" />
                </svg>
              </button>
            </div>
          {/each}
        {/if}
      </div>
    </div>
  </aside>

  <section
    class="min-h-[320px] min-w-0 rounded-xl border border-base-300/40 bg-base-200 p-3 md:p-4"
  >
    <div class="grid min-h-[280px] gap-3">
      {#if !routeId}
        <p class="py-12 text-center text-base text-base-content/60">{$_('skills.placeholder')}</p>
      {:else if detailLoading}
        <div class="skeleton h-10 w-full"></div>
        <div class="skeleton h-24 w-full"></div>
        <div class="skeleton min-h-[200px] w-full"></div>
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
          <div class="skills-action-bar grid w-full min-w-[15rem] max-w-md grid-cols-3 gap-2 sm:w-auto">
            <button type="button" class="btn btn-outline w-full" disabled={!currentId} on:click={addReferenceFile}>
              {$_('skills.add')}
            </button>
            <button type="button" class="btn btn-primary w-full" disabled={saving || !currentId} on:click={saveSkill}>
              {saving ? $_('skills.saving') : $_('skills.save')}
            </button>
            <button
              type="button"
              class="btn btn-outline btn-error w-full"
              disabled={!currentId || !selectedPath || selectedPath === 'SKILL.md'}
              on:click={removeCurrentFile}
            >
              {$_('skills.deleteFileShort')}
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

        <div class="grid min-w-0 gap-3 lg:grid-cols-[minmax(0,12rem)_1fr]">
          <div class="min-w-0">
            <span class="label label-text text-sm">{$_('skills.filesLabel')}</span>
            <ul class="menu rounded-box border border-base-300 bg-base-100 p-1">
              {#each editablePaths as path (path)}
                <li>
                  <button
                    type="button"
                    class="justify-start text-left {selectedPath === path ? 'active' : ''}"
                    on:click={() => (selectedPath = path)}
                  >
                    <span class="truncate font-mono text-xs">{path}</span>
                  </button>
                </li>
              {/each}
            </ul>
          </div>

          <div class="form-control min-w-0 w-full">
            <span class="label label-text text-sm">{selectedPath}</span>
            {#if viewMode === 'edit'}
              <textarea
                class="textarea textarea-bordered min-h-[280px] w-full font-mono text-base leading-relaxed"
                rows="18"
                bind:value={fileContents[selectedPath]}
              ></textarea>
            {:else}
              <article
                class="md-preview rounded-lg border border-base-300 bg-base-100 p-4 text-base leading-relaxed text-base-content min-h-[280px] max-h-[60vh] overflow-y-auto"
              >
                {@html renderedHtml}
              </article>
            {/if}
          </div>
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

  .skills-aside {
    width: 100%;
  }

  .skills-list {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .skill-row {
    display: flex;
    align-items: stretch;
    gap: 0.25rem;
    border: 1px solid color-mix(in oklab, var(--color-base-content) 12%, transparent);
    border-radius: 0.625rem;
    background: var(--color-base-100);
    transition: border-color 0.15s ease, box-shadow 0.15s ease;
  }

  .skill-row:hover {
    border-color: color-mix(in oklab, var(--color-primary) 35%, transparent);
  }

  .skill-row-active {
    border-color: color-mix(in oklab, var(--color-primary) 55%, transparent);
    box-shadow: inset 3px 0 0 var(--color-primary);
  }

  .skill-row-main {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 0.2rem;
    padding: 0.65rem 0.75rem;
    text-align: left;
    background: transparent;
    border: none;
    cursor: pointer;
  }

  .skill-row-main:hover,
  .skill-row-main:focus-visible {
    outline: none;
    background: color-mix(in oklab, var(--color-base-content) 4%, transparent);
  }

  .skill-row-title {
    width: 100%;
    font-size: 0.95rem;
    font-weight: 600;
    line-height: 1.35;
    word-break: break-word;
  }

  .skill-row-slug {
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    font-size: 0.72rem;
    line-height: 1.3;
    color: color-mix(in oklab, var(--color-base-content) 55%, transparent);
    word-break: break-all;
  }

  .skill-row-delete {
    flex-shrink: 0;
    align-self: center;
    margin-right: 0.25rem;
    color: color-mix(in oklab, var(--color-error) 85%, var(--color-base-content));
    opacity: 0.75;
  }

  .skill-row-delete:hover {
    opacity: 1;
    background: color-mix(in oklab, var(--color-error) 12%, transparent);
  }

  .skills-action-bar :global(.btn) {
    min-width: 0;
    padding-left: 0.75rem;
    padding-right: 0.75rem;
  }
</style>
