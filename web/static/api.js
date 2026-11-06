import { t } from './i18n.js';

// Client for the JSON API. The session cookie is HttpOnly, so the page cannot read it;
// the server hands us a CSRF token instead, which must accompany every change.
export const auth = { user: null, demo: false, loginEnabled: true, canChangePassword: false, statusPage: false };

let csrf = '';
let onUnauthorized = () => {};
export const setUnauthorizedHandler = (fn) => { onUnauthorized = fn; };

export class ApiError extends Error {
  constructor(status, message, body) {
    super(message);
    this.status = status;
    this.body = body || {};
  }
}

async function request(method, path, body) {
  const headers = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (method !== 'GET' && csrf) headers['X-CSRF-Token'] = csrf;

  let res;
  try {
    res = await fetch(path, {
      method, headers, credentials: 'same-origin', cache: 'no-store',
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  } catch {
    throw new ApiError(0, t('Cannot reach the server. Check your connection.'));
  }

  let data = null;
  if (res.status !== 204 && (res.headers.get('Content-Type') || '').includes('json')) {
    try { data = await res.json(); } catch { /* leave null */ }
  }
  if (!res.ok) {
    if (res.status === 401 && path !== '/api/login' && path !== '/api/session') onUnauthorized();
    throw new ApiError(res.status, (data && data.error) || t('Request failed ({status})', { status: res.status }), data);
  }
  return data;
}

const e = encodeURIComponent;

export const api = {
  async session() {
    const s = await request('GET', '/api/session');
    csrf = s.csrf || '';
    auth.user = s.user;
    auth.demo = !!s.demo;
    auth.canChangePassword = !!s.can_change_password;
    auth.statusPage = !!s.status_page;
    return s;
  },
  async login(user, password) {
    const s = await request('POST', '/api/login', { user, password });
    csrf = s.csrf || '';
    auth.user = s.user;
    auth.canChangePassword = !!s.can_change_password;
    auth.statusPage = !!s.status_page;
    return s;
  },
  changePassword: (current, next) => request('POST', '/api/password', { current, new: next }),
  async logout() {
    try { await request('POST', '/api/logout'); } finally { csrf = ''; auth.user = null; }
  },
  sites: () => request('GET', '/api/sites').then((r) => r.sites),
  site: (id) => request('GET', `/api/sites/${e(id)}`),
  create: (site) => request('POST', '/api/sites', site),
  patch: (id, fields) => request('PATCH', `/api/sites/${e(id)}`, fields),
  remove: (id) => request('DELETE', `/api/sites/${e(id)}`),
  rotateToken: (id) => request('POST', `/api/sites/${e(id)}/token`),
  pause: (id) => request('POST', `/api/sites/${e(id)}/pause`),
  maintenance: (id, minutes) => request('POST', `/api/sites/${e(id)}/maintenance`, { minutes }),
  endMaintenance: (id) => request('DELETE', `/api/sites/${e(id)}/maintenance`),
  resume: (id) => request('POST', `/api/sites/${e(id)}/resume`),
  checkNow: (id) => request('POST', `/api/sites/${e(id)}/check`),
  checkDraft: (site) => request('POST', '/api/check', site),
  recipients: () => request('GET', '/api/recipients').then((r) => r.recipients),
  exportSites: (tokens) => request('GET', `/api/export${tokens ? '?tokens=1' : ''}`),
  importSites: (file, dryRun) => request('POST', `/api/import${dryRun ? '?dry_run=1' : ''}`, file),
  history: (id, range) => request('GET', `/api/sites/${e(id)}/history?range=${e(range)}`),
  /** One page of incidents, newest first: {incidents, more}. Pass the last `started` as `before` to read on. */
  incidents: (siteId, before) => {
    const q = new URLSearchParams();
    if (siteId) q.set('site', siteId);
    if (before) q.set('before', before);
    return request('GET', `/api/incidents${q.size ? `?${q}` : ''}`);
  },
};
