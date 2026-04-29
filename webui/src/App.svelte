<script>
  import { route, goto } from './lib/route.js';
  import Overview from './views/Overview.svelte';
  import Components from './views/Components.svelte';
  import Sessions from './views/Sessions.svelte';
  import SessionDetail from './views/SessionDetail.svelte';
  import Tools from './views/Tools.svelte';
  import ToolDockerCreate from './views/ToolDockerCreate.svelte';
  import Logger from './views/Logger.svelte';
  import Llm from './views/Llm.svelte';

  const nav = [
    { id: 'overview', label: 'Overview' },
    { id: 'components', label: 'Components' },
    { id: 'sessions', label: 'Sessions' },
    { id: 'logger', label: 'Logger' },
    { id: 'tools', label: 'Tools' },
    { id: 'llm', label: 'LLM' },
  ];
</script>

<div class="app">
  <header>
    <div class="brand">WhalesBot <span>MVP</span></div>
    <nav>
      {#each nav as n}
        <button class:active={$route.name === n.id} on:click={() => goto(n.id)}>
          {n.label}
        </button>
      {/each}
    </nav>
  </header>

  <main>
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
    {:else if $route.name === 'llm'}
      <Llm llmName={$route.params.id || ''} />
    {:else if $route.name === 'tool' && $route.params.id === 'docker-create'}
      <ToolDockerCreate />
    {/if}
  </main>
</div>

<style>
  :global(body) {
    margin: 0;
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
    background: #0f1115;
    color: #e7e9ee;
  }
  :global(*) { box-sizing: border-box; }
  .app {
    min-height: 100vh;
    display: flex;
    flex-direction: column;
  }
  header {
    display: flex;
    align-items: center;
    gap: 2rem;
    padding: 0.75rem 1.5rem;
    background: #151923;
    border-bottom: 1px solid #232838;
  }
  .brand {
    font-weight: 700;
    font-size: 1.15rem;
    letter-spacing: 0.02em;
  }
  .brand span {
    color: #7aa2f7;
    font-weight: 500;
    margin-left: 0.35rem;
  }
  nav {
    display: flex;
    gap: 0.25rem;
  }
  nav button {
    background: transparent;
    color: #a8b0c2;
    border: 1px solid transparent;
    border-radius: 6px;
    padding: 0.4rem 0.8rem;
    cursor: pointer;
    font-size: 0.9rem;
  }
  nav button:hover { color: #fff; background: #1c2130; }
  nav button.active {
    background: #1c2130;
    color: #fff;
    border-color: #2d3448;
  }
  main {
    flex: 1;
    padding: 1.5rem;
    max-width: 1200px;
    width: 100%;
    margin: 0 auto;
  }
</style>
