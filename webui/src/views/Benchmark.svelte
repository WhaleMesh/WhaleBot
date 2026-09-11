<script>
  import { onMount, onDestroy } from 'svelte';
  import { api } from '../lib/api.js';
  import { goto } from '../lib/route.js';
  import { _, t } from '../lib/i18n.js';
  import { formatDateTime24 } from '../lib/datetime.js';

  let runs = [];
  let error = '';
  let startError = '';
  /** @type {{name: string, model: string} | null} */
  let activeModel = null;
  let modelLoadErr = '';
  let includeE2E = false;
  let starting = false;
  let deletingId = '';
  let clearingAll = false;
  let expandedId = '';
  let initialLoad = true;
  /** @type {ReturnType<typeof setInterval> | undefined} */
  let timer;

  $: hasRunning = runs.some((r) => r.status === 'running');
  $: bestId = bestRunId(runs);

  function bestRunId(list) {
    let best = null;
    const caseSet = list.find((r) => r.status === 'completed')?.case_set || '';
    for (const r of list) {
      if (r.status !== 'completed' || (r.case_set || '') !== caseSet) continue;
      if (!best || (r.scores?.total ?? 0) > (best.scores?.total ?? 0)) best = r;
    }
    return best ? best.id : '';
  }

  async function refresh() {
    try {
      const r = await api.benchmarkRuns();
      runs = r.runs || [];
      error = '';
    } catch (e) {
      error = String(e);
    } finally {
      initialLoad = false;
    }
  }

  async function loadActiveModel() {
    try {
      const r = await api.llmConfigGet('llm-openai');
      const cfg = r.config || {};
      const m = (cfg.models || []).find((x) => x.id === cfg.active_model_id);
      activeModel = m ? { name: m.name, model: m.model } : null;
      modelLoadErr = '';
    } catch (e) {
      activeModel = null;
      modelLoadErr = String(e);
    }
  }

  async function start() {
    startError = '';
    starting = true;
    try {
      const r = await api.benchmarkStart({ include_e2e: includeE2E });
      if (r && r.success === false) throw new Error(r.error || 'benchmark start failed');
      await refresh();
    } catch (e) {
      startError = String(e);
    } finally {
      starting = false;
    }
  }

  async function remove(run, event) {
    event?.stopPropagation();
    if (!window.confirm(t('benchmark.confirmDelete', { model: run.model_name || run.model }))) return;
    deletingId = run.id;
    try {
      await api.benchmarkDelete(run.id);
      if (expandedId === run.id) expandedId = '';
      await refresh();
    } catch (e) {
      error = String(e);
    } finally {
      deletingId = '';
    }
  }

  async function clearAll() {
    if (!window.confirm(t('benchmark.confirmClearAll'))) return;
    clearingAll = true;
    try {
      await api.benchmarkDeleteAll();
      expandedId = '';
      await refresh();
    } catch (e) {
      error = String(e);
    } finally {
      clearingAll = false;
    }
  }

  function toggle(id) {
    expandedId = expandedId === id ? '' : id;
  }

  function csvCell(v) {
    const s = v == null ? '' : String(v);
    return /[",\n]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s;
  }

  function downloadCsv() {
    const cols = [
      'model_name', 'model', 'started_at', 'status', 'case_set',
      'total', 'bonus', 'plan_gate', 'tool_call', 'react', 'e2e_status', 'e2e_score',
      'llm_calls', 'avg_latency_ms', 'prompt_tokens', 'completion_tokens', 'total_tokens',
      'e2e_turns', 'e2e_wall_ms',
    ];
    const lines = [cols.join(',')];
    for (const r of runs) {
      lines.push([
        r.model_name, r.model, r.started_at, r.status, r.case_set,
        r.scores?.total, r.scores?.bonus, r.scores?.plan_gate, r.scores?.tool_call, r.scores?.react,
        r.e2e?.status, r.e2e?.score,
        r.metrics?.llm_calls, r.metrics?.avg_latency_ms,
        r.metrics?.prompt_tokens, r.metrics?.completion_tokens, r.metrics?.total_tokens,
        r.e2e?.turns, r.e2e?.wall_ms,
      ].map(csvCell).join(','));
    }
    const blob = new Blob([lines.join('\n') + '\n'], { type: 'text/csv;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `benchmark-runs-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-')}.csv`;
    a.click();
    URL.revokeObjectURL(url);
  }

  function downloadRun(run, event) {
    event?.stopPropagation();
    const blob = new Blob([JSON.stringify(run, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    const model = (run.model_name || run.model || 'model').replace(/[^\w.-]+/g, '_');
    const date = (run.started_at || '').slice(0, 19).replace(/[:T]/g, '-');
    a.href = url;
    a.download = `benchmark-${model}-${date || run.id}.json`;
    a.click();
    URL.revokeObjectURL(url);
  }

  function fmtScore(v) {
    if (v == null) return '—';
    return Math.round(v);
  }

  // Long local-model names blow up the table width. First keep only the
  // initial of every dash segment after the first, then hard-truncate:
  // "Gemma4-12B-QAT-Uncensored-HauhauCS-Balanced-GGUF" -> "Gemma4-1-Q-U-H-B-G".
  // Full name stays available via the cell's title attribute.
  function abbrevModel(name, max = 22) {
    if (!name) return '—';
    if (name.length <= max) return name;
    const parts = name.split('-');
    let out = parts.length > 1 ? parts[0] + '-' + parts.slice(1).map((p) => p.charAt(0)).join('-') : name;
    if (out.length > max) out = out.slice(0, max - 1) + '…';
    return out;
  }

  /** Short local wall time MM-DD HH:mm; full timestamp goes in title. */
  function fmtShortDate(ts) {
    if (!ts) return '—';
    const d = new Date(ts);
    if (Number.isNaN(d.getTime())) return '—';
    const p = (/** @type {number} */ n) => String(n).padStart(2, '0');
    return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
  }

  function fmtLatency(ms) {
    if (!ms) return '—';
    if (ms < 1000) return ms + 'ms';
    return (ms / 1000).toFixed(1) + 's';
  }

  function fmtTokens(n) {
    if (!n) return '—';
    if (n > 1000000) return (n / 1000000).toFixed(1) + 'M';
    if (n > 10000) return (n / 1000).toFixed(1) + 'k';
    return String(n);
  }

  function scoreClass(v) {
    if (v == null) return '';
    if (v >= 80) return 'text-success';
    if (v >= 50) return 'text-warning';
    return 'text-error';
  }

  onMount(() => {
    refresh();
    loadActiveModel();
    timer = setInterval(() => {
      refresh();
      loadActiveModel();
    }, 3000);
  });
  onDestroy(() => {
    if (timer) clearInterval(timer);
  });
</script>

<h1 class="wb-page-title">{$_('benchmark.title')}</h1>
<p class="mt-1 max-w-3xl text-sm text-base-content/70">{$_('benchmark.hint')}</p>

{#if error}
  <div role="alert" class="alert alert-soft alert-error mt-3 text-base">{error}</div>
{/if}

<div class="mt-4 rounded-lg border border-base-300 bg-base-100 p-4">
  <div class="flex flex-wrap items-center gap-4">
    <div class="min-w-0">
      <div class="text-xs uppercase tracking-wide text-base-content/50">{$_('benchmark.activeModel')}</div>
      {#if activeModel}
        <div class="mt-0.5 font-medium">{activeModel.name}</div>
        <div class="wb-mono text-xs text-base-content/60">{activeModel.model}</div>
      {:else}
        <div class="mt-0.5 text-sm text-warning">
          {$_('benchmark.noActiveModel')}
          <button type="button" class="link link-primary ml-1" on:click={() => goto('llm')}>LLM →</button>
        </div>
      {/if}
    </div>
    <div class="ml-auto flex flex-wrap items-center gap-3">
      <label class="label cursor-pointer gap-2">
        <input type="checkbox" class="checkbox checkbox-sm" bind:checked={includeE2E} />
        <span class="label-text text-sm">{$_('benchmark.includeE2E')}</span>
      </label>
      <button
        type="button"
        class="btn btn-primary"
        disabled={starting || hasRunning || !activeModel}
        on:click={start}
      >
        {#if starting}
          <span class="loading loading-spinner loading-sm"></span>
          {$_('benchmark.startingBtn')}
        {:else}
          {$_('benchmark.runBtn')}
        {/if}
      </button>
    </div>
  </div>
  {#if includeE2E}
    <p class="mt-2 max-w-3xl text-xs text-base-content/60">{$_('benchmark.e2eHint')}</p>
  {/if}
  {#if startError}
    <p class="mt-2 text-sm text-error">{startError}</p>
  {/if}
  {#if hasRunning}
    <p class="mt-2 flex items-center gap-2 text-sm text-info">
      <span class="loading loading-spinner loading-xs"></span>
      {$_('benchmark.runningNote')}
    </p>
  {/if}
</div>

<div class="mt-6 flex items-center gap-2">
  <h2 class="text-lg font-semibold">{$_('benchmark.historyTitle')}</h2>
  <div class="ml-auto flex items-center gap-2">
    <button type="button" class="btn btn-ghost btn-sm" disabled={!runs.length} on:click={downloadCsv}>
      <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" aria-hidden="true">
        <path stroke-linecap="round" stroke-linejoin="round" d="M3 16.5v2.25A2.25 2.25 0 0 0 5.25 21h13.5A2.25 2.25 0 0 0 21 18.75V16.5M16.5 12 12 16.5m0 0L7.5 12m4.5 4.5V3" />
      </svg>
      {$_('benchmark.downloadCsv')}
    </button>
    <button
      type="button"
      class="btn btn-ghost btn-sm text-error"
      disabled={clearingAll || !runs.some((r) => r.status !== 'running')}
      on:click={clearAll}
    >
      {#if clearingAll}
        <span class="loading loading-spinner loading-xs"></span>
      {/if}
      {$_('benchmark.clearAll')}
    </button>
  </div>
</div>
<div class="mt-2 min-w-0 w-full max-w-full overflow-x-auto rounded-lg border border-base-300">
  {#if initialLoad}
    <div class="p-4">
      {#each [1, 2, 3] as _sk}
        <div class="skeleton mb-3 h-10 w-full"></div>
      {/each}
    </div>
  {:else}
    <table class="table table-sm wb-table table-list">
      <thead>
        <tr>
          <th>{$_('benchmark.thModel')}</th>
          <th>{$_('benchmark.thCaseSet')}</th>
          <th>
            <div class="leading-tight">{$_('benchmark.thStatus')}</div>
            <div class="leading-tight">{$_('benchmark.thDate')}</div>
          </th>
          <th class="min-w-56 border-x border-base-300 bg-primary/5">{$_('benchmark.thTotal')}</th>
          <th class="border-x border-base-300 bg-warning/5">{$_('benchmark.thBonus')}</th>
          <th class="border-x border-base-300 bg-info/5">{$_('benchmark.thE2E')}</th>
          <th>
            <div class="leading-tight">{$_('benchmark.thLatency')}</div>
            <div class="leading-tight">{$_('benchmark.thTokens')}</div>
          </th>
          <th class="w-1 whitespace-nowrap">{$_('benchmark.thActions')}</th>
        </tr>
      </thead>
      <tbody>
        {#each runs as r (r.id)}
          <tr
            class="cursor-pointer hover:bg-base-300/15"
            on:click={() => toggle(r.id)}
            on:keydown={(e) => e.key === 'Enter' && toggle(r.id)}
            role="button"
            tabindex="0"
          >
            <td>
              <div class="flex max-w-56 items-center gap-1">
                <span class="truncate font-medium" title={r.model_name || ''}>{abbrevModel(r.model_name)}</span>
                {#if r.id === bestId}
                  <span class="badge badge-success badge-sm shrink-0">{$_('benchmark.best')}</span>
                {/if}
              </div>
              <div class="wb-mono max-w-56 truncate text-xs text-base-content/50" title={r.model || ''}>
                {abbrevModel(r.model)}
              </div>
            </td>
            <td class="wb-mono whitespace-nowrap text-xs">{r.case_set || $_('benchmark.legacyCaseSet')}</td>
            <td class="whitespace-nowrap">
              {#if r.status === 'running'}
                <span class="flex max-w-40 items-center gap-1 text-info">
                  <span class="loading loading-spinner loading-xs shrink-0"></span>
                  <span class="truncate text-xs" title={r.progress || ''}>{r.progress || $_('benchmark.statusRunning')}</span>
                </span>
              {:else if r.status === 'failed'}
                <span class="badge badge-error badge-sm">{$_('benchmark.statusFailed')}</span>
              {:else}
                <span class="badge badge-ghost badge-sm">{$_('benchmark.statusCompleted')}</span>
              {/if}
              <div
                class="mt-0.5 font-mono text-xs tabular-nums text-base-content/70"
                title={r.started_at ? formatDateTime24(r.started_at) : ''}
              >
                {fmtShortDate(r.started_at)}
              </div>
            </td>
            <td class="min-w-56 border-x border-base-300 bg-primary/5">
              <div class="wb-mono text-lg font-semibold {scoreClass(r.scores?.total)}">
                {r.status === 'completed' ? fmtScore(r.scores?.total) : '—'}
              </div>
              <div class="mt-1 flex gap-3 whitespace-nowrap text-xs text-base-content/60">
                <span>{$_('benchmark.thPlanGate')} <strong class="wb-mono text-base-content/80">{r.status === 'completed' ? fmtScore(r.scores?.plan_gate) : '—'}</strong></span>
                <span>{$_('benchmark.thToolCall')} <strong class="wb-mono text-base-content/80">{r.status === 'completed' ? fmtScore(r.scores?.tool_call) : '—'}</strong></span>
                <span>{$_('benchmark.thReact')} <strong class="wb-mono text-base-content/80">{r.status === 'completed' ? fmtScore(r.scores?.react) : '—'}</strong></span>
              </div>
            </td>
            <td class="border-x border-base-300 bg-warning/5 wb-mono text-base font-semibold">
              {r.status === 'completed' && String(r.case_set || '').startsWith('v3') ? fmtScore(r.scores?.bonus) : '—'}
            </td>
            <td class="border-x border-base-300 bg-info/5 wb-mono text-base font-semibold">
              {#if r.e2e && r.e2e.status !== 'skipped'}
                <span class={scoreClass(r.e2e.score)}>{fmtScore(r.e2e.score)}</span>
              {:else}
                —
              {/if}
            </td>
            <td class="wb-mono whitespace-nowrap text-xs">
              <div>{fmtLatency(r.metrics?.avg_latency_ms)}</div>
              <div class="text-base-content/60">{fmtTokens(r.metrics?.total_tokens)}</div>
            </td>
            <td class="whitespace-nowrap">
              <button
                type="button"
                class="btn btn-ghost btn-xs btn-square"
                aria-label={$_('benchmark.download')}
                title={$_('benchmark.download')}
                on:click={(event) => downloadRun(r, event)}
              >
                <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" aria-hidden="true">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M3 16.5v2.25A2.25 2.25 0 0 0 5.25 21h13.5A2.25 2.25 0 0 0 21 18.75V16.5M16.5 12 12 16.5m0 0L7.5 12m4.5 4.5V3" />
                </svg>
              </button>
              <button
                type="button"
                class="btn btn-ghost btn-xs btn-square text-error"
                disabled={deletingId === r.id || r.status === 'running'}
                aria-label={$_('benchmark.delete')}
                title={deletingId === r.id ? $_('benchmark.deleting') : $_('benchmark.delete')}
                on:click={(event) => remove(r, event)}
              >
                {#if deletingId === r.id}
                  <span class="loading loading-spinner loading-xs"></span>
                {:else}
                  <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" aria-hidden="true">
                    <path stroke-linecap="round" stroke-linejoin="round" d="m14.74 9-.346 9m-4.788 0L9.26 9m9.968-3.21c.342.052.682.107 1.022.166m-1.022-.165L18.16 19.673a2.25 2.25 0 0 1-2.244 2.077H8.084a2.25 2.25 0 0 1-2.244-2.077L4.772 5.79m14.456 0a48.108 48.108 0 0 0-3.478-.397m-12 .562c.34-.059.68-.114 1.022-.165m0 0a48.11 48.11 0 0 1 3.478-.397m7.5 0v-.916c0-1.18-.91-2.164-2.09-2.201a51.964 51.964 0 0 0-3.32 0c-1.18.037-2.09 1.022-2.09 2.201v.916m7.5 0a48.667 48.667 0 0 0-7.5 0" />
                  </svg>
                {/if}
              </button>
            </td>
          </tr>
          {#if expandedId === r.id}
            <tr>
              <td colspan="8" class="bg-base-200/40 p-3">
                {#if r.error}
                  <div role="alert" class="alert alert-soft alert-error mb-3 text-sm">{r.error}</div>
                {/if}
                {#if r.cases && r.cases.length}
                  <div class="overflow-x-auto">
                    <table class="table table-xs">
                      <thead>
                        <tr>
                          <th></th>
                          <th>{$_('benchmark.caseCol')}</th>
                          <th>{$_('benchmark.scoreCol')}</th>
                          <th>{$_('benchmark.detailCol')}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {#each r.cases as c}
                          <tr>
                            <td class="wb-mono whitespace-nowrap text-xs text-base-content/50">{c.category}</td>
                            <td class="whitespace-nowrap">{c.name}</td>
                            <td class="wb-mono {c.pass ? 'text-success' : c.score > 0 ? 'text-warning' : 'text-error'}">
                              {Math.round((c.score || 0) * 100)}%
                            </td>
                            <td class="max-w-xl text-xs text-base-content/70">{c.detail || ''}</td>
                          </tr>
                        {/each}
                      </tbody>
                    </table>
                  </div>
                {/if}
                {#if r.e2e}
                  <div class="mt-3">
                    <div class="text-sm font-semibold">{$_('benchmark.e2eCheckpoints')}</div>
                    {#if r.e2e.status === 'skipped'}
                      <p class="mt-1 text-sm text-warning">{t('benchmark.e2eSkipped', { reason: r.e2e.reason || '' })}</p>
                    {:else}
                      <p class="mt-1 text-xs text-base-content/60">
                        {t('benchmark.e2eTurns', { n: String(r.e2e.turns || 0) })}
                        · {t('benchmark.e2eWall', { s: String(Math.round((r.e2e.wall_ms || 0) / 1000)) })}
                        {#if r.e2e.reason}· {r.e2e.reason}{/if}
                      </p>
                      <ul class="mt-1 flex flex-col gap-0.5">
                        {#each r.e2e.checkpoints || [] as cp}
                          <li class="flex items-center gap-2 text-sm">
                            <span class={cp.pass ? 'text-success' : 'text-error'}>{cp.pass ? '✓' : '✗'}</span>
                            <span class="wb-mono">{cp.name}</span>
                            {#if cp.detail}<span class="text-xs text-base-content/60">{cp.detail}</span>{/if}
                          </li>
                        {/each}
                      </ul>
                      {#if (r.e2e.turn_replies || []).length}
                        <details class="mt-2">
                          <summary class="cursor-pointer text-xs font-semibold text-base-content/70">{$_('benchmark.e2eTurnReplies')}</summary>
                          <ol class="mt-1 flex flex-col gap-1">
                            {#each r.e2e.turn_replies as reply, i}
                              <li class="rounded border border-base-300 bg-base-200/40 p-2 text-xs">
                                <span class="wb-mono text-base-content/50">#{i + 1}</span>
                                <span class="whitespace-pre-wrap break-words">{reply}</span>
                              </li>
                            {/each}
                          </ol>
                        </details>
                      {/if}
                      {#if (r.e2e.tool_events || []).length}
                        <details class="mt-2">
                          <summary class="cursor-pointer text-xs font-semibold text-base-content/70">{$_('benchmark.e2eToolEvents')}</summary>
                          <ul class="mt-1 flex flex-col gap-0.5">
                            {#each r.e2e.tool_events as ev}
                              <li class="text-xs">
                                <span class={ev.event === 'tool_call_error' ? 'text-error' : 'text-base-content/50'}>{ev.event === 'tool_call_error' ? '✗' : '·'}</span>
                                <span class="wb-mono">{ev.tool || ''}</span>
                                {#if ev.step}<span class="text-base-content/50">step {ev.step}</span>{/if}
                                {#if ev.args}<span class="wb-mono break-all text-base-content/60">{ev.args}</span>{/if}
                                {#if ev.error}<span class="text-error">{ev.error}</span>{/if}
                              </li>
                            {/each}
                          </ul>
                        </details>
                      {/if}
                    {/if}
                  </div>
                {/if}
              </td>
            </tr>
          {/if}
        {:else}
          <tr>
            <td colspan="8" class="text-center text-base-content/60">{$_('benchmark.empty')}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>
