// Formatting for the dashboard's language and the server's time zone (see i18n.js). Numbers, month and
// weekday names, list joining and sorting all come from Intl, so they follow the language without a catalog.
import { t, locale, timezone } from './i18n.js';

const dateParts = new Intl.DateTimeFormat(locale, {
  timeZone: timezone, day: 'numeric', month: 'short', year: 'numeric',
  hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
});

function parts(ms) {
  const o = {};
  for (const p of dateParts.formatToParts(new Date(ms))) o[p.type] = p.value;
  return o;
}

// ---- the zone's name -------------------------------------------------------------------------------------
// The server prints the abbreviation from the system's tz database ("CEST", "WIB", "EDT", "+04"). Intl knows
// an abbreviation only for the zones a locale cares about (en-GB: "CEST", en-US: "EDT", id-ID: "WIB"), and
// otherwise answers "GMT+2". So ask the page's locale first, then en-GB and en-US, and take the first real
// abbreviation; if none has one, show the numeric form. The clock time itself is always exactly the server's.
const NUMERIC_ZONE = /^(GMT|UTC)[+\-−]\d/;
const abbrFormats = [...new Set([locale, 'en-GB', 'en-US'])].map((l) => new Intl.DateTimeFormat(l, { timeZone: timezone, timeZoneName: 'short' }));
const abbrCache = new Map();

/** "CEST", "WIB", "GMT+7": the zone's short name at that moment (it changes with daylight saving). */
export function zoneAbbr(ms) {
  const key = Math.floor(ms / 900000); // a zone's name changes at most every few months: 15 minutes is exact enough
  let hit = abbrCache.get(key);
  if (hit === undefined) {
    const names = abbrFormats.map((f) => (f.formatToParts(new Date(ms)).find((p) => p.type === 'timeZoneName') || {}).value || '');
    hit = names.find((n) => n && !NUMERIC_ZONE.test(n)) || names[names.length - 1] || names[0] || '';
    abbrCache.set(key, hit);
  }
  return hit;
}

export function fmtDateTime(ms) {
  const p = parts(ms);
  return `${p.day} ${p.month} ${p.year} ${p.hour}:${p.minute}:${p.second} ${zoneAbbr(ms)}`;
}
export function fmtShort(ms) {
  const p = parts(ms);
  return `${p.day} ${p.month} ${p.hour}:${p.minute}`;
}
export function fmtClock(ms, withSeconds = false) {
  const p = parts(ms);
  return withSeconds ? `${p.hour}:${p.minute}:${p.second}` : `${p.hour}:${p.minute}`;
}
/** Clock time with the zone's name, as the footers show it: "09:30:12 CEST". */
export function fmtClockZone(ms, withSeconds = false) {
  return `${fmtClock(ms, withSeconds)} ${zoneAbbr(ms)}`;
}
export function fmtDay(ms) {
  const p = parts(ms);
  return `${p.day} ${p.month}`;
}

// ---- durations ---------------------------------------------------------------------------------------------

/** "{n} minutes" in the page's language, from the browser's own unit names. unit: second|minute|hour|day|week. */
export function fmtUnit(n, unit) {
  try {
    return new Intl.NumberFormat(locale, { style: 'unit', unit, unitDisplay: 'long' }).format(n);
  } catch {
    return `${n} ${unit}${n === 1 ? '' : 's'}`;
  }
}

/** A period in its largest whole unit, written out: 7200 -> "2 hours"; for option lists and sentences. */
export function fmtLong(sec) {
  sec = Math.round(sec);
  for (const [size, unit] of [[604800, 'week'], [86400, 'day'], [3600, 'hour'], [60, 'minute']]) {
    if (sec >= size && sec % size === 0) return fmtUnit(sec / size, unit);
  }
  return fmtUnit(sec, 'second');
}

/** A compact elapsed time: "4 min 12 s". */
export function fmtDur(sec) {
  sec = Math.max(0, Math.round(sec));
  if (sec < 60) return t('{s} s', { s: sec });
  if (sec < 3600) {
    const m = Math.floor(sec / 60), s = sec % 60;
    return s ? t('{m} min {s} s', { m, s }) : t('{m} min', { m });
  }
  if (sec < 86400) {
    const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60);
    return m ? t('{h} h {m} min', { h, m }) : t('{h} h', { h });
  }
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600);
  return h ? t('{d} d {h} h', { d, h }) : t('{d} d', { d });
}

export function fmtMs(ms) {
  if (ms === null || ms === undefined) return '–';
  if (ms < 1000) return `${Math.round(ms)} ms`;
  return t('{v} s', { v: (ms / 1000).toLocaleString(locale, { minimumFractionDigits: 1, maximumFractionDigits: 1 }) });
}

/** Uptime percentage; the server already guarantees it never shows 100 when a check failed. */
export function fmtPct(v) {
  if (v === null || v === undefined) return '–';
  if (v === 100) return '100%';
  return `${v.toLocaleString(locale, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}%`;
}

export function ago(ms, now = Date.now()) {
  const d = Math.max(0, (now - ms) / 1000);
  if (d < 5) return t('just now');
  if (d < 60) return t('{n} s ago', { n: Math.floor(d) });
  if (d < 3600) return t('{n} min ago', { n: Math.floor(d / 60) });
  if (d < 86400) return t('{n} h ago', { n: Math.floor(d / 3600) });
  return t('{n} d ago', { n: Math.floor(d / 86400) });
}

export function daysUntil(ms, now = Date.now()) {
  return Math.floor((ms - now) / 86400000);
}

// ---- names -------------------------------------------------------------------------------------------------

const collator = new Intl.Collator(locale);
/** Locale-aware string comparison, for sorting names. */
export const compareText = (a, b) => collator.compare(a, b);

/** A week day by its number (0 = Sunday, as the server counts), "short" or "long". */
const dayNames = {
  short: new Intl.DateTimeFormat(locale, { weekday: 'short', timeZone: 'UTC' }),
  long: new Intl.DateTimeFormat(locale, { weekday: 'long', timeZone: 'UTC' }),
};
export const weekdayName = (d, style = 'long') => dayNames[style].format(new Date(Date.UTC(2024, 0, 7 + d))); // 7 Jan 2024 was a Sunday

/** "Saturday and Sunday" / "Sabtu dan Minggu". */
export const joinList = (items) => {
  try { return new Intl.ListFormat(locale, { style: 'long', type: 'conjunction' }).format(items); } catch { return items.join(', '); }
};

const KINDS = {
  ok: t('Healthy'),
  dns: t('DNS failed'),
  refused: t('Connection refused'),
  unreachable: t('Unreachable'),
  reset: t('Connection reset'),
  timeout_connect: t('Connect timeout'),
  timeout_tls: t('TLS timeout'),
  timeout_response: t('Response timeout'),
  tls_expired: t('Certificate expired'),
  tls_hostname: t('Certificate name mismatch'),
  tls_untrusted: t('Untrusted certificate'),
  tls: t('TLS error'),
  redirect_loop: t('Redirect loop'),
  blocked: t('Address blocked'),
  cloudflare: t('Cloudflare error'),
  http_5xx: t('Server error (5xx)'),
  http_4xx: t('Client error (4xx)'),
  status: t('Unexpected status'),
  keyword: t('Text not found'),
  missed: t('Ping not received'),
  reported_failure: t('Job reported failure'),
  error: t('Error'),
};
export const kindLabel = (k) => KINDS[k] || k || '–';
