<script>
  import { onMount, onDestroy } from 'svelte';
  import { api } from '../lib/api.js';
  import { _ } from '../lib/i18n.js';
  import { formatDateTime24 } from '../lib/datetime.js';

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
  let initialLoad = true;

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
    } finally {
      initialLoad = false;
    }
  }

  onMount(() => {
    refresh();
    timer = setInterval(refresh, 3000);
  });

  onDestroy(() => {
    if (timer) clearInterval(timer);
  });

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

  /** @param {string} lvl */
  function levelRowTint(lvl) {
    const l = String(lvl || 'info').toLowerCase();
    if (l === 'error') return 'bg-error/10 hover:bg-error/15';
    if (l === 'warn') return 'bg-warning/10 hover:bg-warning/15';
    return 'hover:bg-base-300/50';
  }

  /** @param {string} lvl */
  function levelBadgeClass(lvl) {
    const l = String(lvl || 'info').toLowerCase();
    if (l === 'error') return 'badge badge-error badge-xs uppercase shrink-0';
    if (l === 'warn') return 'badge badge-warning badge-xs uppercase shrink-0';
    return 'badge badge-ghost badge-xs uppercase shrink-0';
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

<h1 class="wb-page-title">{$_('logger.title')}</h1>
{#if error}
  <div role="alert" class="alert alert-soft alert-error mt-3 text-sm">{error}</div>
{/if}

<div class="flex flex-wrap items-end gap-3">
  <label class="form-control w-full max-w-[11rem]">
    <span class="label py-1 text-xs">{$_('logger.source')}</span>
    <select class="select select-bordered select-sm" bind:value={source}>
      <option value="persistent">{$_('logger.srcPersistent')}</option>
      <option value="recent">{$_('logger.srcRecent')}</option>
    </select>
  </label>
  <label class="form-control w-full max-w-[7rem]">
    <span class="label py-1 text-xs">{$_('logger.limit')}</span>
    <input
      type="number"
      min="20"
      max="500"
      class="input input-bordered input-sm"
      bind:value={limit}
      on:change={refresh}
    />
  </label>
  <label class="form-control w-full max-w-[9rem]">
    <span class="label py-1 text-xs">{$_('logger.level')}</span>
    <select class="select select-bordered select-sm" bind:value={level}>
      <option value="all">{$_('logger.optAll')}</option>
      <option value="info">{$_('logger.optInfo')}</option>
      <option value="warn">{$_('logger.optWarn')}</option>
      <option value="error">{$_('logger.optError')}</option>
    </select>
  </label>
  <label class="form-control w-full max-w-[9rem]">
    <span class="label py-1 text-xs">{$_('logger.phase')}</span>
    <select class="select select-bordered select-sm" bind:value={phase}>
      <option value="all">{$_('logger.optAll')}</option>
      <option value="start">{$_('logger.optStart')}</option>
      <option value="end">{$_('logger.optEnd')}</option>
      <option value="error">{$_('logger.optError')}</option>
    </select>
  </label>
  <label class="form-control w-full max-w-[8rem]">
    <span class="label py-1 text-xs">{$_('logger.lastMinutes')}</span>
    <input type="number" min="0" class="input input-bordered input-sm" bind:value={minutes} />
  </label>
</div>

<div class="mt-3 flex flex-wrap items-end gap-3">
  <label class="form-control min-w-[10rem] flex-1">
    <span class="label py-1 text-xs">{$_('logger.module')}</span>
    <input
      class="input input-bordered input-sm"
      placeholder={$_('logger.modulePh')}
      bind:value={moduleName}
    />
  </label>
  <label class="form-control min-w-[10rem] flex-1">
    <span class="label py-1 text-xs">{$_('logger.toolName')}</span>
    <input
      class="input input-bordered input-sm"
      placeholder={$_('logger.toolPh')}
      bind:value={toolName}
    />
  </label>
  <label class="form-control min-w-[10rem] flex-1">
    <span class="label py-1 text-xs">{$_('logger.traceId')}</span>
    <input
      class="input input-bordered input-sm"
      placeholder={$_('logger.tracePh')}
      bind:value={traceId}
    />
  </label>
  <button type="button" class="btn btn-sm btn-primary shrink-0" on:click={refresh}>{$_('logger.refresh')}</button>
</div>

{#if initialLoad}
  <div class="stats stats-vertical shadow-sm sm:stats-horizontal mt-4 w-full max-w-2xl rounded-lg border border-base-300 bg-base-200">
    {#each [1, 2, 3] as _}
      <div class="stat place-items-start py-3">
        <div class="skeleton mb-2 h-3 w-20"></div>
        <div class="skeleton h-8 w-16"></div>
      </div>
    {/each}
  </div>
  <div class="mt-4 grid grid-cols-1 gap-3 lg:grid-cols-[1.2fr_1fr]">
    <div class="rounded-lg border border-base-300 bg-base-200 p-2">
      <div class="flex flex-col gap-0">
        {#each [1, 2, 3, 4, 5, 6, 7, 8] as _}
          <div class="skeleton h-10 w-full rounded-none border-b border-base-300/50 last:border-b-0"></div>
        {/each}
      </div>
    </div>
    <div class="card card-border bg-base-200 shadow-sm">
      <div class="card-body gap-3 p-4">
        <div class="skeleton h-6 w-40"></div>
        <div class="skeleton h-4 w-full"></div>
        <div class="skeleton h-4 w-[75%]"></div>
        <div class="skeleton mt-4 h-32 w-full"></div>
      </div>
    </div>
  </div>
{:else}
<div class="stats stats-vertical shadow-sm sm:stats-horizontal mt-4 w-full max-w-2xl rounded-lg border border-base-300 bg-base-200">
  <div class="stat place-items-start py-3">
    <div class="stat-title text-xs">{$_('logger.statTotal')}</div>
    <div class="stat-value text-lg">{filteredLogs.length}</div>
  </div>
  <div class="stat place-items-start py-3">
    <div class="stat-title text-xs">{$_('logger.statFlows')}</div>
    <div class="stat-value text-lg">{groupedToolFlows.length}</div>
  </div>
  <div class="stat place-items-start py-3">
    <div class="stat-title text-xs">{$_('logger.statSource')}</div>
    <div class="stat-value wb-mono text-sm">{source}</div>
  </div>
</div>

<div class="mt-4 grid grid-cols-1 gap-3 lg:grid-cols-[1.2fr_1fr]">
  <div class="rounded-lg border border-base-300 bg-base-200 p-2 text-sm">
    <div class="max-h-[62vh] overflow-y-auto">
      {#each filteredLogs as e}
        <button
          type="button"
          class="flex w-full flex-wrap items-baseline gap-2 border-b border-base-300 px-2 py-2 text-left last:border-b-0 {levelRowTint(
            e.level,
          )}"
          on:click={() => (selected = e)}
        >
          <span class="wb-mono shrink-0 text-xs text-base-content/50">{formatDateTime24(e.time)}</span>
          <span class={levelBadgeClass(e.level)}>{e.level}</span>
          <span class="min-w-0 flex-1 text-balance text-base-content">{e.message}</span>
          <span class="badge badge-ghost badge-xs shrink-0 font-normal">{e.fields?.module || '-'}</span>
          <span class="badge badge-ghost badge-xs shrink-0 font-normal">{e.fields?.phase || '-'}</span>
          <span class="badge badge-ghost badge-xs shrink-0 font-normal max-w-[8rem] truncate"
            >{e.fields?.tool_name || '-'}</span
          >
        </button>
      {:else}
        <div class="p-6 text-center text-base-content/60">{$_('logger.logEmpty')}</div>
      {/each}
    </div>
  </div>

  <div class="card card-border bg-base-200 shadow-sm">
    <div class="card-body gap-3 p-4">
      <h2 class="card-title text-base">{$_('logger.detailTitle')}</h2>
      {#if selected}
        <div class="flex flex-col gap-1 text-sm">
          <div>
            <span class="font-semibold">{$_('logger.time')}</span>:
            <span class="wb-mono">{formatDateTime24(selected.time)}</span>
          </div>
          <div><span class="font-semibold">{$_('logger.lvl')}</span>: {selected.level}</div>
          <div><span class="font-semibold">{$_('logger.message')}</span>: {selected.message}</div>
          <div><span class="font-semibold">{$_('logger.sourceField')}</span>: {selected.source}</div>
        </div>
        <h3 class="text-sm font-semibold text-base-content/80">{$_('logger.fieldsTitle')}</h3>
        <div class="flex flex-col gap-2">
          <div class="rounded-lg border border-base-300 bg-base-100">
            <div class="flex flex-wrap items-center justify-between gap-2 border-b border-base-300 bg-base-200 px-3 py-2">
              <button type="button" class="btn btn-ghost btn-xs" on:click={() => toggleSection('interpreted')}>
                {expanded.interpreted ? '▼' : '▶'} {$_('logger.interpreted')}
              </button>
              <button type="button" class="btn btn-outline btn-xs" on:click={() => copyText(interpretedFieldsText)}>
                {$_('common.copy')}
              </button>
            </div>
            {#if expanded.interpreted || !isLargeText(interpretedFieldsText)}
              <pre
                class="m-0 max-h-[42vh] overflow-auto whitespace-pre-wrap break-words p-3 font-mono text-xs">{interpretedFieldsText || $_('common.emptyParen')}</pre>
            {:else}
              <div class="p-3 text-xs text-base-content/60">{$_('logger.largeHidden')}</div>
            {/if}
          </div>

          <div class="rounded-lg border border-base-300 bg-base-100">
            <div class="flex flex-wrap items-center justify-between gap-2 border-b border-base-300 bg-base-200 px-3 py-2">
              <button type="button" class="btn btn-ghost btn-xs" on:click={() => toggleSection('args')}>
                {expanded.args ? '▼' : '▶'} {$_('logger.args')}
              </button>
              <button type="button" class="btn btn-outline btn-xs" on:click={() => copyText(parsedArgsText)}>
                {$_('common.copy')}
              </button>
            </div>
            {#if expanded.args || !isLargeText(parsedArgsText)}
              <pre
                class="m-0 max-h-[42vh] overflow-auto whitespace-pre-wrap break-words p-3 font-mono text-xs">{parsedArgsText || $_('common.emptyParen')}</pre>
            {:else}
              <div class="p-3 text-xs text-base-content/60">{$_('logger.largeHidden')}</div>
            {/if}
          </div>

          {#if parsedCode}
            <div class="rounded-lg border border-base-300 bg-base-100">
              <div
                class="flex flex-wrap items-center justify-between gap-2 border-b border-base-300 bg-base-200 px-3 py-2"
              >
                <button type="button" class="btn btn-ghost btn-xs" on:click={() => toggleSection('code')}>
                  {expanded.code ? '▼' : '▶'} {$_('logger.argsCode')}
                </button>
                <button type="button" class="btn btn-outline btn-xs" on:click={() => copyText(parsedCode)}>
                  {$_('common.copy')}
                </button>
              </div>
              {#if expanded.code || !isLargeText(parsedCode)}
                <pre
                  class="m-0 max-h-[36vh] overflow-auto whitespace-pre p-3 font-mono text-xs leading-relaxed">{parsedCode}</pre>
              {:else}
                <div class="p-3 text-xs text-base-content/60">{$_('logger.largeCodeHidden')}</div>
              {/if}
            </div>
          {/if}

          <div class="rounded-lg border border-base-300 bg-base-100">
            <div class="flex flex-wrap items-center justify-between gap-2 border-b border-base-300 bg-base-200 px-3 py-2">
              <button type="button" class="btn btn-ghost btn-xs" on:click={() => toggleSection('result')}>
                {expanded.result ? '▼' : '▶'} {$_('logger.result')}
              </button>
              <button type="button" class="btn btn-outline btn-xs" on:click={() => copyText(parsedResultText)}>
                {$_('common.copy')}
              </button>
            </div>
            {#if expanded.result || !isLargeText(parsedResultText)}
              <pre
                class="m-0 max-h-[42vh] overflow-auto whitespace-pre-wrap break-words p-3 font-mono text-xs">{parsedResultText || $_('common.emptyParen')}</pre>
            {:else}
              <div class="p-3 text-xs text-base-content/60">{$_('logger.largeHidden')}</div>
            {/if}
          </div>

          <div class="rounded-lg border border-base-300 bg-base-100">
            <div class="flex flex-wrap items-center justify-between gap-2 border-b border-base-300 bg-base-200 px-3 py-2">
              <button type="button" class="btn btn-ghost btn-xs" on:click={() => toggleSection('raw')}>
                {expanded.raw ? '▼' : '▶'} {$_('logger.rawFields')}
              </button>
              <button type="button" class="btn btn-outline btn-xs" on:click={() => copyText(rawFieldsText)}>
                {$_('common.copy')}
              </button>
            </div>
            {#if expanded.raw}
              <pre
                class="m-0 max-h-[42vh] overflow-auto whitespace-pre-wrap break-words p-3 font-mono text-xs">{rawFieldsText || $_('common.emptyParen')}</pre>
            {/if}
          </div>
        </div>
      {:else}
        <p class="text-base-content/60">{$_('logger.pickRow')}</p>
      {/if}
    </div>
  </div>
</div>

<h2 class="wb-section-title">{$_('logger.flowsTitle')}</h2>
<div class="mt-2 max-h-[34vh] overflow-y-auto rounded-lg border border-base-300 bg-base-200 p-3">
  {#each groupedToolFlows as flow}
    <div class="card card-border bg-base-100 shadow-sm mb-3 last:mb-0">
      <div class="flex flex-wrap gap-x-4 gap-y-1 border-b border-base-300 bg-base-200 px-3 py-2 text-xs">
        <span class="font-semibold">{flow.tool_name || 'unknown-tool'}</span>
        <span class="font-mono text-base-content/80">{$_('logger.flowTrace')}={flow.trace_id || '-'}</span>
        <span class="font-mono text-base-content/80">{$_('logger.flowCall')}={flow.tool_call_id}</span>
        <span class="font-mono text-base-content/80">{$_('logger.flowSession')}={flow.session_id || '-'}</span>
      </div>
      <div class="divide-y divide-base-300">
        {#each flow.steps as step}
          <button
            type="button"
            class="flex w-full flex-wrap items-baseline gap-2 px-3 py-2 text-left text-xs {levelRowTint(step.level)}"
            on:click={() => (selected = step)}
          >
            <span class="wb-mono shrink-0 text-base-content/50">{formatDateTime24(step.time)}</span>
            <span class={levelBadgeClass(step.level)}>{step.level}</span>
            <span class="badge badge-ghost badge-xs shrink-0">{step.fields?.phase || '-'}</span>
            <span class="min-w-0 flex-1 text-base-content">{step.message}</span>
          </button>
        {/each}
      </div>
    </div>
  {/each}
  {#if groupedToolFlows.length === 0}
    <div class="p-6 text-center text-base-content/60">{$_('logger.noFlows')}</div>
  {/if}
</div>
{/if}
