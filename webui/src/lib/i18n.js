import { writable, derived, get } from 'svelte/store';
import { bundles } from './i18n/messages.js';

const STORAGE_KEY = 'whalebot_lang';

/** @param {string} path */
function getNested(obj, path) {
  return path.split('.').reduce((o, k) => (o && o[k] !== undefined ? o[k] : undefined), obj);
}

/** @type {'en' | 'zh' | 'ja'} */
function browserLocale() {
  if (typeof navigator === 'undefined') return 'en';
  const lang = (navigator.language || '').toLowerCase();
  if (lang.startsWith('zh')) return 'zh';
  if (lang.startsWith('ja')) return 'ja';
  return 'en';
}

function readSavedLocale() {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    if (v === 'en' || v === 'zh' || v === 'ja') return v;
  } catch {
    /* ignore */
  }
  return null;
}

function initialLocale() {
  const saved = readSavedLocale();
  if (saved) return saved;
  return browserLocale();
}

/** @type {import('svelte/store').Writable<'en' | 'zh' | 'ja'>} */
export const locale = writable(typeof window !== 'undefined' ? initialLocale() : 'en');

function syncDocument(/** @type {'en' | 'zh' | 'ja'} */ loc) {
  if (typeof document === 'undefined') return;
  const langMap = { en: 'en', zh: 'zh-Hans', ja: 'ja' };
  document.documentElement.lang = langMap[loc] || 'en';
  document.documentElement.setAttribute('data-theme', 'whalebot');
}

locale.subscribe(syncDocument);

/** @param {'en' | 'zh' | 'ja'} loc @param {string} path @param {Record<string, string | number>} [vars] */
export function translate(loc, path, vars = {}) {
  const bundle = bundles[loc] || bundles.en;
  let s = getNested(bundle, path);
  if (s == null) s = getNested(bundles.en, path);
  if (s == null) s = path;
  if (typeof s !== 'string') s = String(s);
  for (const [k, v] of Object.entries(vars)) {
    s = s.split(`{${k}}`).join(String(v));
  }
  return s;
}

/** Reactive: `{$_('nav.overview')}` or `{$_('overview.statDelta', { n: 3 })}` */
export const _ = derived(locale, ($loc) => (path, vars) => translate($loc, path, vars));

/** Use inside non-reactive script (e.g. confirm, async handlers). */
export function t(path, vars) {
  return translate(get(locale), path, vars);
}

/** @param {'en' | 'zh' | 'ja'} loc */
export function setLocale(loc) {
  if (loc !== 'en' && loc !== 'zh' && loc !== 'ja') return;
  locale.set(loc);
  try {
    localStorage.setItem(STORAGE_KEY, loc);
  } catch {
    /* ignore */
  }
}
