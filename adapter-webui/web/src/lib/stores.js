import { writable, get } from 'svelte/store';
import { api } from './api.js';

export const currentSessionId = writable('');
export const sessions = writable([]);
export const messages = writable([]);
export const isLoading = writable(false);
export const progressText = writable('');
export const chatError = writable('');

let pollTimer = null;

export async function loadSessions() {
  try {
    const data = await api.sessions();
    sessions.set(data.sessions || []);
  } catch {
    // ignore
  }
}

export async function loadSession(sid) {
  if (!sid) {
    messages.set([]);
    return;
  }
  try {
    const data = await api.session(sid);
    const sess = data.session || data;
    messages.set(sess.messages || []);
  } catch {
    messages.set([]);
  }
}

export async function deleteSession(sid) {
  await api.deleteSession(sid);
  if (get(currentSessionId) === sid) {
    currentSessionId.set('');
    messages.set([]);
  }
  await loadSessions();
}

export function startNewChat() {
  currentSessionId.set('');
  messages.set([]);
  chatError.set('');
  progressText.set('');
}

export async function sendMessage(text) {
  const trimmed = text.trim();
  if (!trimmed) return;

  const sid = get(currentSessionId);
  chatError.set('');
  isLoading.set(true);
  progressText.set('Sending…');

  messages.update((msgs) => [...msgs, { role: 'user', content: trimmed, timestamp: new Date().toISOString() }]);

  try {
    const data = await api.chat({ message: trimmed, session_id: sid });
    if (data.session_id && !sid) {
      currentSessionId.set(data.session_id);
    }
    if (data.reply) {
      messages.update((msgs) => [...msgs, { role: 'assistant', content: data.reply, timestamp: new Date().toISOString() }]);
    }
    if (data.attachments && data.attachments.length > 0) {
      for (const att of data.attachments) {
        messages.update((msgs) => [...msgs, {
          role: 'assistant',
          content: `[Attachment: ${att.filename}]`,
          timestamp: new Date().toISOString(),
          attachment: att,
        }]);
      }
    }
    progressText.set('');
    await loadSessions();
  } catch (e) {
    chatError.set(String(e));
    messages.update((msgs) => [...msgs, { role: 'assistant', content: `Error: ${e.message}`, timestamp: new Date().toISOString(), isError: true }]);
    progressText.set('');
  } finally {
    isLoading.set(false);
  }
}

export function startProgressPolling(sid, traceId) {
  stopProgressPolling();
  if (!sid) return;
  pollTimer = setInterval(async () => {
    try {
      const data = await api.loggerEvents({ session_id: sid, trace_id: traceId || '', limit: 60 });
      if (data.latest_progress) {
        progressText.set(data.latest_progress);
      }
    } catch {
      // ignore poll errors
    }
  }, 2000);
}

export function stopProgressPolling() {
  if (pollTimer) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
}
