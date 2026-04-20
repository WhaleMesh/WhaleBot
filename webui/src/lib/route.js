import { writable } from 'svelte/store';

// Minimal in-memory router: { name: 'overview' | 'components' | 'sessions' | 'session' | 'tools' | 'env-go', params: {} }
export const route = writable({ name: 'overview', params: {} });

export function goto(name, params = {}) {
  route.set({ name, params });
}
