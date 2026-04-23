<script>
  import { api } from '../lib/api.js';
  import { goto } from '../lib/route.js';

  let name = 'user-task-001';
  let image = '';
  let cmd = '';
  let envText = 'TASK_NAME=demo';
  let labelsText = 'mvp.component=true\nmvp.type=userdocker';
  let network = 'mvp_net';
  let autoRegister = true;
  let result = null;
  let error = '';
  let running = false;

  function parseKV(text) {
    const out = {};
    for (const line of text.split('\n')) {
      const t = line.trim();
      if (!t) continue;
      const idx = t.indexOf('=');
      if (idx < 0) continue;
      out[t.slice(0, idx)] = t.slice(idx + 1);
    }
    return out;
  }

  async function submit() {
    running = true;
    error = '';
    result = null;
    try {
      const body = {
        name,
        image: image || undefined,
        cmd: cmd ? cmd.split('\n').map((s) => s).filter(Boolean) : undefined,
        env: parseKV(envText),
        labels: parseKV(labelsText),
        network: network || 'mvp_net',
        auto_register: autoRegister,
      };
      result = await api.dockerCreate(body);
    } catch (e) {
      error = String(e);
    }
    running = false;
  }
</script>

<div class="top">
  <button on:click={() => goto('tools')}>← Back</button>
  <h1>Tool · Docker Creator</h1>
</div>
<p class="hint">Calls <code>/api/v1/tools/docker-create</code>. New containers join <code>mvp_net</code> and are labeled <code>mvp.component=true</code>.</p>

<form on:submit|preventDefault={submit}>
  <label>Name<input bind:value={name} required /></label>
  <label>Image <span class="dim">(blank ⇒ whalesbot/userdocker-base:latest)</span>
    <input bind:value={image} placeholder="whalesbot/userdocker-base:latest" />
  </label>
  <label>Cmd <span class="dim">(one arg per line — optional)</span>
    <textarea bind:value={cmd} rows="3" placeholder="sh&#10;-c&#10;while true; do echo hello; sleep 60; done"></textarea>
  </label>
  <label>Env (KEY=VALUE per line)
    <textarea bind:value={envText} rows="3"></textarea>
  </label>
  <label>Labels (KEY=VALUE per line)
    <textarea bind:value={labelsText} rows="3"></textarea>
  </label>
  <label>Network<input bind:value={network} /></label>
  <label class="inline"><input type="checkbox" bind:checked={autoRegister} /> Auto-register to orchestrator</label>
  <button disabled={running} type="submit">{running ? 'Creating…' : 'Create container'}</button>
</form>

{#if error}<div class="err">{error}</div>{/if}
{#if result}
  <pre class="out">{JSON.stringify(result, null, 2)}</pre>
{/if}

<style>
  .top { display: flex; align-items: center; gap: 0.75rem; }
  h1 { margin: 0; }
  .hint { color: #9aa3bb; margin-top: 0.5rem; }
  code { background: #1c2130; padding: 0.05rem 0.3rem; border-radius: 4px; font-size: 0.85rem; }
  form { display: flex; flex-direction: column; gap: 0.75rem; max-width: 700px; }
  label { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.9rem; color: #c7cde0; }
  label.inline { flex-direction: row; align-items: center; gap: 0.5rem; }
  input, textarea { background: #0c0f15; border: 1px solid #232838; border-radius: 6px; padding: 0.5rem 0.6rem; color: #e7e9ee; font: inherit; }
  textarea { font-family: ui-monospace, monospace; }
  .dim { color: #6c7389; font-weight: 400; font-size: 0.8rem; }
  button { align-self: flex-start; background: #1c2130; color: #dfe3ee; border: 1px solid #2d3448; border-radius: 6px; padding: 0.5rem 1rem; cursor: pointer; }
  form button[type="submit"] { background: #2a3b63; color: #fff; border-color: #3c5189; }
  button:disabled { opacity: 0.6; cursor: progress; }
  .err { background: #40161a; border: 1px solid #8a2b32; color: #f6c6cb; padding: 0.6rem 0.9rem; border-radius: 6px; margin-top: 1rem; }
  .out { background: #0c0f15; border: 1px solid #232838; border-radius: 6px; padding: 1rem; margin-top: 1rem; font-family: ui-monospace, monospace; font-size: 0.85rem; white-space: pre-wrap; }
</style>
