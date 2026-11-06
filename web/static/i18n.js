// Language and time zone of the dashboard. The server decides both (config "language" and "timezone") and
// announces them in /app-config.js as window.RONDA, so the dashboard speaks the same language and shows the
// same clock times as the Telegram messages. If that script is missing (a static file server, a test) the
// dashboard falls back to English and UTC.
//
// Text is translated by its English source: t('Add site') returns the catalog entry for the active language,
// or the English text itself when there is none. Placeholders are {name} and are filled from the second
// argument. Never build a sentence from translated pieces; give t() the whole sentence with placeholders.
// web/i18n_test.go checks that every t('...') and data-i18n text has an entry in i18n-id.js.
import idCatalog from './i18n-id.js';

/** The product name, kept in one place. It is also available to every message as {app}. */
export const APP_NAME = 'ronda';

const CATALOGS = { id: idCatalog };
const cfg = (typeof window !== 'undefined' && window.RONDA) || {};

function validZone(name) {
  if (typeof name !== 'string' || !name) return false;
  try { new Intl.DateTimeFormat('en-US', { timeZone: name }); return true; } catch { return false; }
}

export const lang = Object.hasOwn(CATALOGS, cfg.lang) ? cfg.lang : 'en';
/** The IANA zone the server runs in; every clock time on the page is shown in it. */
export const timezone = validZone(cfg.timezone) ? cfg.timezone : 'UTC';
/** BCP 47 locale for numbers, dates and sorting. English is en-US so month names are "Sep", not "Sept". */
export const locale = lang === 'id' ? 'id-ID' : 'en-US';

const catalog = CATALOGS[lang] || null;

export function t(msg, vars) {
  let s = catalog && Object.hasOwn(catalog, msg) && catalog[msg] ? catalog[msg] : msg;
  s = s.replace(/\{(\w+)\}/g, (m, k) => {
    if (vars && Object.hasOwn(vars, k)) return String(vars[k]);
    return k === 'app' ? APP_NAME : m;
  });
  return s;
}

/**
 * Like t(), for a sentence that contains elements (a link, <strong>, <code>): the values in vars may be DOM
 * nodes, and the result is an array of text and nodes for h(). The sentence stays whole for the translator,
 * who can move the placeholders around: tNodes('Open {link} now', { link: h('a', {}, 'the page') }).
 */
export function tNodes(msg, vars = {}) {
  const out = [];
  const src = catalog && Object.hasOwn(catalog, msg) && catalog[msg] ? catalog[msg] : msg;
  let last = 0;
  for (const m of src.matchAll(/\{(\w+)\}/g)) {
    if (m.index > last) out.push(src.slice(last, m.index));
    const v = Object.hasOwn(vars, m[1]) ? vars[m[1]] : (m[1] === 'app' ? APP_NAME : m[0]);
    out.push(v instanceof Node ? v.cloneNode(true) : String(v));
    last = m.index + m[0].length;
  }
  if (last < src.length) out.push(src.slice(last));
  return out;
}

/**
 * Translate the static HTML: data-i18n="English text" replaces the element's text, and
 * data-i18n-<attribute>="English text" sets that attribute (aria-label, title, placeholder, ...).
 * The English text in the file is what shows if scripts fail.
 */
export function translateDocument(root = document) {
  for (const el of root.querySelectorAll('[data-i18n]')) el.textContent = t(el.getAttribute('data-i18n'));
  for (const el of root.querySelectorAll('*')) {
    for (const attr of [...el.attributes]) {
      if (attr.name.startsWith('data-i18n-')) el.setAttribute(attr.name.slice('data-i18n-'.length), t(attr.value));
    }
  }
}

if (typeof document !== 'undefined') {
  document.documentElement.lang = lang;
  translateDocument();
}
