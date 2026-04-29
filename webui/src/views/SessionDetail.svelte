<script>
  import { onMount, onDestroy, tick } from 'svelte';
  import { api } from '../lib/api.js';
  import { goto } from '../lib/route.js';
  import { renderMarkdown } from '../lib/markdown.js';
  import { _, locale, t, translate } from '../lib/i18n.js';

  export let sessionId;

  let session = null;
  let logs = [];
  let loggerEvents = [];
  let error = '';
  let timer;
  let lastMessageCount = 0;
  let latestAnchorEl;
  let shouldAutoScroll = true;
  let deletingCurrent = false;
  const AUTO_SCROLL_THRESHOLD_PX = 180;
  const channelThoughtRe = /<\|channel\|?>([\s\S]*?)(?=<\|message\|?>|$)/gi;
  const messageTagRe = /<\|\/?message\|?>/gi;
  const thinkTagRe = /<think>([\s\S]*?)<\/think>/gi;
  const thoughtTagRe = /<thought>([\s\S]*?)<\/thought>/gi;
  const reasoningTagRe = /<reasoning>([\s\S]*?)<\/reasoning>/gi;
  const looseMarkerTagRe = /<\/?\|?(?:channel|message|think|thought|reasoning)\|?>/gi;

  const emptySession = { id: sessionId, messages: [], expired: false };
  let wallClock = 0;
  let tickTimer;

  function fmtTs(ts) {
    if (!ts) return translate($locale, 'common.emDash');
    const d = new Date(ts);
    if (Number.isNaN(d.getTime())) return translate($locale, 'common.emDash');
    return d.toLocaleString();
  }

  function splitMessageContent(content) {
    const raw = String(content || '');
    if (!raw) {
      return { visible: '', thought: '' };
    }
    const thoughts = [];
    let visible = raw;

    visible = visible.replace(channelThoughtRe, (_all, thought) => {
      const t = String(thought || '').trim();
      if (t) thoughts.push(t);
      return '';
    });

    const captureTaggedThought = (_all, thought) => {
      const t = String(thought || '').trim();
      if (t) thoughts.push(t);
      return '';
    };
    visible = visible.replace(thinkTagRe, captureTaggedThought);
    visible = visible.replace(thoughtTagRe, captureTaggedThought);
    visible = visible.replace(reasoningTagRe, captureTaggedThought);
    visible = visible.replace(messageTagRe, '');
    visible = visible.replace(looseMarkerTagRe, '').trim();

    return {
      visible,
      thought: thoughts.join('\n\n').trim(),
    };
  }

  function shortText(v, n = 220) {
    const s = String(v || '');
    if (s.length <= n) return s;
    return `${s.slice(0, n)}...`;
  }

  function getPlanMarker(evt) {
    const message = String(evt?.message || '');
    const phase = String(evt?.fields?.phase || '');
    const status = String(evt?.fields?.plan_status || '');
    if (message === 'runtime_plan_confirmed' || phase === 'plan_confirmed' || status === 'confirmed') {
      return 'plan_confirmed';
    }
    if (message === 'runtime_plan' || phase === 'plan' || status === 'proposed') {
      return 'plan';
    }
    return '';
  }

  /** @param {string | undefined} lvl */
  function traceLevelBadgeClass(lvl) {
    const l = String(lvl || 'info').toLowerCase();
    if (l === 'error') return 'badge badge-error badge-xs uppercase shrink-0';
    if (l === 'warn') return 'badge badge-warning badge-xs uppercase shrink-0';
    return 'badge badge-ghost badge-xs uppercase shrink-0';
  }

  /** @param {string} role */
  function chatAlignClass(role) {
    if (role === 'user') return 'chat-end';
    return 'chat-start';
  }

  /** @param {string} role */
  function chatBubbleClass(role) {
    if (role === 'user') return 'chat-bubble chat-bubble-primary text-primary-content';
    if (role === 'system') return 'chat-bubble chat-bubble-ghost text-base-content/80 italic';
    return 'chat-bubble border border-white/10 bg-neutral text-neutral-content shadow-sm';
  }

  $: messages = session?.messages || [];
  $: assistantMessages = messages.filter((m) => m.role === 'assistant');
  $: messagesWithRealTokens = messages.filter((m) => Number.isFinite(m.total_tokens) && m.total_tokens > 0);
  $: totalRealTokens = messagesWithRealTokens.reduce((sum, m) => sum + m.total_tokens, 0);
  $: hasRealTokenData = messagesWithRealTokens.length > 0;
  $: assistantWithLatency = assistantMessages.filter(
    (m) => Number.isFinite(m.reply_latency_ms) && m.reply_latency_ms > 0,
  );
  $: avgAssistantLatency =
    assistantWithLatency.length > 0
      ? Math.round(
          assistantWithLatency.reduce((sum, m) => sum + m.reply_latency_ms, 0) /
            assistantWithLatency.length,
        )
      : null;
  $: chatCompletedLogs = logs.filter(
    (l) => l?.message === 'chat completed' && l?.fields?.session_id === sessionId,
  );
  $: sessionTraceEvents = (loggerEvents || [])
    .filter((e) => e?.fields?.session_id === sessionId)
    .sort((a, b) => new Date(b.time || 0) - new Date(a.time || 0));
  $: toolEventCount = sessionTraceEvents.filter((e) => (e?.fields?.module || '') === 'tool').length;
  $: if (messages.length > lastMessageCount && shouldAutoScroll) {
    scrollToLatestMessage();
  }
  $: lastMessageCount = messages.length;

  async function refresh() {
    try {
      const [sessionResp, logsResp, loggerResp] = await Promise.all([
        api.session(sessionId),
        api.logs(),
        api.loggerEvents(500),
      ]);
      if (sessionResp && sessionResp.success === false) {
        throw new Error(sessionResp.error || 'session detail api returned success=false');
      }
      if (logsResp && logsResp.success === false) {
        throw new Error(logsResp.error || 'logs api returned success=false');
      }
      if (loggerResp && loggerResp.success === false) {
        throw new Error(loggerResp.error || 'logger events api returned success=false');
      }
      session = sessionResp.session || emptySession;
      logs = logsResp.logs || [];
      loggerEvents = loggerResp.events || [];
      error = '';
    } catch (e) {
      error = String(e);
    }
  }

  async function removeCurrentSession() {
    if (!sessionId) return;
    if (!window.confirm(t('sessions.confirmDelete', { id: sessionId }))) return;
    deletingCurrent = true;
    try {
      const r = await api.deleteSession(sessionId);
      if (r && r.success === false) {
        throw new Error(r.error || 'delete session api returned success=false');
      }
      goto('sessions');
    } catch (e) {
      error = String(e);
    } finally {
      deletingCurrent = false;
    }
  }

  async function scrollToLatestMessage() {
    await tick();
    latestAnchorEl?.scrollIntoView({ behavior: 'smooth', block: 'end' });
  }

  function isNearPageBottom() {
    if (typeof window === 'undefined') return true;
    const doc = document.documentElement;
    const distanceToBottom = doc.scrollHeight - (window.scrollY + window.innerHeight);
    return distanceToBottom <= AUTO_SCROLL_THRESHOLD_PX;
  }

  function syncAutoScrollState() {
    shouldAutoScroll = isNearPageBottom();
  }

  function formatCountdown(sec) {
    if (sec == null || sec < 0) return translate($locale, 'common.emDash');
    const h = Math.floor(sec / 3600);
    const m = Math.floor((sec % 3600) / 60);
    const s = sec % 60;
    if (h > 0) return `${h}h ${m}m ${s}s`;
    if (m > 0) return `${m}m ${s}s`;
    return `${s}s`;
  }

  $: expiresAt = session?.expires_at;
  $: expired = session?.expired;
  $: secLeft = (() => {
    wallClock;
    if (expired) return 0;
    if (!expiresAt) return null;
    const ms = new Date(expiresAt).getTime() - Date.now();
    if (ms <= 0) return 0;
    return Math.floor(ms / 1000);
  })();

  onMount(() => {
    refresh();
    timer = setInterval(refresh, 2000);
    tickTimer = setInterval(() => {
      wallClock += 1;
    }, 1000);
    syncAutoScrollState();
    window.addEventListener('scroll', syncAutoScrollState, { passive: true });
  });

  onDestroy(() => {
    clearInterval(timer);
    if (tickTimer) clearInterval(tickTimer);
    window.removeEventListener('scroll', syncAutoScrollState);
  });
</script>

<div
  class="sticky top-0 z-10 -mx-4 mb-4 border-b border-base-300 bg-base-100/95 px-4 pb-4 pt-0 backdrop-blur-sm supports-[backdrop-filter]:bg-base-100/90 sm:-mx-6 sm:px-6"
>
  <div class="flex flex-wrap items-center gap-2 pt-1">
    <button type="button" class="btn btn-outline btn-sm" on:click={() => goto('sessions')}>
      {$_('sessionDetail.back')}
    </button>
    <button
      type="button"
      class="btn btn-outline btn-error btn-sm"
      disabled={deletingCurrent}
      on:click={removeCurrentSession}
    >
      {deletingCurrent ? $_('sessionDetail.deleting') : $_('sessionDetail.deleteSession')}
    </button>
    <h1 class="min-w-0 flex-1 text-xl font-semibold tracking-tight sm:text-2xl">
      {$_('sessionDetail.titlePrefix')}
      <span class="font-mono text-base text-primary">{sessionId}</span>
    </h1>
  </div>

  {#if error}
    <div role="alert" class="alert alert-soft alert-error mt-3 text-sm">{error}</div>
  {/if}

  <div class="mt-4 grid grid-cols-2 gap-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5">
    <div class="rounded-lg border border-base-300 bg-base-200 p-3">
      <div class="text-[0.65rem] font-medium uppercase tracking-wide text-base-content/60">
        {$_('sessionDetail.created')}
      </div>
      <div class="mt-0.5 text-sm font-medium text-base-content">{fmtTs(session?.created_at)}</div>
    </div>
    <div class="rounded-lg border border-base-300 bg-base-200 p-3">
      <div class="text-[0.65rem] font-medium uppercase tracking-wide text-base-content/60">
        {$_('sessionDetail.updated')}
      </div>
      <div class="mt-0.5 text-sm font-medium text-base-content">{fmtTs(session?.updated_at)}</div>
    </div>
    <div class="rounded-lg border border-base-300 bg-base-200 p-3">
      <div class="text-[0.65rem] font-medium uppercase tracking-wide text-base-content/60">
        {$_('sessionDetail.messages')}
      </div>
      <div class="mt-0.5 text-sm font-medium text-base-content">{messages.length}</div>
    </div>
    <div class="rounded-lg border border-base-300 bg-base-200 p-3">
      <div class="text-[0.65rem] font-medium uppercase tracking-wide text-base-content/60">
        {$_('sessionDetail.sessionStatus')}
      </div>
      <div class="mt-0.5 text-sm font-medium" class:text-warning={expired}>
        {expired ? $_('sessions.expired') : $_('sessionDetail.active')}
      </div>
    </div>
    <div class="rounded-lg border border-base-300 bg-base-200 p-3">
      <div class="text-[0.65rem] font-medium uppercase tracking-wide text-base-content/60">
        {$_('sessionDetail.idleCountdown')}
      </div>
      <div
        class="mt-0.5 text-sm font-medium"
        class:text-warning={secLeft === 0 && !expired}
      >
        {expired ? $_('common.emDash') : formatCountdown(secLeft)}
      </div>
    </div>
    <div class="rounded-lg border border-base-300 bg-base-200 p-3">
      <div class="text-[0.65rem] font-medium uppercase tracking-wide text-base-content/60">
        {$_('sessionDetail.totalTokensReal')}
      </div>
      <div class="mt-0.5 text-sm font-medium text-base-content">
        {hasRealTokenData ? totalRealTokens : $_('common.na')}
      </div>
    </div>
    <div class="rounded-lg border border-base-300 bg-base-200 p-3">
      <div class="text-[0.65rem] font-medium uppercase tracking-wide text-base-content/60">
        {$_('sessionDetail.avgLatency')}
      </div>
      <div class="mt-0.5 text-sm font-medium text-base-content">
        {avgAssistantLatency !== null ? `${avgAssistantLatency} ms` : $_('common.na')}
      </div>
    </div>
    <div class="rounded-lg border border-base-300 bg-base-200 p-3">
      <div class="text-[0.65rem] font-medium uppercase tracking-wide text-base-content/60">
        {$_('sessionDetail.traceEvents')}
      </div>
      <div class="mt-0.5 text-sm font-medium text-base-content">{chatCompletedLogs.length}</div>
    </div>
    <div class="rounded-lg border border-base-300 bg-base-200 p-3">
      <div class="text-[0.65rem] font-medium uppercase tracking-wide text-base-content/60">
        {$_('sessionDetail.runtimeEvents')}
      </div>
      <div class="mt-0.5 text-sm font-medium text-base-content">{sessionTraceEvents.length}</div>
    </div>
    <div class="rounded-lg border border-base-300 bg-base-200 p-3">
      <div class="text-[0.65rem] font-medium uppercase tracking-wide text-base-content/60">
        {$_('sessionDetail.toolEvents')}
      </div>
      <div class="mt-0.5 text-sm font-medium text-base-content">{toolEventCount}</div>
    </div>
  </div>
</div>

<div class="card card-border bg-base-200 shadow-sm mb-4">
  <div class="card-body gap-3 p-4">
    <h2 class="card-title text-base">{$_('sessionDetail.runtimeTimeline')}</h2>
    {#if sessionTraceEvents.length > 0}
      <div class="flex max-h-[34vh] flex-col gap-2 overflow-y-auto pr-1">
        {#each sessionTraceEvents.slice(0, 120) as evt}
          <details class="trace-item rounded-lg border border-base-300 bg-base-100 open:ring-1 open:ring-base-300">
            <summary
              class="flex cursor-pointer list-none flex-wrap items-center gap-2 px-3 py-2 text-xs marker:hidden [&::-webkit-details-marker]:hidden"
            >
              <span class="shrink-0 text-base-content/50">{fmtTs(evt.time)}</span>
              <span class={traceLevelBadgeClass(evt.level)}>{evt.level || 'info'}</span>
              {#if getPlanMarker(evt) === 'plan'}
                <span class="badge badge-info badge-xs shrink-0 whitespace-nowrap uppercase"
                  >{$_('sessionDetail.planMarkPlan')}</span
                >
              {:else if getPlanMarker(evt) === 'plan_confirmed'}
                <span class="badge badge-success badge-xs shrink-0 whitespace-nowrap uppercase"
                  >{$_('sessionDetail.planMarkConfirmed')}</span
                >
              {/if}
              <span class="min-w-0 flex-1 text-balance text-base-content">{evt.message || '-'}</span>
              <span class="badge badge-ghost badge-xs shrink-0 font-normal"
                >{evt.fields?.module || '-'} / {evt.fields?.phase || '-'}</span
              >
              {#if evt.fields?.step}
                <span class="badge badge-ghost badge-xs shrink-0 font-normal"
                  >{$_('common.step')} {evt.fields.step}</span
                >
              {/if}
              {#if evt.fields?.tool_name}
                <span class="badge badge-ghost badge-xs max-w-[10rem] shrink-0 truncate font-normal"
                  >{evt.fields.tool_name}</span
                >
              {/if}
            </summary>
            <div class="flex flex-col gap-2 border-t border-base-300 px-3 py-2 text-xs text-base-content/90">
              {#if evt.fields?.plan_status}
                <div><span class="font-semibold">{$_('sessionDetail.tracePlanStatus')}</span> {evt.fields.plan_status}</div>
              {/if}
              {#if evt.fields?.duration_ms}
                <div><span class="font-semibold">{$_('sessionDetail.traceDuration')}</span> {evt.fields.duration_ms}</div>
              {/if}
              {#if evt.fields?.trace_id}
                <div class="break-all font-mono">
                  <span class="font-semibold">{$_('sessionDetail.traceId')}</span> {evt.fields.trace_id}
                </div>
              {/if}
              {#if evt.fields?.error_message}
                <div class="text-error">
                  <span class="font-semibold">{$_('sessionDetail.traceError')}</span> {evt.fields.error_message}
                </div>
              {/if}
              {#if evt.fields?.args}
                <pre
                  class="m-0 rounded-md border border-base-300 bg-base-200 p-2 font-mono text-[0.7rem] whitespace-pre-wrap break-words">{shortText(
                    evt.fields.args,
                    1200,
                  )}</pre>
              {/if}
              {#if evt.fields?.result}
                <pre
                  class="m-0 rounded-md border border-base-300 bg-base-200 p-2 font-mono text-[0.7rem] whitespace-pre-wrap break-words">{shortText(
                    evt.fields.result,
                    1200,
                  )}</pre>
              {/if}
            </div>
          </details>
        {/each}
      </div>
    {:else}
      <p class="text-sm text-base-content/60">{$_('sessionDetail.traceEmpty')}</p>
    {/if}
  </div>
</div>

<div class="flex flex-col gap-3 pb-8">
  {#if session}
    {#each session.messages || [] as m}
      {@const parts = m.role === 'assistant' ? splitMessageContent(m.content || '') : null}
      <div class="chat w-full {chatAlignClass(m.role)}">
        <div class="chat-header flex flex-wrap items-baseline gap-x-2 gap-y-0.5 px-1 text-[0.7rem] text-base-content/60">
          <span class="font-semibold uppercase tracking-wide text-primary">{m.role}</span>
          <span>{fmtTs(m.timestamp)}</span>
          <span>
            {$_('sessionDetail.tokensLabel')}: {Number.isFinite(m.total_tokens) && m.total_tokens > 0
              ? m.total_tokens
              : $_('common.na')}
          </span>
          {#if m.role === 'assistant'}
            <span>
              {$_('sessionDetail.latencyLabel')}: {Number.isFinite(m.reply_latency_ms) && m.reply_latency_ms > 0
                ? `${m.reply_latency_ms} ms`
                : $_('common.na')}
            </span>
          {/if}
        </div>
        {#if m.role === 'assistant' && parts?.thought}
          <details class="mb-1 max-w-[min(100%,42rem)] rounded-lg border border-dashed border-base-300 bg-base-200/60 p-2 open:bg-base-200">
            <summary class="cursor-pointer text-xs text-base-content/70 marker:hidden [&::-webkit-details-marker]:hidden">
              {$_('sessionDetail.thoughtSummary')}
            </summary>
            <div class="markdown thought-body mt-2 text-sm leading-relaxed" dir="auto">
              {@html renderMarkdown(parts.thought)}
            </div>
          </details>
        {/if}
        <div class="{chatBubbleClass(m.role)} markdown max-w-[min(100%,42rem)] whitespace-pre-wrap text-sm leading-relaxed" dir="auto">
          {@html renderMarkdown(m.role === 'assistant' ? parts?.visible || '' : m.content || '')}
        </div>
      </div>
    {:else}
      <p class="py-8 text-center text-base-content/60">{$_('sessionDetail.noMessages')}</p>
    {/each}
    <div bind:this={latestAnchorEl}></div>
  {/if}
</div>

<style>
  .markdown :global(pre) {
    margin: 0.5rem 0;
    border-radius: 0.5rem;
    border: 1px solid var(--color-base-300);
    background: var(--color-base-300);
    padding: 0.6rem;
    overflow: auto;
  }
  .markdown :global(code) {
    border-radius: 0.25rem;
    border: 1px solid var(--color-base-300);
    background: var(--color-base-300);
    padding: 0.08rem 0.3rem;
    font-family: ui-monospace, monospace;
    font-size: 0.85em;
  }
  .markdown :global(pre code) {
    border: 0;
    padding: 0;
    background: transparent;
  }
  .markdown :global(a) {
    color: var(--color-primary);
    text-decoration: underline;
  }
</style>
