<script>
  import { onMount, onDestroy, tick } from 'svelte';
  import { api } from '../lib/api.js';
  import { goto } from '../lib/route.js';
  import { renderMarkdown } from '../lib/markdown.js';

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

  const emptySession = { id: sessionId, messages: [] };

  function fmtTs(ts) {
    if (!ts) return '—';
    const d = new Date(ts);
    if (Number.isNaN(d.getTime())) return '—';
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
    } catch (e) { error = String(e); }
  }

  async function removeCurrentSession() {
    if (!sessionId) return;
    if (!window.confirm(`Delete session "${sessionId}"? This cannot be undone.`)) return;
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

  onMount(() => {
    refresh();
    timer = setInterval(refresh, 3000);
    syncAutoScrollState();
    window.addEventListener('scroll', syncAutoScrollState, { passive: true });
  });

  onDestroy(() => {
    clearInterval(timer);
    window.removeEventListener('scroll', syncAutoScrollState);
  });
</script>

<div class="sticky-header">
  <div class="top">
    <button on:click={() => goto('sessions')}>← Back</button>
    <button class="danger" disabled={deletingCurrent} on:click={removeCurrentSession}>
      {deletingCurrent ? 'Deleting...' : 'Delete Session'}
    </button>
    <h1>Session <span class="mono">{sessionId}</span></h1>
  </div>

  {#if error}<div class="err">{error}</div>{/if}

  <div class="meta-grid">
    <div class="meta-item">
      <div class="k">Created</div>
      <div class="v">{fmtTs(session?.created_at)}</div>
    </div>
    <div class="meta-item">
      <div class="k">Updated</div>
      <div class="v">{fmtTs(session?.updated_at)}</div>
    </div>
    <div class="meta-item">
      <div class="k">Messages</div>
      <div class="v">{messages.length}</div>
    </div>
    <div class="meta-item">
      <div class="k">Total Tokens (Real)</div>
      <div class="v">{hasRealTokenData ? totalRealTokens : 'N/A'}</div>
    </div>
    <div class="meta-item">
      <div class="k">Avg AI Latency</div>
      <div class="v">{avgAssistantLatency !== null ? `${avgAssistantLatency} ms` : 'N/A'}</div>
    </div>
    <div class="meta-item">
      <div class="k">Trace Events</div>
      <div class="v">{chatCompletedLogs.length}</div>
    </div>
    <div class="meta-item">
      <div class="k">Runtime Events</div>
      <div class="v">{sessionTraceEvents.length}</div>
    </div>
    <div class="meta-item">
      <div class="k">Tool Events</div>
      <div class="v">{toolEventCount}</div>
    </div>
  </div>
</div>

<div class="trace-panel">
  <h2>Runtime Timeline</h2>
  {#if sessionTraceEvents.length > 0}
    <div class="trace-list">
      {#each sessionTraceEvents.slice(0, 120) as evt}
        <details class="trace-item">
          <summary>
            <span class="t">{fmtTs(evt.time)}</span>
            <span class="lvl {evt.level || 'info'}">{evt.level || 'info'}</span>
            {#if getPlanMarker(evt) === 'plan'}
              <span class="plan-mark plan">plan</span>
            {:else if getPlanMarker(evt) === 'plan_confirmed'}
              <span class="plan-mark confirmed">plan_confirmed</span>
            {/if}
            <span class="msg">{evt.message || '-'}</span>
            <span class="meta">{evt.fields?.module || '-'} / {evt.fields?.phase || '-'}</span>
            {#if evt.fields?.step}<span class="meta">step {evt.fields.step}</span>{/if}
            {#if evt.fields?.tool_name}<span class="meta">{evt.fields.tool_name}</span>{/if}
          </summary>
          <div class="trace-body">
            {#if evt.fields?.plan_status}<div><b>plan_status:</b> {evt.fields.plan_status}</div>{/if}
            {#if evt.fields?.duration_ms}<div><b>duration_ms:</b> {evt.fields.duration_ms}</div>{/if}
            {#if evt.fields?.trace_id}<div><b>trace_id:</b> {evt.fields.trace_id}</div>{/if}
            {#if evt.fields?.error_message}<div class="trace-err"><b>error:</b> {evt.fields.error_message}</div>{/if}
            {#if evt.fields?.args}<pre>{shortText(evt.fields.args, 1200)}</pre>{/if}
            {#if evt.fields?.result}<pre>{shortText(evt.fields.result, 1200)}</pre>{/if}
          </div>
        </details>
      {/each}
    </div>
  {:else}
    <div class="trace-empty">No runtime events for this session yet.</div>
  {/if}
</div>

<div class="thread">
  {#if session}
    {#each session.messages || [] as m}
      {@const parts = m.role === 'assistant' ? splitMessageContent(m.content || '') : null}
      <div class="msg {m.role}">
        <div class="msg-head">
          <div class="role">{m.role}</div>
          <div class="sub">
            <span>{fmtTs(m.timestamp)}</span>
            <span>tokens: {Number.isFinite(m.total_tokens) && m.total_tokens > 0 ? m.total_tokens : 'N/A'}</span>
            {#if m.role === 'assistant'}
              <span>latency: {Number.isFinite(m.reply_latency_ms) && m.reply_latency_ms > 0 ? `${m.reply_latency_ms} ms` : 'N/A'}</span>
            {/if}
          </div>
        </div>
        {#if m.role === 'assistant' && parts?.thought}
          <details class="thought">
            <summary>Thought (click to expand)</summary>
            <div class="content markdown thought-body" dir="auto">{@html renderMarkdown(parts.thought)}</div>
          </details>
        {/if}
        <div class="content markdown" dir="auto">
          {@html renderMarkdown(m.role === 'assistant' ? parts?.visible || '' : m.content || '')}
        </div>
      </div>
    {:else}
      <div class="empty">No messages in this session.</div>
    {/each}
    <div bind:this={latestAnchorEl}></div>
  {/if}
</div>

<style>
  .sticky-header {
    position: sticky;
    top: 0;
    z-index: 10;
    background: #0f1115;
    padding-bottom: 0.8rem;
    margin-bottom: 0.8rem;
    border-bottom: 1px solid #232838;
  }
  .top { display: flex; align-items: center; gap: 0.75rem; }
  h1 { margin: 0; }
  h1 .mono { font-family: ui-monospace, monospace; font-size: 1rem; color: #8ea6ff; }
  button { background: #1c2130; color: #dfe3ee; border: 1px solid #2d3448; border-radius: 6px; padding: 0.35rem 0.7rem; cursor: pointer; }
  .danger {
    background: #33141a;
    border: 1px solid #7f2936;
    color: #ffd8dd;
  }
  .danger:disabled {
    opacity: 0.7;
    cursor: not-allowed;
  }
  .meta-grid { margin-top: 1rem; display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 0.6rem; }
  .meta-item { background: #151923; border: 1px solid #232838; border-radius: 8px; padding: 0.6rem 0.7rem; }
  .meta-item .k { font-size: 0.74rem; color: #8f98ae; text-transform: uppercase; letter-spacing: 0.04em; margin-bottom: 0.2rem; }
  .meta-item .v { font-size: 0.92rem; color: #dfe3ee; }
  .thread { display: flex; flex-direction: column; gap: 0.75rem; }
  .trace-panel {
    margin: 0.4rem 0 1rem;
    background: #111522;
    border: 1px solid #232838;
    border-radius: 8px;
    padding: 0.75rem;
  }
  .trace-panel h2 {
    margin: 0 0 0.5rem;
    font-size: 0.92rem;
    color: #cfd7ea;
  }
  .trace-list {
    display: flex;
    flex-direction: column;
    gap: 0.45rem;
    max-height: 34vh;
    overflow: auto;
  }
  .trace-item {
    border: 1px solid #263049;
    border-radius: 6px;
    background: #0d111a;
    padding: 0.25rem 0.45rem;
  }
  .trace-item summary {
    cursor: pointer;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.45rem;
    color: #d8e0f1;
    font-size: 0.78rem;
  }
  .trace-item .t { color: #8f98ae; }
  .trace-item .msg { color: #dfe3ee; }
  .trace-item .meta {
    color: #aeb8cd;
    background: #1a2133;
    border-radius: 4px;
    padding: 0.05rem 0.3rem;
  }
  .trace-item .lvl {
    text-transform: uppercase;
    font-size: 0.7rem;
    letter-spacing: 0.03em;
    color: #8ea6ff;
  }
  .trace-item .lvl.error { color: #f16a6a; }
  .trace-item .lvl.warn { color: #f5c469; }
  .plan-mark {
    border-radius: 999px;
    padding: 0.08rem 0.45rem;
    font-size: 0.68rem;
    letter-spacing: 0.03em;
    text-transform: uppercase;
    font-weight: 600;
  }
  .plan-mark.plan {
    background: rgba(114, 173, 255, 0.2);
    color: #91bdff;
  }
  .plan-mark.confirmed {
    background: rgba(90, 211, 155, 0.18);
    color: #74dca8;
  }
  .trace-body {
    margin-top: 0.4rem;
    font-size: 0.8rem;
    color: #cbd3e5;
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
  }
  .trace-body pre {
    margin: 0;
    background: #0a0f17;
    border: 1px solid #232838;
    border-radius: 6px;
    padding: 0.45rem 0.55rem;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .trace-err { color: #ffb8bd; }
  .trace-empty { color: #8f98ae; font-size: 0.82rem; }
  .msg { padding: 0.75rem 1rem; border-radius: 8px; border: 1px solid #232838; max-width: 80%; }
  .msg.user { background: #172232; align-self: flex-end; }
  .msg.assistant { background: #151923; align-self: flex-start; }
  .msg.system { background: #1b1d2a; align-self: center; font-style: italic; color: #9aa3bb; }
  .msg-head { display: flex; align-items: baseline; gap: 0.6rem; margin-bottom: 0.35rem; flex-wrap: wrap; }
  .role { font-size: 0.72rem; color: #8ea6ff; text-transform: uppercase; letter-spacing: 0.05em; }
  .sub { font-size: 0.76rem; color: #95a0b8; display: inline-flex; gap: 0.55rem; flex-wrap: wrap; }
  .content { white-space: pre-wrap; font-size: 0.95rem; line-height: 1.45; }
  .thought {
    margin: 0.2rem 0 0.6rem;
    border: 1px dashed #2c3548;
    border-radius: 6px;
    background: #121621;
    padding: 0.35rem 0.55rem;
  }
  .thought summary {
    cursor: pointer;
    color: #9cb0de;
    font-size: 0.82rem;
    user-select: none;
  }
  .thought-body {
    margin-top: 0.45rem;
    color: #c2cbdf;
    font-size: 0.9rem;
  }
  .markdown :global(pre) { margin: 0.5rem 0; background: #0c0f15; border: 1px solid #232838; border-radius: 6px; padding: 0.6rem; overflow: auto; }
  .markdown :global(code) { background: #0c0f15; border: 1px solid #232838; border-radius: 4px; padding: 0.08rem 0.3rem; font-family: ui-monospace, monospace; font-size: 0.85em; }
  .markdown :global(pre code) { border: 0; padding: 0; background: transparent; }
  .markdown :global(a) { color: #8ab0ff; text-decoration: underline; }
  .empty { color: #6c7389; padding: 1rem; text-align: center; }
  .err { background: #40161a; border: 1px solid #8a2b32; color: #f6c6cb; padding: 0.6rem 0.9rem; border-radius: 6px; }
  @media (max-width: 1000px) {
    .meta-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
    .msg { max-width: 100%; }
  }
</style>
