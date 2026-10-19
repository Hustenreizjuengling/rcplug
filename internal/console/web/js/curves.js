// The graphs of a channel's processing, drawn as the device's own screen
// draws them (rc_gui_juce, firmware 1.7.6): the EQ's three bell filters with
// the high-pass filter, the high-pass alone, the compressor's transfer curve
// with the level on it, the noise gate's level history with its two
// thresholds, and the de-esser's dip at its frequency. Small SVGs, redrawn
// at most once per animation frame; the gate's history ticks only while it
// is shown. Units and scales: scales.js.
import { val } from './store.js';
import { t } from './i18n.js';
import { meterDb, reductionDb } from './meters.js';
import { SCALES, fmtHz, fmtDb } from './scales.js';

const FS = 48000; // the device computes its curves at 48 kHz
const NS = 'http://www.w3.org/2000/svg';
const el = (name, attrs, text) => {
  const e = document.createElementNS(NS, name);
  for (const [k, v] of Object.entries(attrs || {})) e.setAttribute(k, v);
  if (text !== undefined) e.textContent = text;
  return e;
};
const num = (ch, p) => Number(val(ch, p)) || 0;
const on = (ch, p) => val(ch, p) === true;

// bell returns |H| in dB of the device's peaking filter: A = 10^(gain/40),
// alpha = sin(w0) / (2Q).
function bell(f0, gainDb, q, f) {
  const A = Math.pow(10, gainDb / 40), w0 = (2 * Math.PI * f0) / FS, alpha = Math.sin(w0) / (2 * q), cw = Math.cos(w0);
  const b0 = 1 + alpha * A, b1 = -2 * cw, b2 = 1 - alpha * A, a0 = 1 + alpha / A, a1 = -2 * cw, a2 = 1 - alpha / A;
  const w = (2 * Math.PI * f) / FS;
  const c1 = Math.cos(w), s1 = Math.sin(w), c2 = Math.cos(2 * w), s2 = Math.sin(2 * w);
  const nr = b0 + b1 * c1 + b2 * c2, ni = -(b1 * s1 + b2 * s2);
  const dr = a0 + a1 * c1 + a2 * c2, di = -(a1 * s1 + a2 * s2);
  return 10 * Math.log10((nr * nr + ni * ni) / (dr * dr + di * di));
}
const hpf = (fc, slope, f) => -10 * Math.log10(1 + Math.pow(fc / f, 2 * (slope / 6)));

// The frequency axis: 20 Hz · 1100^x, 20 Hz to 22 kHz, as on the device.
const W = 600;
const fx = (f) => (Math.log(f / 20) / Math.log(1100)) * W;
const xf = (x) => 20 * Math.pow(1100, x / W);

function frame(cls, label, h) {
  return el('svg', { viewBox: `0 0 ${W} ${h}`, class: 'graph ' + cls, role: 'img', 'aria-label': label });
}
function freqGrid(h) {
  const g = el('g', { class: 'grid' });
  for (const f of [50, 100, 200, 500, 1000, 2000, 5000, 10000]) g.append(el('line', { x1: fx(f), x2: fx(f), y1: 0, y2: h }));
  for (const [f, s] of [[100, '100'], [1000, '1k'], [10000, '10k']]) g.append(el('text', { x: fx(f) + 3, y: h - 4 }, s));
  return g;
}
// redraw coalesces updates into one per animation frame.
function redraw(fn) {
  let frame = 0;
  return () => { if (!frame) frame = requestAnimationFrame(() => { frame = 0; fn(); }); };
}

const EQ_PROPS = ['eqOn', 'eqLowOn', 'eqMidOn', 'eqHighOn', 'eqLowGain', 'eqLowShelf', 'eqLowQ', 'eqMidGain', 'eqMidBell', 'eqMidQ',
  'eqHighGain', 'eqHighBell', 'eqHighQ', 'hpfOn', 'hpfFrequency', 'hpfSlope', 'channelBypassProcessing'];
const BANDS = [['Low', 'eqLowShelf', 'var(--blue)'], ['Mid', 'eqMidBell', 'var(--amber)'], ['High', 'eqHighBell', 'var(--violet)']];

// eqCurve: the EQ's bands and the high-pass filter (opts.hpfOnly: the
// high-pass page's curve).
export function eqCurve(scope, ch, opts = {}) {
  const H = 170, DB = 18;
  const y = (db) => H / 2 - (Math.max(-DB, Math.min(DB, db)) / DB) * (H / 2 - 6);
  const svg = frame('eq', t(opts.hpfOnly ? 'graph.hpf' : 'eq.curve'), H);
  const grid = freqGrid(H);
  for (const db of [-12, -6, 6, 12]) grid.append(el('line', { x1: 0, x2: W, y1: y(db), y2: y(db) }));
  grid.append(el('line', { x1: 0, x2: W, y1: y(0), y2: y(0), class: 'zero' }));
  const area = el('path', { class: 'area' }), line = el('path', { class: 'line' }), dots = el('g');
  svg.append(grid, area, line, dots);
  const draw = () => {
    const bypass = on(ch, 'channelBypassProcessing');
    const bands = [];
    if (!opts.hpfOnly && !bypass && on(ch, 'eqOn')) {
      for (const [b, fp, c] of BANDS) {
        if (on(ch, 'eq' + b + 'On')) bands.push({ f: SCALES[fp](num(ch, fp)), g: SCALES['eq' + b + 'Gain'](num(ch, 'eq' + b + 'Gain')), q: SCALES['eq' + b + 'Q'](num(ch, 'eq' + b + 'Q')), c });
      }
    }
    const cut = !bypass && on(ch, 'hpfOn') ? { f: SCALES.hpfFrequency(num(ch, 'hpfFrequency')), s: SCALES.hpfSlope(num(ch, 'hpfSlope')) } : null;
    const resp = (f) => bands.reduce((db, b) => db + bell(b.f, b.g, b.q, f), cut ? hpf(cut.f, cut.s, f) : 0);
    let d = '';
    for (let x = 0; x <= W; x += 4) d += (x ? 'L' : 'M') + x + ' ' + y(resp(xf(x))).toFixed(1);
    line.setAttribute('d', d);
    area.setAttribute('d', d + `L${W} ${y(0)}L0 ${y(0)}Z`);
    const marks = bands.map((b) => el('circle', { cx: fx(b.f).toFixed(1), cy: y(resp(b.f)).toFixed(1), r: 5, style: 'fill:' + b.c }));
    if (cut) marks.push(el('circle', { cx: fx(cut.f).toFixed(1), cy: y(resp(cut.f)).toFixed(1), r: 4, style: 'fill:var(--red)' }));
    dots.replaceChildren(...marks);
  };
  const upd = redraw(draw);
  for (const p of EQ_PROPS) scope.bind(ch, p, upd);
  return svg;
}

// stripLevel is a strip's level in dB (the louder side), or -Infinity.
function stripLevel(meter) {
  return meter ? Math.max(meterDb(val(meter, 'meterLevelL')), meterDb(val(meter, 'meterLevelR'))) : -Infinity;
}

// compCurve: the compressor's transfer curve, input and output from −60 to
// +6 dB, a hard knee drawn rounded, make-up gain added; the dot is the
// channel's level on the curve (the device works the input out of the
// output level the same way).
export function compCurve(scope, ch, meter) {
  const H = 220, P = 10, w = W - 2 * P, hh = H - 2 * P;
  const x = (db) => P + ((db + 60) / 66) * w, y = (db) => H - P - ((Math.max(-60, Math.min(6, db)) + 60) / 66) * hh;
  const svg = frame('comp', t('graph.comp'), H);
  const grid = el('g', { class: 'grid' });
  for (const db of [-48, -36, -24, -12, 0]) {
    grid.append(el('line', { x1: x(db), x2: x(db), y1: P, y2: H - P }), el('line', { x1: P, x2: W - P, y1: y(db), y2: y(db), class: db === 0 ? 'zero' : '' }));
    grid.append(el('text', { x: x(db) + 3, y: H - P - 4 }, String(db)));
  }
  grid.append(el('line', { x1: x(-60), y1: y(-60), x2: x(6), y2: y(6), class: 'unity' }));
  const thr = el('line', { class: 'mark' }), line = el('path', { class: 'line' }), dot = el('circle', { r: 6, class: 'dot' });
  const label = el('text', { class: 'label', x: W - P - 4, y: P + 14, 'text-anchor': 'end' });
  svg.append(grid, thr, line, dot, label);
  const params = () => ({ T: SCALES.compressorThreshold(num(ch, 'compressorThreshold')), R: SCALES.compressorRatio(num(ch, 'compressorRatio')), M: SCALES.compressorGain(num(ch, 'compressorGain')), active: on(ch, 'compressorOn') && !on(ch, 'channelBypassProcessing') });
  const out = (p, i) => (i < p.T ? i : p.T + (i - p.T) / p.R) + p.M;
  const drawCurve = () => {
    const p = params(), K = 6; // the knee is drawn rounded over ±6 dB
    let d = `M${x(-60)} ${y(out(p, -60))}`, knee = false;
    for (let i = -59; i <= 6; i++) {
      if (i > p.T - K && i < p.T + K) {
        if (!knee) d += `L${x(p.T - K)} ${y(out(p, p.T - K))}Q${x(p.T)} ${y(out(p, p.T))} ${x(p.T + K)} ${y(out(p, p.T + K))}`;
        knee = true;
        continue;
      }
      d += `L${x(i)} ${y(out(p, i))}`;
    }
    line.setAttribute('d', d);
    svg.classList.toggle('off', !p.active);
    thr.setAttribute('x1', x(p.T)); thr.setAttribute('x2', x(p.T)); thr.setAttribute('y1', P); thr.setAttribute('y2', H - P);
    label.textContent = fmtDb(p.T) + ' · ' + p.R.toFixed(1) + ' : 1';
    drawDot();
  };
  const drawDot = () => {
    const p = params(), o = stripLevel(meter);
    let i = o - p.M;
    if (i > p.T) i = p.T + p.R * (i - p.T);
    const show = p.active && isFinite(o) && i > -60 && i <= 6;
    dot.style.display = show ? '' : 'none';
    if (show) { dot.setAttribute('cx', x(i).toFixed(1)); dot.setAttribute('cy', y(o).toFixed(1)); }
  };
  const upd = redraw(drawCurve), updDot = redraw(drawDot);
  for (const n of ['compressorThreshold', 'compressorRatio', 'compressorGain', 'compressorOn', 'channelBypassProcessing']) scope.bind(ch, n, upd);
  if (meter) for (const n of ['meterLevelL', 'meterLevelR']) scope.bind(meter, n, updDot);
  return svg;
}

// gateGraph: the channel's level over the last five seconds (+6 to −64 dB),
// open (above the threshold) or closed: once open the gate stays open until
// the level falls below the threshold minus the hysteresis (0..1 = 0..30 dB).
export function gateGraph(scope, ch, meter) {
  const H = 170, N = 100, STEP = 50; // 100 samples, 50 ms apart
  const y = (db) => Math.min(H, Math.max(0, ((6 - db) / 70) * H));
  const svg = frame('gate', t('graph.gate'), H);
  const grid = el('g', { class: 'grid' });
  for (const db of [0, -20, -40, -60]) grid.append(el('line', { x1: 0, x2: W, y1: y(db), y2: y(db) }), el('text', { x: 4, y: y(db) - 3 }, String(db)));
  const closed = el('path', { class: 'line closed' }), open = el('path', { class: 'line' });
  const thr = el('line', { class: 'mark', x1: 0, x2: W }), close = el('line', { class: 'mark dashed', x1: 0, x2: W });
  const label = el('text', { class: 'label', x: W - 6, y: 16, 'text-anchor': 'end' });
  svg.append(grid, closed, open, thr, close, label);
  const hist = new Float32Array(N).fill(-Infinity);
  let head = 0;
  const params = () => {
    const T = SCALES.noiseGateThreshold(num(ch, 'noiseGateThreshold'));
    return { T, C: T - 30 * Math.min(1, Math.max(0, num(ch, 'noiseGateHysteresis'))), active: on(ch, 'noiseGateOn') && !on(ch, 'channelBypassProcessing') };
  };
  const draw = () => {
    const p = params();
    svg.classList.toggle('off', !p.active);
    thr.setAttribute('y1', y(p.T)); thr.setAttribute('y2', y(p.T));
    close.setAttribute('y1', y(p.C)); close.setAttribute('y2', y(p.C));
    label.textContent = p.active ? fmtDb(p.T) : t('graph.off');
    let dOpen = '', dClosed = '', state = false, prev = '', last = null;
    for (let k = 0; k < N; k++) {
      const db = hist[(head + k) % N], pt = ((k * W) / (N - 1)).toFixed(1) + ' ' + y(isFinite(db) ? db : -64).toFixed(1);
      state = db >= p.T || (state && db >= p.C);
      if (k > 0) {
        const seg = (last === state ? 'L' : 'M' + prev + 'L') + pt; // the segment to here takes this point's state
        if (state) dOpen += seg; else dClosed += seg;
        last = state;
      }
      prev = pt;
    }
    open.setAttribute('d', dOpen);
    closed.setAttribute('d', dClosed);
  };
  const upd = redraw(draw);
  for (const n of ['noiseGateThreshold', 'noiseGateHysteresis', 'noiseGateOn', 'channelBypassProcessing']) scope.bind(ch, n, upd);
  if (meter) {
    scope.interval(() => {
      if (!svg.isConnected) return;
      hist[head] = stripLevel(meter);
      head = (head + 1) % N;
      upd();
    }, STEP);
  }
  return svg;
}

// deessCurve: the de-esser's dip at its frequency (a bell, Q 1.7), as deep
// as the channel's current gain reduction, at the height of the threshold.
export function deessCurve(scope, ch, reduction) {
  const H = 150;
  const svg = frame('deess', t('graph.deess'), H);
  const line = el('path', { class: 'line' }), mark = el('line', { class: 'mark', y1: 0, y2: H });
  const label = el('text', { class: 'label', y: 16 });
  svg.append(freqGrid(H), mark, line, label);
  const draw = () => {
    const f = SCALES.deesserFrequency(num(ch, 'deesserFrequency')), T = SCALES.deesserThreshold(num(ch, 'deesserThreshold'));
    const active = on(ch, 'deesserOn') && !on(ch, 'channelBypassProcessing');
    const depth = active && reduction ? Math.min(24, reductionDb(val(reduction, 'meterLevelL'))) : 0;
    const base = H * 0.75 - ((T + 60) / 60) * (H * 0.45); // a higher threshold draws higher
    let d = '';
    for (let x = 0; x <= W; x += 4) d += (x ? 'L' : 'M') + x + ' ' + Math.min(H - 2, base - bell(f, -depth, 1.7, xf(x)) * 2.5).toFixed(1);
    line.setAttribute('d', d);
    svg.classList.toggle('off', !active);
    mark.setAttribute('x1', fx(f)); mark.setAttribute('x2', fx(f));
    label.setAttribute('x', Math.min(W - 70, fx(f) + 6));
    label.textContent = fmtHz(f);
  };
  const upd = redraw(draw);
  for (const n of ['deesserFrequency', 'deesserThreshold', 'deesserOn', 'channelBypassProcessing']) scope.bind(ch, n, upd);
  if (reduction) scope.bind(reduction, 'meterLevelL', upd);
  return svg;
}
