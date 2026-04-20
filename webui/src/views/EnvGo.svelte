<script>
  import { api } from '../lib/api.js';

  let code = `package main

import "fmt"

func main() {
    fmt.Println("hello from env-golang!")
}
`;
  let timeout = 10;
  let result = null;
  let error = '';
  let running = false;

  async function run() {
    running = true;
    error = '';
    result = null;
    try {
      result = await api.runGo({ code, timeout_sec: timeout });
    } catch (e) { error = String(e); }
    running = false;
  }
</script>

<h1>Go Environment</h1>
<p class="hint">Calls <code>/api/v1/environments/golang/run</code>. Code runs inside the <code>env-golang</code> container with a timeout.</p>

<label>Timeout (seconds)<input type="number" min="1" max="30" bind:value={timeout} /></label>
<textarea class="code" bind:value={code} rows="18" spellcheck="false"></textarea>

<div class="actions">
  <button disabled={running} on:click={run}>{running ? 'Running…' : 'Run Go'}</button>
</div>

{#if error}<div class="err">{error}</div>{/if}
{#if result}
  <div class="result">
    <div class="meta">
      exit_code: <b class:ok={result.exit_code === 0} class:bad={result.exit_code !== 0}>{result.exit_code}</b>
      · duration: {result.duration_ms} ms
      · success: {result.success}
    </div>
    <div class="panes">
      <div class="pane">
        <div class="lbl">stdout</div>
        <pre>{result.stdout || '(empty)'}</pre>
      </div>
      <div class="pane">
        <div class="lbl">stderr</div>
        <pre>{result.stderr || '(empty)'}</pre>
      </div>
    </div>
    {#if result.error}<div class="err">{result.error}</div>{/if}
  </div>
{/if}

<style>
  h1 { margin-top: 0; }
  .hint { color: #9aa3bb; margin-top: -0.25rem; }
  code { background: #1c2130; padding: 0.05rem 0.3rem; border-radius: 4px; font-size: 0.85rem; }
  label { display: inline-flex; flex-direction: column; gap: 0.25rem; font-size: 0.85rem; color: #c7cde0; margin-bottom: 0.5rem; }
  input { background: #0c0f15; border: 1px solid #232838; border-radius: 6px; padding: 0.3rem 0.5rem; color: #e7e9ee; width: 6rem; }
  .code { width: 100%; background: #0c0f15; border: 1px solid #232838; border-radius: 6px; padding: 0.75rem; color: #e7e9ee; font-family: ui-monospace, monospace; font-size: 0.9rem; }
  .actions { margin: 0.75rem 0; }
  button { background: #2a3b63; color: #fff; border: 1px solid #3c5189; border-radius: 6px; padding: 0.5rem 1rem; cursor: pointer; }
  button:disabled { opacity: 0.6; cursor: progress; }
  .result { margin-top: 1rem; }
  .meta { color: #9aa3bb; margin-bottom: 0.5rem; font-size: 0.9rem; }
  .meta .ok { color: #5ad39b; }
  .meta .bad { color: #f16a6a; }
  .panes { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; }
  .pane { background: #151923; border: 1px solid #232838; border-radius: 6px; }
  .pane .lbl { padding: 0.35rem 0.6rem; border-bottom: 1px solid #232838; color: #9aa3bb; font-size: 0.78rem; text-transform: uppercase; letter-spacing: 0.05em; }
  .pane pre { margin: 0; padding: 0.75rem; font-family: ui-monospace, monospace; font-size: 0.85rem; max-height: 260px; overflow: auto; white-space: pre-wrap; }
  .err { background: #40161a; border: 1px solid #8a2b32; color: #f6c6cb; padding: 0.6rem 0.9rem; border-radius: 6px; margin-top: 1rem; }
</style>
