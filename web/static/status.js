// The public status page. It reads one anonymous JSON document and shows only what that document
// holds: names, states, uptime and general kinds of trouble. No login, no session, no other endpoint.
import { h, clear, poll, announce } from './dom.js';
import { icon } from './icons.js';
import { fmtPct, fmtDay, fmtDateTime, fmtDur, fmtClockZone } from './fmt.js';
import { t } from './i18n.js';

const main = document.getElementById('main');

const OVERALL = {
  none: ['info', t('No services are shown yet'), ''],
  operational: ['circle-check', t('All services are running normally'), 'callout-ok'],
  maintenance: ['wrench', t('Services are under maintenance'), 'callout-warn'],
  partial: ['alert', t('Some services are having problems'), 'callout-bad'],
  outage: ['circle-x', t('Disruption across all services'), 'callout-bad'],
};
const STATES = {
  up: ['circle-check', t('Operational'), 'st-up'],
  down: ['circle-x', t('Disruption'), 'st-down'],
  maintenance: ['wrench', t('Maintenance'), 'st-maintenance'],
  unknown: ['circle-help', t('Waiting'), 'st-unknown'],
};

function badge(state) {
  const [ic, label, cls] = STATES[state] || STATES.unknown;
  return h('span', { class: `badge ${cls}` }, icon(ic, 14), label);
}

/** One bar per day. Height says how the day went as well as colour: tall is good, short is bad. */
function bars(days) {
  let good = 0, bad = 0, none = 0;
  const els = days.map(([start, up, total]) => {
    let cls = 'none', text = t('no data');
    if (total > 0) {
      const ratio = up / total;
      text = fmtPct(ratio === 1 ? 100 : Math.min(99.99, Math.floor(ratio * 10000) / 100));
      // One failed check in ~100 on a day is not an outage; thresholds keep the chart honest, not noisy.
      if (ratio >= 0.999) { cls = 'ok'; good++; } else { cls = ratio >= 0.99 ? 'part' : 'bad'; bad++; }
    } else none++;
    return h('i', { class: `pb ${cls}`, title: `${fmtDay(start)}: ${text}` });
  });
  const label = t('Last {n} days: {good} normal, {bad} with problems, {none} without data', { n: days.length, good, bad, none });
  return h('div', { class: 'pbars', role: 'img', 'aria-label': label }, els);
}

function siteCard(s) {
  return h('li', { class: 'pubsite' },
    h('div', { class: 'pubsite-head' },
      h('h3', {}, s.name),
      h('span', { class: 'pubsite-state' }, badge(s.state))),
    bars(s.days),
    h('div', { class: 'pubsite-foot muted' },
      h('span', {}, t('{n} days ago', { n: s.days.length })),
      h('span', { class: 'pubsite-uptime' }, s.uptime_90d == null ? t('No data yet') : t('{pct} available', { pct: fmtPct(s.uptime_90d) })),
      h('span', {}, t('Today'))));
}

function incident(i) {
  const when = fmtDateTime(i.started);
  const line = i.ongoing ? t('{when}, still ongoing', { when }) : t('{when}, for {duration}', { when, duration: fmtDur(i.duration_sec) });
  return h('li', { class: 'pubinc' },
    h('div', {}, h('strong', {}, i.site), ' · ', i.kind),
    h('div', { class: 'muted small' }, line),
    i.ongoing ? h('span', { class: 'badge st-down' }, icon('circle-x', 14), t('Ongoing')) : null);
}

let lastOverall = null;

function render(d) {
  document.title = t('{title} · status', { title: d.title });
  const [ic, text, cls] = OVERALL[d.overall] || OVERALL.none;
  clear(main);
  main.append(
    h('header', { class: 'status-head' },
      h('h1', {}, d.title),
      d.description ? h('p', { class: 'muted' }, d.description) : null),
    h('div', { class: `callout ${cls}`, role: 'status' }, icon(ic, 22), h('strong', {}, text)),
    d.sites.length
      ? h('section', { 'aria-labelledby': 'h-services' }, h('h2', { id: 'h-services', class: 'sr-only' }, t('Services')),
        h('ul', { class: 'plain pubsites' }, d.sites.map(siteCard)))
      : null,
    h('section', { 'aria-labelledby': 'h-incidents' },
      h('h2', { id: 'h-incidents' }, t('Incidents in the last 14 days')),
      d.incidents.length
        ? h('ul', { class: 'plain pubincs' }, d.incidents.map(incident))
        : h('p', { class: 'muted' }, t('No incidents in the last 14 days.'))),
    h('p', { class: 'foot muted' }, t('Updated {time} · refreshes every minute', { time: fmtClockZone(d.updated, true) })));
  if (lastOverall !== null && lastOverall !== d.overall) announce(text);
  lastOverall = d.overall;
}

function message(title, text) {
  clear(main);
  main.append(h('div', { class: 'empty card' }, icon('info', 28), h('h1', {}, title), h('p', { class: 'muted' }, text)));
}

let shown = false;
poll(async () => {
  try {
    const res = await fetch('/api/public/status', { cache: 'no-store' });
    if (res.status === 404) { message(t('Status page is not active'), t('The administrator has not enabled this page.')); return; }
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    render(await res.json());
    shown = true;
  } catch {
    if (!shown) message(t('Status could not be loaded'), t('Trying again shortly. Reload the page if this keeps happening.'));
    // after a first success a failed refresh keeps showing the last known state
  }
}, 60000);
