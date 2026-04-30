<script>
  import { api } from '../lib/api.js';
  import { goto } from '../lib/route.js';
  import { _, t } from '../lib/i18n.js';

  let name = 'user-task-001';
  let image = '';
  let cmd = '';
  let envText = 'TASK_NAME=demo';
  let labelsText = 'whalebot.component=true\nwhalebot.type=userdocker';
  let network = 'whalebot_net';
  let autoRegister = true;
  let port = 9000;
  let createResult = null;
  let listResult = null;
  let contractResult = null;
  let interfaceResult = null;
  let targetName = '';
  let removeForce = false;
  let restartTimeoutSec = 10;
  let error = '';
  let running = false;

  function parseKV(text) {
    const out = {};
    for (const line of text.split('\n')) {
      const ln = line.trim();
      if (!ln) continue;
      const idx = ln.indexOf('=');
      if (idx < 0) continue;
      out[ln.slice(0, idx)] = ln.slice(idx + 1);
    }
    return out;
  }

  async function submit() {
    running = true;
    error = '';
    createResult = null;
    try {
      const body = {
        name,
        image: image || undefined,
        cmd: cmd ? cmd.split('\n').map((s) => s).filter(Boolean) : undefined,
        env: parseKV(envText),
        labels: parseKV(labelsText),
        network: network || 'whalebot_net',
        auto_register: autoRegister,
        port: Number(port) || 9000,
      };
      createResult = await api.userDockerCreate(body);
    } catch (e) {
      error = String(e);
    }
    running = false;
  }

  async function refreshList() {
    running = true;
    error = '';
    try {
      listResult = await api.userDockerList(true);
    } catch (e) {
      error = String(e);
    }
    running = false;
  }

  async function readContract() {
    running = true;
    error = '';
    try {
      contractResult = await api.userDockerContract();
    } catch (e) {
      error = String(e);
    }
    running = false;
  }

  async function readInterface() {
    if (!targetName) {
      error = t('toolDocker.targetRequired');
      return;
    }
    running = true;
    error = '';
    try {
      interfaceResult = await api.userDockerInterface(targetName, Number(port) || undefined);
    } catch (e) {
      error = String(e);
    }
    running = false;
  }

  async function removeContainer() {
    if (!targetName) {
      error = t('toolDocker.targetRequired');
      return;
    }
    running = true;
    error = '';
    try {
      await api.userDockerRemove(targetName, removeForce);
      await refreshList();
    } catch (e) {
      error = String(e);
    }
    running = false;
  }

  async function restartContainer() {
    if (!targetName) {
      error = t('toolDocker.targetRequired');
      return;
    }
    running = true;
    error = '';
    try {
      await api.userDockerRestart(targetName, Number(restartTimeoutSec) || 10);
      await refreshList();
    } catch (e) {
      error = String(e);
    }
    running = false;
  }
</script>

<div class="mx-auto max-w-3xl">
  <div class="mb-4 flex flex-wrap items-center gap-3">
    <button type="button" class="btn btn-outline" on:click={() => goto('tools')}>{$_('toolDocker.back')}</button>
    <h1 class="wb-page-title">{$_('toolDocker.title')}</h1>
  </div>

  <p
    class="mb-6 text-base leading-relaxed text-base-content/70 [&_code]:rounded [&_code]:bg-base-300 [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-sm"
  >
    {@html $_('toolDocker.hint')}
  </p>

  <form class="card card-border border-wb border-base-300 bg-base-200 shadow-sm" on:submit|preventDefault={submit}>
    <div class="card-body grid gap-4 p-5 sm:p-6">
      <label class="form-control w-full">
        <span class="label label-text text-sm">{$_('toolDocker.name')}</span>
        <input class="input input-bordered w-full" bind:value={name} required />
      </label>
      <label class="form-control w-full">
        <span class="label label-text text-sm">
          {$_('toolDocker.image')}
          <span class="label-text-alt font-normal text-base-content/50">{$_('toolDocker.imageHint')}</span>
        </span>
        <input
          class="input input-bordered w-full font-mono text-sm"
          bind:value={image}
          placeholder="whalebot/userdocker-base:latest"
        />
      </label>
      <label class="form-control w-full">
        <span class="label label-text text-sm">
          {$_('toolDocker.cmd')}
          <span class="label-text-alt font-normal text-base-content/50">{$_('toolDocker.cmdHint')}</span>
        </span>
        <textarea
          class="textarea textarea-bordered min-h-[5.5rem] w-full font-mono text-sm leading-relaxed"
          bind:value={cmd}
          rows="3"
          placeholder="sh&#10;-c&#10;while true; do echo hello; sleep 60; done"
        ></textarea>
      </label>
      <label class="form-control w-full">
        <span class="label label-text text-sm">{$_('toolDocker.env')}</span>
        <textarea
          class="textarea textarea-bordered min-h-[5.5rem] w-full font-mono text-sm"
          bind:value={envText}
          rows="3"
        ></textarea>
      </label>
      <label class="form-control w-full">
        <span class="label label-text text-sm">{$_('toolDocker.labels')}</span>
        <textarea
          class="textarea textarea-bordered min-h-[5.5rem] w-full font-mono text-sm"
          bind:value={labelsText}
          rows="3"
        ></textarea>
      </label>
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <label class="form-control w-full">
          <span class="label label-text text-sm">{$_('toolDocker.network')}</span>
          <input class="input input-bordered w-full" bind:value={network} />
        </label>
        <label class="form-control w-full">
          <span class="label label-text text-sm">{$_('toolDocker.port')}</span>
          <input class="input input-bordered w-full" type="number" min="1" bind:value={port} />
        </label>
      </div>
      <label class="label cursor-pointer justify-start gap-3 py-0">
        <input type="checkbox" class="checkbox" bind:checked={autoRegister} />
        <span class="label-text text-sm">{$_('toolDocker.autoRegister')}</span>
      </label>
    </div>
    <div class="card-actions justify-end border-t border-base-300 bg-base-300/20 px-5 py-4 sm:px-6">
      <button class="btn btn-primary min-w-[10rem]" disabled={running} type="submit">
        {running ? $_('toolDocker.creating') : $_('toolDocker.createSubmit')}
      </button>
    </div>
  </form>

  <div class="card card-border border-wb border-base-300 bg-base-200 shadow-sm mt-5">
    <div class="card-body flex flex-col gap-3 p-5 sm:flex-row sm:flex-wrap sm:items-stretch">
      <button type="button" class="btn btn-outline min-h-12 min-w-0 flex-1 sm:min-w-[10rem]" disabled={running} on:click={refreshList}>
        {$_('toolDocker.listBtn')}
      </button>
      <button type="button" class="btn btn-outline min-h-12 min-w-0 flex-1 sm:min-w-[10rem]" disabled={running} on:click={readContract}>
        {$_('toolDocker.contractBtn')}
      </button>
    </div>
  </div>

  <div class="card card-border border-wb border-base-300 bg-base-200 shadow-sm mt-5">
    <div class="card-body grid gap-4 p-5 sm:p-6">
      <label class="form-control w-full">
        <span class="label label-text text-sm">{$_('toolDocker.targetName')}</span>
        <input class="input input-bordered w-full" bind:value={targetName} placeholder="user-task-001" />
      </label>
      <div class="grid grid-cols-1 items-end gap-4 sm:grid-cols-2">
        <label class="form-control w-full">
          <span class="label label-text text-sm">{$_('toolDocker.restartTimeout')}</span>
          <input class="input input-bordered w-full" type="number" min="1" bind:value={restartTimeoutSec} />
        </label>
        <label class="label mb-2 cursor-pointer justify-start gap-3 self-end sm:mb-0 sm:justify-center">
          <input type="checkbox" class="checkbox" bind:checked={removeForce} />
          <span class="label-text text-sm">{$_('toolDocker.forceRemove')}</span>
        </label>
      </div>
      <div class="flex flex-wrap gap-2 border-t border-base-300 pt-4">
        <button type="button" class="btn btn-outline min-w-[9rem] flex-1 sm:flex-none" disabled={running} on:click={readInterface}>
          {$_('toolDocker.getInterface')}
        </button>
        <button type="button" class="btn btn-outline min-w-[9rem] flex-1 sm:flex-none" disabled={running} on:click={restartContainer}>
          {$_('toolDocker.restart')}
        </button>
        <button type="button" class="btn btn-outline btn-error min-w-[9rem] flex-1 sm:flex-none" disabled={running} on:click={removeContainer}>
          {$_('toolDocker.remove')}
        </button>
      </div>
    </div>
  </div>

  {#if error}
    <div role="alert" class="alert alert-soft alert-error mt-5 text-sm">{error}</div>
  {/if}

  {#if createResult}
    <h3 class="mt-8 text-base font-semibold text-base-content/80">{$_('toolDocker.createResult')}</h3>
    <pre
      class="mt-2 max-h-[50vh] overflow-auto rounded-lg border border-base-300 bg-base-300/40 p-4 font-mono text-sm whitespace-pre-wrap break-words">{JSON.stringify(
      createResult,
      null,
      2,
    )}</pre>
  {/if}
  {#if listResult}
    <h3 class="mt-8 text-base font-semibold text-base-content/80">{$_('toolDocker.listResult')}</h3>
    <pre
      class="mt-2 max-h-[50vh] overflow-auto rounded-lg border border-base-300 bg-base-300/40 p-4 font-mono text-sm whitespace-pre-wrap break-words">{JSON.stringify(
      listResult,
      null,
      2,
    )}</pre>
  {/if}
  {#if contractResult}
    <h3 class="mt-8 text-base font-semibold text-base-content/80">{$_('toolDocker.publicContract')}</h3>
    <pre
      class="mt-2 max-h-[50vh] overflow-auto rounded-lg border border-base-300 bg-base-300/40 p-4 font-mono text-sm whitespace-pre-wrap break-words">{JSON.stringify(
      contractResult,
      null,
      2,
    )}</pre>
  {/if}
  {#if interfaceResult}
    <h3 class="mt-8 text-base font-semibold text-base-content/80">{$_('toolDocker.containerInterface')}</h3>
    <pre
      class="mt-2 max-h-[50vh] overflow-auto rounded-lg border border-base-300 bg-base-300/40 p-4 font-mono text-sm whitespace-pre-wrap break-words">{JSON.stringify(
      interfaceResult,
      null,
      2,
    )}</pre>
  {/if}
</div>
