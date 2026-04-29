<script>
  import { onMount, onDestroy } from 'svelte';
  import { goto } from '../lib/route.js';
  import { api, getOrchestratorBase } from '../lib/api.js';

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
      saveMsg = 'Saved.';
      await loadConfig();
      await refreshList();
    } catch (e) {
      cfgError = String(e);
    }
  }

  function rowValidationError(i) {
    const m = editModels[i];
    if (!m.name.trim() || !m.base_url.trim() || !m.model.trim()) {
      return 'Name, Base URL, and Model id are required for this row.';
    }
    if (!m.persisted && !(m.apiKeyInput || '').trim() && !m.has_api_key) {
      return 'New rows require an API key before saving.';
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
      saveMsg = 'Saved.';
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
      saveMsg = 'Active model updated.';
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
    testResult = 'Testing…';
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
        testResult = data.error || 'test already in progress';
        return;
      }
      if (!res.ok) {
        testResult = `${res.status} ${res.statusText}: ${data.error || JSON.stringify(data)}`;
        return;
      }
      if (data.success) {
        testResult = 'OK — upstream accepted the minimal completion request.';
      } else {
        testResult = data.error || '(no error message)';
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
  <div class="toolbar">
    <button type="button" class="back" on:click={() => goto('llm')}>← All LLM components</button>
  </div>
  <h1>LLM · {llmName}</h1>
  {#if listError}<div class="err">{listError}</div>{/if}
  {#if !loaded}
    <p class="hint">Loading…</p>
  {:else if !detail}
    <p class="hint">No registered component named <code>{llmName}</code> with type <code>llm</code>.</p>
  {:else}
    <div class="meta card">
      <div class="row"><span class="k">Registry status</span><span class="v">{detail.status || '—'}</span></div>
      <div class="row"><span class="k">Endpoint</span><span class="v mono">{detail.endpoint || '—'}</span></div>
    </div>

    <h2 class="section-title">Model profiles</h2>
    {#if cfgLoading}<p class="hint">Loading configuration…</p>{/if}
    {#if cfgError}<div class="err">{cfgError}</div>{/if}
    {#if saveMsg}<div class="ok">{saveMsg}</div>{/if}

    <p class="hint">
      Choose <b>Active</b> in the table for the profile used at runtime. Leave <b>API key</b> blank on saved rows to keep
      the stored key. New rows need a key before saving.
    </p>

    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>Active</th>
            <th>Name</th>
            <th>Base URL</th>
            <th>Model id</th>
            <th>API key</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr class="none-row">
            <td>
              <input
                type="radio"
                name="llm-active-{llmName}"
                value=""
                checked={activeModelId === ''}
                disabled={testInProgress}
                on:change={(e) => {
                  if (e.currentTarget.checked) applyActiveChange('');
                }}
              />
            </td>
            <td colspan="5"><span class="none-label">No active model</span></td>
          </tr>
          {#each editModels as m, i}
            <tr>
              <td>
                <input
                  type="radio"
                  name="llm-active-{llmName}"
                  value={m.id}
                  checked={activeModelId === m.id}
                  disabled={!m.persisted || testInProgress}
                  on:change={(e) => {
                    if (e.currentTarget.checked && m.persisted) applyActiveChange(m.id);
                  }}
                />
              </td>
              <td>
                <div class="name-cell">
                  <input bind:value={m.name} placeholder="display name" />
                  <span class="hint-id mono" title={m.id}>{m.id}</span>
                </div>
              </td>
              <td><input bind:value={m.base_url} class="wide" /></td>
              <td><input bind:value={m.model} /></td>
              <td>
                <input type="password" bind:value={m.apiKeyInput} placeholder={m.has_api_key ? '(unchanged if empty)' : 'required'} />
                {#if m.has_api_key && m.api_key_hint}
                  <div class="hint sm">stored: {m.api_key_hint}</div>
                {/if}
              </td>
              <td>
                {#if m.persisted}
                  <button type="button" class="btn sm danger" disabled={testInProgress} on:click={() => removeRow(i)}>Remove</button>
                  <button type="button" class="btn sm" disabled={testInProgress} on:click={() => runTest(m.id)}>Test</button>
                {:else}
                  <button type="button" class="btn sm primary" disabled={testInProgress} on:click={() => saveRow(i)}>Save</button>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>

    <div class="actions">
      <button type="button" class="btn" disabled={testInProgress} on:click={newRow}>Add model</button>
      <button type="button" class="btn primary" disabled={testInProgress} on:click={saveAll}>Save all</button>
      <button
        type="button"
        class="btn"
        disabled={testInProgress || !activeModelId}
        on:click={() => runTest(null)}>Test active model</button>
    </div>

    <h3>Test output</h3>
    <pre class="test-out">{testResult || '—'}</pre>
  {/if}
{:else}
  <h1>LLM</h1>
  <p class="hint">Components with <code>type=llm</code>. Open one to edit persisted model profiles (via orchestrator proxy).</p>
  {#if listError}<div class="err">{listError}</div>{/if}
  <div class="grid">
    {#each llmComponents as c}
      <button type="button" class="card" on:click={() => goto('llm', { id: c.name })}>
        <div class="row">
          <h2>{c.name || '—'}</h2>
          <span class="status">{c.status || 'unknown'}</span>
        </div>
        <p class="sub">{c.endpoint || '—'}</p>
      </button>
    {:else}
      <div class="empty">No <code>llm</code> components in the registry.</div>
    {/each}
  </div>
{/if}

<style>
  h1 {
    margin-top: 0;
  }
  h2.section-title {
    margin: 1rem 0 0.5rem;
    font-size: 1rem;
    color: #c7d0e6;
  }
  h3 {
    margin: 1rem 0 0.35rem;
    font-size: 0.9rem;
    color: #9aa3bb;
  }
  .toolbar {
    margin-bottom: 0.5rem;
  }
  .back {
    background: #1c2130;
    color: #c7d0e6;
    border: 1px solid #2d3448;
    border-radius: 6px;
    padding: 0.35rem 0.65rem;
    cursor: pointer;
    font-size: 0.85rem;
  }
  .back:hover {
    color: #fff;
    border-color: #324163;
  }
  .hint {
    color: #9aa3bb;
    margin-top: -0.25rem;
    margin-bottom: 0.75rem;
    font-size: 0.88rem;
  }
  .hint.sm {
    font-size: 0.75rem;
    margin: 0.2rem 0 0;
  }
  .err {
    color: #f5a9a9;
    margin-bottom: 0.75rem;
  }
  .ok {
    color: #85d8a7;
    margin-bottom: 0.5rem;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 0.9rem;
  }
  .card {
    background: #151923;
    border: 1px solid #232838;
    border-radius: 10px;
    padding: 0.85rem 0.95rem;
    text-align: left;
    color: inherit;
  }
  .meta.card {
    margin-bottom: 0.5rem;
  }
  button.card {
    cursor: pointer;
  }
  button.card:hover {
    border-color: #324163;
    background: #181d29;
  }
  .row {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 1rem;
  }
  .grid .card h2 {
    margin: 0;
    font-size: 1rem;
    font-weight: 600;
  }
  .sub {
    margin: 0.55rem 0 0;
    color: #9aa3bb;
    font-size: 0.82rem;
    word-break: break-all;
  }
  .status {
    font-size: 0.72rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    padding: 0.16rem 0.45rem;
    border-radius: 999px;
    border: 1px solid #2d3448;
    color: #a9b4cc;
  }
  .k {
    color: #8b93a8;
    min-width: 7rem;
  }
  .v {
    word-break: break-word;
  }
  .mono {
    font-family: ui-monospace, monospace;
    font-size: 0.8rem;
  }
  .empty {
    color: #9aa3bb;
    grid-column: 1 / -1;
  }
  .table-wrap {
    overflow-x: auto;
    margin-bottom: 0.75rem;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.82rem;
  }
  th,
  td {
    border: 1px solid #232838;
    padding: 0.35rem 0.45rem;
    vertical-align: top;
  }
  th {
    text-align: left;
    color: #8b93a8;
    font-weight: 600;
  }
  tr:hover .hint-id {
    opacity: 1;
  }
  .name-cell {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
  }
  .hint-id {
    font-size: 0.68rem;
    color: #6b7288;
    opacity: 0;
    max-width: 12rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    cursor: default;
  }
  .none-row .none-label {
    color: #8b93a8;
    font-size: 0.85rem;
  }
  input {
    width: 100%;
    max-width: 12rem;
    background: #0f1115;
    color: #e7e9ee;
    border: 1px solid #2d3448;
    border-radius: 4px;
    padding: 0.25rem 0.35rem;
    font-size: 0.82rem;
  }
  input.wide {
    max-width: 18rem;
  }
  td .wide {
    max-width: 22rem;
  }
  .sm {
    font-size: 0.75rem;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    margin-bottom: 0.5rem;
  }
  .btn {
    background: #1c2130;
    color: #c7d0e6;
    border: 1px solid #2d3448;
    border-radius: 6px;
    padding: 0.4rem 0.75rem;
    cursor: pointer;
    font-size: 0.85rem;
  }
  .btn:hover:not(:disabled) {
    color: #fff;
    border-color: #324163;
  }
  .btn:disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }
  .btn.primary {
    background: #2d4a7c;
    border-color: #3d5a9c;
    color: #fff;
  }
  .btn.sm {
    font-size: 0.72rem;
    padding: 0.2rem 0.45rem;
    display: block;
    margin-top: 0.25rem;
    width: 100%;
  }
  .btn.danger {
    border-color: #78323a;
    color: #f5a9a9;
  }
  .test-out {
    background: #0b0d12;
    border: 1px solid #232838;
    border-radius: 8px;
    padding: 0.65rem 0.75rem;
    white-space: pre-wrap;
    word-break: break-word;
    font-size: 0.78rem;
    color: #c7d0e6;
    max-height: 14rem;
    overflow: auto;
  }
  @media (max-width: 900px) {
    .grid {
      grid-template-columns: 1fr;
    }
  }
</style>
