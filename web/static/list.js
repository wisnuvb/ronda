// Site list: summary tiles, search, filter, sortable table. Refreshes itself every 10 s.
import { h, clear, announce, poll } from './dom.js';
import { icon } from './icons.js';
import { api } from './api.js';
import { fmtPct, fmtMs, ago, fmtDateTime, fmtClockZone, kindLabel, compareText } from './fmt.js';
import { t, APP_NAME } from './i18n.js';
import { statusBadge, strip, tile, typeTag } from './ui.js';
import { openSiteForm } from './form.js';
import { openExportDialog, openImportDialog } from './transfer.js';

const RANK = { down: 0, unknown: 1, up: 2, maintenance: 3, paused: 4 };
// "Paused" covers both a manual pause and a maintenance window: either way the site is silent.
const bucket = (s) => (s.state === 'maintenance' ? 'paused' : s.state);
const FILTERS = [['all', t('All')], ['down', t('Down')], ['up', t('Up')], ['paused', t('Paused')]];
const SORTS = [
  ['status:1', t('Status (down first)')], ['name:1', t('Name (A–Z)')], ['uptime:1', t('Uptime (lowest first)')], ['latency:-1', t('Response time (slowest first)')],
];

let saved = { filter: 'all', q: '', key: 'status', dir: 1 }; // survives navigating to a site and back

export function mount(root) {
  document.title = `${t('Sites')} · ${APP_NAME}`;
  const st = { sites: null, error: null, updated: 0, ...saved };
  let stopped = false;
  let lastDown = null;

  // A new push site is opened straight away: its ping address is what the person needs next.
  const afterSave = (saved) => { if (saved && saved.type === 'push') location.hash = `#/sites/${encodeURIComponent(saved.id)}`; else load(); };
  const addBtn = h('button', { type: 'button', class: 'btn btn-primary', onclick: () => openSiteForm({ onSaved: afterSave }) }, icon('plus', 16), t('Add site'));
  const exportBtn = h('button', { type: 'button', class: 'btn', onclick: () => openExportDialog({ hasPush: (st.sites || []).some((s) => s.type === 'push') }) }, icon('download', 16), t('Export'));
  const importBtn = h('button', { type: 'button', class: 'btn', onclick: () => openImportDialog({ onDone: load }) }, icon('upload', 16), t('Import'));
  const tiles = h('section', { class: 'tiles', 'aria-label': t('Status summary') });
  const searchId = 'site-search';
  const search = h('input', { id: searchId, type: 'search', class: 'input', autocomplete: 'off', placeholder: t('Name or address'), value: st.q });
  search.addEventListener('input', () => { st.q = search.value; saved.q = st.q; render(); });
  const chips = h('div', { class: 'chips', role: 'group', 'aria-label': t('Filter by status') });
  const chipEls = {};
  for (const [key, label] of FILTERS) {
    const b = h('button', { type: 'button', class: 'chip', 'aria-pressed': String(st.filter === key), onclick: () => { st.filter = key; saved.filter = key; render(); } });
    chipEls[key] = { b, label };
    chips.append(b);
  }
  const sortSel = h('select', { id: 'site-sort', class: 'input', onchange: (e) => { const [k, d] = e.target.value.split(':'); setSort(k, Number(d)); } },
    SORTS.map(([v, label]) => h('option', { value: v, selected: v === `${st.key}:${st.dir}` }, label)));
  const banner = h('div', { class: 'callout callout-warn', role: 'status', hidden: true });
  const body = h('div', { class: 'list-body' });
  const foot = h('p', { class: 'foot muted' });

  root.append(
    h('div', { class: 'page-head' }, h('h1', {}, t('Sites')), h('div', { class: 'actions' }, exportBtn, importBtn, addBtn)),
    tiles,
    h('div', { class: 'toolbar' },
      h('div', { class: 'toolbar-search' }, h('label', { for: searchId, class: 'label' }, t('Search')), search),
      chips,
      h('div', { class: 'toolbar-sort' }, h('label', { for: 'site-sort', class: 'label' }, t('Sort by')), sortSel)),
    banner, body, foot);

  function setSort(key, dir) {
    st.key = key; st.dir = dir;
    saved.key = key; saved.dir = dir;
    const v = `${key}:${dir}`;
    if ([...sortSel.options].some((o) => o.value === v)) sortSel.value = v;
    render();
  }

  async function load() {
    try {
      const sites = await api.sites();
      if (stopped) return;
      st.sites = sites; st.error = null; st.updated = Date.now();
    } catch (err) {
      if (stopped) return;
      st.error = err.message;
    }
    render();
  }

  function visible() {
    const q = st.q.trim().toLowerCase();
    const rows = (st.sites || []).filter((s) =>
      (st.filter === 'all' || bucket(s) === st.filter) && (!q || s.name.toLowerCase().includes(q) || s.url.toLowerCase().includes(q)));
    const by = {
      status: (a, b) => (RANK[a.state] ?? 1) - (RANK[b.state] ?? 1),
      name: () => 0,
      uptime: (a, b) => (a.uptime_24h ?? 101) - (b.uptime_24h ?? 101),
      latency: (a, b) => (a.last && a.last.ok ? a.last.latency_ms : -1) - (b.last && b.last.ok ? b.last.latency_ms : -1),
    }[st.key];
    return rows.sort((a, b) => st.dir * by(a, b) || compareText(a.name, b.name));
  }

  function renderTiles(c, maint) {
    clear(tiles);
    tiles.append(
      tile(t('Total sites'), c.all),
      tile(t('Up'), c.up, { ic: 'circle-check', cls: 'tile-up' }),
      tile(t('Down'), c.down, { ic: 'circle-x', cls: c.down ? 'tile-down' : '' }),
      tile(t('Paused'), c.paused, { ic: 'circle-pause', sub: maint ? t('{n} in maintenance', { n: maint }) : null }));
    if (c.unknown) tiles.append(tile(t('Waiting'), c.unknown, { ic: 'circle-help', sub: t('not checked yet') }));
  }

  function sortHead(key, label, extra = '') {
    const active = st.key === key;
    const dir = active ? (st.dir === 1 ? 'ascending' : 'descending') : 'none';
    return h('th', { scope: 'col', class: extra, 'aria-sort': dir },
      h('button', { type: 'button', class: 'th-btn', dataset: { key: `sort-${key}` }, onclick: () => setSort(key, active ? -st.dir : (key === 'latency' ? -1 : 1)) },
        label, h('span', { class: 'sort-mark', 'aria-hidden': 'true' }, active ? (st.dir === 1 ? '▲' : '▼') : '')));
  }

  function row(s) {
    const last = s.last;
    const why = s.state === 'down' && last && !last.ok ? kindLabel(last.kind) : null;
    return h('tr', { class: `row st-row-${s.state}` },
      h('td', { class: 'c-status', 'data-label': t('Status') }, statusBadge(s.state)),
      h('td', { class: 'c-site' },
        h('a', { class: 'site-link', href: `#/sites/${encodeURIComponent(s.id)}`, dataset: { key: `link-${s.id}` } }, s.name),
        h('span', { class: 'url' }, typeTag(s), s.url),
        why ? h('span', { class: 'why' }, icon('alert', 13), why) : null),
      h('td', { class: 'c-uptime num', 'data-label': t('Uptime (24 h)') }, fmtPct(s.uptime_24h)),
      h('td', { class: 'c-lat num', 'data-label': t('Response time') }, last && last.ok ? fmtMs(last.latency_ms) : '–'),
      h('td', { class: 'c-strip', 'data-label': t('History') }, strip(s.strip)),
      h('td', { class: 'c-seen muted', 'data-label': s.type === 'push' ? t('Last ping') : t('Checked') },
        (() => { const at = s.type === 'push' ? s.last_ping : last && last.time; return at ? h('time', { datetime: new Date(at).toISOString(), title: fmtDateTime(at) }, ago(at)) : '–'; })()));
  }

  function render() {
    const sites = st.sites || [];
    const c = { all: sites.length, up: 0, down: 0, paused: 0, unknown: 0 };
    for (const s of sites) c[bucket(s)] = (c[bucket(s)] || 0) + 1;
    const maint = sites.filter((s) => s.state === 'maintenance').length;

    renderTiles(c, maint);
    for (const [key, { b, label }] of Object.entries(chipEls)) {
      b.replaceChildren(`${label} `, h('span', { class: 'chip-n' }, String(c[key])));
      b.setAttribute('aria-pressed', String(st.filter === key));
    }

    banner.hidden = !st.error;
    if (st.error) banner.replaceChildren(icon('alert', 16), h('span', {}, t('Could not load the latest data: {error} Retrying automatically.', { error: st.error })));

    const focusKey = document.activeElement && document.activeElement.dataset ? document.activeElement.dataset.key : null;
    clear(body);
    if (st.sites === null) {
      body.setAttribute('aria-busy', 'true');
      body.append(h('div', { class: 'skeleton', 'aria-hidden': 'true' }, [1, 2, 3, 4].map(() => h('div', { class: 'sk-row' }))), h('p', { class: 'sr-only' }, t('Loading sites…')));
    } else if (!sites.length) {
      body.removeAttribute('aria-busy');
      body.append(h('div', { class: 'empty card' },
        icon('shield', 28), h('h2', {}, t('No sites are being monitored yet')),
        h('p', { class: 'muted' }, t('Add your first site address. {app} will check it from the outside and tell you on Telegram when it is down.')),
        h('button', { type: 'button', class: 'btn btn-primary', onclick: () => openSiteForm({ onSaved: afterSave }) }, icon('plus', 16), t('Add site'))));
    } else {
      body.removeAttribute('aria-busy');
      const rows = visible();
      if (!rows.length) {
        body.append(h('div', { class: 'empty card' },
          h('h2', {}, t('No sites match')),
          h('p', { class: 'muted' }, t('Change the search or the status filter.')),
          h('button', { type: 'button', class: 'btn', onclick: () => { st.q = ''; st.filter = 'all'; saved.q = ''; saved.filter = 'all'; search.value = ''; render(); } }, t('Clear filters'))));
      } else {
        body.append(h('div', { class: 'table-wrap card' },
          h('table', { class: 'table sites' },
            h('caption', { class: 'sr-only' }, rows.length === 1 ? t('List of 1 site') : t('List of {n} sites', { n: rows.length })),
            h('thead', {}, h('tr', {},
              sortHead('status', t('Status')), sortHead('name', t('Site')),
              sortHead('uptime', t('Uptime (24 h)'), 'num'), sortHead('latency', t('Response time'), 'num'),
              h('th', { scope: 'col' }, t('History (last 30)')), h('th', { scope: 'col' }, t('Checked')))),
            h('tbody', {}, rows.map(row)))));
      }
    }
    if (focusKey) { const el = root.querySelector(`[data-key="${CSS.escape(focusKey)}"]`); if (el) el.focus({ preventScroll: true }); }

    foot.textContent = st.updated ? t('Updated {time} · refreshes every 10 seconds', { time: fmtClockZone(st.updated, true) }) : '';
    if (lastDown !== null && lastDown !== c.down) announce(c.down ? t('{down} of {all} sites down', { down: c.down, all: c.all }) : t('All sites are back to normal'));
    lastDown = c.down;
  }

  render();
  const stop = poll(load, 10000);
  return { unmount() { stopped = true; stop(); } };
}
