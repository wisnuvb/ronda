// Small shared components: status badge, history strip, stat tile, toast, confirm dialog.
import { h, uid } from './dom.js';
import { icon } from './icons.js';
import { t } from './i18n.js';
import { fmtUnit } from './fmt.js';

const STATES = {
  up: ['circle-check', t('Up')],
  down: ['circle-x', t('Down')],
  paused: ['circle-pause', t('Paused')],
  maintenance: ['wrench', t('Maintenance')],
  unknown: ['circle-help', t('Waiting')],
};

/** Status is always icon + word, never colour alone. */
export function statusBadge(state) {
  const [ic, label] = STATES[state] || STATES.unknown;
  return h('span', { class: `badge st-${STATES[state] ? state : 'unknown'}` }, icon(ic, 14), label);
}

/** The last checks as a row of bars: tall = failed, short = ok. values are latency ms, -1 for a failure. */
export function strip(values, slots = 30) {
  const vals = values || [];
  const bad = vals.filter((v) => v < 0).length;
  const label = vals.length
    ? t('Last {n} checks: {ok} passed, {bad} failed', { n: vals.length, ok: vals.length - bad, bad })
    : t('No history yet');
  const bars = [];
  for (let i = 0; i < slots - vals.length; i++) bars.push(h('i', { class: 'b none' }));
  for (const v of vals.slice(-slots)) bars.push(h('i', { class: v < 0 ? 'b bad' : 'b ok' }));
  return h('span', { class: 'strip', role: 'img', 'aria-label': label }, bars);
}

/** A small "TCP" or "DNS" tag; web sites need none. */
export function typeTag(site) {
  if (site.type === 'tcp') return h('span', { class: 'type-tag' }, 'TCP');
  if (site.type === 'dns') return h('span', { class: 'type-tag' }, `DNS ${site.dns_type || 'A'}`);
  if (site.type === 'push') return h('span', { class: 'type-tag' }, 'PUSH');
  return null;
}

export function tile(label, value, { sub, cls, ic } = {}) {
  return h('div', { class: `tile${cls ? ` ${cls}` : ''}` },
    h('div', { class: 'tile-label' }, ic ? icon(ic, 14) : null, label),
    h('div', { class: 'tile-value' }, value),
    sub ? h('div', { class: 'tile-sub' }, sub) : null);
}

export function toast(message, kind = 'ok') {
  const root = document.getElementById('toasts');
  const el = h('div', { class: `toast toast-${kind}` },
    icon(kind === 'ok' ? 'circle-check' : 'alert', 16),
    h('span', {}, message),
    h('button', { type: 'button', class: 'toast-close', 'aria-label': t('Dismiss notification'), onclick: () => el.remove() }, icon('x', 14)));
  root.append(el);
  setTimeout(() => el.remove(), 6000);
}

// Minutes, with their label written out in the page's language ("2 hours", "2 jam").
const DURATIONS = [[30, 'minute', 30], [60, 'hour', 1], [120, 'hour', 2], [240, 'hour', 4], [720, 'hour', 12], [1440, 'hour', 24], [4320, 'day', 3], [10080, 'day', 7]];

/** Asks how long a site should be in maintenance. Resolves the minutes, or null if cancelled. */
export function maintenanceDialog(site) {
  return new Promise((resolve) => {
    const titleId = uid('dlg-title');
    const descId = uid('dlg-desc');
    const selId = uid('dlg-dur');
    const sel = h('select', { id: selId, class: 'input' }, DURATIONS.map(([v, unit, n]) => h('option', { value: v, selected: v === 120 }, fmtUnit(n, unit))));
    const dlg = h('dialog', { class: 'dlg dlg-sm', 'aria-labelledby': titleId, 'aria-describedby': descId },
      h('form', { class: 'dlg-body', method: 'dialog' },
        h('h2', { id: titleId, class: 'dlg-title' }, t('Maintenance: {name}', { name: site.name })),
        h('p', { id: descId }, t('During maintenance the site is not checked and sends no notifications, and checking resumes by itself when the time is up. Good for deploys or server migrations.')),
        h('div', { class: 'field' }, h('label', { for: selId, class: 'label' }, t('Maintenance length')), sel),
        h('div', { class: 'dlg-actions' },
          h('button', { type: 'button', class: 'btn', onclick: () => dlg.close('cancel') }, t('Cancel')),
          h('button', { type: 'submit', class: 'btn btn-primary', value: 'ok' }, t('Start maintenance')))));
    dlg.addEventListener('close', () => {
      const minutes = dlg.returnValue === 'ok' ? Number(sel.value) : null;
      dlg.remove();
      resolve(minutes);
    });
    document.body.append(dlg);
    dlg.showModal();
    sel.focus();
  });
}

/** Resolves true on confirm. Focus starts on "Cancel" so Enter never destroys anything by accident. */
export function confirmDialog({ title, message, confirmText = t('Yes'), danger = false }) {
  return new Promise((resolve) => {
    const titleId = uid('dlg-title');
    const descId = uid('dlg-desc');
    const cancel = h('button', { type: 'button', class: 'btn', onclick: () => dlg.close('cancel') }, t('Cancel'));
    const dlg = h('dialog', { class: 'dlg dlg-sm', 'aria-labelledby': titleId, 'aria-describedby': descId },
      h('div', { class: 'dlg-body' },
        h('h2', { id: titleId, class: 'dlg-title' }, title),
        h('p', { id: descId }, message),
        h('div', { class: 'dlg-actions' },
          cancel,
          h('button', { type: 'button', class: `btn ${danger ? 'btn-danger' : 'btn-primary'}`, onclick: () => dlg.close('ok') }, confirmText))));
    dlg.addEventListener('close', () => {
      const ok = dlg.returnValue === 'ok';
      dlg.remove();
      resolve(ok);
    });
    document.body.append(dlg);
    dlg.showModal();
    cancel.focus();
  });
}
