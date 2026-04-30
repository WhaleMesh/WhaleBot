<script>
  import { route, goto } from './lib/route.js';
  import { _, setLocale, locale } from './lib/i18n.js';
  import Overview from './views/Overview.svelte';
  import Components from './views/Components.svelte';
  import Sessions from './views/Sessions.svelte';
  import SessionDetail from './views/SessionDetail.svelte';
  import Tools from './views/Tools.svelte';
  import Skills from './views/Skills.svelte';
  import ToolDockerCreate from './views/ToolDockerCreate.svelte';
  import Logger from './views/Logger.svelte';
  import Llm from './views/Llm.svelte';

  const navIds = ['overview', 'components', 'sessions', 'logger', 'tools', 'skills', 'llm'];

  function onLangChange(/** @type {Event & { currentTarget: HTMLSelectElement }} */ e) {
    const v = e.currentTarget.value;
    if (v === 'en' || v === 'zh' || v === 'ja') setLocale(v);
  }
</script>

<div class="app wb-page min-h-screen flex flex-col text-base-content">
  <!-- Explicit flex (not DaisyUI navbar) so the center nav keeps flex-1 width; DaisyUI navbar-center often collapses. -->
  <header
    class="flex w-full min-h-14 flex-nowrap items-center gap-2 border-b border-base-300 bg-base-200 px-2 sm:gap-3 sm:px-4"
  >
    <div class="shrink-0">
      <span class="text-2xl text-primary font-bold tracking-tight">
        {$_('brand.title')}
        <span class="text-base-content text-base font-normal">{$_('brand.mvp')}</span>
      </span>
    </div>

    <div class="min-w-0 flex-1 overflow-x-auto px-0.5">
      <nav
        class="mx-auto grid w-full max-w-5xl gap-2"
        style="grid-template-columns: repeat({navIds.length}, minmax(0, 1fr));"
        aria-label="Main"
      >
        {#each navIds as id}
          <button
            type="button"
            class="border-wb min-h-11 w-full min-w-0 max-w-full truncate rounded-lg border-base-300 bg-base-100 px-2 py-2 text-center text-base font-medium leading-snug text-base-content shadow-none transition-colors hover:border-primary/60 hover:bg-base-300 sm:px-3 {$route.name === id
              ? 'border-primary bg-primary text-primary-content hover:border-primary hover:bg-primary'
              : ''}"
            on:click={() => goto(id)}
          >
            {$_('nav.' + id)}
          </button>
        {/each}
      </nav>
    </div>

    <div class="shrink-0">
      <label class="sr-only" for="lang-select">{$_('lang.aria')}</label>
      <select
        id="lang-select"
        class="select select-bordered w-[8.25rem] max-w-full text-sm"
        value={$locale}
        on:change={onLangChange}
      >
        <option value="en">{$_('lang.en')}</option>
        <option value="zh">{$_('lang.zh')}</option>
        <option value="ja">{$_('lang.ja')}</option>
      </select>
    </div>
  </header>

  <main
    class="flex-1 min-w-0 w-full max-w-[1200px] mx-auto p-4 text-base leading-relaxed sm:p-6"
  >
    {#if $route.name === 'overview'}
      <Overview />
    {:else if $route.name === 'components'}
      <Components />
    {:else if $route.name === 'sessions'}
      <Sessions />
    {:else if $route.name === 'logger'}
      <Logger />
    {:else if $route.name === 'session'}
      <SessionDetail sessionId={$route.params.id} />
    {:else if $route.name === 'tools'}
      <Tools />
    {:else if $route.name === 'skills'}
      <Skills />
    {:else if $route.name === 'llm'}
      <Llm llmName={$route.params.id || ''} />
    {:else if $route.name === 'tool' && $route.params.id === 'docker-create'}
      <ToolDockerCreate />
    {/if}
  </main>
</div>

<style>
  :global(*) {
    box-sizing: border-box;
  }
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }
</style>
