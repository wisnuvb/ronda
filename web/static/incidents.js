// Incident history: every period a site was down.
import { h, poll, announce } from './dom.js';
import { icon } from './icons.js';
import { api } from './api.js';
import { fmtDateTime, fmtDur, kindLabel } from './fmt.js';
import { toast } from './ui.js';
import { t, APP_NAME } from './i18n.js';

function endCell(i) {
  if (i.ongoing) return h('span', { class: 'badge st-down' }, icon('circle-x', 14), t('Ongoing'));
  if (i.ended) return fmtDateTime(i.ended);
  return h('span', { class: 'muted', title: t('The site was paused, changed or deleted before its recovery was confirmed') }, t('Ended without confirmation'));
}

/** Shared by this page and the site detail page. */
export function incidentTable(list, { showSite = true } = {}) {
  const now = Date.now();
  return h('div', { class: 'table-wrap table-scroll' },
    h('table', { class: 'table incidents' },
      h('caption', { class: 'sr-only' }, t('Incident history, newest first')),
      h('thead', {}, h('tr', {},
        showSite ? h('th', { scope: 'col' }, t('Site')) : null,
        h('th', { scope: 'col' }, t('Started')), h('th', { scope: 'col' }, t('Ended')),
        h('th', { scope: 'col', class: 'num' }, t('Duration')), h('th', { scope: 'col' }, t('Cause')))),
      h('tbody', {}, list.map((i) => h('tr', {},
        showSite ? h('td', { class: 'c-site' }, i.site_id
          ? h('a', { class: 'site-link', href: `#/sites/${encodeURIComponent(i.site_id)}` }, i.site_name)
          : i.site_name) : null,
        h('td', { 'data-label': t('Started') }, fmtDateTime(i.started)),
        h('td', { 'data-label': t('Ended') }, endCell(i)),
        h('td', { class: 'num', 'data-label': t('Duration') }, i.ongoing ? fmtDur((now - i.started) / 1000) : i.ended ? fmtDur(i.duration_sec) : '–'),
        h('td', { class: 'cause', 'data-label': t('Cause') }, h('strong', {}, kindLabel(i.kind)), i.hint ? h('span', { class: 'hint' }, i.hint) : null))))));
}

export function mount(root) {
  document.title = `${t('Incidents')} · ${APP_NAME}`;
  let stopped = false;
  let head = [];        // the newest page, refreshed every 30 s
  let older = [];       // pages the person asked for with "Load older incidents"
  let more = false;
  let loadingMore = false;
  let loaded = false;

  const summary = h('p', { class: 'muted' });
  const body = h('div', { class: 'list-body', 'aria-busy': 'true' }, h('p', { class: 'muted' }, t('Loading…')));
  const moreBtn = h('button', { type: 'button', class: 'btn' }, t('Load older incidents'));
  const moreBox = h('div', { class: 'more', hidden: true }, moreBtn);
  root.append(h('div', { class: 'page-head' }, h('h1', {}, t('Incidents'))), summary, body, moreBox);

  // New incidents push the old head down into what was already loaded, so drop duplicates.
  const key = (i) => `${i.site_id}:${i.started}`;
  const combined = () => {
    const seen = new Set(head.map(key));
    return [...head, ...older.filter((i) => !seen.has(key(i)))];
  };

  function render(announceMore) {
    body.removeAttribute('aria-busy');
    const list = combined();
    const open = list.filter((i) => i.ongoing).length;
    summary.textContent = list.length
      ? (more ? t('Incidents shown: {n}, ongoing: {open} · older ones are available', { n: list.length, open }) : t('Incidents shown: {n}, ongoing: {open}', { n: list.length, open }))
      : '';
    body.replaceChildren(list.length
      ? h('div', { class: 'card' }, incidentTable(list))
      : h('div', { class: 'empty card' }, icon('circle-check', 28), h('h2', {}, t('No incidents yet')),
        h('p', { class: 'muted' }, t('Every time a site is declared down, it is recorded here.'))));
    moreBox.hidden = !more;
    if (announceMore) announce(t('Incidents shown: {n}', { n: list.length }));
  }

  async function load() {
    try {
      const page = await api.incidents();
      if (stopped) return;
      head = page.incidents;
      if (!older.length) more = page.more; // once older pages are loaded, their own "more" is the truth
      loaded = true;
      render(false);
    } catch (err) {
      if (stopped || loaded) return; // keep what is on screen if a refresh fails
      body.removeAttribute('aria-busy');
      body.replaceChildren(h('div', { class: 'callout callout-warn', role: 'alert' }, icon('alert', 16), h('span', {}, t('Could not load incidents: {error}', { error: err.message }))));
    }
  }

  moreBtn.addEventListener('click', async () => {
    if (loadingMore) return;
    loadingMore = true;
    moreBtn.disabled = true;
    moreBtn.textContent = t('Loading…');
    try {
      const list = combined();
      const page = await api.incidents(null, list[list.length - 1].started);
      if (stopped) return;
      older = [...older, ...page.incidents];
      more = page.more;
      render(true);
    } catch (err) {
      toast(err.message, 'bad');
    } finally {
      loadingMore = false;
      moreBtn.disabled = false;
      moreBtn.textContent = t('Load older incidents');
    }
  });

  const stop = poll(load, 30000);
  return { unmount() { stopped = true; stop(); } };
}
