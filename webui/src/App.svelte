<script>
  import { onMount } from 'svelte';
  import { route, goto } from './lib/route.js';
  import { _, setLocale, locale } from './lib/i18n.js';
  import * as auth from './lib/auth.js';
  import NavGlyph from './lib/NavGlyph.svelte';
  import Overview from './views/Overview.svelte';
  import Components from './views/Components.svelte';
  import Sessions from './views/Sessions.svelte';
  import SessionDetail from './views/SessionDetail.svelte';
  import Tools from './views/Tools.svelte';
  import Skills from './views/Skills.svelte';
  import ToolDockerCreate from './views/ToolDockerCreate.svelte';
  import Logger from './views/Logger.svelte';
  import Llm from './views/Llm.svelte';
  import Adapters from './views/Adapters.svelte';
  import WbBrandIcon from './lib/WbBrandIcon.svelte';
  import { BRAND_REPO_URL, WHALEMESH_ORG_URL } from './lib/brandUrls.js';

  const navIds = ['overview', 'components', 'sessions', 'logger', 'tools', 'skills', 'llm', 'adapter'];
  const SIDEBAR_LS_KEY = 'whalebot_sidebar_collapsed';

  /** @type {'loading' | 'anon' | 'user'} */
  let authPhase = 'loading';
  let authUsername = '';
  /** @type {string} */
  let bootError = '';

  let loginName = '';
  let loginPass = '';
  let loginBusy = false;
  /** @type {string} */
  let loginErr = '';

  let accountMenuOpen = false;
  let langMenuOpen = false;
  let sidebarCollapsed = false;
  let modalAccount = false;
  let acUsername = '';
  let acCurrent = '';
  let acNewPass = '';
  let acConfirmPass = '';
  let acBusy = false;
  /** @type {string} */
  let acErr = '';

  async function refreshAuth() {
    authPhase = 'loading';
    bootError = '';
    try {
      const r = await auth.me();
      if (r.ok) {
        authPhase = 'user';
        authUsername = r.username;
      } else {
        authPhase = 'anon';
        authUsername = '';
      }
    } catch (e) {
      authPhase = 'anon';
      authUsername = '';
      bootError = typeof e === 'object' && e && 'message' in e ? String(/** @type {Error} */ (e).message) : String(e);
    }
  }

  function readSidebarPref() {
    try {
      if (typeof localStorage !== 'undefined' && localStorage.getItem(SIDEBAR_LS_KEY) === '1') {
        sidebarCollapsed = true;
      }
    } catch {
      /* ignore */
    }
  }

  function persistSidebar() {
    try {
      if (typeof localStorage !== 'undefined') {
        localStorage.setItem(SIDEBAR_LS_KEY, sidebarCollapsed ? '1' : '0');
      }
    } catch {
      /* ignore */
    }
  }

  function toggleSidebar() {
    sidebarCollapsed = !sidebarCollapsed;
    accountMenuOpen = false;
    langMenuOpen = false;
    persistSidebar();
  }

  /**
   * @param {string} id
   * @param {string} routeName
   */
  function isNavActive(id, routeName) {
    if (id === 'sessions' && (routeName === 'sessions' || routeName === 'session')) return true;
    if (id === 'skills' && routeName === 'skills') return true;
    if (id === 'llm' && routeName === 'llm') return true;
    if (id === 'adapter' && routeName === 'adapter') return true;
    if (id === 'tools' && (routeName === 'tools' || routeName === 'tool')) return true;
    return routeName === id;
  }

  /** @param {'en' | 'zh' | 'ja'} code */
  function pickLang(code) {
    setLocale(code);
    langMenuOpen = false;
  }

  onMount(() => {
    readSidebarPref();
    refreshAuth();
  });

  async function submitLogin(/** @type {SubmitEvent} */ e) {
    e.preventDefault();
    loginErr = '';
    const u = auth.validateAccountUsername(loginName);
    if (!u.ok) {
      loginErr = $_(u.errorKey);
      return;
    }
    if (!loginPass) {
      loginErr = $_('auth.loginPasswordRequired');
      return;
    }
    loginBusy = true;
    try {
      const r = await auth.login(u.username, loginPass);
      if (r.ok) {
        loginPass = '';
        await refreshAuth();
      } else {
        loginErr = r.error || $_('auth.errorLogin');
      }
    } catch (err) {
      loginErr = String(err);
    } finally {
      loginBusy = false;
    }
  }

  async function doLogout() {
    accountMenuOpen = false;
    await auth.logout();
    authPhase = 'anon';
    authUsername = '';
    loginName = '';
    loginPass = '';
  }

  function openAccountModal() {
    accountMenuOpen = false;
    acErr = '';
    acUsername = authUsername;
    acCurrent = '';
    acNewPass = '';
    acConfirmPass = '';
    modalAccount = true;
  }

  async function submitAccount() {
    acErr = '';
    const u = auth.validateAccountUsername(acUsername);
    if (!u.ok) {
      acErr = $_(u.errorKey);
      return;
    }
    const p = auth.validateOptionalNewPassword(acNewPass, acConfirmPass);
    if (!p.ok) {
      acErr = $_(p.errorKey);
      return;
    }
    if (!acCurrent) {
      acErr = $_('auth.requiredField');
      return;
    }
    if (u.username === authUsername && p.password == null) {
      acErr = $_('auth.noChanges');
      return;
    }
    acBusy = true;
    try {
      const r = await auth.updateCredentials({
        currentPassword: acCurrent,
        newUsername: u.username,
        newPassword: p.password ?? undefined,
      });
      if (r.ok) {
        authUsername = r.username;
        modalAccount = false;
        acCurrent = '';
        acNewPass = '';
        acConfirmPass = '';
      } else {
        acErr = r.errorKey ? $_(r.errorKey) : r.error || $_('auth.errorGeneric');
      }
    } catch (e) {
      acErr = String(e);
    } finally {
      acBusy = false;
    }
  }
</script>

{#if authPhase === 'loading'}
  <div class="wb-page flex min-h-screen flex-col items-center justify-center gap-3 bg-base-200 p-6 text-base-content">
    <span class="loading loading-spinner loading-lg text-primary"></span>
    <p class="text-base">{$_('auth.loadSession')}</p>
  </div>
{:else if authPhase === 'anon'}
  <div class="wb-page flex min-h-screen flex-col items-center justify-center bg-base-200 p-4 text-base-content">
    <div class="card w-full max-w-md border border-base-300 bg-base-100 shadow-xl">
      <div class="card-body gap-4">
        <div>
          <div class="flex items-start gap-3">
            <a
              href={BRAND_REPO_URL}
              target="_blank"
              rel="noopener noreferrer"
              class="shrink-0 text-primary hover:opacity-90"
              aria-label={$_('layout.brandRepoAria')}
            >
              <WbBrandIcon className="h-9 w-9 shrink-0" />
            </a>
            <div class="min-w-0 flex-1">
              <h1 class="text-2xl font-bold text-primary">{$_('brand.title')}</h1>
              <p class="text-sm text-base-content/70">{$_('auth.loginSubtitle')}</p>
            </div>
          </div>
        </div>
        <h2 class="card-title text-lg">{$_('auth.loginTitle')}</h2>
        {#if bootError}
          <div role="alert" class="alert alert-warning text-sm">
            <span>{$_('auth.sessionError')}</span>
          </div>
        {/if}
        <form class="flex flex-col gap-3" on:submit={submitLogin}>
          <label class="form-control w-full">
            <span class="label-text">{$_('auth.username')}</span>
            <input class="input input-bordered w-full" name="username" autocomplete="username" bind:value={loginName} required />
          </label>
          <label class="form-control w-full">
            <span class="label-text">{$_('auth.password')}</span>
            <input
              class="input input-bordered w-full"
              type="password"
              name="password"
              autocomplete="current-password"
              bind:value={loginPass}
              required
            />
          </label>
          {#if loginErr}
            <p class="text-sm text-error" role="alert">{loginErr}</p>
          {/if}
          <button type="submit" class="btn btn-primary mt-1" disabled={loginBusy}>
            {#if loginBusy}
              <span class="loading loading-spinner loading-sm"></span>
              {$_('auth.signingIn')}
            {:else}
              {$_('auth.signIn')}
            {/if}
          </button>
        </form>
      </div>
    </div>
  </div>
{:else}
  <div class="app wb-page flex min-h-screen flex-row text-base-content">
    <aside
      id="app-sidebar"
      class="sticky top-0 flex h-screen min-h-0 shrink-0 flex-col border-r border-base-300 bg-base-200 transition-[width] duration-200 ease-out {sidebarCollapsed
        ? 'w-16'
        : 'w-56'}"
    >
      <div
        class="flex shrink-0 items-center gap-2 border-b border-base-300 p-2 {sidebarCollapsed ? 'flex-col justify-center py-3' : 'justify-between'}"
      >
        {#if !sidebarCollapsed}
          <div class="flex min-w-0 flex-1 items-center gap-2">
            <a
              href={BRAND_REPO_URL}
              target="_blank"
              rel="noopener noreferrer"
              class="shrink-0 text-primary hover:opacity-90"
              aria-label={$_('layout.brandRepoAria')}
            >
              <WbBrandIcon className="h-7 w-7 shrink-0" />
            </a>
            <span class="min-w-0 flex-1 truncate text-lg font-bold leading-tight tracking-tight text-primary" title={$_('layout.brandShort')}>
              {$_('brand.title')}
            </span>
          </div>
        {:else}
          <a
            href={BRAND_REPO_URL}
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex text-primary hover:opacity-90"
            title={$_('layout.brandShort')}
            aria-label={$_('layout.brandRepoAria')}
          >
            <WbBrandIcon className="h-7 w-7 shrink-0" />
            <span class="sr-only">{$_('layout.brandShort')}</span>
          </a>
        {/if}
        <button
          type="button"
          class="btn btn-ghost btn-square btn-sm shrink-0 border border-base-300"
          aria-expanded={!sidebarCollapsed}
          aria-controls="app-sidebar"
          title={sidebarCollapsed ? $_('layout.sidebarToggleExpand') : $_('layout.sidebarToggleCollapse')}
          aria-label={sidebarCollapsed ? $_('layout.sidebarToggleExpand') : $_('layout.sidebarToggleCollapse')}
          on:click={toggleSidebar}
        >
          {#if sidebarCollapsed}
            <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75" aria-hidden="true">
              <path stroke-linecap="round" stroke-linejoin="round" d="M8.25 4.5l7.5 7.5-7.5 7.5" />
            </svg>
          {:else}
            <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75" aria-hidden="true">
              <path stroke-linecap="round" stroke-linejoin="round" d="M15.75 19.5L8.25 12l7.5-7.5" />
            </svg>
          {/if}
        </button>
      </div>

      <nav class="flex min-h-0 min-w-0 flex-1 flex-col" aria-label={$_('layout.mainNav')}>
        <div class="flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto p-2">
        {#each navIds as id}
          <button
            type="button"
            class="border-wb flex min-h-11 w-full min-w-0 items-center rounded-lg border-base-300 bg-base-100 text-sm font-medium text-base-content shadow-none transition-colors hover:border-primary/60 hover:bg-base-300 {sidebarCollapsed
              ? 'justify-center px-0 py-2'
              : 'gap-3 px-3 py-2'} {isNavActive(id, $route.name)
              ? 'border-primary bg-primary text-primary-content hover:border-primary hover:bg-primary hover:text-primary-content'
              : ''}"
            title={$_('nav.' + id)}
            aria-current={isNavActive(id, $route.name) ? 'page' : undefined}
            on:click={() => goto(id)}
          >
            <NavGlyph {id} />
            {#if !sidebarCollapsed}
              <span class="min-w-0 flex-1 truncate text-left">{$_('nav.' + id)}</span>
            {:else}
              <span class="sr-only">{$_('nav.' + id)}</span>
            {/if}
          </button>
        {/each}
        </div>
      </nav>

      <div class="mt-auto flex shrink-0 flex-col gap-2 border-t border-base-300 p-2">
        <details class="relative" bind:open={langMenuOpen}>
          <summary
            class="btn btn-ghost h-auto min-h-11 w-full flex-nowrap justify-start gap-2 border border-base-300 py-2 {sidebarCollapsed
              ? 'justify-center px-0'
              : 'px-3'} list-none [&::-webkit-details-marker]:hidden"
          >
            <svg class="h-5 w-5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75" aria-hidden="true">
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                d="M12 21a9.004 9.004 0 008.716-6.747M12 21a9.004 9.004 0 01-8.716-6.747M12 21c2.485 0 4.5-4.03 4.5-9S14.485 3 12 3m0 18c-2.485 0-4.5-4.03-4.5-9S9.515 3 12 3m0 0a8.997 8.997 0 017.843 4.582M12 3a8.997 8.997 0 00-7.843 4.582m15.686 0A11.953 11.953 0 0112 10.5c-2.998 0-5.74-1.1-7.843-2.918m15.686 0A8.959 8.959 0 0121 12c0 .778-.099 1.533-.284 2.253m0 0A17.919 17.919 0 0112 16.5c-3.162 0-6.133-.815-8.716-2.247m0 0A9.015 9.015 0 013 12c0-1.605.42-3.113 1.157-4.418"
              />
            </svg>
            {#if !sidebarCollapsed}
              <span class="min-w-0 flex-1 truncate text-left text-sm">{$_('lang.' + $locale)}</span>
            {:else}
              <span class="sr-only">{$_('lang.aria')}</span>
            {/if}
          </summary>
          <ul
            class="menu absolute bottom-0 left-full z-[110] mb-0 ml-1 w-44 rounded-box border border-base-300 bg-base-100 p-2 shadow-lg"
          >
            <li>
              <button type="button" class={$locale === 'en' ? 'active' : ''} on:click={() => pickLang('en')}>{$_('lang.en')}</button>
            </li>
            <li>
              <button type="button" class={$locale === 'zh' ? 'active' : ''} on:click={() => pickLang('zh')}>{$_('lang.zh')}</button>
            </li>
            <li>
              <button type="button" class={$locale === 'ja' ? 'active' : ''} on:click={() => pickLang('ja')}>{$_('lang.ja')}</button>
            </li>
          </ul>
        </details>

        <details class="relative" bind:open={accountMenuOpen}>
          <summary
            class="btn btn-ghost h-auto min-h-11 w-full flex-nowrap justify-start gap-2 border border-base-300 py-2 {sidebarCollapsed
              ? 'justify-center px-0'
              : 'max-w-full px-3'} list-none [&::-webkit-details-marker]:hidden"
            title={authUsername}
            aria-label={$_('auth.account')}
          >
            {#if sidebarCollapsed}
              <svg class="h-5 w-5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75" aria-hidden="true">
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  d="M15.75 6a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0zM4.501 20.118a7.5 7.5 0 0114.998 0A17.933 17.933 0 0112 21.75c-2.676 0-5.216-.584-7.499-1.632z"
                />
              </svg>
              <span class="sr-only">{authUsername}</span>
            {:else}
              <span class="min-w-0 flex-1 truncate text-left text-sm">{authUsername}</span>
            {/if}
          </summary>
          <ul
            class="menu absolute left-full bottom-0 z-[110] mb-0 ml-1 w-52 rounded-box border border-base-300 bg-base-100 p-2 shadow-lg"
          >
            <li>
              <button type="button" class="justify-start" on:click={openAccountModal}>{$_('auth.accountSettings')}</button>
            </li>
            <li>
              <button type="button" class="justify-start text-error" on:click={doLogout}>{$_('auth.logout')}</button>
            </li>
          </ul>
        </details>

        <p
          class="border-t border-base-300/60 pt-2 text-center text-[10px] leading-snug text-base-content/55 sm:text-xs {sidebarCollapsed
            ? 'px-0'
            : 'px-1'}"
        >
          <span>{$_('layout.poweredByBefore')}</span>
          <a
            href={WHALEMESH_ORG_URL}
            target="_blank"
            rel="noopener noreferrer"
            class="link link-hover font-medium text-primary/90"
          >{$_('layout.whaleMesh')}</a><span>{$_('layout.poweredByAfter')}</span>
        </p>
      </div>
    </aside>

    <main class="mx-auto w-full min-w-0 max-w-[1200px] flex-1 p-4 text-base leading-relaxed sm:p-6">
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
      {:else if $route.name === 'adapter'}
        <Adapters adapterName={$route.params.id || ''} />
      {:else if $route.name === 'tool' && $route.params.id === 'docker-create'}
        <ToolDockerCreate />
      {/if}
    </main>
  </div>
{/if}

{#if modalAccount}
  <div class="modal modal-open">
    <div class="modal-box max-w-md">
      <h3 class="mb-1 text-lg font-bold">{$_('auth.accountSettings')}</h3>
      <p class="mb-3 text-sm text-base-content/70">{$_('auth.accountPasswordHint')}</p>
      <div class="flex flex-col gap-3">
        <label class="form-control w-full">
          <span class="label-text">{$_('auth.username')}</span>
          <input class="input input-bordered w-full" bind:value={acUsername} autocomplete="username" />
        </label>
        <label class="form-control w-full">
          <span class="label-text">{$_('auth.currentPassword')}</span>
          <input class="input input-bordered w-full" type="password" bind:value={acCurrent} autocomplete="current-password" />
        </label>
        <label class="form-control w-full">
          <span class="label-text">{$_('auth.newPassword')}</span>
          <input class="input input-bordered w-full" type="password" bind:value={acNewPass} autocomplete="new-password" />
        </label>
        <label class="form-control w-full">
          <span class="label-text">{$_('auth.confirmNewPassword')}</span>
          <input class="input input-bordered w-full" type="password" bind:value={acConfirmPass} autocomplete="new-password" />
        </label>
        {#if acErr}
          <p class="text-sm text-error">{acErr}</p>
        {/if}
        <div class="modal-action mt-2">
          <button type="button" class="btn" on:click={() => (modalAccount = false)}>{$_('auth.cancel')}</button>
          <button type="button" class="btn btn-primary" disabled={acBusy} on:click={submitAccount}>
            {#if acBusy}
              <span class="loading loading-spinner loading-sm"></span>
            {/if}
            {$_('auth.save')}
          </button>
        </div>
      </div>
    </div>
    <button type="button" class="modal-backdrop bg-transparent" aria-label={$_('auth.cancel')} on:click={() => (modalAccount = false)}></button>
  </div>
{/if}

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
