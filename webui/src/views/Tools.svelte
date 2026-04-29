<script>
  import { goto } from '../lib/route.js';
  import { _ } from '../lib/i18n.js';

  const tools = [{ id: 'docker-create', status: 'available' }];
</script>

<h1>{$_('tools.title')}</h1>
<p class="mb-4 text-base text-base-content/70">{$_('tools.hint')}</p>

<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
  {#each tools as t}
    <button
      type="button"
      class="card card-border bg-base-200 text-left shadow-sm transition-colors hover:border-primary/40 hover:shadow-md"
      on:click={() => goto('tool', { id: t.id })}
    >
      <div class="card-body gap-0 p-4">
        <div class="flex min-w-0 w-full items-center gap-3">
          <h2 class="card-title min-w-0 flex-1 truncate text-base font-semibold">
            {$_('tools.dockerName')}
          </h2>
          <span class="badge badge-success badge-sm shrink-0 whitespace-nowrap">
            {$_('tools.statusAvailable')}
          </span>
        </div>
        <p class="mt-2 text-sm text-base-content/70">{$_('tools.dockerDesc')}</p>
      </div>
    </button>
  {/each}
</div>
