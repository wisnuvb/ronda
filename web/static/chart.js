// Response-time chart as SVG. Outages are red bands with an X (shape and text, not colour alone);
// the plot is keyboard-operable and has a table alternative and a text summary.
import { h, s, uid } from './dom.js';
import { fmtMs, fmtPct, fmtClock, fmtDay, fmtDateTime, fmtShort } from './fmt.js';
import { t } from './i18n.js';

const RANGE_MS = { '1h': 36e5, '6h': 216e5, '24h': 864e5, '7d': 6048e5, '30d': 2592e6, '90d': 7776e6 };
const HOUR = 36e5;
const M = { l: 48, r: 12, t: 10, b: 26 };

/** Turn the API's two shapes (raw checks / hourly rollups) into one list of points. */
export function normalize(data) {
  if (data.hourly && data.hourly.length) {
    return {
      hourly: true,
      pts: data.hourly.map(([t, avg, up, total]) => ({ t: t + HOUR / 2, start: t, ms: up > 0 ? avg : null, up, total, fail: up < total })),
    };
  }
  return { hourly: false, pts: (data.raw || []).map(([t, ms]) => ({ t, ms: ms < 0 ? null : ms, fail: ms < 0 })) };
}

function niceStep(v) {
  const p = 10 ** Math.floor(Math.log10(v));
  for (const m of [1, 2, 5, 10]) if (m * p >= v) return m * p;
  return 10 * p;
}

function median(xs) {
  if (!xs.length) return 0;
  const a = [...xs].sort((x, y) => x - y);
  return a[Math.floor(a.length / 2)];
}

export function createChart(root) {
  const sumId = uid('chart-sum');
  const helpId = uid('chart-help');
  const readout = h('div', { class: 'chart-readout' });
  const live = h('div', { class: 'sr-only', 'aria-live': 'polite', 'aria-atomic': 'true' });
  const plot = h('div', {
    class: 'chart-plot', tabindex: '0', role: 'group',
    'aria-label': t('Response time chart'), 'aria-describedby': `${sumId} ${helpId}`,
  });
  const summary = h('p', { id: sumId, class: 'chart-summary' });
  const help = h('p', { id: helpId, class: 'sr-only' }, t('Use the left and right arrow keys to move between points, Home and End to jump to the ends, Escape to close.'));
  const legend = h('ul', { class: 'legend' },
    h('li', {}, s('svg', { width: 22, height: 10, 'aria-hidden': 'true' }, s('line', { x1: 1, x2: 21, y1: 5, y2: 5, class: 'line' })), t('Response time')),
    h('li', {}, s('svg', { width: 22, height: 12, 'aria-hidden': 'true' },
      s('rect', { x: 0, y: 0, width: 22, height: 12, class: 'fail-rect' }),
      s('path', { d: 'M7 3l8 6M15 3l-8 6', class: 'mark-fail' })), t('Failed / down')));
  const tableBox = h('details', { class: 'chart-table' }, h('summary', {}, t('Show data as a table')));
  root.append(readout, plot, summary, help, legend, tableBox, live);

  let model = { hourly: false, pts: [] };
  let range = '24h';
  let width = 0;
  let cursor = -1;
  let geo = null; // x/y mapping of the last draw
  let svg = null;
  let cursorG = null;

  const ro = new ResizeObserver(() => {
    const w = Math.floor(plot.clientWidth);
    if (w && Math.abs(w - width) > 1) { width = w; draw(); }
  });
  ro.observe(plot);

  function setReadout(text, active) {
    readout.textContent = text;
    readout.classList.toggle('active', !!active);
  }

  function describe(p) {
    if (model.hourly) {
      return t('{from}–{to}: average {avg}, {pct} succeeded ({up} of {total} checks)', {
        from: fmtShort(p.start), to: fmtClock(p.start + HOUR), avg: fmtMs(p.ms),
        pct: fmtPct(p.total ? Math.floor((p.up / p.total) * 10000) / 100 : null), up: p.up, total: p.total,
      });
    }
    return p.ms === null ? t('{time}: FAILED', { time: fmtDateTime(p.t) }) : `${fmtDateTime(p.t)}: ${fmtMs(p.ms)}`;
  }

  function idleReadout() {
    const pts = model.pts;
    if (!pts.length) return setReadout(t('No data yet'), false);
    setReadout(t('Hover or focus the chart to see the value of each point'), false);
  }

  function showCursor(i, announceIt) {
    if (!geo || !model.pts.length) return;
    cursor = Math.max(0, Math.min(model.pts.length - 1, i));
    const p = model.pts[cursor];
    const x = geo.x(p.t);
    cursorG.removeAttribute('hidden');
    const line = cursorG.firstChild;
    line.setAttribute('x1', x); line.setAttribute('x2', x);
    const dot = cursorG.lastChild;
    dot.setAttribute('cx', x);
    dot.setAttribute('cy', p.ms === null ? geo.bottom - 8 : geo.y(p.ms));
    dot.setAttribute('class', p.ms === null ? 'cursor-dot bad' : 'cursor-dot');
    setReadout(describe(p), true);
    if (announceIt) live.textContent = describe(p);
  }

  function hideCursor() {
    cursor = -1;
    if (cursorG) cursorG.setAttribute('hidden', '');
    idleReadout();
  }

  function nearest(t) {
    const pts = model.pts;
    let lo = 0, hi = pts.length - 1;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (pts[mid].t < t) lo = mid + 1; else hi = mid;
    }
    if (lo > 0 && Math.abs(pts[lo - 1].t - t) < Math.abs(pts[lo].t - t)) lo--;
    return lo;
  }

  function summarize() {
    const pts = model.pts;
    if (!pts.length) return t('No data for this range yet.');
    const oks = pts.filter((p) => p.ms !== null);
    const avg = oks.length ? Math.round(oks.reduce((a, p) => a + p.ms, 0) / oks.length) : null;
    const max = oks.length ? Math.max(...oks.map((p) => p.ms)) : null;
    if (model.hourly) {
      const total = pts.reduce((a, p) => a + p.total, 0);
      const bad = pts.reduce((a, p) => a + (p.total - p.up), 0);
      return t('Average {avg}, highest (hourly average) {max}. {bad} of {total} checks failed.', { avg: fmtMs(avg), max: fmtMs(max), bad, total });
    }
    const bad = pts.length - oks.length;
    return t('Average {avg}, highest {max}. {bad} of {total} checks failed.', { avg: fmtMs(avg), max: fmtMs(max), bad, total: pts.length });
  }

  function draw() {
    plot.replaceChildren();
    cursorG = null;
    geo = null;
    summary.textContent = summarize();
    const pts = model.pts;
    plot.setAttribute('aria-label', t('Response time chart, {n} points', { n: pts.length }));
    if (!pts.length || !width) {
      plot.append(h('div', { class: 'chart-empty' }, t('No data for this range yet. The first check will appear shortly.')));
      idleReadout();
      return;
    }

    const W = width;
    const H = W < 480 ? 180 : 230;
    const left = M.l, right = W - M.r, top = M.t, bottom = H - M.b;
    const now = Date.now();
    const tMax = now;
    const tMin = now - RANGE_MS[range];
    const x = (t) => left + ((t - tMin) / (tMax - tMin)) * (right - left);

    const oks = pts.filter((p) => p.ms !== null);
    const peak = Math.max(10, ...oks.map((p) => p.ms));
    const step = niceStep((peak * 1.08) / 4);
    const yMax = step * 4;
    const y = (v) => bottom - (v / yMax) * (bottom - top);
    geo = { x, y, bottom };

    svg = s('svg', { viewBox: `0 0 ${W} ${H}`, width: W, height: H, class: 'chart-svg', 'aria-hidden': 'true', focusable: 'false' });

    for (let i = 0; i <= 4; i++) {
      const v = step * i;
      svg.append(
        s('line', { x1: left, x2: right, y1: y(v), y2: y(v), class: 'grid' }),
        s('text', { x: left - 6, y: y(v) + 4, class: 'axis-label', 'text-anchor': 'end' }, i === 0 ? '0' : fmtMs(v)));
    }
    const ticks = Math.max(2, Math.min(7, Math.floor((right - left) / 96)));
    for (let i = 0; i <= ticks; i++) {
      const at = tMin + ((tMax - tMin) * i) / ticks;
      // A 24h window starts and ends at the same clock time, so it needs the day too.
      const label = RANGE_MS[range] < RANGE_MS['24h'] ? fmtClock(at) : RANGE_MS[range] === RANGE_MS['24h'] ? `${fmtDay(at)} ${fmtClock(at)}` : fmtDay(at);
      svg.append(s('text', {
        x: x(at), y: H - 7, class: 'axis-label',
        'text-anchor': i === 0 ? 'start' : i === ticks ? 'end' : 'middle',
      }, label));
    }

    // Outages: group consecutive failures into one band.
    const deltas = [];
    for (let i = 1; i < pts.length; i++) deltas.push(pts[i].t - pts[i - 1].t);
    const gap = model.hourly ? HOUR * 1.6 : Math.max(median(deltas) * 4, 120000);
    let i = 0;
    while (i < pts.length) {
      if (!pts[i].fail) { i++; continue; }
      let j = i;
      while (j + 1 < pts.length && pts[j + 1].fail && pts[j + 1].t - pts[j].t <= gap) j++;
      const a = model.hourly ? pts[i].start : pts[i].t;
      const b = model.hourly ? pts[j].start + HOUR : pts[j].t;
      const x1 = x(a), w = Math.max(3, x(b) - x1);
      svg.append(s('rect', { x: x1 - (w < 4 ? 1 : 0), y: top, width: w, height: bottom - top, class: 'fail-rect' }));
      const cx = x1 + w / 2, cy = bottom - 12;
      svg.append(s('path', { d: `M${cx - 4} ${cy - 4}l8 8M${cx + 4} ${cy - 4}l-8 8`, class: 'mark-fail' }));
      i = j + 1;
    }

    // Line. With more points than pixels, average per ~2px column and show the min-max spread
    // as a soft band, instead of painting thousands of overlapping strokes.
    const cols = Math.max(1, Math.floor((right - left) / 2));
    const dense = pts.length > cols * 1.5;
    let drawPts = pts;
    if (dense) {
      const buckets = new Map();
      for (const p of pts) {
        if (p.ms === null) continue;
        const k = Math.min(cols - 1, Math.max(0, Math.floor(((p.t - tMin) / (tMax - tMin)) * cols)));
        const b = buckets.get(k) || { n: 0, sum: 0, t: 0, min: Infinity, max: -Infinity };
        b.n++; b.sum += p.ms; b.t += p.t; b.min = Math.min(b.min, p.ms); b.max = Math.max(b.max, p.ms);
        buckets.set(k, b);
      }
      drawPts = [...buckets.entries()].sort((a, b) => a[0] - b[0])
        .map(([, b]) => ({ t: b.t / b.n, ms: b.sum / b.n, min: b.min, max: b.max }));
    }
    const colGap = dense ? Math.max(gap, 6 * ((tMax - tMin) / cols)) : gap;

    let seg = [];
    const flush = () => {
      if (seg.length > 1) {
        const d = seg.map((p, k) => `${k ? 'L' : 'M'}${x(p.t).toFixed(1)} ${y(p.ms).toFixed(1)}`).join('');
        if (dense) {
          const up = seg.map((p, k) => `${k ? 'L' : 'M'}${x(p.t).toFixed(1)} ${y(p.max).toFixed(1)}`).join('');
          const down = [...seg].reverse().map((p) => `L${x(p.t).toFixed(1)} ${y(p.min).toFixed(1)}`).join('');
          svg.append(s('path', { d: `${up}${down}Z`, class: 'band' }));
        } else {
          svg.append(s('path', { d: `${d}L${x(seg[seg.length - 1].t).toFixed(1)} ${bottom}L${x(seg[0].t).toFixed(1)} ${bottom}Z`, class: 'area' }));
        }
        svg.append(s('path', { d, class: 'line' }));
      } else if (seg.length === 1) {
        svg.append(s('circle', { cx: x(seg[0].t), cy: y(seg[0].ms), r: 2.5, class: 'dot' }));
      }
      seg = [];
    };
    let prev = null;
    for (const p of drawPts) {
      if (p.ms === null || (prev && p.t - prev.t > colGap)) flush();
      if (p.ms !== null) seg.push(p);
      prev = p;
    }
    flush();

    cursorG = s('g', { hidden: '' },
      s('line', { y1: top, y2: bottom, class: 'cursor-line' }),
      s('circle', { r: 4.5, class: 'cursor-dot' }));
    svg.append(cursorG);
    plot.append(svg);

    plot.onpointermove = (e) => {
      const r = svg.getBoundingClientRect();
      const px = ((e.clientX - r.left) / r.width) * W;
      showCursor(nearest(tMin + ((px - left) / (right - left)) * (tMax - tMin)), false);
    };
    plot.onpointerleave = () => { if (document.activeElement !== plot) hideCursor(); };
    if (cursor >= 0 && cursor < pts.length) showCursor(cursor, false); else idleReadout();
    if (tableBox.open) buildTable();
  }

  plot.addEventListener('keydown', (e) => {
    const n = model.pts.length;
    if (!n) return;
    const at = cursor < 0 ? n - 1 : cursor;
    let next = null;
    if (e.key === 'ArrowLeft') next = cursor < 0 ? n - 1 : at - 1;
    else if (e.key === 'ArrowRight') next = cursor < 0 ? n - 1 : at + 1;
    else if (e.key === 'Home') next = 0;
    else if (e.key === 'End') next = n - 1;
    else if (e.key === 'Escape') { hideCursor(); return; }
    if (next === null) return;
    e.preventDefault();
    showCursor(next, true);
  });
  plot.addEventListener('blur', () => hideCursor());

  function buildTable() {
    tableBox.querySelector('table')?.remove();
    tableBox.querySelector('p')?.remove();
    const pts = model.pts;
    const rows = pts.slice(-200).reverse();
    const head = model.hourly ? [t('Hour'), t('Succeeded'), t('Average response time')] : [t('Time'), t('Result'), t('Response time')];
    const body = rows.map((p) => model.hourly
      ? h('tr', {}, h('td', {}, fmtShort(p.start)), h('td', { class: 'num' }, t('{up} of {total}', { up: p.up, total: p.total })), h('td', { class: 'num' }, fmtMs(p.ms)))
      : h('tr', {}, h('td', {}, fmtDateTime(p.t)), h('td', {}, p.ms === null ? t('Failed') : t('Succeeded')), h('td', { class: 'num' }, fmtMs(p.ms))));
    if (pts.length > rows.length) tableBox.append(h('p', { class: 'muted' }, t('Showing the latest {n} of {total} points.', { n: rows.length, total: pts.length })));
    tableBox.append(h('div', { class: 'table-wrap table-scroll' },
      h('table', { class: 'table compact' },
        h('caption', { class: 'sr-only' }, t('Response time chart data, newest first')),
        h('thead', {}, h('tr', {}, head.map((label, k) => h('th', { scope: 'col', class: k ? 'num' : '' }, label)))),
        h('tbody', {}, body))));
  }
  tableBox.addEventListener('toggle', () => { if (tableBox.open) buildTable(); });

  return {
    update(data, r) {
      range = r;
      model = normalize(data);
      width = Math.floor(plot.clientWidth) || width;
      cursor = -1;
      draw();
    },
    destroy() { ro.disconnect(); },
  };
}
