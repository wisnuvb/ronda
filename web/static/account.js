// Change-password dialog. The fields carry the autocomplete hints password managers look for
// (current-password / new-password), and nothing here blocks paste.
import { h, uid } from './dom.js';
import { icon } from './icons.js';
import { api, ApiError } from './api.js';
import { toast } from './ui.js';
import { t } from './i18n.js';

const MIN = 12;

export function openPasswordForm() {
  const titleId = uid('pw-title');
  let busy = false;

  const make = (label, autocomplete, help) => {
    const id = uid('pw');
    const errId = `${id}-err`;
    const input = h('input', { id, class: 'input', type: 'password', autocomplete, required: true, 'aria-required': 'true', 'aria-describedby': help ? `${id}-help ${errId}` : errId });
    const err = h('p', { id: errId, class: 'field-error', hidden: true });
    const wrap = h('div', { class: 'field' },
      h('label', { for: id, class: 'label' }, label),
      input,
      help ? h('p', { id: `${id}-help`, class: 'help' }, help) : null,
      err);
    return { input, err, wrap };
  };
  const cur = make(t('Current password'), 'current-password');
  const next = make(t('New password'), 'new-password', t('At least {min} characters. A long phrase is stronger than a short complicated password; a password manager may fill it in.', { min: MIN }));
  const again = make(t('Repeat the new password'), 'new-password');

  const show = h('input', { type: 'checkbox', id: uid('pw-show') });
  show.addEventListener('change', () => { for (const f of [cur, next, again]) f.input.type = show.checked ? 'text' : 'password'; });

  const summary = h('div', { class: 'error-summary', role: 'alert', tabindex: '-1', hidden: true });
  const saveBtn = h('button', { type: 'submit', class: 'btn btn-primary' }, t('Change password'));
  const cancelBtn = h('button', { type: 'button', class: 'btn btn-ghost', onclick: () => dlg.close() }, t('Cancel'));

  const form = h('form', { class: 'dlg-body', novalidate: true },
    h('div', { class: 'dlg-head' },
      h('h2', { id: titleId, class: 'dlg-title' }, t('Change password')),
      h('button', { type: 'button', class: 'btn btn-ghost btn-icon', 'aria-label': t('Close'), onclick: () => dlg.close() }, icon('x', 18))),
    h('p', { class: 'help' }, t('After the change, all other devices that are signed in will be signed out. This session stays signed in.')),
    summary, cur.wrap, next.wrap, again.wrap,
    h('div', { class: 'check' }, show, h('label', { for: show.id }, t('Show password'))),
    h('div', { class: 'dlg-actions' }, cancelBtn, saveBtn));

  const dlg = h('dialog', { class: 'dlg dlg-sm', 'aria-labelledby': titleId }, form);

  function setError(f, msg) {
    f.err.textContent = msg || '';
    f.err.hidden = !msg;
    if (msg) f.input.setAttribute('aria-invalid', 'true'); else f.input.removeAttribute('aria-invalid');
  }
  for (const f of [cur, next, again]) f.input.addEventListener('input', () => setError(f, ''));

  /** The same rules the server applies, so a typo costs no round trip. Returns the first field at fault. */
  function validate() {
    const problems = [
      [cur, !cur.input.value ? t('Enter your current password.') : ''],
      [next, [...next.input.value].length < MIN ? t('The new password must be at least {min} characters.', { min: MIN })
        : next.input.value === cur.input.value ? t('The new password must differ from the current one.') : ''],
      [again, again.input.value !== next.input.value ? t('The repeated password does not match the new one.') : ''],
    ];
    let first = null;
    for (const [f, msg] of problems) {
      setError(f, msg);
      if (msg && !first) first = f;
    }
    return first;
  }

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    if (busy) return;
    summary.hidden = true;
    const bad = validate();
    if (bad) { bad.input.focus(); return; }
    busy = true;
    saveBtn.disabled = true;
    saveBtn.textContent = t('Saving…');
    try {
      await api.changePassword(cur.input.value, next.input.value);
      dlg.close('saved');
      toast(t('Password changed. Other devices have been signed out.'));
    } catch (err) {
      if (err instanceof ApiError && err.status === 403 && /current|saat ini/i.test(err.message)) {
        setError(cur, err.message);
        cur.input.select();
      } else if (err instanceof ApiError && err.status === 400) {
        setError(next, err.message);
        next.input.focus();
      } else {
        summary.replaceChildren(h('strong', {}, err.message || t('Something went wrong.')));
        summary.hidden = false;
        summary.focus();
      }
      busy = false;
      saveBtn.disabled = false;
      saveBtn.textContent = t('Change password');
    }
  });

  dlg.addEventListener('close', () => dlg.remove());
  document.body.append(dlg);
  dlg.showModal();
  cur.input.focus();
}
