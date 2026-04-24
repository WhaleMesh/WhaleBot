<script>
  import { onMount, onDestroy } from 'svelte';
  import { api } from '../lib/api.js';

  let persistentEvents = [];
  let recentLogs = [];
  let source = 'persistent';
  let limit = 200;
  let level = 'all';
  let moduleName = '';
  let toolName = '';
  let traceId = '';
  let phase = 'all';
  let minutes = 120;
  let selected = null;
  let expanded = {};
  let error = '';
  let timer;

  function normalize(items = [], sourceName = 'persistent') {
    return items.map((item, idx) => ({
      id: item.id ?? `${sourceName}-${idx}-${item.time}-${item.message}`,
      time: item.time,
      level: item.level || 'info',
      message: item.message || '',
      fields: item.fields || {},
      source: sourceName,
    }));
  }

  async function refresh() {
    try {
      const [persistent, recent] = await Promise.all([
        api.loggerEvents(limit),
        api.logs(),
      ]);
      persistentEvents = normalize((persistent.events || []).slice(), 'persistent').reverse();
      recentLogs = normalize((recent.logs || []).slice(), 'recent').reverse();
      error = '';
    } catch (e) {
      error = String(e);
    }
  }

  onMount(() => {
    refresh();
    timer = setInterval(refresh, 3000);
  });

  onDestroy(() => clearInterval(timer));

  $: activeLogs = source === 'persistent' ? persistentEvents : recentLogs;
  $: cutoff = minutes > 0 ? Date.now() - minutes * 60 * 1000 : 0;
  $: filteredLogs = activeLogs.filter((item) => {
    const t = new Date(item.time).getTime();
    const f = item.fields || {};
    if (cutoff > 0 && Number.isFinite(t) && t < cutoff) return false;
    if (level !== 'all' && (item.level || 'info') !== level) return false;
    if (phase !== 'all' && (f.phase || '') !== phase) return false;
    if (moduleName && !(f.module || '').toLowerCase().includes(moduleName.toLowerCase())) return false;
    if (toolName && !(f.tool_name || '').toLowerCase().includes(toolName.toLowerCase())) return false;
    if (traceId && !(f.trace_id || '').toLowerCase().includes(traceId.toLowerCase())) return false;
    return true;
  });
  $: groupedToolFlows = buildToolFlows(filteredLogs);

  function buildToolFlows(logItems) {
    const grouped = new Map();
    for (const item of logItems) {
      const f = item.fields || {};
      if ((f.module || '') !== 'tool') continue;
      if (!f.tool_call_id) continue;
      const key = `${f.trace_id || 'trace-unknown'}::${f.tool_call_id}`;
      if (!grouped.has(key)) {
        grouped.set(key, {
          key,
          trace_id: f.trace_id || '',
          tool_call_id: f.tool_call_id,
          tool_name: f.tool_name || '',
          session_id: f.session_id || '',
          steps: [],
        });
      }
      grouped.get(key).steps.push(item);
    }
    return Array.from(grouped.values())
      .map((group) => ({
        ...group,
        steps: group.steps.slice().sort((a, b) => new Date(a.time) - new Date(b.time)),
      }))
      .sort((a, b) => {
        const ta = a.steps.length ? new Date(a.steps[a.steps.length - 1].time).getTime() : 0;
        const tb = b.steps.length ? new Date(b.steps[b.steps.length - 1].time).getTime() : 0;
        return tb - ta;
      });
  }

  function parseJSONSafe(raw) {
    if (typeof raw !== 'string' || !raw.trim()) return null;
    try {
      return JSON.parse(raw);
    } catch {
      return null;
    }
  }

  function parseNestedJSON(raw, maxDepth = 2) {
    let current = raw;
    for (let i = 0; i < maxDepth; i += 1) {
      if (typeof current !== 'string') break;
      const parsed = parseJSONSafe(current);
      if (parsed === null) break;
      current = parsed;
    }
    return current;
  }

  function parseKVText(raw) {
    if (typeof raw !== 'string') return null;
    const text = raw.trim();
    if (!text.includes('=')) return null;

    // Avoid treating source code / expressions as key-value text.
    if (
      text.includes('package main') ||
      text.includes('func ') ||
      text.includes(':=') ||
      text.includes('==') ||
      text.includes('!=') ||
      text.includes('<=') ||
      text.includes('>=')
    ) {
      return null;
    }

    const chunks = text
      .split(/\r?\n/)
      .map((s) => s.trim())
      .filter(Boolean);
    const out = {};
    let pairs = 0;
    let eqCandidateLines = 0;
    const kvLine = /^([A-Za-z_][A-Za-z0-9_.-]{0,127})\s*=\s*(.+)$/;

    for (const chunk of chunks) {
      if (chunk.startsWith('#') || chunk.startsWith('//')) continue;
      if (!chunk.includes('=')) continue;
      eqCandidateLines += 1;
      const m = chunk.match(kvLine);
      if (!m) continue;
      const key = m[1];
      const val = m[2];
      out[key] = val.trim();
      pairs += 1;
    }

    // Require high confidence before converting to object.
    if (pairs < 2) return null;
    if (eqCandidateLines > 0 && pairs / eqCandidateLines < 0.8) return null;
    return out;
  }

  function deepInterpret(value, depth = 0) {
    if (depth > 4) return value;
    if (Array.isArray(value)) {
      return value.map((item) => deepInterpret(item, depth + 1));
    }
    if (value && typeof value === 'object') {
      const out = {};
      for (const [k, v] of Object.entries(value)) {
        out[k] = deepInterpret(v, depth + 1);
      }
      return out;
    }
    if (typeof value !== 'string') return value;

    const nestedJSON = parseNestedJSON(value, 3);
    if (nestedJSON !== value) {
      return deepInterpret(nestedJSON, depth + 1);
    }

    const kv = parseKVText(value);
    if (kv) {
      return deepInterpret(kv, depth + 1);
    }
    return value;
  }

  function asPretty(value) {
    if (value == null) return '';
    if (typeof value === 'string') return value;
    try {
      return JSON.stringify(value, null, 2);
    } catch {
      return String(value);
    }
  }

  function isLargeText(value) {
    return (value || '').length > 2000;
  }

  function toggleSection(key) {
    expanded = { ...expanded, [key]: !expanded[key] };
  }

  async function copyText(value) {
    try {
      await navigator.clipboard.writeText(value || '');
    } catch {
      // ignore clipboard permission errors in read-only contexts
    }
  }

  $: selectedFields = selected?.fields || {};
  $: interpretedFields = deepInterpret(selectedFields);
  $: parsedArgs = deepInterpret(interpretedFields?.args ?? selectedFields.args ?? '');
  $: parsedResult = deepInterpret(interpretedFields?.result ?? selectedFields.result ?? '');
  $: parsedArgsText = asPretty(parsedArgs);
  $: parsedResultText = asPretty(parsedResult);
  $: interpretedFieldsText = asPretty(interpretedFields);
  $: rawFieldsText = JSON.stringify(selectedFields, null, 2);
  $: parsedCode = parsedArgs && typeof parsedArgs === 'object' ? parsedArgs.code : '';
</script>

<h1>Logger</h1>
{#if error}<div class="err">{error}</div>{/if}

<div class="toolbar">
  <label>Source
    <select bind:value={source}>
      <option value="persistent">Persistent (logger sqlite)</option>
      <option value="recent">Orchestrator recent ring</option>
    </select>
  </label>
  <label>Limit
    <input type="number" min="20" max="500" bind:value={limit} on:change={refresh} />
  </label>
  <label>Level
    <select bind:value={level}>
      <option value="all">all</option>
      <option value="info">info</option>
      <option value="warn">warn</option>
      <option value="error">error</option>
    </select>
  </label>
  <label>Phase
    <select bind:value={phase}>
      <option value="all">all</option>
      <option value="start">start</option>
      <option value="end">end</option>
      <option value="error">error</option>
    </select>
  </label>
  <label>Last minutes
    <input type="number" min="0" bind:value={minutes} />
  </label>
</div>

<div class="toolbar">
  <label>Module
    <input placeholder="tool / react / session" bind:value={moduleName} />
  </label>
  <label>Tool name
    <input placeholder="manage_user_docker" bind:value={toolName} />
  </label>
  <label>Trace ID
    <input placeholder="trace_xxx" bind:value={traceId} />
  </label>
  <button on:click={refresh}>Refresh</button>
</div>

<div class="stats">
  <span>Total: {filteredLogs.length}</span>
  <span>Tool Flows: {groupedToolFlows.length}</span>
  <span>Selected Source: {source}</span>
</div>

<div class="layout">
  <div class="logs">
    {#each filteredLogs as e}
      <button class="log {e.level}" on:click={() => (selected = e)}>
        <span class="t">{new Date(e.time).toLocaleString()}</span>
        <span class="lvl">{e.level}</span>
        <span class="msg">{e.message}</span>
        <span class="mod">{e.fields?.module || '-'}</span>
        <span class="phase">{e.fields?.phase || '-'}</span>
        <span class="tool">{e.fields?.tool_name || '-'}</span>
      </button>
    {:else}
      <div class="empty">No log entries under current filters.</div>
    {/each}
  </div>

  <div class="detail">
    <h2>Event Detail</h2>
    {#if selected}
      <div class="meta">
        <div><b>Time</b>: {new Date(selected.time).toLocaleString()}</div>
        <div><b>Level</b>: {selected.level}</div>
        <div><b>Message</b>: {selected.message}</div>
        <div><b>Source</b>: {selected.source}</div>
      </div>
      <h3>Fields</h3>
      <div class="sections">
        <div class="section">
          <div class="sectionHead">
            <button class="toggleBtn" on:click={() => toggleSection('interpreted')}>
              {expanded.interpreted ? '▼' : '▶'} Interpreted fields
            </button>
            <button class="copyBtn" on:click={() => copyText(interpretedFieldsText)}>Copy</button>
          </div>
          {#if expanded.interpreted || !isLargeText(interpretedFieldsText)}
            <pre>{interpretedFieldsText || '(empty)'}</pre>
          {:else}
            <div class="collapsedHint">Large content hidden. Click to expand.</div>
          {/if}
        </div>

        <div class="section">
          <div class="sectionHead">
            <button class="toggleBtn" on:click={() => toggleSection('args')}>
              {expanded.args ? '▼' : '▶'} Args
            </button>
            <button class="copyBtn" on:click={() => copyText(parsedArgsText)}>Copy</button>
          </div>
          {#if expanded.args || !isLargeText(parsedArgsText)}
            <pre>{parsedArgsText || '(empty)'}</pre>
          {:else}
            <div class="collapsedHint">Large content hidden. Click to expand.</div>
          {/if}
        </div>

        {#if parsedCode}
          <div class="section">
            <div class="sectionHead">
              <button class="toggleBtn" on:click={() => toggleSection('code')}>
                {expanded.code ? '▼' : '▶'} Args.code (Go)
              </button>
              <button class="copyBtn" on:click={() => copyText(parsedCode)}>Copy</button>
            </div>
            {#if expanded.code || !isLargeText(parsedCode)}
              <pre class="codeBlock">{parsedCode}</pre>
            {:else}
              <div class="collapsedHint">Large code hidden. Click to expand.</div>
            {/if}
          </div>
        {/if}

        <div class="section">
          <div class="sectionHead">
            <button class="toggleBtn" on:click={() => toggleSection('result')}>
              {expanded.result ? '▼' : '▶'} Result
            </button>
            <button class="copyBtn" on:click={() => copyText(parsedResultText)}>Copy</button>
          </div>
          {#if expanded.result || !isLargeText(parsedResultText)}
            <pre>{parsedResultText || '(empty)'}</pre>
          {:else}
            <div class="collapsedHint">Large content hidden. Click to expand.</div>
          {/if}
        </div>

        <div class="section">
          <div class="sectionHead">
            <button class="toggleBtn" on:click={() => toggleSection('raw')}>
              {expanded.raw ? '▼' : '▶'} Raw fields
            </button>
            <button class="copyBtn" on:click={() => copyText(rawFieldsText)}>Copy</button>
          </div>
          {#if expanded.raw}
            <pre>{rawFieldsText || '(empty)'}</pre>
          {/if}
        </div>
      </div>
    {:else}
      <div class="empty">Click a log row to inspect complete payload.</div>
    {/if}
  </div>
</div>

<h2>Tool Call Flows</h2>
<div class="flows">
  {#each groupedToolFlows as flow}
    <div class="flow">
      <div class="flowHead">
        <span><b>{flow.tool_name || 'unknown-tool'}</b></span>
        <span>trace={flow.trace_id || '-'}</span>
        <span>call={flow.tool_call_id}</span>
        <span>session={flow.session_id || '-'}</span>
      </div>
      {#each flow.steps as step}
        <button class="flowStep {step.level}" on:click={() => (selected = step)}>
          <span class="t">{new Date(step.time).toLocaleString()}</span>
          <span class="lvl">{step.level}</span>
          <span>{step.fields?.phase || '-'}</span>
          <span>{step.message}</span>
        </button>
      {/each}
    </div>
  {/each}
  {#if groupedToolFlows.length === 0}
    <div class="empty">No tool flows under current filters.</div>
  {/if}
</div>

<style>
  h1 { margin-top: 0; }
  h2 { margin-top: 1.1rem; margin-bottom: 0.5rem; font-size: 1rem; }
  h3 { margin: 0.6rem 0 0.4rem; font-size: 0.9rem; }
  .toolbar {
    display: flex;
    gap: 0.75rem;
    flex-wrap: wrap;
    margin-bottom: 0.75rem;
    align-items: flex-end;
  }
  .toolbar label {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
    font-size: 0.78rem;
    color: #a8b0c2;
  }
  .toolbar input, .toolbar select {
    min-width: 160px;
    background: #111522;
    border: 1px solid #2a3042;
    color: #dfe3ee;
    border-radius: 6px;
    padding: 0.35rem 0.45rem;
    font-size: 0.82rem;
  }
  .toolbar button {
    background: #1f2a43;
    border: 1px solid #31456f;
    border-radius: 6px;
    color: #fff;
    height: 2rem;
    padding: 0 0.8rem;
    cursor: pointer;
  }
  .stats {
    display: flex;
    gap: 1rem;
    color: #9aa3bb;
    font-size: 0.8rem;
    margin-bottom: 0.8rem;
  }
  .layout {
    display: grid;
    grid-template-columns: 1.2fr 1fr;
    gap: 0.8rem;
  }
  .logs {
    background: #0c0f15;
    border: 1px solid #232838;
    border-radius: 8px;
    padding: 0.5rem;
    font-family: ui-monospace, monospace;
    font-size: 0.82rem;
    max-height: 62vh;
    overflow: auto;
  }
  .log {
    width: 100%;
    padding: 0.4rem 0.5rem;
    border-bottom: 1px dashed #1b2030;
    display: flex;
    gap: 0.5rem;
    flex-wrap: wrap;
    background: transparent;
    border-left: none;
    border-right: none;
    border-top: none;
    color: inherit;
    text-align: left;
    cursor: pointer;
  }
  .log:hover { background: #131827; }
  .log:last-child { border-bottom: none; }
  .t { color: #6c7389; }
  .lvl {
    color: #8ea6ff;
    text-transform: uppercase;
    font-weight: 600;
    font-size: 0.75rem;
    padding-top: 0.1rem;
  }
  .log.error .lvl { color: #f16a6a; }
  .log.warn .lvl { color: #f5c469; }
  .msg { color: #dfe3ee; }
  .mod, .phase, .tool { color: #a8b0c2; font-size: 0.75rem; background: #1a2030; border-radius: 4px; padding: 0.05rem 0.3rem; }
  .detail {
    background: #0c0f15;
    border: 1px solid #232838;
    border-radius: 8px;
    padding: 0.7rem;
    min-height: 14rem;
  }
  .meta { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.82rem; color: #dfe3ee; }
  .sections { display: flex; flex-direction: column; gap: 0.6rem; }
  .section { border: 1px solid #252d41; border-radius: 8px; overflow: hidden; }
  .sectionHead {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 0.5rem;
    background: #151b2b;
    border-bottom: 1px solid #252d41;
    padding: 0.35rem 0.45rem;
  }
  .toggleBtn, .copyBtn {
    background: #1d2740;
    border: 1px solid #31456f;
    color: #dfe3ee;
    border-radius: 6px;
    padding: 0.2rem 0.45rem;
    font-size: 0.76rem;
    cursor: pointer;
  }
  .copyBtn { background: #1a3040; border-color: #355a74; }
  .collapsedHint {
    padding: 0.55rem 0.65rem;
    color: #8d97b0;
    font-size: 0.78rem;
  }
  pre {
    margin: 0;
    background: #111522;
    border: 1px solid #252d41;
    border-radius: 8px;
    padding: 0.6rem;
    max-height: 42vh;
    overflow: auto;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .codeBlock {
    white-space: pre;
    font-size: 0.78rem;
    line-height: 1.4;
    max-height: 36vh;
  }
  .flows {
    background: #0c0f15;
    border: 1px solid #232838;
    border-radius: 8px;
    padding: 0.7rem;
    max-height: 34vh;
    overflow: auto;
  }
  .flow { border: 1px solid #21293c; border-radius: 8px; margin-bottom: 0.7rem; }
  .flow:last-child { margin-bottom: 0; }
  .flowHead {
    display: flex;
    flex-wrap: wrap;
    gap: 0.6rem;
    padding: 0.45rem 0.55rem;
    border-bottom: 1px solid #21293c;
    color: #cdd5e8;
    font-size: 0.78rem;
    background: #121828;
  }
  .flowStep {
    width: 100%;
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    padding: 0.35rem 0.55rem;
    border-bottom: 1px dashed #1f2637;
    font-size: 0.78rem;
    background: transparent;
    border-left: none;
    border-right: none;
    border-top: none;
    color: #cfd7ea;
    text-align: left;
    cursor: pointer;
  }
  .flowStep:hover { background: #131a2b; }
  .flowStep.error { background: #2a1418; }
  .flowStep.warn { background: #2a2112; }
  .flowStep:last-child { border-bottom: none; }
  .empty { padding: 1rem; color: #6c7389; }
  .err {
    background: #40161a;
    border: 1px solid #8a2b32;
    color: #f6c6cb;
    padding: 0.6rem 0.9rem;
    border-radius: 6px;
    margin-bottom: 1rem;
  }
  @media (max-width: 1050px) {
    .layout { grid-template-columns: 1fr; }
  }
</style>
