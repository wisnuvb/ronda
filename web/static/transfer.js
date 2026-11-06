// Export and import of the site list. The file holds configuration only (no history, no ids), so it
// moves a setup to another server or restores one after a mistake.
import { h, uid, announce } from './dom.js';
import { icon } from './icons.js';
import { api, ApiError } from './api.js';
import { toast } from './ui.js';
import { t, tNodes, timezone, APP_NAME } from './i18n.js';

const MAX_BYTES = 8 << 20; // the server refuses more

function download(name, text) {
  const url = URL.createObjectURL(new Blob([text], { type: 'application/json' }));
  const a = h('a', { href: url, download: name, hidden: true });
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10000);
}

function shell(title, ...parts) {
  const titleId = uid('tr-title');
  const closeBtn = h('button', { type: 'button', class: 'btn btn-ghost btn-icon', 'aria-label': t('Close'), onclick: () => dlg.close() }, icon('x', 18));
  const dlg = h('dialog', { class: 'dlg', 'aria-labelledby': titleId },
    h('div', { class: 'dlg-body' },
      h('div', { class: 'dlg-head' }, h('h2', { id: titleId, class: 'dlg-title' }, title), closeBtn),
      ...parts));
  dlg.addEventListener('close', () => dlg.remove());
  document.body.append(dlg);
  dlg.showModal();
  return dlg;
}

/** hasPush: whether any push site exists, which is when the "include tokens" choice means something. */
export function openExportDialog({ hasPush }) {
  const tokId = uid('tr-tok');
  const tokens = h('input', { id: tokId, type: 'checkbox', 'aria-describedby': `${tokId}-help` });
  const summary = h('div', { class: 'error-summary', role: 'alert', tabindex: '-1', hidden: true });
  const go = h('button', { type: 'button', class: 'btn btn-primary' }, icon('download', 16), t('Download file'));
  const dlg = shell(t('Export sites'),
    h('p', {}, tNodes('Downloads all sites with their settings as a single JSON file. Uptime history and incidents are not included. On another server, use {action} to restore them.',
      { action: h('strong', {}, t('Import')) })),
    hasPush ? h('div', { class: 'check' }, tokens, h('label', { for: tokId }, t('Include the ping tokens of push monitors')),
      h('p', { id: `${tokId}-help`, class: 'help' }, t('With tokens, scheduled jobs (cron/backup) keep using the same ping address on the new server. Without tokens, every push monitor gets a new address that must be installed again. A file with tokens is as secret as a password.'))) : null,
    summary,
    h('div', { class: 'dlg-actions' }, h('button', { type: 'button', class: 'btn btn-ghost', onclick: () => dlg.close() }, t('Cancel')), go));

  go.addEventListener('click', async () => {
    go.disabled = true;
    summary.hidden = true;
    try {
      const data = await api.exportSites(hasPush && tokens.checked);
      const day = new Date().toLocaleDateString('sv-SE', { timeZone: timezone }).replaceAll('-', ''); // sv-SE writes YYYY-MM-DD
      download(`${APP_NAME}-sites-${day}.json`, `${JSON.stringify(data, null, 2)}\n`);
      dlg.close();
      toast(data.sites.length === 1 ? t('1 site exported.') : t('{n} sites exported.', { n: data.sites.length }));
    } catch (err) {
      summary.replaceChildren(h('strong', {}, err.message || t('Export failed.')));
      summary.hidden = false;
      summary.focus();
      go.disabled = false;
    }
  });
  go.focus();
}

/** "1 site" / "3 sites", for use inside a longer message as {sites}. */
const sites = (n) => (n === 1 ? t('1 site') : t('{n} sites', { n }));

export function openImportDialog({ onDone }) {
  const fileId = uid('tr-file');
  const file = h('input', { id: fileId, type: 'file', class: 'input', accept: '.json,application/json', 'aria-describedby': `${fileId}-help` });
  const preview = h('div', { class: 'import-preview', role: 'status', 'aria-live': 'polite' });
  const go = h('button', { type: 'button', class: 'btn btn-primary', disabled: true }, t('Import'));
  let parsed = null;
  let busy = false;

  const dlg = shell(t('Import sites'),
    h('p', {}, tNodes('Choose a file made by {action}. You will see the result first; nothing is saved until you press Import. Sites whose address already exists are skipped, so importing the same file twice is safe.',
      { action: h('strong', {}, t('Export')) })),
    h('div', { class: 'field' }, h('label', { for: fileId, class: 'label' }, t('Export file (.json)')), file,
      h('p', { id: `${fileId}-help`, class: 'help' }, t('At most 500 sites on this server.'))),
    preview,
    h('div', { class: 'dlg-actions' }, h('button', { type: 'button', class: 'btn btn-ghost', onclick: () => dlg.close() }, t('Cancel')), go));

  const problem = (msg) => {
    parsed = null;
    go.disabled = true;
    go.textContent = t('Import');
    preview.replaceChildren(h('div', { class: 'callout callout-warn' }, icon('alert', 20), h('div', {}, h('strong', {}, msg))));
    announce(msg);
  };

  file.addEventListener('change', async () => {
    const f = file.files[0];
    preview.replaceChildren();
    parsed = null;
    go.disabled = true;
    if (!f) return;
    if (f.size > MAX_BYTES) return problem(t('The file is too large (8 MB at most).'));
    let data;
    try { data = JSON.parse(await f.text()); } catch { return problem(t('This file is not JSON. Use a file made by Export.')); }
    preview.replaceChildren(h('p', { class: 'muted' }, t('Checking the file…')));
    try {
      const res = await api.importSites(data, true);
      parsed = data;
      show(res, data);
    } catch (err) {
      problem(err.message || t('The file could not be checked.'));
    }
  });

  function show(res, data) {
    const bad = res.items.filter((it) => it.status === 'invalid');
    const skipped = res.items.filter((it) => it.status === 'skipped');
    const hasPush = Array.isArray(data.sites) && data.sites.some((s) => s && s.type === 'push');
    const hasTokens = hasPush && data.sites.some((s) => s && s.type === 'push' && s.push_token);
    const line = (ic, text, cls) => h('p', { class: `import-line ${cls || ''}` }, icon(ic, 18), h('span', {}, text));

    preview.replaceChildren(
      line(res.added ? 'circle-check' : 'info', res.added ? t('{sites} will be added.', { sites: sites(res.added) }) : t('There are no new sites to add.'), res.added ? 'ok' : ''),
      skipped.length ? h('details', { class: 'import-more' },
        h('summary', {}, skipped.length === 1 ? t('1 site already exists and is skipped') : t('{n} sites already exist and are skipped', { n: skipped.length })),
        h('ul', {}, skipped.map((it) => h('li', {}, it.name || it.url)))) : null,
      bad.length ? h('div', { class: 'callout callout-warn' }, icon('alert', 20), h('div', {},
        h('strong', {}, bad.length === 1 ? t('1 site is invalid and will not be imported:') : t('{n} sites are invalid and will not be imported:', { n: bad.length })),
        h('ul', { class: 'import-bad' }, bad.map((it) => h('li', {}, h('span', { class: 'mono' }, `#${it.index}`), ` ${it.name || it.url || ''}: ${it.reason}`))))) : null,
      hasPush ? h('div', { class: 'callout' }, icon('info', 20), h('p', {}, hasTokens
        ? t('Push monitors keep the tokens in the file, so their old ping addresses still work.')
        : t('Push monitors get new ping addresses. Open each monitor to copy it into your job.'))) : null);
    go.disabled = res.added === 0;
    go.textContent = res.added ? t('Import {sites}', { sites: sites(res.added) }) : t('Import');
    announce(t('Check finished: {added} added, {skipped} already exist, {bad} invalid.', { added: res.added, skipped: skipped.length, bad: bad.length }));
  }

  go.addEventListener('click', async () => {
    if (busy || !parsed) return;
    busy = true;
    go.disabled = true;
    go.textContent = t('Importing…');
    try {
      const res = await api.importSites(parsed, false);
      dlg.close();
      toast(res.invalid ? t('{sites} imported, {invalid} skipped as invalid.', { sites: sites(res.added), invalid: res.invalid }) : t('{sites} imported.', { sites: sites(res.added) }));
      onDone();
    } catch (err) {
      busy = false;
      go.disabled = false;
      go.textContent = t('Try again');
      preview.prepend(h('div', { class: 'error-summary', role: 'alert' }, h('strong', {}, err instanceof ApiError ? err.message : t('Import failed.'))));
    }
  });
  file.focus();
}
