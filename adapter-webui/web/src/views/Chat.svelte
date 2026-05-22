<script>
  import { createEventDispatcher, onMount, onDestroy, tick, afterUpdate } from 'svelte';
  import { _ } from '../lib/i18n.js';
  import { renderMarkdown } from '../lib/markdown.js';
  import {
    currentSessionId,
    sessions,
    messages,
    isLoading,
    progressText,
    chatError,
    sendMessage,
    loadSessions,
    loadSession,
    deleteSession,
    startNewChat,
    startProgressPolling,
    stopProgressPolling,
  } from '../lib/stores.js';

  export let mode = 'main'; // 'main' | 'sidebar'

  const dispatch = createEventDispatcher();

  let inputText = '';
  let chatContainer;
  let shouldAutoScroll = true;
  const AUTO_SCROLL_THRESHOLD_PX = 180;

  const channelThoughtRe = /<\|channel\|?>([\s\S]*?)(?=<\|message\|?>|$)/gi;
  const messageTagRe = /<\|\/?message\|?>/gi;
  const thinkTagRe = /<think>([\s\S]*?)<\/think>/gi;
  const thoughtTagRe = /<thought>([\s\S]*?)<\/thought>/gi;
  const reasoningTagRe = /<reasoning>([\s\S]*?)<\/reasoning>/gi;
  const looseMarkerTagRe = /<\/?\|?(?:channel|message|think|thought|reasoning)\|?>/gi;

  function splitMessageContent(content) {
    const raw = String(content || '');
    if (!raw) return { visible: '', thought: '' };
    const thoughts = [];
    let visible = raw;
    visible = visible.replace(channelThoughtRe, (_all, thought) => {
      const t = String(thought || '').trim();
      if (t) thoughts.push(t);
      return '';
    });
    const captureTaggedThought = (_all, thought) => {
      const t = String(thought || '').trim();
      if (t) thoughts.push(t);
      return '';
    };
    visible = visible.replace(thinkTagRe, captureTaggedThought);
    visible = visible.replace(thoughtTagRe, captureTaggedThought);
    visible = visible.replace(reasoningTagRe, captureTaggedThought);
    visible = visible.replace(messageTagRe, '');
    visible = visible.replace(looseMarkerTagRe, '').trim();
    return { visible, thought: thoughts.join('\n\n').trim() };
  }

  function chatBubbleClass(role) {
    if (role === 'user') return 'chat-bubble chat-bubble-primary text-primary-content rounded-2xl';
    return 'chat-bubble rounded-2xl border border-white/10 bg-neutral text-neutral-content shadow-sm';
  }

  function isNearPageBottom() {
    if (!chatContainer) return true;
    const distanceToBottom = chatContainer.scrollHeight - (chatContainer.scrollTop + chatContainer.clientHeight);
    return distanceToBottom <= AUTO_SCROLL_THRESHOLD_PX;
  }

  async function scrollToBottom() {
    await tick();
    if (chatContainer) {
      chatContainer.scrollTop = chatContainer.scrollHeight;
    }
  }

  $: if ($messages.length && shouldAutoScroll) {
    scrollToBottom();
  }

  afterUpdate(() => {
    if (chatContainer) {
      shouldAutoScroll = isNearPageBottom();
    }
  });

  async function handleSubmit() {
    const text = inputText.trim();
    if (!text || $isLoading) return;
    inputText = '';
    shouldAutoScroll = true;
    await sendMessage(text);
  }

  function handleKeydown(e) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSubmit();
    }
  }

  function handleSelectSession(sid) {
    dispatch('selectSession', sid);
  }

  function handleNewChat() {
    startNewChat();
  }

  async function handleDeleteSession(sid, e) {
    e.stopPropagation();
    if (!window.confirm($_('chat.confirmDelete'))) return;
    await deleteSession(sid);
  }

  function downloadAttachment(att) {
    if (!att?.content_base64) return;
    const raw = atob(att.content_base64);
    const bytes = new Uint8Array(raw.length);
    for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
    const blob = new Blob([bytes], { type: att.mime_type || 'application/octet-stream' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = att.filename || 'attachment';
    a.click();
    URL.revokeObjectURL(url);
  }

  function formatTime(ts) {
    if (!ts) return '';
    try {
      return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    } catch {
      return '';
    }
  }

  function sessionPreview(sid) {
    const parts = sid.split('-');
    if (parts.length >= 2) {
      return parts.slice(1).join('-').slice(0, 12);
    }
    return sid.slice(0, 16);
  }
</script>

{#if mode === 'sidebar'}
  <!-- Sidebar session list -->
  <div class="flex flex-col gap-1">
    {#each $sessions as s}
      <button
        type="button"
        class="group flex items-center gap-2 rounded-lg px-3 py-2 text-left text-sm transition-colors hover:bg-base-300 {$currentSessionId === s.id ? 'bg-primary text-primary-content' : 'text-base-content'}"
        on:click={() => handleSelectSession(s.id)}
      >
        <svg class="h-4 w-4 shrink-0 opacity-60" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75"><path stroke-linecap="round" stroke-linejoin="round" d="M8.625 12a.375.375 0 11-.75 0 .375.375 0 01.75 0zm4.125 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zm4.125 0a.375.375 0 11-.75 0 .375.375 0 01.75 0z" /></svg>
        <span class="min-w-0 flex-1 truncate">{sessionPreview(s.id)}</span>
        <button
          type="button"
          class="btn btn-ghost btn-xs hidden shrink-0 group-hover:inline-flex"
          title={$_('chat.deleteSession')}
          on:click={(e) => handleDeleteSession(s.id, e)}
        >
          <svg class="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" /></svg>
        </button>
      </button>
    {:else}
      <p class="px-3 py-4 text-center text-xs text-base-content/50">{$_('chat.noSessions')}</p>
    {/each}
  </div>

{:else}
  <!-- Main chat area -->
  <div class="flex flex-1 flex-col overflow-hidden">
    <!-- Messages -->
    <div
      class="flex-1 overflow-y-auto px-4 py-4"
      bind:this={chatContainer}
      on:scroll={() => { shouldAutoScroll = isNearPageBottom(); }}
    >
      {#if $messages.length === 0}
        <div class="flex h-full flex-col items-center justify-center gap-3 text-center">
          <svg class="h-16 w-16 text-primary/30" viewBox="0 0 24 24" fill="currentColor"><path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm-1 17.93c-3.95-.49-7-3.85-7-7.93 0-.62.08-1.21.21-1.79L9 15v1c0 1.1.9 2 2 2v1.93zm6.9-2.54c-.26-.81-1-1.39-1.9-1.39h-1v-3c0-.55-.45-1-1-1H8v-2h2c.55 0 1-.45 1-1V7h2c1.1 0 2-.9 2-2v-.41c2.93 1.19 5 4.06 5 7.41 0 2.08-.8 3.97-2.1 5.39z"/></svg>
          <h2 class="text-xl font-semibold text-base-content/70">{$_('chat.emptyTitle')}</h2>
          <p class="text-sm text-base-content/50">{$_('chat.emptyHint')}</p>
        </div>
      {:else}
        <div class="mx-auto max-w-3xl space-y-4">
          {#each $messages as m}
            {@const parts = m.role === 'assistant' ? splitMessageContent(m.content || '') : null}
            <div class="chat {m.role === 'user' ? 'chat-end' : 'chat-start'}">
              <div class="chat-header px-1 text-[0.7rem] text-base-content/60">
                <span class="font-semibold uppercase tracking-wide text-primary">{m.role}</span>
                <span class="ml-2">{formatTime(m.timestamp)}</span>
              </div>
              {#if m.role === 'assistant' && parts?.thought}
                <details class="mb-1 max-w-[min(100%,42rem)] rounded-lg border border-dashed border-base-300 bg-base-200/60 p-2">
                  <summary class="cursor-pointer text-xs text-base-content/70">{$_('chat.thoughtToggle')}</summary>
                  <div class="markdown thought-body mt-2 text-sm leading-relaxed">
                    {@html renderMarkdown(parts.thought)}
                  </div>
                </details>
              {/if}
              {#if m.attachment}
                <div class="chat-bubble rounded-2xl border border-white/10 bg-neutral p-3">
                  <div class="flex items-center gap-2">
                    <svg class="h-5 w-5 text-primary" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75"><path stroke-linecap="round" stroke-linejoin="round" d="M18.375 12.739l-7.693 7.693a4.5 4.5 0 01-6.364-6.364l10.94-10.94A3 3 0 1119.5 7.372L8.552 18.32m.009-.01l-.01.01m5.699-9.941l-7.81 7.81a1.5 1.5 0 002.112 2.13" /></svg>
                    <span class="text-sm font-medium">{m.attachment.filename}</span>
                    <button type="button" class="btn btn-primary btn-xs ml-auto" on:click={() => downloadAttachment(m.attachment)}>
                      {$_('chat.attachmentDownload')}
                    </button>
                  </div>
                </div>
              {:else}
                <div class="{chatBubbleClass(m.role)} markdown whitespace-pre-wrap text-base leading-relaxed" dir="auto">
                  {@html renderMarkdown(m.role === 'assistant' ? parts?.visible || '' : m.content || '')}
                </div>
              {/if}
            </div>
          {/each}
        </div>
      {/if}
    </div>

    <!-- Error -->
    {#if $chatError}
      <div class="mx-auto w-full max-w-3xl px-4">
        <div class="alert alert-error text-sm mb-2">{$_('chat.errorPrefix')}{$chatError}</div>
      </div>
    {/if}

    <!-- Progress -->
    {#if $isLoading && $progressText}
      <div class="mx-auto w-full max-w-3xl px-4 pb-1">
        <div class="flex items-center gap-2 rounded-lg bg-base-300/50 px-3 py-2 text-xs text-base-content/70">
          <span class="loading loading-spinner loading-xs text-primary"></span>
          <span class="truncate">{$progressText}</span>
        </div>
      </div>
    {/if}

    <!-- Input -->
    <div class="border-t border-base-300 bg-base-100 p-3">
      <div class="mx-auto flex max-w-3xl items-end gap-2">
        <textarea
          class="textarea textarea-bordered min-h-[2.5rem] max-h-32 flex-1 resize-none"
          placeholder={$_('chat.inputPlaceholder')}
          bind:value={inputText}
          on:keydown={handleKeydown}
          rows="1"
          disabled={$isLoading}
        ></textarea>
        <button
          type="button"
          class="btn btn-primary btn-square shrink-0"
          disabled={$isLoading || !inputText.trim()}
          on:click={handleSubmit}
          title={$_('chat.send')}
        >
          {#if $isLoading}
            <span class="loading loading-spinner loading-sm"></span>
          {:else}
            <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M6 12L3.269 3.126A59.768 59.768 0 0121.485 12 59.77 59.77 0 013.27 20.876L5.999 12zm0 0h7.5" /></svg>
          {/if}
        </button>
      </div>
    </div>
  </div>
{/if}
