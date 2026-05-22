<script>
  import { createEventDispatcher } from 'svelte';
  import { _ } from '../lib/i18n.js';
  import * as auth from '../lib/auth.js';
  import { BRAND_REPO_URL } from '../lib/brandUrls.js';

  export let bootError = '';

  const dispatch = createEventDispatcher();

  let loginName = '';
  let loginPass = '';
  let loginBusy = false;
  let loginErr = '';

  async function submitLogin(e) {
    e.preventDefault();
    loginErr = '';
    const u = auth.validateUsername(loginName);
    if (!u.ok) {
      loginErr = u.error;
      return;
    }
    if (!loginPass) {
      loginErr = $_('auth.passwordRequired');
      return;
    }
    loginBusy = true;
    try {
      const r = await auth.login(u.username, loginPass);
      if (r.ok) {
        loginPass = '';
        dispatch('login');
      } else {
        loginErr = r.error || $_('auth.errorLogin');
      }
    } catch (err) {
      loginErr = String(err);
    } finally {
      loginBusy = false;
    }
  }
</script>

<div class="flex min-h-screen flex-col items-center justify-center bg-base-200 p-4 text-base-content">
  <div class="card w-full max-w-md border border-base-300 bg-base-100 shadow-xl">
    <div class="card-body gap-4">
      <div>
        <div class="flex items-start gap-3">
          <a href={BRAND_REPO_URL} target="_blank" rel="noopener noreferrer" class="shrink-0 text-primary hover:opacity-90">
            <svg class="h-9 w-9" viewBox="0 0 24 24" fill="currentColor"><path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm-1 17.93c-3.95-.49-7-3.85-7-7.93 0-.62.08-1.21.21-1.79L9 15v1c0 1.1.9 2 2 2v1.93zm6.9-2.54c-.26-.81-1-1.39-1.9-1.39h-1v-3c0-.55-.45-1-1-1H8v-2h2c.55 0 1-.45 1-1V7h2c1.1 0 2-.9 2-2v-.41c2.93 1.19 5 4.06 5 7.41 0 2.08-.8 3.97-2.1 5.39z"/></svg>
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
          <span>{bootError}</span>
        </div>
      {/if}
      <form class="flex flex-col gap-3" on:submit={submitLogin}>
        <label class="form-control w-full">
          <span class="label-text">{$_('auth.username')}</span>
          <input class="input input-bordered w-full" name="username" autocomplete="username" bind:value={loginName} required />
        </label>
        <label class="form-control w-full">
          <span class="label-text">{$_('auth.password')}</span>
          <input class="input input-bordered w-full" type="password" name="password" autocomplete="current-password" bind:value={loginPass} required />
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
