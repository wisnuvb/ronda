// Add / edit dialog. Native <dialog> gives a focus trap and Escape-to-close; everything else
// (labels, helper text, inline errors, error summary, test-before-save) is built here.
import { h, uid } from './dom.js';
import { icon } from './icons.js';
import { api, auth, ApiError } from './api.js';
import { fmtMs, fmtLong, kindLabel } from './fmt.js';
import { toast } from './ui.js';
import { t, tNodes } from './i18n.js';

// Periods are seconds; the labels are written out in the page's language by fmtLong.
const INTERVALS = [10, 30, 60, 120, 300, 600, 1800, 3600];
const TIMEOUTS = [5, 10, 15, 30, 60];

// Server messages start with the field name, so they can be shown next to the right field.
const FIELDS = ['url', 'name', 'method', 'interval_sec', 'timeout_sec', 'fail_threshold', 'expect_status', 'keyword', 'user_agent', 'dns_type', 'dns_server', 'grace_sec'];
const LABELS = {
  url: t('Address (URL)'), name: t('Name'), method: t('Method'), interval_sec: t('Interval'), timeout_sec: t('Timeout'),
  fail_threshold: t('Down threshold'), expect_status: t('Status codes'), keyword: t('Required text'), user_agent: 'User-Agent',
  dns_type: t('Record type'), dns_server: t('DNS server'), grace_sec: t('Tolerance'),
};

// How often a scheduled job is expected to report, and how late it may be before that counts as a miss.
const PUSH_INTERVALS = [300, 900, 3600, 21600, 43200, 86400, 172800, 604800, 2592000];
const GRACES = [60, 300, 900, 1800, 3600, 14400];

// What the address field means for each kind of check.
const TYPES = {
  http: {
    label: t('Website'), url: t('Address (URL)'), ph: t('https://example.com'),
    help: t('The full address visitors open, starting with http:// or https://.'),
  },
  tcp: {
    label: t('TCP port'), url: t('Host and port'), ph: t('db.example.com:5432'),
    help: t('E.g. db.example.com:5432 or 203.0.113.5:25. Only checks whether the port accepts connections (database, SMTP, SSH, ...).'),
  },
  dns: {
    label: t('DNS record'), url: t('Domain name'), ph: t('example.com'),
    help: t('The name to resolve, e.g. example.com or mail.example.com (not an IP address or a URL).'),
  },
  push: { label: t('Scheduled job'), url: '', ph: '', help: '' },
};
const DNS_TYPES = ['A', 'AAAA', 'MX', 'TXT', 'NS'];

function options(list, current) {
  const opts = [...list];
  if (current && !opts.includes(current)) opts.push(current);
  return opts.sort((a, b) => a - b).map((v) => h('option', { value: v, selected: v === current }, fmtLong(v)));
}

export async function openSiteForm({ site, onSaved }) {
  const recipients = await api.recipients().catch(() => []); // the names a site may notify besides the main chats
  const editing = !!site;
  const cur = site || { type: 'http', interval_sec: 60, timeout_sec: 10, fail_threshold: 3, method: 'GET' };
  let type = cur.type || 'http';
  const titleId = uid('form-title');
  const errors = {}; // field -> message
  const ids = Object.fromEntries(FIELDS.map((f) => [f, uid(`f-${f}`)]));
  let busy = false;

  // ---- fields ----
  const input = (name, attrs = {}) => h('input', { id: ids[name], name, class: 'input', autocomplete: 'off', ...attrs });
  const url = input('url', { type: 'text', inputmode: 'url', spellcheck: 'false', value: (cur.url || '').replace(/^(tcp|dns):\/\//i, ''), 'aria-required': 'true', required: true });
  const name = input('name', { type: 'text', maxlength: '100', value: cur.name || '' });
  const interval = h('select', { id: ids.interval_sec, name: 'interval_sec', class: 'input' }, options((cur.type === 'push' ? PUSH_INTERVALS : INTERVALS), cur.interval_sec));
  const grace = h('select', { id: ids.grace_sec, name: 'grace_sec', class: 'input' }, options(GRACES, cur.grace_sec || 300));
  const timeout = h('select', { id: ids.timeout_sec, name: 'timeout_sec', class: 'input' }, options(TIMEOUTS, cur.timeout_sec));
  const threshold = input('fail_threshold', { type: 'number', min: '1', max: '10', step: '1', inputmode: 'numeric', value: cur.fail_threshold });
  const keyword = input('keyword', { type: 'text', maxlength: '200', value: cur.keyword || '' });
  const method = h('select', { id: ids.method, name: 'method', class: 'input' },
    ['GET', 'HEAD'].map((m) => h('option', { value: m, selected: (cur.method || 'GET') === m }, m)));
  const expect = input('expect_status', { type: 'text', inputmode: 'numeric', placeholder: t('e.g. 200, 301'), value: (cur.expect_status || []).join(', ') });
  const ua = input('user_agent', { type: 'text', maxlength: '200', value: cur.user_agent || '' });
  const noRedirect = h('input', { type: 'checkbox', id: uid('f-nr'), checked: !!cur.no_redirect });
  const allowPrivate = h('input', { type: 'checkbox', id: uid('f-ap'), checked: !!cur.allow_private });
  const isPublic = h('input', { type: 'checkbox', id: uid('f-pub'), checked: !!cur.public });

  // Who hears about this site. Shown only when the config defines recipients; otherwise everything goes to the main chats.
  const routeIds = new Map(); // name -> checkbox
  const known = ['default', ...recipients];
  const chosen = new Set(cur.notify_to && cur.notify_to.length ? cur.notify_to : ['default']);
  for (const stale of [...chosen].filter((n) => !known.includes(n))) known.push(stale); // keep a route the config no longer lists, visibly
  const routeErr = h('p', { class: 'field-error', id: uid('f-route-err'), hidden: true });
  const routeBox = recipients.length ? h('fieldset', { class: 'field types' },
    h('legend', { class: 'label' }, t('Send notifications to')),
    h('div', { class: 'routes' }, known.map((n) => {
      const id = uid('f-route');
      const cb = h('input', { type: 'checkbox', id, checked: chosen.has(n), 'aria-describedby': routeErr.id });
      cb.addEventListener('change', () => { routeErr.hidden = true; routeErr.textContent = ''; });
      routeIds.set(n, cb);
      const label = n === 'default' ? t('Main chat') : (recipients.includes(n) ? n : t('{name} (no longer in the config)', { name: n }));
      return h('div', { class: 'check' }, cb, h('label', { for: id }, label));
    })),
    h('p', { class: 'help' }, t('Who is told when this site goes down, recovers, or its certificate is about to expire. Untick Main chat if only the other recipients should know.')),
    routeErr) : null;

  const dnsType = h('select', { id: ids.dns_type, name: 'dns_type', class: 'input' },
    DNS_TYPES.map((rt) => h('option', { value: rt, selected: (cur.dns_type || 'A') === rt }, rt)));
  const dnsServer = input('dns_server', { type: 'text', spellcheck: 'false', placeholder: t('e.g. 1.1.1.1 or 10.0.0.2:53'), value: cur.dns_server || '' });

  const els = {
    url, name, interval_sec: interval, timeout_sec: timeout, fail_threshold: threshold, keyword, method,
    expect_status: expect, user_agent: ua, dns_type: dnsType, dns_server: dnsServer, grace_sec: grace,
  };

  function field(f, label, control, helper, { required = false } = {}) {
    const helpId = `${ids[f]}-help`;
    const errId = `${ids[f]}-err`;
    control.setAttribute('aria-describedby', helper ? `${helpId} ${errId}` : errId);
    return h('div', { class: 'field', dataset: { field: f } },
      h('label', { for: ids[f], class: 'label' }, label, required ? h('span', { class: 'req', 'aria-hidden': 'true' }, ' *') : null),
      control,
      helper ? h('p', { id: helpId, class: 'help' }, helper) : null,
      h('p', { id: errId, class: 'field-error', hidden: true }));
  }

  const summary = h('div', { class: 'error-summary', role: 'alert', tabindex: '-1', hidden: true });
  const testBox = h('div', { class: 'test-result', role: 'status', 'aria-live': 'polite' });
  const submitBtn = h('button', { type: 'submit', class: 'btn btn-primary' }, editing ? t('Save changes') : t('Add site'));
  const testBtn = h('button', { type: 'button', class: 'btn' }, t('Test first'));
  const cancelBtn = h('button', { type: 'button', class: 'btn btn-ghost', onclick: () => dlg.close() }, t('Cancel'));

  // The kind of check. A site's kind cannot change later: a different target starts a different history.
  const radios = Object.entries(TYPES).map(([kind, info]) => {
    const id = uid('f-type');
    const input = h('input', { type: 'radio', name: 'type', id, value: kind, checked: type === kind, disabled: editing && type !== kind });
    input.addEventListener('change', () => { type = kind; applyType(); });
    return h('label', { class: `radio${editing && type !== kind ? ' off' : ''}`, for: id }, input, info.label);
  });
  const typeBox = h('fieldset', { class: 'field types' },
    h('legend', { class: 'label' }, t('Check type')),
    h('div', { class: 'radios' }, radios),
    editing ? h('p', { class: 'help' }, t('The type cannot be changed. Create a new site if you need another type.')) : null);

  const urlF = field('url', TYPES[type].url, url, TYPES[type].help, { required: true });
  const dnsTypeF = field('dns_type', t('Record type'), dnsType, null);
  const dnsServerF = field('dns_server', t('DNS server'), dnsServer, t('Optional. Leave empty to use the system resolver. Private/internal addresses are blocked unless allowed under Advanced settings.'));
  const intervalF = field('interval_sec', t('Check every'), interval, null);
  const timeoutF = field('timeout_sec', t('Response timeout'), timeout, null);
  const graceF = field('grace_sec', t('Lateness tolerance'), grace, null);
  const thresholdF = field('fail_threshold', t('Declared down after (consecutive failures)'), threshold,
    t('Lower means you hear sooner, but brief glitches also trigger notifications. 3 is a reasonable starting point.'));
  const keywordF = field('keyword', t('Text that must be on the page'), keyword, t('Optional. The page counts as broken if this text disappears even though the server answers 200. Case is ignored.'));
  const webOnly = h('div', { class: 'only-http' },
    field('method', t('Method'), method, t('HEAD is lighter but cannot be used together with the text search.')),
    field('expect_status', t('Status codes considered healthy'), expect, t('Separate with commas. Leave empty to accept 200–399.')),
    field('user_agent', 'User-Agent', ua, t('Optional. Useful when a WAF or Cloudflare blocks the monitor.')),
    h('div', { class: 'check' }, noRedirect, h('label', { for: noRedirect.id }, t('Do not follow redirects'))));

  const form = h('form', { class: 'dlg-body', novalidate: true },
    h('div', { class: 'dlg-head' },
      h('h2', { id: titleId, class: 'dlg-title' }, editing ? t('Edit site') : t('Add site')),
      h('button', { type: 'button', class: 'btn btn-ghost btn-icon', 'aria-label': t('Close'), onclick: () => dlg.close() }, icon('x', 18))),
    summary,
    h('p', { class: 'help' }, tNodes('{mark} required', { mark: h('span', { class: 'req', 'aria-hidden': 'true' }, '*') })),
    typeBox,
    urlF,
    dnsTypeF,
    dnsServerF,
    field('name', t('Name'), name, t('Optional. Leave empty to use the host name.')),
    h('div', { class: 'grid-2' }, intervalF, timeoutF, graceF),
    thresholdF,
    keywordF,
    routeBox,
    h('div', { class: 'check' }, isPublic, h('label', { for: isPublic.id }, t('Show on the public status page')),
      h('p', { class: 'help' }, t('The name, status and uptime of this site are visible to anyone who opens the status page. The URL and technical details are never shown.'),
        auth.statusPage ? null : ` ${t('The status page is not enabled in the config yet (status_page.enabled).')}`)),
    h('details', { class: 'advanced', open: !!(cur.expect_status && cur.expect_status.length) || !!cur.user_agent || cur.method === 'HEAD' || !!cur.no_redirect || !!cur.allow_private },
      h('summary', {}, t('Advanced settings')),
      h('div', { class: 'advanced-body' },
        webOnly,
        h('div', { class: 'check' }, allowPrivate, h('label', { for: allowPrivate.id }, t('Allow private/internal addresses (loopback, local network)')),
          h('p', { class: 'help' }, t('Off by default so the monitor cannot be pointed at internal services.'))))),
    testBox,
    h('div', { class: 'dlg-actions' }, cancelBtn, testBtn, submitBtn));

  /** Show only what applies to the chosen kind, and say what the address field means. */
  function applyType(initial = false) {
    const info = TYPES[type];
    const push = type === 'push';
    if (!push) {
      urlF.querySelector('.label').firstChild.textContent = info.url;
      urlF.querySelector('.help').textContent = info.help;
      url.placeholder = info.ph;
    }
    urlF.hidden = push; // a push site's address is minted by the server, with a secret in it
    dnsTypeF.hidden = dnsServerF.hidden = type !== 'dns';
    webOnly.hidden = type !== 'http';
    keywordF.hidden = type === 'tcp' || push;
    timeoutF.hidden = push;
    graceF.hidden = !push;
    testBtn.hidden = push; // there is nothing to fetch
    intervalF.querySelector('.label').firstChild.textContent = push ? t('Job reports every') : t('Check every');
    thresholdF.querySelector('.label').firstChild.textContent = push
      ? t('Declared down after (consecutive missed pings)') : t('Declared down after (consecutive failures)');
    thresholdF.querySelector('.help').textContent = push
      ? t('1 means you are told as soon as one scheduled ping is missed. Raise it if the job is sometimes late.')
      : t('Lower means you hear sooner, but brief glitches also trigger notifications. 3 is a reasonable starting point.');
    if (!initial) { // the person just switched kinds: offer that kind's usual period and threshold
      interval.replaceChildren(...options(push ? PUSH_INTERVALS : INTERVALS, push ? 86400 : 60));
      threshold.value = push ? 1 : 3;
    }
    const kw = type === 'dns'
      ? [t('Required answer'), t('Optional. E.g. the expected IP address. Fails if the DNS answer does not contain it; useful to detect a record that changed or points the wrong way.')]
      : [t('Text that must be on the page'), t('Optional. The page counts as broken if this text disappears even though the server answers 200. Case is ignored.')];
    keywordF.querySelector('.label').firstChild.textContent = kw[0];
    keywordF.querySelector('.help').textContent = kw[1];
    for (const f of FIELDS) setError(f, '');
  }

  const dlg = h('dialog', { class: 'dlg', 'aria-labelledby': titleId }, form);
  applyType(true);

  // ---- behaviour ----
  function setError(f, msg) {
    const wrap = form.querySelector(`[data-field="${f}"]`);
    if (!wrap) return;
    const p = wrap.querySelector('.field-error');
    const ctl = els[f];
    if (msg) {
      errors[f] = msg;
      p.textContent = msg;
      p.hidden = false;
      ctl.setAttribute('aria-invalid', 'true');
    } else {
      delete errors[f];
      p.hidden = true;
      p.textContent = '';
      ctl.removeAttribute('aria-invalid');
    }
    if (!summary.hidden && summary.dataset.kind === 'fields') renderSummary(false);
  }

  function parseStatuses() {
    const raw = expect.value.trim();
    if (!raw) return [];
    return raw.split(/[\s,]+/).filter(Boolean).map((x) => (/^\d{3}$/.test(x) ? Number(x) : NaN));
  }

  // Cheap checks the server would repeat anyway; they save a round trip and point at the field.
  const validators = {
    url() {
      const u = url.value.trim();
      if (type === 'push') return '';
      if (type === 'tcp') {
        if (!u) return t('Enter the host and port.');
        const m = u.replace(/^tcp:\/\//i, '').match(/^(\[[0-9a-f:.]+\]|[^\s:/@]+):(\d{1,5})$/i);
        if (!m) return t('Write it as host:port, e.g. db.example.com:5432.');
        return Number(m[2]) >= 1 && Number(m[2]) <= 65535 ? '' : t('The port must be between 1 and 65535.');
      }
      if (type === 'dns') {
        if (!u) return t('Enter a domain name.');
        const n = u.replace(/^dns:\/\//i, '').replace(/\.$/, '');
        if (/^\d+(\.\d+){3}$/.test(n)) return t('Enter a domain name, not an IP address.');
        return /^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$/.test(n) ? '' : t('Enter a valid domain name, e.g. example.com (not a URL).');
      }
      if (!u) return t('Enter the site address.');
      if (!/^https?:\/\/[^\s/?#]+/i.test(u)) return t('The address must start with http:// or https:// and contain a host name.');
      if (/^https?:\/\/[^/?#]*@/i.test(u)) return t('The address must not contain user:password.');
      return '';
    },
    fail_threshold() {
      const n = Number(threshold.value);
      return Number.isInteger(n) && n >= 1 && n <= 10 ? '' : t('Enter a whole number between 1 and 10.');
    },
    timeout_sec() {
      if (type === 'push') return '';
      return Number(timeout.value) > Number(interval.value) ? t('The timeout cannot be larger than the check interval.') : '';
    },
    expect_status() {
      if (type !== 'http') return '';
      return parseStatuses().some((x) => Number.isNaN(x) || x < 100 || x > 599)
        ? t('Enter three-digit status codes (100–599) separated by commas, e.g. 200, 301.') : '';
    },
    keyword() {
      return type === 'http' && keyword.value.trim() && method.value === 'HEAD'
        ? t('Text search cannot be used with the HEAD method. Switch to GET or clear the text.') : '';
    },
  };

  /** Names ticked in the "Kirim notifikasi ke" box. Only the main chats ticked is the same as no list at all. */
  function routes() {
    if (!routeBox) return cur.notify_to || [];
    const on = [...routeIds].filter(([, cb]) => cb.checked).map(([n]) => n);
    return on.length === 1 && on[0] === 'default' ? [] : on;
  }

  function validate() {
    for (const f of FIELDS) setError(f, '');
    for (const [f, check] of Object.entries(validators)) setError(f, check());
    const fieldsOk = Object.keys(errors).length === 0;
    let routeOk = true;
    if (routeBox && ![...routeIds.values()].some((cb) => cb.checked)) {
      routeOk = false;
      routeErr.textContent = t('Choose at least one recipient, or notifications for this site will reach nobody.');
      routeErr.hidden = false;
      if (fieldsOk) [...routeIds.values()][0].focus(); // otherwise the summary of field errors takes the focus
    }
    return fieldsOk && routeOk;
  }

  // The address is typed without its scheme for tcp and dns; the scheme is what tells the server the kind.
  function target() {
    const v = url.value.trim();
    if (type === 'push') return editing ? cur.url : ''; // the server mints it on create and never lets it change
    if (type === 'tcp') return `tcp://${v.replace(/^tcp:\/\//i, '')}`;
    if (type === 'dns') return `dns://${v.replace(/^dns:\/\//i, '').replace(/\.$/, '')}`;
    return v;
  }

  function payload() {
    const p = {
      type, url: target(), name: name.value.trim(),
      interval_sec: Number(interval.value), timeout_sec: Number(timeout.value), fail_threshold: Number(threshold.value),
      allow_private: allowPrivate.checked, public: isPublic.checked, notify_to: routes(),
    };
    if (type === 'http') {
      Object.assign(p, {
        method: method.value, keyword: keyword.value.trim(), expect_status: parseStatuses(),
        user_agent: ua.value.trim(), no_redirect: noRedirect.checked,
      });
    } else if (type === 'dns') {
      Object.assign(p, { dns_type: dnsType.value, dns_server: dnsServer.value.trim(), keyword: keyword.value.trim() });
    } else if (type === 'push') {
      p.grace_sec = Number(grace.value);
    }
    return p;
  }

  // The summary lists what is still wrong. It re-renders as fields get fixed, so it never shows a stale
  // complaint, and it disappears when nothing is left. Only a failed submit moves focus to it.
  function renderSummary(moveFocus) {
    const list = Object.entries(errors);
    summary.replaceChildren();
    if (!list.length) { summary.hidden = true; return; }
    summary.hidden = false;
    summary.dataset.kind = 'fields';
    summary.append(
      h('strong', {}, list.length === 1 ? t('1 field needs fixing:') : t('{n} fields need fixing:', { n: list.length })),
      h('ul', {}, list.map(([f, msg]) => h('li', {},
        h('a', { href: `#${ids[f]}`, onclick: (e) => { e.preventDefault(); els[f].focus(); } }, `${LABELS[f]}: `), msg))));
    if (moveFocus) summary.focus();
  }
  const showSummary = () => renderSummary(true);

  function setBusy(on, label) {
    busy = on;
    for (const b of [submitBtn, testBtn]) { b.disabled = on; }
    form.setAttribute('aria-busy', on ? 'true' : 'false');
    if (label) (label.btn).textContent = on ? label.busy : label.idle;
  }

  /** Map a server error onto its field, or onto the summary when it names none. */
  function serverError(err) {
    if (!(err instanceof ApiError) || err.status !== 400) {
      for (const f of FIELDS) setError(f, '');
      summary.hidden = false;
      summary.dataset.kind = 'message';
      summary.replaceChildren(h('strong', {}, err.message || t('Something went wrong.')));
      summary.focus();
      return;
    }
    for (const f of FIELDS) setError(f, '');
    const field = FIELDS.find((f) => err.message.startsWith(f));
    if (field) setError(field, err.message.replace(new RegExp(`^${field}\\s*`), '').replace(/^./, (c) => c.toUpperCase()));
    else { summary.hidden = false; summary.dataset.kind = 'message'; summary.replaceChildren(h('strong', {}, err.message)); summary.focus(); return; }
    showSummary();
  }

  // Validate a field when the person leaves it (not on every key), and clear its error once it is fixed.
  // Two cases stay silent: an empty field (the submit says so) and focus moving to a button. An error
  // line appearing there would shift the layout and slide the button out from under the pointer, so the
  // first click on "Add site" would be lost.
  const onLeave = (f) => (e) => {
    if (e.relatedTarget instanceof HTMLButtonElement) return;
    const el = els[f];
    if (el.value.trim() === '') { setError(f, ''); return; }
    setError(f, validators[f]());
  };
  for (const f of ['url', 'fail_threshold', 'expect_status']) els[f].addEventListener('blur', onLeave(f));
  for (const f of ['timeout_sec', 'method']) {
    els[f].addEventListener('change', () => {
      setError('timeout_sec', validators.timeout_sec());
      setError('keyword', validators.keyword());
    });
  }
  interval.addEventListener('change', () => setError('timeout_sec', validators.timeout_sec()));
  keyword.addEventListener('blur', onLeave('keyword'));
  for (const f of FIELDS) {
    els[f].addEventListener('input', () => { if (errors[f] && !validators[f]?.()) setError(f, ''); });
  }

  testBtn.addEventListener('click', async () => {
    testBox.replaceChildren();
    if (!validate()) { showSummary(); return; }
    summary.hidden = true;
    setBusy(true, { btn: testBtn, busy: t('Testing…'), idle: t('Test first') });
    testBox.replaceChildren(h('p', { class: 'muted' }, t('Checking the site…')));
    try {
      const r = await api.checkDraft(payload());
      const time = fmtMs(r.latency_ms);
      const okText = type === 'tcp' ? t('Success: the port accepts connections ({time}).', { time })
        : type === 'dns' ? t('Success: answer {answer} ({time}).', { answer: r.answer, time })
        : t('Success: HTTP {status}, {time}.', { status: r.status, time });
      testBox.replaceChildren(r.ok
        ? h('p', { class: 'test ok' }, icon('circle-check', 16), h('span', {}, okText))
        : h('div', { class: 'test bad' }, icon('circle-x', 16),
          h('div', {}, h('strong', {}, t('Failed: {reason}', { reason: kindLabel(r.kind) })), r.hint ? h('p', {}, r.hint) : null)));
    } catch (err) {
      testBox.replaceChildren(h('p', { class: 'test bad' }, icon('circle-x', 16), h('span', {}, err.message)));
    } finally {
      setBusy(false, { btn: testBtn, busy: t('Testing…'), idle: t('Test first') });
      testBox.scrollIntoView({ block: 'nearest' }); // the dialog can be taller than the screen; show the verdict
    }
  });

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    if (busy) return;
    if (!validate()) { showSummary(); return; }
    summary.hidden = true;
    const idle = editing ? t('Save changes') : t('Add site');
    setBusy(true, { btn: submitBtn, busy: t('Saving…'), idle });
    try {
      const saved = editing ? await api.patch(site.id, payload()) : await api.create(payload());
      dlg.close('saved');
      toast(editing ? t('Changes to {name} saved.', { name: saved.name }) : t('{name} added and now being monitored.', { name: saved.name }));
      if (onSaved) onSaved(saved);
    } catch (err) {
      serverError(err);
      setBusy(false, { btn: submitBtn, busy: t('Saving…'), idle });
    }
  });

  dlg.addEventListener('close', () => dlg.remove());
  document.body.append(dlg);
  dlg.showModal();
  url.focus();
}
