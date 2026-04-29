<script>
  import { onMount } from 'svelte';
  import { route, goto } from '../lib/route.js';
  import { api } from '../lib/api.js';
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
        title: '新技能',
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
    if (!confirm('Delete this skill?')) return;
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

<h1>Skills</h1>
<p class="hint">
  Markdown 技能库；运行时通过 FTS 检索注入上下文。默认浏览为预览，可切换到编辑。
</p>

{#if listErr}
  <p class="err">{listErr}</p>
{/if}

<div class="layout">
  <aside class="side">
    <div class="sidehead">
      <button type="button" class="primary" on:click={createSkill}>新增</button>
      <button type="button" class="ghost" on:click={refreshList}>刷新</button>
    </div>
    <ul class="rows">
      {#each list as s (s.id)}
        <li>
          <button
            type="button"
            class:active={String(s.id) === routeId}
            on:click={() => selectRow(s.id)}
          >
            <span class="t">{s.title || '(无标题)'}</span>
            <span class="id">#{s.id}</span>
          </button>
        </li>
      {:else}
        <li class="empty">暂无条目</li>
      {/each}
    </ul>
  </aside>

  <section class="main">
    {#if !routeId}
      <div class="placeholder">选择左侧条目，或点击「新增」。</div>
    {:else}
      <div class="toolbar">
        <div class="tabs">
          <button
            type="button"
            class:active={viewMode === 'preview'}
            on:click={() => (viewMode = 'preview')}
          >预览</button>
          <button
            type="button"
            class:active={viewMode === 'edit'}
            on:click={() => (viewMode = 'edit')}
          >编辑</button>
        </div>
        <div class="actions">
          <button type="button" class="primary" disabled={saving || !currentId} on:click={saveSkill}>
            {saving ? '保存中…' : '保存'}
          </button>
          <button type="button" class="danger" disabled={!currentId} on:click={removeSkill}>删除</button>
        </div>
      </div>

      {#if detailErr}
        <p class="err">{detailErr}</p>
      {/if}

      <label class="field">
        <span>标题</span>
        <input type="text" bind:value={form.title} />
      </label>
      <label class="field">
        <span>摘要</span>
        <textarea rows="2" bind:value={form.summary}></textarea>
      </label>
      <label class="field">
        <span>标签</span>
        <input type="text" bind:value={form.tags} placeholder="逗号或空格分隔" />
      </label>

      <div class="bodyblock">
        <span class="label">正文 (Markdown)</span>
        {#if viewMode === 'edit'}
          <textarea class="body" rows="18" bind:value={form.body_md}></textarea>
        {:else}
          <article class="md-preview">{@html renderedHtml}</article>
        {/if}
      </div>
    {/if}
  </section>
</div>

<style>
  h1 { margin-top: 0; }
  .hint { color: #9aa3bb; margin-top: -0.25rem; margin-bottom: 1rem; font-size: 0.9rem; }
  .err { color: #f7768e; margin: 0.5rem 0; }
  .layout {
    display: grid;
    grid-template-columns: minmax(200px, 260px) 1fr;
    gap: 1.25rem;
    align-items: start;
  }
  @media (max-width: 800px) {
    .layout { grid-template-columns: 1fr; }
  }
  .side {
    background: #151923;
    border: 1px solid #232838;
    border-radius: 10px;
    padding: 0.65rem;
    max-height: calc(100vh - 8rem);
    overflow: auto;
  }
  .sidehead {
    display: flex;
    gap: 0.5rem;
    margin-bottom: 0.65rem;
  }
  .rows { list-style: none; margin: 0; padding: 0; }
  .rows li { margin: 0; }
  .rows button {
    width: 100%;
    text-align: left;
    padding: 0.45rem 0.5rem;
    border-radius: 6px;
    border: 1px solid transparent;
    background: transparent;
    color: #e7e9ee;
    cursor: pointer;
    display: flex;
    justify-content: space-between;
    gap: 0.5rem;
    align-items: center;
  }
  .rows button:hover { background: #1c2130; }
  .rows button.active { background: #1c2130; border-color: #324163; }
  .rows .t { font-size: 0.88rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .rows .id { font-size: 0.72rem; color: #7aa2f7; flex-shrink: 0; }
  .rows .empty { color: #6b7288; font-size: 0.85rem; padding: 0.5rem; }
  .main {
    background: #151923;
    border: 1px solid #232838;
    border-radius: 10px;
    padding: 1rem;
    min-height: 320px;
  }
  .placeholder { color: #6b7288; padding: 2rem 1rem; text-align: center; }
  .toolbar {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    align-items: center;
    gap: 0.75rem;
    margin-bottom: 1rem;
  }
  .tabs { display: flex; gap: 0.25rem; }
  .tabs button {
    background: #1c2130;
    border: 1px solid #2d3448;
    color: #a8b0c2;
    padding: 0.35rem 0.75rem;
    border-radius: 6px;
    cursor: pointer;
    font-size: 0.85rem;
  }
  .tabs button.active { color: #fff; border-color: #7aa2f7; }
  .actions { display: flex; gap: 0.5rem; }
  button.primary {
    background: #3d59a1;
    border: 1px solid #4a6cbd;
    color: #fff;
    padding: 0.35rem 0.75rem;
    border-radius: 6px;
    cursor: pointer;
    font-size: 0.85rem;
  }
  button.primary:disabled { opacity: 0.5; cursor: not-allowed; }
  button.ghost {
    background: transparent;
    border: 1px solid #2d3448;
    color: #a8b0c2;
    padding: 0.35rem 0.75rem;
    border-radius: 6px;
    cursor: pointer;
    font-size: 0.85rem;
  }
  button.danger {
    background: transparent;
    border: 1px solid #6b2f3a;
    color: #f7768e;
    padding: 0.35rem 0.75rem;
    border-radius: 6px;
    cursor: pointer;
    font-size: 0.85rem;
  }
  .field { display: flex; flex-direction: column; gap: 0.25rem; margin-bottom: 0.75rem; }
  .field span { font-size: 0.78rem; color: #9aa3bb; text-transform: uppercase; letter-spacing: 0.04em; }
  .field input, .field textarea {
    background: #0f1115;
    border: 1px solid #2d3448;
    border-radius: 6px;
    color: #e7e9ee;
    padding: 0.45rem 0.55rem;
    font-size: 0.9rem;
  }
  .bodyblock { margin-top: 0.5rem; }
  .bodyblock .label {
    display: block;
    font-size: 0.78rem;
    color: #9aa3bb;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    margin-bottom: 0.35rem;
  }
  textarea.body {
    width: 100%;
    background: #0f1115;
    border: 1px solid #2d3448;
    border-radius: 8px;
    color: #e7e9ee;
    padding: 0.65rem;
    font-family: ui-monospace, monospace;
    font-size: 0.85rem;
    resize: vertical;
    min-height: 280px;
  }
  .md-preview {
    background: #0f1115;
    border: 1px solid #2d3448;
    border-radius: 8px;
    padding: 0.85rem 1rem;
    font-size: 0.9rem;
    line-height: 1.55;
    min-height: 280px;
    overflow: auto;
  }
  .md-preview :global(h1), .md-preview :global(h2), .md-preview :global(h3) {
    margin: 0.75rem 0 0.4rem;
    font-weight: 600;
  }
  .md-preview :global(p) { margin: 0.5rem 0; }
  .md-preview :global(ul), .md-preview :global(ol) { margin: 0.5rem 0; padding-left: 1.25rem; }
  .md-preview :global(code) {
    background: #1c2130;
    padding: 0.12rem 0.35rem;
    border-radius: 4px;
    font-size: 0.85em;
  }
  .md-preview :global(pre) {
    background: #1c2130;
    border: 1px solid #2d3448;
    border-radius: 8px;
    padding: 0.65rem 0.75rem;
    overflow: auto;
  }
  .md-preview :global(pre code) { background: none; padding: 0; }
  .md-preview :global(a) { color: #7aa2f7; }
</style>
