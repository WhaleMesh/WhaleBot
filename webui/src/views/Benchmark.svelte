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
  let expandedId = '';
  let initialLoad = true;
  /** @type {ReturnType<typeof setInterval> | undefined} */
  let timer;

  $: hasRunning = runs.some((r) => r.status === 'running');
  $: bestId = bestRunId(runs);

  function bestRunId(list) {
    let best = null;
    for (const r of list) {
      if (r.status !== 'completed') continue;
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

  function toggle(id) {
    expandedId = expandedId === id ? '' : id;
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

<h2 class="mt-6 text-lg font-semibold">{$_('benchmark.historyTitle')}</h2>
<div class="mt-2 min-w-0 w-full max-w-full overflow-x-auto rounded-lg border border-base-300">
  {#if initialLoad}
    <div class="p-4">
      {#each [1, 2, 3] as _sk}
        <div class="skeleton mb-3 h-10 w-full"></div>
      {/each}
    </div>
  {:else}
    <table class="table wb-table table-list text-base">
      <thead>
        <tr>
          <th>{$_('benchmark.thModel')}</th>
          <th>{$_('benchmark.thDate')}</th>
          <th>{$_('benchmark.thStatus')}</th>
          <th>{$_('benchmark.thTotal')}</th>
          <th>{$_('benchmark.thPlanGate')}</th>
          <th>{$_('benchmark.thToolCall')}</th>
          <th>{$_('benchmark.thReact')}</th>
          <th>{$_('benchmark.thE2E')}</th>
          <th>{$_('benchmark.thLatency')}</th>
          <th>{$_('benchmark.thTokens')}</th>
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
            <td class="whitespace-nowrap">
              <span class="font-medium">{r.model_name || '—'}</span>
              <span class="wb-mono ml-1 text-xs text-base-content/50">{r.model}</span>
              {#if r.id === bestId}
                <span class="badge badge-success badge-sm ml-1">{$_('benchmark.best')}</span>
              {/if}
            </td>
            <td class="whitespace-nowrap font-mono text-xs tabular-nums text-base-content/70">
              {r.started_at ? formatDateTime24(r.started_at) : '—'}
            </td>
            <td class="whitespace-nowrap">
              {#if r.status === 'running'}
                <span class="flex items-center gap-1 text-info">
                  <span class="loading loading-spinner loading-xs"></span>
                  <span class="text-xs">{r.progress || $_('benchmark.statusRunning')}</span>
                </span>
              {:else if r.status === 'failed'}
                <span class="badge badge-error badge-sm">{$_('benchmark.statusFailed')}</span>
              {:else}
                <span class="badge badge-ghost badge-sm">{$_('benchmark.statusCompleted')}</span>
              {/if}
            </td>
            <td class="wb-mono text-base font-semibold {scoreClass(r.scores?.total)}">
              {r.status === 'completed' ? fmtScore(r.scores?.total) : '—'}
            </td>
            <td class="wb-mono text-sm">{r.status === 'completed' ? fmtScore(r.scores?.plan_gate) : '—'}</td>
            <td class="wb-mono text-sm">{r.status === 'completed' ? fmtScore(r.scores?.tool_call) : '—'}</td>
            <td class="wb-mono text-sm">{r.status === 'completed' ? fmtScore(r.scores?.react) : '—'}</td>
            <td class="wb-mono text-sm">
              {#if r.e2e && r.e2e.status !== 'skipped'}
                <span class={scoreClass(r.e2e.score)}>{fmtScore(r.e2e.score)}</span>
              {:else}
                —
              {/if}
            </td>
            <td class="wb-mono text-sm">{fmtLatency(r.metrics?.avg_latency_ms)}</td>
            <td class="wb-mono text-sm">{fmtTokens(r.metrics?.total_tokens)}</td>
            <td class="whitespace-nowrap">
              <button
                type="button"
                class="btn btn-xs btn-outline"
                on:click={(event) => downloadRun(r, event)}
              >
                {$_('benchmark.download')}
              </button>
              <button
                type="button"
                class="btn btn-xs btn-outline btn-error"
                disabled={deletingId === r.id || r.status === 'running'}
                on:click={(event) => remove(r, event)}
              >
                {deletingId === r.id ? $_('benchmark.deleting') : $_('benchmark.delete')}
              </button>
            </td>
          </tr>
          {#if expandedId === r.id}
            <tr>
              <td colspan="11" class="bg-base-200/40 p-3">
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
            <td colspan="11" class="text-center text-base-content/60">{$_('benchmark.empty')}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>
