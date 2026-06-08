<script>
  import { onMount } from 'svelte';
  import { _, setLocale, locale } from './lib/i18n.js';
  import * as auth from './lib/auth.js';
  import { currentSessionId, loadSessions, loadSession, messages, startNewChat } from './lib/stores.js';
  import Login from './views/Login.svelte';
  import Chat from './views/Chat.svelte';
  import Settings from './views/Settings.svelte';
  import { BRAND_REPO_URL, WHALEMESH_ORG_URL } from './lib/brandUrls.js';

  /** @type {'loading' | 'anon' | 'user'} */
  let authPhase = 'loading';
  let authUsername = '';
  let bootError = '';
  let showSettings = false;
  let sidebarOpen = true;

  async function refreshAuth() {
    authPhase = 'loading';
    bootError = '';
    try {
      const r = await auth.me();
      if (r.ok) {
        authPhase = 'user';
        authUsername = r.username;
        await loadSessions();
      } else {
        authPhase = 'anon';
        authUsername = '';
      }
    } catch (e) {
      authPhase = 'anon';
      authUsername = '';
      bootError = e?.message || String(e);
    }
  }

  function handleLogin() {
    refreshAuth();
  }

  async function doLogout() {
    await auth.logout();
    authPhase = 'anon';
    authUsername = '';
  }

  function pickLang(code) {
    setLocale(code);
  }

  function handleNewChat() {
    startNewChat();
  }

  function handleSelectSession(sid) {
    currentSessionId.set(sid);
    loadSession(sid);
  }

  onMount(() => {
    refreshAuth();
  });
</script>

{#if authPhase === 'loading'}
  <div class="flex min-h-screen items-center justify-center bg-base-200 text-base-content">
    <div class="flex flex-col items-center gap-3">
      <span class="loading loading-spinner loading-lg text-primary"></span>
      <p class="text-base">{$_('auth.loadSession')}</p>
    </div>
  </div>
{:else if authPhase === 'anon'}
  <Login {bootError} on:login={handleLogin} />
{:else}
  <div class="flex h-screen overflow-hidden bg-base-200 text-base-content">
    <!-- Sidebar -->
    <aside class="flex w-64 shrink-0 flex-col border-r border-base-300 bg-base-100 {sidebarOpen ? '' : 'hidden'}">
      <div class="flex items-center justify-between border-b border-base-300 p-3">
        <a href={BRAND_REPO_URL} target="_blank" rel="noopener noreferrer" class="flex items-center gap-2 text-primary hover:opacity-90">
          <svg class="h-6 w-6" viewBox="0 0 24 24" fill="currentColor"><path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm-1 17.93c-3.95-.49-7-3.85-7-7.93 0-.62.08-1.21.21-1.79L9 15v1c0 1.1.9 2 2 2v1.93zm6.9-2.54c-.26-.81-1-1.39-1.9-1.39h-1v-3c0-.55-.45-1-1-1H8v-2h2c.55 0 1-.45 1-1V7h2c1.1 0 2-.9 2-2v-.41c2.93 1.19 5 4.06 5 7.41 0 2.08-.8 3.97-2.1 5.39z"/></svg>
          <span class="text-lg font-bold">{$_('brand.title')}</span>
        </a>
      </div>

      <div class="p-2">
        <button type="button" class="btn btn-outline btn-sm w-full gap-2" on:click={handleNewChat}>
          <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4" /></svg>
          {$_('layout.newChat')}
        </button>
      </div>

      <nav class="flex-1 overflow-y-auto px-2 pb-2">
        <Chat on:selectSession={(e) => handleSelectSession(e.detail)} mode="sidebar" />
      </nav>

      <div class="border-t border-base-300 p-2">
        <!-- Language -->
        <div class="dropdown dropdown-top w-full">
          <button type="button" class="btn btn-ghost btn-sm w-full justify-start gap-2">
            <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75"><path stroke-linecap="round" stroke-linejoin="round" d="M12 21a9.004 9.004 0 008.716-6.747M12 21a9.004 9.004 0 01-8.716-6.747M12 21c2.485 0 4.5-4.03 4.5-9S14.485 3 12 3m0 18c-2.485 0-4.5-4.03-4.5-9S9.515 3 12 3m0 0a8.997 8.997 0 017.843 4.582M12 3a8.997 8.997 0 00-7.843 4.582m15.686 0A11.953 11.953 0 0112 10.5c-2.998 0-5.74-1.1-7.843-2.918m15.686 0A8.959 8.959 0 0121 12c0 .778-.099 1.533-.284 2.253m0 0A17.919 17.919 0 0112 16.5c-3.162 0-6.133-.815-8.716-2.247m0 0A9.015 9.015 0 013 12c0-1.605.42-3.113 1.157-4.418" /></svg>
            <span class="text-sm">{$_('lang.' + $locale)}</span>
          </button>
          <ul class="dropdown-content menu z-[110] w-40 rounded-box border border-base-300 bg-base-100 p-2 shadow-lg">
            <li><button type="button" on:click={() => pickLang('en')}>{$_('lang.en')}</button></li>
            <li><button type="button" on:click={() => pickLang('zh')}>{$_('lang.zh')}</button></li>
            <li><button type="button" on:click={() => pickLang('ja')}>{$_('lang.ja')}</button></li>
          </ul>
        </div>

        <!-- Account -->
        <div class="dropdown dropdown-top w-full">
          <button type="button" class="btn btn-ghost btn-sm w-full justify-start gap-2" title={authUsername}>
            <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75"><path stroke-linecap="round" stroke-linejoin="round" d="M15.75 6a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0zM4.501 20.118a7.5 7.5 0 0114.998 0A17.933 17.933 0 0112 21.75c-2.676 0-5.216-.584-7.499-1.632z" /></svg>
            <span class="truncate text-sm">{authUsername}</span>
          </button>
          <ul class="dropdown-content menu z-[110] w-48 rounded-box border border-base-300 bg-base-100 p-2 shadow-lg">
            <li><button type="button" on:click={() => (showSettings = true)}>{$_('layout.accountSettings')}</button></li>
            <li><button type="button" class="text-error" on:click={doLogout}>{$_('layout.logout')}</button></li>
          </ul>
        </div>

        <p class="border-t border-base-300/60 pt-2 text-center text-[10px] leading-snug text-base-content/55">
          <span>{$_('layout.poweredByBefore')}</span>
          <a href={WHALEMESH_ORG_URL} target="_blank" rel="noopener noreferrer" class="link link-hover font-medium text-primary/90">{$_('layout.whaleMesh')}</a><span>{$_('layout.poweredByAfter')}</span>
        </p>
      </div>
    </aside>

    <!-- Main -->
    <main class="flex min-w-0 flex-1 flex-col">
      <Chat on:selectSession={(e) => handleSelectSession(e.detail)} mode="main" />
    </main>
  </div>
{/if}

{#if showSettings}
  <Settings username={authUsername} on:close={() => (showSettings = false)} on:updated={(e) => { authUsername = e.detail; }} />
{/if}

<style>
  :global(*) {
    box-sizing: border-box;
  }
</style>
