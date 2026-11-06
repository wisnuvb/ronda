// Tiny DOM helpers. Elements are built with createElement and text nodes, never by
// parsing strings, so nothing a site owner types (names, URLs, hints) can become markup.
const SVG_NS = 'http://www.w3.org/2000/svg';

function apply(el, attrs) {
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === false || v === null || v === undefined) continue;
    if (k === 'dataset') Object.assign(el.dataset, v);
    else if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? '' : v);
  }
}

function append(el, children) {
  for (const c of children.flat(Infinity)) {
    if (c === null || c === undefined || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
}

export function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  apply(el, attrs);
  append(el, children);
  return el;
}

export function s(tag, attrs, ...children) {
  const el = document.createElementNS(SVG_NS, tag);
  apply(el, attrs);
  append(el, children);
  return el;
}

export const clear = (el) => el.replaceChildren();

let counter = 0;
export const uid = (prefix) => `${prefix}-${++counter}`;

/** Say something to screen readers without moving focus. */
export function announce(message) {
  const live = document.getElementById('live');
  if (!live) return;
  live.textContent = '';
  setTimeout(() => { live.textContent = message; }, 50); // a change after a clear is announced reliably
}

/**
 * Run fn now and then every ms while the tab is visible; run it again as soon as the
 * tab becomes visible. Returns a function that stops everything.
 */
export function poll(fn, ms) {
  let timer = null;
  let stopped = false;
  const tick = async () => {
    if (stopped) return;
    if (!document.hidden) await fn();
    if (!stopped) timer = setTimeout(tick, ms);
  };
  const onVisible = () => {
    if (!document.hidden && !stopped) { clearTimeout(timer); tick(); }
  };
  document.addEventListener('visibilitychange', onVisible);
  tick();
  return () => { stopped = true; clearTimeout(timer); document.removeEventListener('visibilitychange', onVisible); };
}
