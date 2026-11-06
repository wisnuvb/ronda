// Shell: session check, hash router, theme, logout. Views live in their own modules.
import { api, auth, setUnauthorizedHandler } from './api.js';
import { h, clear } from './dom.js';
import { icon } from './icons.js';
import { toast } from './ui.js';
import { t, isDemo } from './i18n.js';
import { openPasswordForm } from './account.js';
import * as login from './login.js';
import * as list from './list.js';
import * as detail from './detail.js';
import * as incidents from './incidents.js';

const main = document.getElementById('main');
const topbar = document.getElementById('topbar');
const themeBtn = document.getElementById('theme-toggle');
const logoutBtn = document.getElementById('logout');
const accountBtn = document.getElementById('account');
const statusLink = document.getElementById('status-link');
if (isDemo) document.getElementById('demo-banner').hidden = false;
let current = null;

// ---- theme ----
const dark = window.matchMedia('(prefers-color-scheme: dark)');
const effectiveTheme = () => document.documentElement.dataset.theme || (dark.matches ? 'dark' : 'light');

function paintThemeButton() {
  const isDark = effectiveTheme() === 'dark';
  themeBtn.replaceChildren(icon(isDark ? 'sun' : 'moon', 18), h('span', { class: 'sr-only' }, isDark ? t('Switch to light mode') : t('Switch to dark mode')));
}
themeBtn.addEventListener('click', () => {
  const next = effectiveTheme() === 'dark' ? 'light' : 'dark';
  document.documentElement.dataset.theme = next;
  try { localStorage.setItem('ronda-theme', next); } catch { /* storage blocked: the choice lasts until reload */ }
  paintThemeButton();
});
dark.addEventListener('change', paintThemeButton);
paintThemeButton();

logoutBtn.replaceChildren(icon('log-out', 16), t('Sign out'));
accountBtn.replaceChildren(icon('key', 18), h('span', { class: 'sr-only' }, t('Change password')));
accountBtn.title = t('Change password');
accountBtn.addEventListener('click', openPasswordForm);
logoutBtn.addEventListener('click', async () => {
  try { await api.logout(); } catch { /* the cookie is cleared server-side either way */ }
  location.hash = '#/';
  show();
});

// The skip link must not touch the address: the router reads location.hash, so a plain href="#main"
// would be taken for a route and throw the person out of the page they were on.
document.getElementById('skip').addEventListener('click', (e) => {
  e.preventDefault();
  main.focus();
});

// ---- routing ----
function parseRoute() {
  const parts = location.hash.replace(/^#/, '').split('/').filter(Boolean);
  // "situs" and "insiden" are the old Indonesian paths; keep them so bookmarks still open.
  if ((parts[0] === 'sites' || parts[0] === 'situs') && parts[1]) return { name: 'detail', id: decodeURIComponent(parts[1]) };
  if (parts[0] === 'incidents' || parts[0] === 'insiden') return { name: 'incidents' };
  return { name: 'list' };
}

function show({ focus = true } = {}) {
  if (current) current.unmount();
  clear(main);

  if (!auth.user) {
    topbar.hidden = true;
    current = login.mount(main, { onLogin: () => { location.hash = '#/'; show(); } });
    return;
  }
  topbar.hidden = false;
  logoutBtn.hidden = auth.demo; // the demo has no login to leave
  accountBtn.hidden = !auth.canChangePassword;
  statusLink.hidden = !auth.statusPage;

  const route = parseRoute();
  for (const a of topbar.querySelectorAll('[data-nav]')) {
    const on = a.dataset.nav === (route.name === 'incidents' ? 'incidents' : 'list');
    if (on) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
  }
  const view = route.name === 'detail' ? detail : route.name === 'incidents' ? incidents : list;
  current = view.mount(main, route);

  if (focus) {
    // Move focus to the page heading so keyboard and screen reader users land at the top of the new view.
    requestAnimationFrame(() => {
      const heading = main.querySelector('h1');
      if (heading) { heading.tabIndex = -1; heading.focus({ preventScroll: true }); }
      window.scrollTo(0, 0);
    });
  }
}

setUnauthorizedHandler(() => {
  if (!auth.user) return;
  auth.user = null;
  show();
  toast(t('Session expired. Please sign in again.'), 'bad');
});
window.addEventListener('hashchange', () => show());

(async function boot() {
  try {
    await api.session();
  } catch (err) {
    auth.user = null;
    auth.loginEnabled = !(err.body && err.body.login_enabled === false);
    if (err.status === 0) toast(err.message, 'bad');
  }
  show({ focus: false });
})();
