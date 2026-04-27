<script>
  import { onMount, onDestroy } from 'svelte';
  import { api } from '../lib/api.js';
  import {
    parseUserDockerManagerMeta,
    tempRemovalCountdown,
    formatDurationSec,
  } from '../lib/userdockerPolicy.js';

  let components = [];
  let userdockers = [];
  let stats = null;
  let statsError = '';
  let statsDisabled = false;
  let error = '';
  /** Non-empty when GET /health reports chat_ready === false or health fetch failed. */
  let chatBlockedMessage = '';
  let timer;
  let tick = 0;
  let tickTimer;

  function fmtCount(n) {
    const v = Number(n);
    if (!Number.isFinite(v) || v < 0) return '0';
    if (v >= 1_000_000) return (v / 1_000_000).toFixed(1) + 'M';
    if (v >= 10_000) return (v / 1_000).toFixed(1) + 'k';
    return String(Math.trunc(v));
  }

  function fmtTs(ts) {
    if (!ts) return '—';
    const d = new Date(ts);
    if (Number.isNaN(d.getTime())) return '—';
    return d.toLocaleString();
  }

  function shortId(v) {
    const s = String(v || '');
    if (!s) return '—';
    return s.length > 12 ? s.slice(0, 12) : s;
  }

  function normalizeStatus(v) {
    const s = String(v || '').toLowerCase().trim();
    if (!s) return 'unknown';
    if (s.includes('healthy') || s.includes('running') || s === 'up' || s.startsWith('up ')) return 'healthy';
    if (s.includes('warn') || s.includes('unhealthy') || s.includes('restarting')) return 'warn';
    if (s.includes('removed') || s.includes('stopped') || s.includes('exited') || s.includes('dead')) return 'bad';
    return 'unknown';
  }

  function displayStatus(v) {
    const normalized = normalizeStatus(v);
    if (normalized === 'unknown') {
      const raw = String(v || '').trim();
      return raw || 'unknown';
    }
    return normalized;
  }

  function isUserDockerType(v) {
    return String(v || '').toLowerCase().trim() === 'userdocker';
  }

  async function refresh() {
    try {
      const [c, u, s, h] = await Promise.all([
        api.components(),
        api.userDockerList(true),
        api.statsOverview().catch((e) => ({ _err: String(e) })),
        api.health().catch((e) => ({ _health_err: String(e) })),
      ]);
      components = c.components || [];
      userdockers = u.containers || [];
      if (h && h._health_err) {
        chatBlockedMessage = `Could not read orchestrator health: ${h._health_err}`;
      } else if (h && h.chat_ready === false) {
        chatBlockedMessage =
          String(h.chat_error || '').trim() ||
          'Chat dependencies are not ready (runtime, session, and chat_model must all be healthy).';
      } else {
        chatBlockedMessage = '';
      }
      if (s && s.disabled) {
        statsDisabled = true;
        stats = null;
        statsError = '';
      } else if (s && s._err) {
        statsDisabled = false;
        statsError = s._err;
      } else if (s && !s._err && s.success === false) {
        statsDisabled = false;
        stats = null;
        statsError = s.error || '统计服务返回错误';
      } else if (s && !s._err && s.success !== false) {
        statsDisabled = false;
        stats = s.stats || null;
        statsError = '';
      }
      error = '';
    } catch (e) {
      error = String(e);
    }
  }

  onMount(() => {
    refresh();
    timer = setInterval(refresh, 3000);
    tickTimer = setInterval(() => {
      tick += 1;
    }, 1000);
  });
  onDestroy(() => {
    clearInterval(timer);
    if (tickTimer) clearInterval(tickTimer);
  });

  $: systemComponents = components.filter((c) => !isUserDockerType(c?.type));
  $: udPolicy = parseUserDockerManagerMeta(components);
  $: messagesStat = stats
    ? {
        total: Number((stats.messages || {}).total || 0),
        delta: Number((stats.messages || {}).last_24h || 0),
      }
    : { total: 0, delta: 0 };
  $: toolCallsStat = stats
    ? {
        total: Number((stats.tool_calls || {}).total || 0),
        delta: Number((stats.tool_calls || {}).last_24h || 0),
      }
    : { total: 0, delta: 0 };
  $: tokensStat = stats
    ? {
        total: Number(((stats.tokens || {}).total || {}).total || 0),
        delta: Number(((stats.tokens || {}).total || {}).last_24h || 0),
      }
    : { total: 0, delta: 0 };

  function removalLine(d) {
    tick;
    const r = tempRemovalCountdown(d, udPolicy.ttlSec);
    if (r.kind === 'persistent') return 'Persistent (no idle removal)';
    if (r.kind === 'temp') return `~${formatDurationSec(r.seconds)} until idle removal`;
    return '—';
  }

  $: policyLine =
    udPolicy.ttlSec != null && udPolicy.sweepSec != null
      ? `Temporary userdocker idle removal: ${formatDurationSec(udPolicy.ttlSec)} · sweeper every ${formatDurationSec(udPolicy.sweepSec)}`
      : udPolicy.ttlSec != null
        ? `Temporary userdocker idle removal: ${formatDurationSec(udPolicy.ttlSec)}`
        : '';
</script>

<h1>Overview</h1>
{#if chatBlockedMessage}
  <div class="chat-blocked" role="alert">{chatBlockedMessage}</div>
{/if}
{#if error}<div class="err">{error}</div>{/if}
{#if statsDisabled}
  <div class="stats-disabled" role="status">
    未启用统计服务。在 <code>docker-compose.yml</code> 中启动 <code>stats</code> 服务并重建后，将显示对话数量、工具调用次数与 Token 消耗（近 24 小时对齐整点窗口）。
  </div>
{:else if statsError}
  <div class="stats-err" role="status">统计数据不可用：{statsError}</div>
{/if}

{#if !statsDisabled}
  <div class="stat-cards">
    <div class="stat-card">
      <div class="stat-label">对话数量</div>
      <div class="stat-value">{fmtCount(messagesStat.total)}</div>
      <div class="stat-delta">近24小时 +{fmtCount(messagesStat.delta)}</div>
    </div>
    <div class="stat-card">
      <div class="stat-label">工具调用次数</div>
      <div class="stat-value">{fmtCount(toolCallsStat.total)}</div>
      <div class="stat-delta">近24小时 +{fmtCount(toolCallsStat.delta)}</div>
    </div>
    <div class="stat-card">
      <div class="stat-label">Token 消耗</div>
      <div class="stat-value">{fmtCount(tokensStat.total)}</div>
      <div class="stat-delta">近24小时 +{fmtCount(tokensStat.delta)}</div>
    </div>
  </div>
{/if}

<h2>Userdocker</h2>
{#if policyLine}
  <p class="policy-hint">{policyLine}</p>
{/if}
<div class="grid">
  {#each userdockers as d}
    <div class="card {normalizeStatus(d.status || d.state)}">
      <div class="head">
        <div class="title">{d.name || '—'}</div>
        <span class="chip {normalizeStatus(d.status || d.state)}">{displayStatus(d.status || d.state)}</span>
      </div>
      <div class="rows">
        <div class="row"><span class="k">Image</span><span class="v mono">{d.image || '—'}</span></div>
        <div class="row"><span class="k">Scope</span><span class="v">{d.scope || '—'}</span></div>
        <div class="row"><span class="k">Last active</span><span class="v">{fmtTs(d.last_active_at)}</span></div>
        <div class="row"><span class="k">Idle removal</span><span class="v warn">{removalLine(d)}</span></div>
        <div class="row"><span class="k">State</span><span class="v">{d.state || '—'}</span></div>
        <div class="row"><span class="k">Raw Status</span><span class="v">{d.status || '—'}</span></div>
        <div class="row"><span class="k">Container ID</span><span class="v mono">{shortId(d.id)}</span></div>
      </div>
    </div>
  {:else}
    <div class="empty">No userdocker containers.</div>
  {/each}
</div>

<h2>System Docker</h2>
<div class="grid">
  {#each systemComponents as c}
    <div class="card {normalizeStatus(c.status)}">
      <div class="head">
        <div class="title">{c.name || '—'}</div>
        <span class="chip {normalizeStatus(c.status)}">{c.status || 'unknown'}</span>
      </div>
      <div class="rows">
        <div class="row"><span class="k">Type</span><span class="v">{c.type || '—'}</span></div>
        <div class="row"><span class="k">Endpoint</span><span class="v mono">{c.endpoint || '—'}</span></div>
        <div class="row"><span class="k">Failures</span><span class="v">{Number.isFinite(c.failure_count) ? c.failure_count : '—'}</span></div>
        <div class="row"><span class="k">Last Check</span><span class="v">{fmtTs(c.last_checked_at)}</span></div>
      </div>
    </div>
  {:else}
    <div class="empty">No system components.</div>
  {/each}
</div>

<style>
  h1 { margin-top: 0; }
  h2 {
    margin: 1rem 0 0.6rem;
    font-size: 0.95rem;
    color: #c7d0e6;
    font-weight: 600;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
    gap: 0.8rem;
    margin-bottom: 1.1rem;
  }
  .card {
    background: #151923;
    border: 1px solid #232838;
    border-radius: 10px;
    padding: 0.75rem 0.85rem;
  }
  .card.healthy {
    border-color: #2f6645;
    box-shadow: inset 0 0 0 1px rgba(133, 216, 167, 0.08);
  }
  .card.warn {
    border-color: #6b5422;
    box-shadow: inset 0 0 0 1px rgba(245, 196, 105, 0.08);
  }
  .card.bad {
    border-color: #78323a;
    box-shadow: inset 0 0 0 1px rgba(241, 106, 106, 0.08);
  }
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.6rem;
    margin-bottom: 0.55rem;
  }
  .title {
    font-size: 0.95rem;
    font-weight: 600;
    color: #e5e9f5;
    word-break: break-word;
  }
  .chip {
    border-radius: 999px;
    font-size: 0.72rem;
    letter-spacing: 0.03em;
    text-transform: uppercase;
    padding: 0.14rem 0.5rem;
    border: 1px solid #2d3448;
    color: #a8b2ca;
    white-space: nowrap;
  }
  .chip.healthy {
    color: #85d8a7;
    border-color: #2f6645;
    background: #163222;
  }
  .chip.warn {
    color: #f5c469;
    border-color: #6b5422;
    background: #2f2715;
  }
  .chip.bad {
    color: #f16a6a;
    border-color: #78323a;
    background: #341a1e;
  }
  .rows {
    display: flex;
    flex-direction: column;
    gap: 0.38rem;
  }
  .row {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 0.8rem;
  }
  .k {
    color: #8f98ae;
    font-size: 0.75rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    flex: 0 0 auto;
  }
  .v {
    color: #dfe3ee;
    font-size: 0.86rem;
    text-align: right;
    word-break: break-all;
  }
  .v.warn {
    color: #e8a854;
  }
  .policy-hint {
    margin: 0 0 0.65rem;
    font-size: 0.82rem;
    color: #9aa3bb;
    line-height: 1.35;
  }
  .mono {
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
    font-size: 0.8rem;
  }
  .empty {
    background: #111522;
    border: 1px dashed #263049;
    border-radius: 8px;
    color: #8f98ae;
    padding: 0.8rem 0.9rem;
  }
  .err { background: #40161a; border: 1px solid #8a2b32; color: #f6c6cb; padding: 0.6rem 0.9rem; border-radius: 6px; margin-bottom: 1rem; }
  .chat-blocked {
    background: #3a1518;
    border: 2px solid #c44a54;
    color: #ffd6d9;
    padding: 0.75rem 1rem;
    border-radius: 8px;
    margin-bottom: 1rem;
    font-size: 0.9rem;
    line-height: 1.45;
    font-weight: 500;
  }
  .stats-err {
    background: #2a2112;
    border: 1px solid #6b5422;
    color: #f5c469;
    padding: 0.45rem 0.75rem;
    border-radius: 6px;
    margin-bottom: 0.75rem;
    font-size: 0.82rem;
  }
  .stats-disabled {
    background: #1a2230;
    border: 1px solid #2d3a52;
    color: #9aa3bb;
    padding: 0.55rem 0.85rem;
    border-radius: 6px;
    margin-bottom: 0.85rem;
    font-size: 0.84rem;
    line-height: 1.45;
  }
  .stats-disabled code {
    font-size: 0.8rem;
    color: #c7d0e6;
  }
  .stat-cards {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
    gap: 0.8rem;
    margin: 0.2rem 0 1.2rem;
  }
  .stat-card {
    background: #151923;
    border: 1px solid #232838;
    border-radius: 10px;
    padding: 0.85rem 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }
  .stat-label {
    color: #8f98ae;
    font-size: 0.78rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  .stat-value {
    color: #e5e9f5;
    font-size: 1.85rem;
    font-weight: 600;
    line-height: 1.1;
    font-variant-numeric: tabular-nums;
  }
  .stat-delta {
    color: #85d8a7;
    font-size: 0.78rem;
  }
</style>
