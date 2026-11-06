// Login screen. Browsers and password managers can fill and paste into it (autocomplete
// attributes, no paste blocking); the show/hide button is there for people typing by hand.
import { h, uid } from './dom.js';
import { icon } from './icons.js';
import { api, auth } from './api.js';
import { t, tNodes, APP_NAME } from './i18n.js';

export function mount(root, { onLogin }) {
  document.title = `${t('Sign in')} · ${APP_NAME}`;
  const userId = uid('login-user');
  const passId = uid('login-pass');
  const errId = uid('login-err');

  const user = h('input', { id: userId, name: 'username', class: 'input', type: 'text', autocomplete: 'username', autocapitalize: 'none', spellcheck: 'false', required: true, 'aria-required': 'true', value: 'admin' });
  const pass = h('input', { id: passId, name: 'password', class: 'input', type: 'password', autocomplete: 'current-password', required: true, 'aria-required': 'true', 'aria-describedby': errId });
  const error = h('p', { id: errId, class: 'form-error', role: 'alert', hidden: true });
  const submit = h('button', { type: 'submit', class: 'btn btn-primary btn-block' }, t('Sign in'));

  const toggle = h('button', { type: 'button', class: 'btn btn-ghost btn-icon pw-toggle', 'aria-pressed': 'false', 'aria-label': t('Show password'), 'aria-controls': passId });
  toggle.append(icon('eye', 18));
  toggle.addEventListener('click', () => {
    const show = pass.type === 'password';
    pass.type = show ? 'text' : 'password';
    toggle.setAttribute('aria-pressed', String(show));
    toggle.setAttribute('aria-label', show ? t('Hide password') : t('Show password'));
    toggle.replaceChildren(icon(show ? 'eye-off' : 'eye', 18));
  });

  const form = h('form', { class: 'card login-card', novalidate: true },
    h('h1', { class: 'login-title' }, t('Sign in to {app}')),
    h('p', { class: 'muted' }, t('Monitor your sites from the outside and get a Telegram message when one goes down.')),
    auth.loginEnabled ? null : h('div', { class: 'callout callout-warn' },
      icon('alert', 18),
      h('div', {},
        h('strong', {}, t('Dashboard login is not enabled yet.')),
        h('p', {}, tNodes('Create a password hash, then put it in {field} in the config and restart:', { field: h('code', {}, 'auth.password_hash') })),
        h('pre', { class: 'code' }, "read -rs PW; printf '%s\\n' \"$PW\" | ronda hash-password"))),
    h('div', { class: 'field' }, h('label', { for: userId, class: 'label' }, t('Username')), user),
    h('div', { class: 'field' },
      h('label', { for: passId, class: 'label' }, t('Password')),
      h('div', { class: 'input-group' }, pass, toggle)),
    error,
    submit);

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    error.hidden = true;
    if (!user.value.trim() || !pass.value) {
      error.textContent = !user.value.trim() ? t('Enter your username.') : t('Enter your password.');
      error.hidden = false;
      (!user.value.trim() ? user : pass).focus();
      return;
    }
    submit.disabled = true;
    submit.textContent = t('Checking…');
    form.setAttribute('aria-busy', 'true');
    try {
      await api.login(user.value.trim(), pass.value);
      pass.value = '';
      onLogin();
    } catch (err) {
      error.textContent = err.message;
      error.hidden = false;
      pass.select();
      submit.disabled = false;
      submit.textContent = t('Sign in');
      form.setAttribute('aria-busy', 'false');
    }
  });

  root.append(h('div', { class: 'login-wrap' }, form));
  (user.value ? pass : user).focus();
  return { unmount() {} };
}
