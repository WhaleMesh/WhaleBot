<script>
  import { onMount } from 'svelte';
  import { route, goto } from './lib/route.js';
  import { _, setLocale, locale } from './lib/i18n.js';
  import * as auth from './lib/auth.js';
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

  const navIds = ['overview', 'components', 'sessions', 'logger', 'tools', 'skills', 'llm', 'adapter'];

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

  onMount(refreshAuth);

  function onLangChange(/** @type {Event & { currentTarget: HTMLSelectElement }} */ e) {
    const v = e.currentTarget.value;
    if (v === 'en' || v === 'zh' || v === 'ja') setLocale(v);
  }

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
          <h1 class="text-2xl font-bold text-primary">{$_('brand.title')} <span class="font-normal text-base-content">{$_('brand.mvp')}</span></h1>
          <p class="text-sm text-base-content/70">{$_('auth.loginSubtitle')}</p>
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
  <div class="app wb-page min-h-screen flex flex-col text-base-content">
    <header
      class="flex w-full min-h-14 flex-nowrap items-center gap-2 border-b border-base-300 bg-base-200 px-2 sm:gap-3 sm:px-4"
    >
      <div class="shrink-0">
        <span class="text-2xl font-bold tracking-tight text-primary">
          {$_('brand.title')}
          <span class="text-base font-normal text-base-content">{$_('brand.mvp')}</span>
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

      <div class="flex shrink-0 flex-nowrap items-center gap-2">
        <label class="sr-only" for="lang-select">{$_('lang.aria')}</label>
        <select
          id="lang-select"
          class="select select-bordered max-w-full w-[8.25rem] text-sm"
          value={$locale}
          on:change={onLangChange}
        >
          <option value="en">{$_('lang.en')}</option>
          <option value="zh">{$_('lang.zh')}</option>
          <option value="ja">{$_('lang.ja')}</option>
        </select>

        <details class="dropdown dropdown-end" bind:open={accountMenuOpen}>
          <summary class="btn btn-ghost max-w-[10rem] truncate border border-base-300">{authUsername}</summary>
          <ul class="menu dropdown-content z-[100] mt-1 w-52 rounded-box border border-base-300 bg-base-100 p-2 shadow">
            <li>
              <button type="button" class="justify-start" on:click={openAccountModal}>{$_('auth.accountSettings')}</button>
            </li>
            <li>
              <button type="button" class="justify-start text-error" on:click={doLogout}>{$_('auth.logout')}</button>
            </li>
          </ul>
        </details>
      </div>
    </header>

    <main class="mx-auto w-full max-w-[1200px] flex-1 min-w-0 p-4 text-base leading-relaxed sm:p-6">
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
