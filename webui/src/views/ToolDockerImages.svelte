<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api.js';
  import { goto } from '../lib/route.js';
  import { _, t } from '../lib/i18n.js';

  let nodes = [];
  let node = '';
  /** @type {Array<{id:string,repo_tags:string[],size:number,created:number}>} */
  let images = [];
  let pullRef = '';
  let estimateText = '';
  let pullStatus = '';
  let error = '';
  let running = false;

  function formatSize(n) {
    const v = Number(n) || 0;
    if (v >= 1e9) return (v / 1e9).toFixed(1) + ' GB';
    if (v >= 1e6) return (v / 1e6).toFixed(1) + ' MB';
    if (v >= 1e3) return (v / 1e3).toFixed(1) + ' KB';
    return String(v) + ' B';
  }

  function formatCreated(sec) {
    const n = Number(sec);
    if (!Number.isFinite(n) || n <= 0) return '—';
    try {
      return new Date(n * 1000).toLocaleString();
    } catch {
      return '—';
    }
  }

  function tagsLabel(tags) {
    if (!Array.isArray(tags) || tags.length === 0) return '<none>';
    return tags.join(', ');
  }

  async function loadNodes() {
    try {
      const res = await api.userDockerNodes();
      nodes = res.nodes || [];
      if (!node && nodes.length) node = nodes[0].node || '';
    } catch (e) {
      nodes = [];
      error = String(e);
    }
  }

  async function refreshList() {
    if (!node) {
      error = t('toolDockerImages.nodeRequired');
      return;
    }
    running = true;
    error = '';
    try {
      const res = await api.userDockerImagesLocal(node);
      if (res.success === false) {
        throw new Error(res.error || 'list failed');
      }
      images = Array.isArray(res.local_images) ? res.local_images : [];
    } catch (e) {
      images = [];
      error = String(e);
    }
    running = false;
  }

  async function runEstimate() {
    const ref = pullRef.trim();
    if (!node) {
      error = t('toolDockerImages.nodeRequired');
      return;
    }
    if (!ref) {
      error = t('toolDockerImages.refRequired');
      return;
    }
    running = true;
    error = '';
    estimateText = '';
    try {
      const res = await api.userDockerImageEstimate(ref, node);
      if (res.success === false) {
        throw new Error(res.error || 'estimate failed');
      }
      const est = res.estimate || {};
      const parts = [];
      if (est.already_local) parts.push(t('toolDockerImages.alreadyLocal'));
      if (est.compressed_bytes != null && Number(est.compressed_bytes) > 0) {
        parts.push(t('toolDockerImages.compressedSize', { size: formatSize(est.compressed_bytes) }));
      }
      if (est.layer_count != null) {
        parts.push(t('toolDockerImages.layers', { n: String(est.layer_count) }));
      }
      if (est.note) parts.push(String(est.note));
      estimateText = parts.join(' · ') || JSON.stringify(est);
    } catch (e) {
      error = String(e);
    }
    running = false;
  }

  async function runPull() {
    const ref = pullRef.trim();
    if (!node) {
      error = t('toolDockerImages.nodeRequired');
      return;
    }
    if (!ref) {
      error = t('toolDockerImages.refRequired');
      return;
    }
    running = true;
    error = '';
    pullStatus = t('toolDockerImages.pulling');
    try {
      const res = await api.userDockerPull({
        node,
        ref,
        external_image_approved_by_user: true,
      });
      if (res.success === false) {
        throw new Error(res.error || 'pull failed');
      }
      const jobId = res.job_id;
      if (!jobId) {
        throw new Error('missing job_id');
      }
      // Poll until done (async pull, 30m cap server-side).
      for (;;) {
        const st = await api.userDockerPullStatus(jobId);
        if (st.success === false) {
          throw new Error(st.error || 'pull status failed');
        }
        const job = st.job || {};
        if (job.done) {
          if (job.error) {
            throw new Error(job.error);
          }
          pullStatus = t('toolDockerImages.pullDone');
          break;
        }
        pullStatus = t('toolDockerImages.pulling');
        await new Promise((r) => setTimeout(r, 1500));
      }
      await refreshList();
    } catch (e) {
      pullStatus = '';
      error = String(e);
    }
    running = false;
  }

  onMount(async () => {
    await loadNodes();
    if (node) await refreshList();
  });

  async function onNodeChange() {
    images = [];
    estimateText = '';
    pullStatus = '';
    if (node) await refreshList();
  }
</script>

<div class="mx-auto max-w-4xl">
  <div class="mb-4 flex flex-wrap items-center gap-3">
    <button type="button" class="btn btn-outline" on:click={() => goto('tools')}>{$_('toolDockerImages.back')}</button>
    <h1 class="wb-page-title">{$_('toolDockerImages.title')}</h1>
  </div>

  <p
    class="mb-6 text-base leading-relaxed text-base-content/70 [&_code]:rounded [&_code]:bg-base-300 [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-sm"
  >
    {@html $_('toolDockerImages.hint')}
  </p>

  {#if nodes.length === 0}
    <div role="alert" class="alert alert-soft alert-warning text-sm">{$_('toolDockerImages.noNodes')}</div>
  {:else}
    <div class="card card-border border-wb border-base-300 bg-base-200 shadow-sm">
      <div class="card-body flex flex-col gap-4 p-5 sm:flex-row sm:items-end sm:p-6">
        <label class="form-control min-w-0 flex-1">
          <span class="label label-text text-sm">{$_('toolDockerImages.node')}</span>
          <select class="select select-bordered w-full" bind:value={node} on:change={onNodeChange} disabled={running}>
            {#each nodes as n}
              <option value={n.node}>{n.node}</option>
            {/each}
          </select>
        </label>
        <button type="button" class="btn btn-outline min-w-[8rem]" disabled={running || !node} on:click={refreshList}>
          {$_('toolDockerImages.refresh')}
        </button>
      </div>
    </div>

    <div class="card card-border border-wb border-base-300 bg-base-200 shadow-sm mt-5 overflow-x-auto">
      <div class="card-body p-0">
        <table class="table table-sm">
          <thead>
            <tr>
              <th>{$_('toolDockerImages.colTags')}</th>
              <th>{$_('toolDockerImages.colId')}</th>
              <th>{$_('toolDockerImages.colSize')}</th>
              <th>{$_('toolDockerImages.colCreated')}</th>
            </tr>
          </thead>
          <tbody>
            {#if images.length === 0}
              <tr>
                <td colspan="4" class="text-base-content/60">{$_('toolDockerImages.empty')}</td>
              </tr>
            {:else}
              {#each images as img}
                <tr>
                  <td class="wb-mono max-w-xs break-all text-sm">{tagsLabel(img.repo_tags)}</td>
                  <td class="wb-mono text-sm">{img.id}</td>
                  <td class="text-sm whitespace-nowrap">{formatSize(img.size)}</td>
                  <td class="text-sm whitespace-nowrap">{formatCreated(img.created)}</td>
                </tr>
              {/each}
            {/if}
          </tbody>
        </table>
      </div>
    </div>

    <div class="card card-border border-wb border-base-300 bg-base-200 shadow-sm mt-5">
      <div class="card-body grid gap-4 p-5 sm:p-6">
        <h2 class="text-base font-semibold">{$_('toolDockerImages.pullTitle')}</h2>
        <label class="form-control w-full">
          <span class="label label-text text-sm">{$_('toolDockerImages.ref')}</span>
          <input
            class="input input-bordered w-full font-mono text-sm"
            bind:value={pullRef}
            placeholder="alpine:3.20"
            disabled={running}
          />
        </label>
        <div class="flex flex-wrap gap-2">
          <button type="button" class="btn btn-outline min-w-[8rem]" disabled={running || !node} on:click={runEstimate}>
            {$_('toolDockerImages.estimate')}
          </button>
          <button type="button" class="btn btn-primary min-w-[8rem]" disabled={running || !node} on:click={runPull}>
            {$_('toolDockerImages.pull')}
          </button>
        </div>
        {#if estimateText}
          <p class="text-sm text-base-content/70">{estimateText}</p>
        {/if}
        {#if pullStatus}
          <p class="text-sm text-base-content/70">{pullStatus}</p>
        {/if}
      </div>
    </div>
  {/if}

  {#if error}
    <div role="alert" class="alert alert-soft alert-error mt-5 text-sm">{error}</div>
  {/if}
</div>
