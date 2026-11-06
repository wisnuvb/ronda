// One site: status, uptime windows, response-time chart, last check breakdown, settings, incidents.
import { h, s, clear, announce, poll } from './dom.js';
import { icon } from './icons.js';
import { api, ApiError } from './api.js';
import { fmtPct, fmtMs, fmtDateTime, fmtDur, fmtUnit, ago, daysUntil, kindLabel } from './fmt.js';
import { t, tNodes, APP_NAME } from './i18n.js';
import { statusBadge, tile, toast, confirmDialog, maintenanceDialog, typeTag } from './ui.js';
import { createChart } from './chart.js';
import { openSiteForm } from './form.js';
import { incidentTable } from './incidents.js';
import { openScheduleForm, describeWindow } from './schedule.js';

const RANGES = [['1h', 1, 'hour'], ['6h', 6, 'hour'], ['24h', 24, 'hour'], ['7d', 7, 'day'], ['30d', 30, 'day'], ['90d', 90, 'day']];
let savedRange = '24h';

function breakdown(last) {
  const segs = [['DNS', last.dns_ms, 'seg-dns'], [t('Connection'), last.connect_ms, 'seg-conn'], ['TLS', last.tls_ms, 'seg-tls'], [t('First byte'), last.ttfb_ms, 'seg-ttfb']].filter(([, v]) => v > 0);
  if (!segs.length) return null;
  const total = segs.reduce((a, [, v]) => a + v, 0);
  let x = 0;
  const rects = segs.map(([label, v, cls]) => {
    const w = Math.max(2, (v / total) * 100);
    const r = s('rect', { x: `${x}%`, y: 0, width: `${w}%`, height: 14, class: cls });
    x += w;
    return r;
  });
  return h('div', { class: 'breakdown' },
    s('svg', { class: 'bar', width: '100%', height: 14, 'aria-hidden': 'true', focusable: 'false' }, rects),
    h('ul', { class: 'bd-legend' }, segs.map(([label, v, cls]) =>
      h('li', {}, s('svg', { width: 10, height: 10, 'aria-hidden': 'true' }, s('rect', { width: 10, height: 10, class: cls })), `${label} `, h('strong', {}, fmtMs(v))))));
}

function fact(label, value) {
  return h('div', { class: 'fact' }, h('dt', {}, label), h('dd', {}, value));
}

export function mount(root, { id }) {
  let stopped = false;
  let site = null;
  let range = savedRange;
  let chart = null;
  let notFound = false;

  const shell = h('div', {});
  root.append(shell);
  const back = h('a', { class: 'back', href: '#/' }, icon('arrow-left', 16), t('All sites'));
  const head = h('div', { class: 'page-head' });
  const checkOut = h('div', { class: 'check-out', role: 'status', 'aria-live': 'polite' });
  const callout = h('div', {});
  const tiles = h('section', { class: 'tiles tiles-detail', 'aria-label': t('Statistics') });
  const rangeBar = h('div', { class: 'range', role: 'group', 'aria-label': t('Chart time range') });
  const rangeBtns = {};
  for (const [k, n, unit] of RANGES) {
    const b = h('button', { type: 'button', class: 'chip', 'aria-pressed': String(k === range), onclick: () => { range = k; savedRange = k; for (const [kk, bb] of Object.entries(rangeBtns)) bb.setAttribute('aria-pressed', String(kk === k)); loadHistory(); } }, fmtUnit(n, unit));
    rangeBtns[k] = b;
    rangeBar.append(b);
  }
  const chartBox = h('div', { class: 'chart' });
  const lastBox = h('div', {});
  const settingsBox = h('div', {});
  const incBox = h('div', {});
  const schedBox = h('div', {});
  const pushBox = h('div', {});
  const pushCard = h('section', { class: 'card pad', hidden: true }, h('h2', {}, t('How to use: ping from your job')), pushBox);
  const chartCard = h('section', { class: 'card pad' }, h('div', { class: 'card-head' }, h('h2', {}, t('Response time')), rangeBar), chartBox);

  shell.append(back, head, checkOut, callout, tiles, pushCard, chartCard,
    h('div', { class: 'two-col' },
      h('section', { class: 'card pad' }, h('h2', {}, t('Latest check')), lastBox),
      h('section', { class: 'card pad' }, h('h2', {}, t('Settings')), settingsBox)),
    h('section', { class: 'card pad' }, h('h2', {}, t('Scheduled maintenance')), schedBox),
    h('section', { class: 'card pad' }, h('h2', {}, t('Incidents for this site')), incBox));
  chart = createChart(chartBox);

  async function act(btn, busyLabel, fn) {
    const idle = btn.textContent;
    btn.disabled = true; btn.setAttribute('aria-busy', 'true');
    try { await fn(); } catch (err) { toast(err.message, 'bad'); } finally {
      if (!stopped) { btn.disabled = false; btn.removeAttribute('aria-busy'); }
    }
    return idle;
  }

  function renderHead() {
    const paused = site.state === 'paused';
    const checkBtn = h('button', { type: 'button', class: 'btn' }, icon('refresh', 16), t('Check now'));
    checkBtn.addEventListener('click', () => act(checkBtn, t('Checking…'), async () => {
      checkOut.replaceChildren(h('p', { class: 'muted' }, t('Checking…')));
      const r = await api.checkNow(site.id);
      checkOut.replaceChildren(h('div', { class: `callout ${r.ok ? 'callout-ok' : 'callout-bad'}` },
        icon(r.ok ? 'circle-check' : 'circle-x', 18),
        h('div', {},
          h('strong', {}, r.ok ? t('Success: HTTP {status}, {time}', { status: r.status, time: fmtMs(r.latency_ms) }) : t('Failed: {reason}', { reason: kindLabel(r.kind) })),
          r.hint ? h('p', {}, r.hint) : null,
          h('p', { class: 'muted small' }, t('This manual check does not change the status or trigger notifications.'))),
        h('button', { type: 'button', class: 'btn btn-ghost btn-icon', 'aria-label': t('Dismiss check result'), onclick: () => checkOut.replaceChildren() }, icon('x', 16))));
    }));
    const inMaint = site.state === 'maintenance';
    const maintBtn = h('button', { type: 'button', class: 'btn' }, icon('wrench', 16), inMaint ? t('End maintenance') : t('Maintenance'));
    maintBtn.addEventListener('click', () => act(maintBtn, '', async () => {
      if (inMaint) {
        const scheduled = (site.maintenance_weekly || []).length > 0 && !(site.maintenance_until > Date.now());
        await api.endMaintenance(site.id);
        toast(scheduled
          ? t('Maintenance for {name} ended; the site is being checked now. The next window still runs on schedule.', { name: site.name })
          : t('Maintenance for {name} ended; the site is being checked now.', { name: site.name }));
      } else {
        const minutes = await maintenanceDialog(site);
        if (!minutes) return;
        await api.maintenance(site.id, minutes);
        toast(t('{name} is now in maintenance. No checks or notifications until the time is up.', { name: site.name }));
      }
      await load();
    }));
    const pauseBtn = h('button', { type: 'button', class: 'btn' }, icon(paused ? 'play' : 'pause', 16), paused ? t('Resume') : t('Pause'));
    pauseBtn.addEventListener('click', () => act(pauseBtn, '', async () => {
      await (paused ? api.resume(site.id) : api.pause(site.id));
      toast(paused ? t('{name} resumed.', { name: site.name }) : t('{name} paused. No checks or notifications until you resume.', { name: site.name }));
      await load();
    }));
    const editBtn = h('button', { type: 'button', class: 'btn', onclick: () => openSiteForm({ site, onSaved: () => load() }) }, icon('pencil', 16), t('Edit'));
    const delBtn = h('button', { type: 'button', class: 'btn btn-danger-ghost' }, icon('trash', 16), t('Delete'));
    delBtn.addEventListener('click', async () => {
      const ok = await confirmDialog({ title: t('Delete {name}?', { name: site.name }), message: t('The site stops being monitored and its whole uptime history and charts are deleted. This cannot be undone.'), confirmText: t('Delete site'), danger: true });
      if (!ok) return;
      try {
        await api.remove(site.id);
        toast(t('{name} deleted.', { name: site.name }));
        location.hash = '#/';
      } catch (err) { toast(err.message, 'bad'); }
    });

    head.replaceChildren(
      h('div', { class: 'head-main' },
        h('h1', {}, site.name),
        h('p', { class: 'sub' }, statusBadge(site.state),
          site.type === 'push'
            ? h('span', { class: 'ext' }, typeTag(site), t('waiting for a ping from its job'))
            : site.type === 'tcp' || site.type === 'dns'
            ? h('span', { class: 'ext' }, typeTag(site), site.url) // not something a browser can open
            : h('a', { class: 'ext', href: site.url, target: '_blank', rel: 'noopener noreferrer' }, site.url, icon('external', 14), h('span', { class: 'sr-only' }, ` ${t('(opens in a new tab)')}`)))),
      h('div', { class: 'actions' }, ...[site.type === 'push' ? null : checkBtn, maintBtn, pauseBtn, editBtn, delBtn].filter(Boolean)));
  }

  function renderCallout() {
    const last = site.last;
    if (site.state === 'down' && last) {
      callout.replaceChildren(h('div', { class: 'callout callout-bad' }, icon('circle-x', 20),
        h('div', {},
          h('strong', {}, t('Down since {since} ({duration})', { since: fmtDateTime(site.since), duration: fmtDur((Date.now() - site.since) / 1000) })),
          h('p', {}, h('b', {}, kindLabel(last.kind)), last.hint ? ` — ${last.hint}` : ''))));
    } else if (site.state === 'maintenance') {
      callout.replaceChildren(h('div', { class: 'callout callout-warn' }, icon('wrench', 20),
        h('div', {},
          h('strong', {}, t('In maintenance until {until} ({remaining} left).', { until: fmtDateTime(site.maintenance_ends || site.maintenance_until), remaining: fmtDur(((site.maintenance_ends || site.maintenance_until) - Date.now()) / 1000) })),
          h('p', {}, t('The site is not checked and sends no notifications. When the time is up it is checked again by itself; if it is still down, you will be told.')))));
    } else if (site.state === 'paused') {
      callout.replaceChildren(h('div', { class: 'callout' }, icon('circle-pause', 20), h('div', {}, h('strong', {}, t('Paused.')), h('p', {}, t('The site is not checked and sends no notifications. Click Resume to start monitoring again.')))));
    } else if (!site.last) {
      callout.replaceChildren(h('div', { class: 'callout' }, icon('circle-help', 20), h('div', {}, ...site.type === 'push'
        ? [h('strong', {}, t('Waiting for the first ping.')), h('p', {}, t('{app} does not check this site; it is only judged once its job calls the ping address below.'))]
        : [h('strong', {}, t('Waiting for the first check.')), h('p', {}, t('The result appears in a few seconds.'))])));
    } else callout.replaceChildren();
  }

  function renderTiles() {
    clear(tiles);
    const days = site.last && site.last.cert_not_after ? daysUntil(site.last.cert_not_after) : null;
    tiles.append(...[
      tile(t('Uptime (24 h)'), fmtPct(site.uptime_24h)), tile(t('Uptime (7 d)'), fmtPct(site.uptime_7d)),
      tile(t('Uptime (30 d)'), fmtPct(site.uptime_30d)), tile(t('Uptime (90 d)'), fmtPct(site.uptime_90d)),
      ...(site.type === 'push' ? [] : [tile(t('Average response time'), fmtMs(site.avg_ms || null), { sub: t('24 h') }), tile(t('p95 response time'), fmtMs(site.p95_ms || null), { sub: t('24 h') })]),
      days === null ? null : tile(t('TLS certificate'), days < 0 ? t('Expired') : days === 1 ? t('1 day') : t('{n} days', { n: days }), {
        sub: days < 0 ? t('renew immediately') : days <= 14 ? t('renew soon') : t('until expiry'),
        cls: days <= 14 ? 'tile-warn' : '', ic: days <= 14 ? 'alert' : null }),
    ].filter(Boolean)); // Element.append(null) would write the word "null" into the page
  }

  function renderLast() {
    const last = site.last;
    if (site.type === 'push') { renderLastPush(); return; }
    if (!last) { lastBox.replaceChildren(h('p', { class: 'muted' }, t('No checks yet.'))); return; }
    lastBox.replaceChildren(...[
      h('dl', { class: 'facts' },
        fact(t('Result'), h('span', {}, last.ok ? t('Healthy') : kindLabel(last.kind))),
        site.type === 'tcp' || site.type === 'dns' ? null : fact(t('HTTP status'), last.status ? String(last.status) : '–'),
        last.answer ? fact(t('Answer'), last.answer) : null,
        fact(t('Total time'), last.ok || last.status ? fmtMs(last.latency_ms) : '–'), // no response at all: nothing to time
        fact(t('Time'), h('time', { datetime: new Date(last.time).toISOString(), title: fmtDateTime(last.time) }, ago(last.time)))),
      last.ok ? null : h('p', { class: 'hint-text' }, last.hint || last.error),
      site.type === 'tcp' || site.type === 'dns' ? null : breakdown(last),
    ].filter(Boolean)); // native replaceChildren(null) would print the word "null"
  }

  function renderSettings() {
    const yn = (v) => (v ? t('Yes') : t('No'));
    const push = site.type === 'push';
    const common = push ? [fact(t('Down after'), t('{n}× missed pings in a row', { n: site.fail_threshold }))] : [
      fact(t('Check every'), fmtDur(site.interval_sec)), fact(t('Timeout'), fmtDur(site.timeout_sec)),
      fact(t('Down after'), t('{n}× failures in a row', { n: site.fail_threshold })),
    ];
    let own;
    if (push) {
      own = [
        fact(t('Type'), t('Scheduled job (push)')),
        fact(t('Job reports every'), fmtDur(site.interval_sec)),
        fact(t('Lateness tolerance'), fmtDur(site.grace_sec)),
      ];
    } else if (site.type === 'tcp') {
      own = [fact(t('Type'), t('TCP port: connection accepted or not'))];
    } else if (site.type === 'dns') {
      own = [
        fact(t('Record type'), site.dns_type || 'A'),
        fact(t('DNS server'), site.dns_server || t('System resolver')),
        site.keyword ? fact(t('Expected answer'), site.keyword) : null,
      ];
    } else {
      own = [
        fact(t('Method'), site.method),
        fact(t('Healthy status'), site.expect_status && site.expect_status.length ? site.expect_status.join(', ') : '200–399'),
        site.keyword ? fact(t('Text searched for'), site.keyword) : null,
        fact(t('Follow redirects'), yn(!site.no_redirect)),
        site.user_agent ? fact('User-Agent', site.user_agent) : null,
      ];
    }
    settingsBox.replaceChildren(h('dl', { class: 'facts' },
      ...own, ...common,
      site.allow_private ? fact(t('Private addresses'), t('Allowed')) : null,
      site.notify_to && site.notify_to.length ? fact(t('Notifications go to'), site.notify_to.map((n) => (n === 'default' ? t('Main chat') : n)).join(', ')) : null,
      fact(t('Public status page'), site.public ? t('Shown') : t('Not shown')),
      fact(t('Added'), fmtDateTime(site.created))));
  }

  function renderSchedule() {
    const ws = site.maintenance_weekly || [];
    const edit = h('button', { type: 'button', class: 'btn', onclick: () => openScheduleForm({ site, onSaved: () => load() }) },
      icon('wrench', 16), ws.length ? t('Edit schedule') : t('Set schedule'));
    schedBox.replaceChildren(...[
      ws.length
        ? h('ul', { class: 'plain' }, ws.map((w) => h('li', {}, describeWindow(w))))
        : h('p', { class: 'muted' }, t('No schedule yet. Set a recurring window for backups or routine updates, so there are no false alarms.')),
      ws.length && site.next_maintenance
        ? h('p', { class: 'muted' }, t('Next: {time}', { time: fmtDateTime(site.next_maintenance) })) : null,
      edit,
    ].filter(Boolean));
  }

  function renderLastPush() {
    const last = site.last;
    lastBox.replaceChildren(h('dl', { class: 'facts' },
      fact(t('Result'), last ? (last.ok ? t('Ping received') : kindLabel(last.kind)) : t('None yet')),
      fact(t('Last ping'), site.last_ping ? h('time', { datetime: new Date(site.last_ping).toISOString(), title: fmtDateTime(site.last_ping) }, ago(site.last_ping)) : t('Never')),
      site.ping_deadline ? fact(t('Next ping due by'), fmtDateTime(site.ping_deadline)) : null),
      ...(last && !last.ok && last.hint ? [h('p', { class: 'hint-text' }, last.hint)] : []));
  }

  const RESULT_TEXT = { ok: t('Ping received'), missed: t('Ping not received'), reported_failure: t('Job reported failure') };

  /** The ping address with its secret, how to call it, and the last few results. Shown for push sites only. */
  function renderPush() {
    pushCard.hidden = site.type !== 'push';
    chartCard.hidden = site.type === 'push'; // a response-time chart means nothing when nothing is fetched
    if (site.type !== 'push') return;

    const url = `${location.origin}/ping/${site.push_token}`;
    const copy = h('button', { type: 'button', class: 'btn' }, icon('check', 16), t('Copy address'));
    copy.addEventListener('click', async () => {
      try { await navigator.clipboard.writeText(url); toast(t('Ping address copied.')); } catch { toast(t('Could not copy automatically; select and copy the address.'), 'bad'); }
    });
    const rotate = h('button', { type: 'button', class: 'btn btn-danger-ghost' }, icon('refresh', 16), t('Rotate token'));
    rotate.addEventListener('click', async () => {
      const ok = await confirmDialog({ title: t('Rotate the token?'), message: t('The old ping address stops working immediately. Jobs still using it will count as not pinging until you install the new address.'), confirmText: t('Rotate'), danger: true });
      if (!ok) return;
      try { await api.rotateToken(site.id); toast(t('Token rotated. Install the new ping address in your job.')); await load(); } catch (err) { toast(err.message, 'bad'); }
    });

    const recent = [...(site.recent || [])].reverse().slice(0, 15);
    pushBox.replaceChildren(
      h('p', {}, t('Call this address from your job every time it finishes successfully. If no ping arrives within {duration} (schedule + tolerance), the site is declared down.', { duration: fmtDur(site.interval_sec + site.grace_sec) })),
      h('div', { class: 'code-row' }, h('code', { class: 'code ping-url' }, url), copy),
      h('p', { class: 'muted small' }, t('Crontab example (every day at 02:00; pings only if the backup succeeds):')),
      h('pre', { class: 'code' }, `0 2 * * * /usr/local/bin/backup.sh && curl -fsS -m 10 --retry 3 ${url} >/dev/null`),
      h('p', { class: 'muted small' }, tNodes('To report a failure explicitly (without waiting for the schedule), call {url}.', { url: h('code', {}, `${url}/fail`) })),
      h('div', { class: 'callout callout-warn' }, icon('alert', 18), h('div', {},
        h('strong', {}, t('Treat this address like a password.')),
        h('p', {}, t('Anyone who knows the address can send pings. The address above uses the address you are browsing; replace it with the public address of {app} that your job can reach (the reverse proxy must forward /ping/).')))),
      rotate,
      recent.length ? h('div', { class: 'table-wrap table-scroll', style: null },
        h('table', { class: 'table compact' },
          h('caption', { class: 'sr-only' }, t('Latest results, newest first')),
          h('thead', {}, h('tr', {}, h('th', { scope: 'col' }, t('Time')), h('th', { scope: 'col' }, t('Result')))),
          h('tbody', {}, recent.map((r) => h('tr', {}, h('td', {}, fmtDateTime(r.time)), h('td', {}, r.ok ? RESULT_TEXT.ok : (RESULT_TEXT[r.kind] || kindLabel(r.kind)))))))) : null);
  }

  async function loadHistory() {
    try {
      const data = await api.history(id, range);
      if (stopped) return;
      chart.update(data, range);
    } catch (err) {
      if (stopped || err instanceof ApiError && err.status === 401) return;
      chartBox.prepend(h('div', { class: 'callout callout-warn', role: 'alert' }, icon('alert', 16), h('span', {}, t('Could not load the chart: {error}', { error: err.message }))));
    }
  }

  async function loadIncidents() {
    try {
      const { incidents: list, more } = await api.incidents(id);
      if (stopped) return;
      incBox.replaceChildren(...[
        list.length ? incidentTable(list, { showSite: false }) : h('p', { class: 'muted' }, t('No incidents for this site yet.')),
        more ? h('p', { class: 'muted small' }, tNodes('Showing the latest {n} incidents. {link}', { n: list.length, link: h('a', { href: '#/incidents' }, t('See all of them on the Incidents page')) })) : null,
      ].filter(Boolean));
    } catch { /* the rest of the page is still useful */ }
  }

  async function load() {
    try {
      const fresh = await api.site(id);
      if (stopped) return;
      const changed = site && site.state !== fresh.state;
      site = fresh;
      document.title = `${site.name} · ${APP_NAME}`;
      renderHead(); renderCallout(); renderTiles(); renderLast(); renderSettings(); renderSchedule(); renderPush();
      if (changed) announce({ up: t('{name} is now up'), down: t('{name} is now down'), paused: t('{name} is now paused'), maintenance: t('{name} is now in maintenance') }[site.state]?.replace('{name}', site.name) || t('{name} is now waiting', { name: site.name }));
    } catch (err) {
      if (stopped) return;
      if (err instanceof ApiError && err.status === 404) {
        notFound = true;
        shell.replaceChildren(back, h('div', { class: 'empty card' }, icon('info', 28), h('h1', {}, t('Site not found')),
          h('p', { class: 'muted' }, t('This site may have been deleted.')), h('a', { class: 'btn btn-primary', href: '#/' }, t('Back to the list'))));
        return;
      }
      toast(err.message, 'bad');
    }
  }

  const stop = poll(async () => {
    if (notFound) return;
    await load();
    if (site) await Promise.all([site.type === 'push' ? null : loadHistory(), loadIncidents()]);
  }, 15000);
  return { unmount() { stopped = true; stop(); chart.destroy(); } };
}
