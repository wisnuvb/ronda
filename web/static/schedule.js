// Weekly maintenance windows: a summary line for the detail page and the dialog that edits them.
// Start times are wall-clock times in the server's time zone (the same one the rest of the dashboard and the
// Telegram messages use), so a window that starts at 02:00 stays at 02:00 when daylight saving changes.
import { h, uid } from './dom.js';
import { icon } from './icons.js';
import { api, ApiError } from './api.js';
import { fmtLong, weekdayName, joinList } from './fmt.js';
import { toast } from './ui.js';
import { t, timezone } from './i18n.js';

// Monday first, as people read a week; the numbers are the server's (0 = Sunday). Names come from Intl.
const DAYS = [1, 2, 3, 4, 5, 6, 0];
const DURATIONS = [30, 60, 120, 180, 240, 360, 720, 1440]; // minutes
const MAX_ROWS = 5;
const order = (d) => DAYS.indexOf(d);

/** "Every Saturday and Sunday at 02:00, for 2 hours" */
export function describeWindow(w) {
  const duration = fmtLong(w.minutes * 60);
  if (w.days.length === 7) return t('Every day at {time}, for {duration}', { time: w.start, duration });
  const days = joinList([...w.days].sort((a, b) => order(a) - order(b)).map((d) => weekdayName(d, 'long')));
  return t('Every {days} at {time}, for {duration}', { days, time: w.start, duration });
}

const blank = () => ({ days: [6], start: '02:00', minutes: 120 });

export function openScheduleForm({ site, onSaved }) {
  const titleId = uid('sched-title');
  let windows = (site.maintenance_weekly || []).map((w) => ({ days: [...w.days], start: w.start, minutes: w.minutes }));
  let busy = false;

  const list = h('div', { class: 'windows' });
  const summary = h('div', { class: 'error-summary', role: 'alert', tabindex: '-1', hidden: true });
  const addBtn = h('button', { type: 'button', class: 'btn' }, icon('plus', 16), t('Add window'));
  const saveBtn = h('button', { type: 'submit', class: 'btn btn-primary' }, t('Save schedule'));
  const cancelBtn = h('button', { type: 'button', class: 'btn btn-ghost', onclick: () => dlg.close() }, t('Cancel'));

  function row(w, i) {
    const err = h('p', { class: 'field-error', hidden: true });
    const timeId = uid('win-time');
    const durId = uid('win-dur');
    const time = h('input', { id: timeId, class: 'input', type: 'time', required: true, value: w.start });
    time.addEventListener('input', () => { w.start = time.value; });
    const dur = h('select', { id: durId, class: 'input' }, (DURATIONS.includes(w.minutes) ? DURATIONS : [...DURATIONS, w.minutes])
      .map((m) => h('option', { value: m, selected: m === w.minutes }, fmtLong(m * 60))));
    dur.addEventListener('change', () => { w.minutes = Number(dur.value); });
    const days = DAYS.map((d) => {
      const cb = h('input', { type: 'checkbox', checked: w.days.includes(d) });
      cb.addEventListener('change', () => {
        w.days = cb.checked ? [...w.days, d] : w.days.filter((x) => x !== d);
        err.hidden = true;
      });
      // The visible text is the short form; screen readers get the full name.
      return h('label', { class: 'radio day' }, cb, h('span', { 'aria-hidden': 'true' }, weekdayName(d, 'short')), h('span', { class: 'sr-only' }, weekdayName(d, 'long')));
    });
    const el = h('fieldset', { class: 'win' },
      h('legend', { class: 'label' }, t('Window {n}', { n: i + 1 })),
      h('div', { class: 'radios', role: 'group', 'aria-label': t('Days for window {n}', { n: i + 1 }) }, days),
      h('div', { class: 'grid-2' },
        h('div', { class: 'field' }, h('label', { for: timeId, class: 'label' }, t('Start ({zone})', { zone: timezone })), time),
        h('div', { class: 'field' }, h('label', { for: durId, class: 'label' }, t('Length')), dur)),
      err,
      h('button', { type: 'button', class: 'btn btn-ghost', onclick: () => { windows.splice(i, 1); render(); } }, icon('trash', 16), t('Delete window {n}', { n: i + 1 })));
    el.err = err;
    return el;
  }

  let rowEls = [];
  function render() {
    rowEls = windows.map(row);
    list.replaceChildren(...(rowEls.length ? rowEls : [h('p', { class: 'muted' }, t('No schedule. Save it empty to delete the existing schedule.'))]));
    addBtn.disabled = windows.length >= MAX_ROWS;
  }
  addBtn.addEventListener('click', () => {
    windows.push(blank());
    render();
    rowEls[rowEls.length - 1].querySelector('input').focus();
  });

  const form = h('form', { class: 'dlg-body', novalidate: true },
    h('div', { class: 'dlg-head' },
      h('h2', { id: titleId, class: 'dlg-title' }, t('Scheduled maintenance: {name}', { name: site.name })),
      h('button', { type: 'button', class: 'btn btn-ghost btn-icon', 'aria-label': t('Close'), onclick: () => dlg.close() }, icon('x', 18))),
    h('p', { class: 'help' }, t('While a window runs, the site is not checked and sends no notifications, then resumes by itself. Good for backups, restarts or routine updates. Times are in the server time zone ({zone}) and follow its daylight saving; a window may cross midnight.', { zone: timezone })),
    summary, list,
    h('div', { class: 'dlg-actions' }, addBtn, h('span', { class: 'spacer' }), cancelBtn, saveBtn));

  const dlg = h('dialog', { class: 'dlg', 'aria-labelledby': titleId }, form);
  render();

  /** Returns the first row with a problem, after marking each one. */
  function validate() {
    let first = null;
    windows.forEach((w, i) => {
      const e = rowEls[i].err;
      e.hidden = true;
      let msg = '';
      if (!w.days.length) msg = t('Choose at least one day.');
      else if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(w.start || '')) msg = t('Enter a valid start time.');
      if (msg) {
        e.textContent = msg;
        e.hidden = false;
        first = first || rowEls[i];
      }
    });
    return first;
  }

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    if (busy) return;
    summary.hidden = true;
    const bad = validate();
    if (bad) { bad.querySelector('input').focus(); return; }
    busy = true;
    saveBtn.disabled = true;
    saveBtn.textContent = t('Saving…');
    try {
      const body = windows.map((w) => ({ days: [...w.days].sort((a, b) => order(a) - order(b)), start: w.start, minutes: w.minutes }));
      const saved = await api.patch(site.id, { maintenance_weekly: body });
      dlg.close('saved');
      toast(body.length ? t('Maintenance schedule for {name} saved.', { name: saved.name }) : t('Maintenance schedule for {name} deleted.', { name: saved.name }));
      if (onSaved) onSaved(saved);
    } catch (err) {
      summary.replaceChildren(h('strong', {}, err instanceof ApiError ? err.message : t('Something went wrong.')));
      summary.hidden = false;
      summary.focus();
      busy = false;
      saveBtn.disabled = false;
      saveBtn.textContent = t('Save schedule');
    }
  });

  dlg.addEventListener('close', () => dlg.remove());
  document.body.append(dlg);
  dlg.showModal();
  const firstInput = dlg.querySelector('input');
  (firstInput || addBtn).focus();
}
