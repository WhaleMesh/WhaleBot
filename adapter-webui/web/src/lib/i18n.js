import { writable, derived, get } from 'svelte/store';

const STORAGE_KEY = 'adapter_webui_lang';

function getNested(obj, path) {
  return path.split('.').reduce((o, k) => (o && o[k] !== undefined ? o[k] : undefined), obj);
}

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
  } catch { /* ignore */ }
  return null;
}

function initialLocale() {
  return readSavedLocale() || browserLocale();
}

export const locale = writable(typeof window !== 'undefined' ? initialLocale() : 'en');

function syncDocument(loc) {
  if (typeof document === 'undefined') return;
  const langMap = { en: 'en', zh: 'zh-Hans', ja: 'ja' };
  document.documentElement.lang = langMap[loc] || 'en';
  document.documentElement.setAttribute('data-theme', 'whalebot');
}

locale.subscribe(syncDocument);

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

export const _ = derived(locale, ($loc) => (path, vars) => translate($loc, path, vars));

export function t(path, vars) {
  return translate(get(locale), path, vars);
}

export function setLocale(loc) {
  if (loc !== 'en' && loc !== 'zh' && loc !== 'ja') return;
  locale.set(loc);
  try { localStorage.setItem(STORAGE_KEY, loc); } catch { /* ignore */ }
}

const en = {
  brand: { title: 'WhaleBot Chat' },
  layout: {
    brandShort: 'WhaleBot',
    brandRepoAria: 'WhaleBot repository on GitHub (opens in a new tab)',
    poweredByBefore: 'Powered by ',
    poweredByAfter: '',
    whaleMesh: 'WhaleMesh',
    newChat: 'New Chat',
    accountSettings: 'Account settings',
    logout: 'Log out',
  },
  lang: { aria: 'Language', en: 'English', zh: '中文', ja: '日本語' },
  auth: {
    loginTitle: 'Sign in',
    loginSubtitle: 'Chat interface',
    username: 'Username',
    password: 'Password',
    signIn: 'Sign in',
    signingIn: 'Signing in…',
    loadSession: 'Checking session…',
    errorLogin: 'Invalid username or password',
    currentPassword: 'Current password',
    newPassword: 'New password',
    confirmNewPassword: 'Confirm new password',
    accountPasswordHint: 'Leave new password and confirmation empty to keep your current password.',
    save: 'Save',
    cancel: 'Cancel',
    errorGeneric: 'Something went wrong',
    noChanges: 'No changes to apply.',
    invalidCurrentPassword: 'Current password is incorrect.',
    passwordRequired: 'Password is required.',
    requiredField: 'Required.',
  },
  chat: {
    emptyTitle: 'How can I help you today?',
    emptyHint: 'Start a conversation by typing a message below.',
    inputPlaceholder: 'Type a message…',
    send: 'Send',
    sending: 'Sending…',
    processing: 'Processing…',
    deleteSession: 'Delete',
    confirmDelete: 'Delete this conversation? This cannot be undone.',
    sessionsTitle: 'Conversations',
    noSessions: 'No conversations yet.',
    errorPrefix: 'Error: ',
    attachmentDownload: 'Download',
    thoughtToggle: 'Thought (click to expand)',
  },
};

const zhPart = {
  brand: { title: 'WhaleBot 对话' },
  layout: {
    brandShort: 'WhaleBot',
    brandRepoAria: 'WhaleBot 项目仓库（新标签页打开）',
    poweredByBefore: '由 ',
    poweredByAfter: ' 驱动',
    whaleMesh: 'WhaleMesh',
    newChat: '新对话',
    accountSettings: '账户设置',
    logout: '退出登录',
  },
  lang: { en: 'English', zh: '中文', ja: '日本語' },
  auth: {
    loginTitle: '登录',
    loginSubtitle: '对话界面',
    username: '用户名',
    password: '密码',
    signIn: '登录',
    signingIn: '登录中…',
    loadSession: '正在验证会话…',
    errorLogin: '用户名或密码错误',
    currentPassword: '当前密码',
    newPassword: '新密码',
    confirmNewPassword: '确认新密码',
    accountPasswordHint: '不修改密码时，请将新密码与确认留空。',
    save: '保存',
    cancel: '取消',
    errorGeneric: '出错了',
    noChanges: '没有可提交的更改。',
    invalidCurrentPassword: '当前密码不正确。',
    passwordRequired: '密码不能为空。',
    requiredField: '必填。',
  },
  chat: {
    emptyTitle: '有什么可以帮您的？',
    emptyHint: '在下方输入消息开始对话。',
    inputPlaceholder: '输入消息…',
    send: '发送',
    sending: '发送中…',
    processing: '处理中…',
    deleteSession: '删除',
    confirmDelete: '删除此对话？此操作不可撤销。',
    sessionsTitle: '对话列表',
    noSessions: '暂无对话。',
    errorPrefix: '错误：',
    attachmentDownload: '下载',
    thoughtToggle: '思考过程（点击展开）',
  },
};

const jaPart = {
  brand: { title: 'WhaleBot チャット' },
  layout: {
    brandShort: 'WhaleBot',
    brandRepoAria: 'GitHub 上の WhaleBot リポジトリ（新しいタブ）',
    poweredByBefore: 'Powered by ',
    poweredByAfter: '',
    whaleMesh: 'WhaleMesh',
    newChat: '新しいチャット',
    accountSettings: 'アカウント設定',
    logout: 'ログアウト',
  },
  lang: { en: 'English', zh: '中文', ja: '日本語' },
  auth: {
    loginTitle: 'サインイン',
    loginSubtitle: 'チャット',
    username: 'ユーザー名',
    password: 'パスワード',
    signIn: 'サインイン',
    signingIn: 'サインイン中…',
    loadSession: 'セッション確認中…',
    errorLogin: 'ユーザー名またはパスワードが正しくありません',
    currentPassword: '現在のパスワード',
    newPassword: '新しいパスワード',
    confirmNewPassword: '新しいパスワード（確認）',
    accountPasswordHint: 'パスワードを変更しない場合は、新しいパスワードと確認の両方を空にしてください。',
    save: '保存',
    cancel: 'キャンセル',
    errorGeneric: 'エラーが発生しました',
    noChanges: '変更がありません。',
    invalidCurrentPassword: '現在のパスワードが正しくありません。',
    passwordRequired: 'パスワードを入力してください。',
    requiredField: '必須です。',
  },
  chat: {
    emptyTitle: 'お手伝いできることはありますか？',
    emptyHint: '下にメッセージを入力して会話を始めましょう。',
    inputPlaceholder: 'メッセージを入力…',
    send: '送信',
    sending: '送信中…',
    processing: '処理中…',
    deleteSession: '削除',
    confirmDelete: 'この会話を削除しますか？元に戻せません。',
    sessionsTitle: '会話一覧',
    noSessions: '会話はまだありません。',
    errorPrefix: 'エラー: ',
    attachmentDownload: 'ダウンロード',
    thoughtToggle: '思考（クリックで展開）',
  },
};

function deepMerge(base, over) {
  if (!over) return base;
  const out = { ...base };
  for (const k of Object.keys(over)) {
    const bv = base[k];
    const ov = over[k];
    if (ov && typeof ov === 'object' && !Array.isArray(ov) && bv && typeof bv === 'object' && !Array.isArray(bv)) {
      out[k] = deepMerge(bv, ov);
    } else {
      out[k] = ov;
    }
  }
  return out;
}

export const bundles = {
  en,
  zh: deepMerge(en, zhPart),
  ja: deepMerge(en, jaPart),
};
