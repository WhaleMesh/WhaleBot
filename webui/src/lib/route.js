import { writable } from 'svelte/store';

// Minimal hash router:
// { name: 'overview' | 'components' | 'sessions' | 'session' | 'tools' | 'tool' | 'logger', params: {} }
const DEFAULT_ROUTE = { name: 'overview', params: {} };

function parseHash() {
  if (typeof window === 'undefined') return DEFAULT_ROUTE;
  const raw = window.location.hash.replace(/^#/, '');
  const parts = raw.split('/').filter(Boolean);

  if (parts.length === 0) return DEFAULT_ROUTE;

  if (parts[0] === 'session' && parts[1]) {
    return { name: 'session', params: { id: decodeURIComponent(parts[1]) } };
  }
  if (parts[0] === 'tool' && parts[1]) {
    return { name: 'tool', params: { id: decodeURIComponent(parts[1]) } };
  }
  if (parts[0] === 'overview' || parts[0] === 'components' || parts[0] === 'sessions' || parts[0] === 'tools' || parts[0] === 'logger') {
    return { name: parts[0], params: {} };
  }

  return DEFAULT_ROUTE;
}

function routeToHash(name, params = {}) {
  if (name === 'session' && params.id) return `#/session/${encodeURIComponent(params.id)}`;
  if (name === 'tool' && params.id) return `#/tool/${encodeURIComponent(params.id)}`;
  if (name === 'overview' || name === 'components' || name === 'sessions' || name === 'tools' || name === 'logger') {
    return `#/${name}`;
  }
  return '#/overview';
}

export const route = writable(parseHash());

if (typeof window !== 'undefined') {
  if (!window.location.hash) {
    window.history.replaceState(null, '', routeToHash(DEFAULT_ROUTE.name, DEFAULT_ROUTE.params));
  } else {
    route.set(parseHash());
  }

  window.addEventListener('hashchange', () => {
    route.set(parseHash());
  });
}

export function goto(name, params = {}) {
  if (typeof window === 'undefined') {
    route.set({ name, params });
    return;
  }

  const nextHash = routeToHash(name, params);
  if (window.location.hash !== nextHash) {
    window.location.hash = nextHash;
    return;
  }

  route.set(parseHash());
}
