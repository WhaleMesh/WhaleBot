<script>
  import { createEventDispatcher } from 'svelte';
  import { _ } from '../lib/i18n.js';
  import * as auth from '../lib/auth.js';

  export let username = '';

  const dispatch = createEventDispatcher();

  let acUsername = username;
  let acCurrent = '';
  let acNewPass = '';
  let acConfirmPass = '';
  let acBusy = false;
  let acErr = '';

  async function submitAccount() {
    acErr = '';
    const u = auth.validateUsername(acUsername);
    if (!u.ok) {
      acErr = u.error;
      return;
    }
    const p = auth.validateOptionalNewPassword(acNewPass, acConfirmPass);
    if (!p.ok) {
      acErr = p.error;
      return;
    }
    if (!acCurrent) {
      acErr = $_('auth.requiredField');
      return;
    }
    if (u.username === username && p.password == null) {
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
        dispatch('updated', r.username);
        dispatch('close');
      } else {
        acErr = r.error || $_('auth.errorGeneric');
      }
    } catch (e) {
      acErr = String(e);
    } finally {
      acBusy = false;
    }
  }
</script>

<div class="modal modal-open">
  <div class="modal-box max-w-md">
    <h3 class="mb-1 text-lg font-bold">{$_('layout.accountSettings')}</h3>
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
        <button type="button" class="btn" on:click={() => dispatch('close')}>{$_('auth.cancel')}</button>
        <button type="button" class="btn btn-primary" disabled={acBusy} on:click={submitAccount}>
          {#if acBusy}
            <span class="loading loading-spinner loading-sm"></span>
          {/if}
          {$_('auth.save')}
        </button>
      </div>
    </div>
  </div>
  <button type="button" class="modal-backdrop bg-transparent" on:click={() => dispatch('close')}></button>
</div>
