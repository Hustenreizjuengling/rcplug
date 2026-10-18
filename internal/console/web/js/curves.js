// The frequency response of a channel's EQ and high-pass filter, drawn as a
// small SVG and redrawn once per animation frame when a parameter changes.
//
// The desk stores these parameters as 0..1 without units. SCALES says how
// they map to Hz, dB and Q: estimates that fit a real Duo's values (gain 0.5
// is 0 dB; frequencies are logarithmic, a low shelf at 0.35 lands near
// 100 Hz; the Q values are multiples of 1/99, as on a 0.1..10 scale) until
// the device's own read-outs confirm them. Everything that depends on them is
// here, so a confirmed scale changes one line.
import { val } from './store.js';
import { t } from './i18n.js';

const logMap = (v, lo, hi) => lo * Math.pow(hi / lo, Math.min(1, Math.max(0, v)));

export const SCALES = {
  gain: (v) => (v - 0.5) * 24, // dB, ±12
  lowFreq: (v) => logMap(v, 20, 2000),
  midFreq: (v) => logMap(v, 100, 10000),
  highFreq: (v) => logMap(v, 1000, 20000),
  q: (v) => 0.1 + v * 9.9,
  hpfFreq: (v) => logMap(v, 20, 500),
  hpfSlope: (v) => [6, 12, 18, 24][Math.min(3, Math.max(0, Math.round(v * 3)))], // dB per octave
};

export const fmtHz = (f) => (f >= 1000 ? '≈' + (f / 1000).toFixed(f >= 10000 ? 0 : 1) + ' kHz' : '≈' + Math.round(f) + ' Hz');
export const fmtDb = (db) => (db > 0.05 ? '+' : db < -0.05 ? '−' : '') + Math.abs(db).toFixed(1) + ' dB';

// Display formats for the sliders of these parameters.
export const FORMATS = {
  eqLowGain: (v) => fmtDb(SCALES.gain(v)), eqMidGain: (v) => fmtDb(SCALES.gain(v)), eqHighGain: (v) => fmtDb(SCALES.gain(v)),
  eqLowShelf: (v) => fmtHz(SCALES.lowFreq(v)), eqMidBell: (v) => fmtHz(SCALES.midFreq(v)), eqHighBell: (v) => fmtHz(SCALES.highFreq(v)),
  eqLowQ: (v) => SCALES.q(v).toFixed(2), eqMidQ: (v) => SCALES.q(v).toFixed(2), eqHighQ: (v) => SCALES.q(v).toFixed(2),
  hpfFrequency: (v) => fmtHz(SCALES.hpfFreq(v)), hpfSlope: (v) => SCALES.hpfSlope(v) + ' ' + t('eq.perOctave'),
};

const FS = 48000;

// biquad returns |H| in dB at frequency f for RBJ cookbook filters.
function biquadDb(type, f0, gainDb, q, f) {
  const A = Math.pow(10, gainDb / 40), w0 = (2 * Math.PI * f0) / FS, cw = Math.cos(w0), sw = Math.sin(w0);
  const alpha = sw / (2 * q);
  let b0, b1, b2, a0, a1, a2;
  if (type === 'peak') {
    b0 = 1 + alpha * A; b1 = -2 * cw; b2 = 1 - alpha * A; a0 = 1 + alpha / A; a1 = -2 * cw; a2 = 1 - alpha / A;
  } else if (type === 'lowshelf') {
    const s = 2 * Math.sqrt(A) * alpha;
    b0 = A * ((A + 1) - (A - 1) * cw + s); b1 = 2 * A * ((A - 1) - (A + 1) * cw); b2 = A * ((A + 1) - (A - 1) * cw - s);
    a0 = (A + 1) + (A - 1) * cw + s; a1 = -2 * ((A - 1) + (A + 1) * cw); a2 = (A + 1) + (A - 1) * cw - s;
  } else { // highshelf
    const s = 2 * Math.sqrt(A) * alpha;
    b0 = A * ((A + 1) + (A - 1) * cw + s); b1 = -2 * A * ((A - 1) + (A + 1) * cw); b2 = A * ((A + 1) + (A - 1) * cw - s);
    a0 = (A + 1) - (A - 1) * cw + s; a1 = 2 * ((A - 1) - (A + 1) * cw); a2 = (A + 1) - (A - 1) * cw - s;
  }
  const w = (2 * Math.PI * f) / FS;
  const c1 = Math.cos(w), s1 = Math.sin(w), c2 = Math.cos(2 * w), s2 = Math.sin(2 * w);
  const nr = b0 + b1 * c1 + b2 * c2, ni = -(b1 * s1 + b2 * s2);
  const dr = a0 + a1 * c1 + a2 * c2, di = -(a1 * s1 + a2 * s2);
  return 10 * Math.log10((nr * nr + ni * ni) / (dr * dr + di * di));
}

const PROPS = ['eqOn', 'eqLowOn', 'eqMidOn', 'eqHighOn', 'eqLowGain', 'eqLowShelf', 'eqLowQ', 'eqMidGain', 'eqMidBell', 'eqMidQ',
  'eqHighGain', 'eqHighBell', 'eqHighQ', 'hpfOn', 'hpfFrequency', 'hpfSlope', 'channelBypassProcessing'];

// bands lists the active filters of a channel with their scaled parameters.
function bands(ch) {
  const n = (p) => Number(val(ch, p)) || 0;
  const on = (p) => val(ch, p) === true;
  const out = [];
  if (on('channelBypassProcessing')) return out;
  if (on('eqOn')) {
    if (on('eqLowOn')) out.push({ type: 'lowshelf', f: SCALES.lowFreq(n('eqLowShelf')), g: SCALES.gain(n('eqLowGain')), q: SCALES.q(n('eqLowQ')), c: 'var(--blue)' });
    if (on('eqMidOn')) out.push({ type: 'peak', f: SCALES.midFreq(n('eqMidBell')), g: SCALES.gain(n('eqMidGain')), q: SCALES.q(n('eqMidQ')), c: 'var(--amber)' });
    if (on('eqHighOn')) out.push({ type: 'peak', f: SCALES.highFreq(n('eqHighBell')), g: SCALES.gain(n('eqHighGain')), q: SCALES.q(n('eqHighQ')), c: 'var(--violet)' });
  }
  if (on('hpfOn')) out.push({ type: 'hpf', f: SCALES.hpfFreq(n('hpfFrequency')), order: SCALES.hpfSlope(n('hpfSlope')) / 6, c: 'var(--red)' });
  return out;
}

function responseDb(bs, f) {
  let db = 0;
  for (const b of bs) {
    if (b.type === 'hpf') db += -10 * Math.log10(1 + Math.pow(b.f / f, 2 * b.order));
    else db += biquadDb(b.type, b.f, b.g, b.q, f);
  }
  return db;
}

const NS = 'http://www.w3.org/2000/svg';
const el = (name, attrs) => {
  const e = document.createElementNS(NS, name);
  for (const [k, v] of Object.entries(attrs || {})) e.setAttribute(k, v);
  return e;
};

// eqCurve returns the curve for a channel, kept up to date.
export function eqCurve(scope, ch) {
  const W = 600, H = 170, DB = 18; // ±18 dB on the grid
  const x = (f) => (Math.log10(f / 20) / Math.log10(1000)) * W; // 20 Hz .. 20 kHz
  const y = (db) => H / 2 - (Math.max(-DB, Math.min(DB, db)) / DB) * (H / 2 - 6);
  const svg = el('svg', { viewBox: `0 0 ${W} ${H}`, class: 'eqcurve', role: 'img', 'aria-label': t('eq.curve') });
  const grid = el('g', { class: 'grid' });
  for (const f of [50, 100, 200, 500, 1000, 2000, 5000, 10000]) grid.append(el('line', { x1: x(f), x2: x(f), y1: 0, y2: H }));
  for (const db of [-12, -6, 6, 12]) grid.append(el('line', { x1: 0, x2: W, y1: y(db), y2: y(db) }));
  grid.append(el('line', { x1: 0, x2: W, y1: y(0), y2: y(0), class: 'zero' }));
  for (const [f, label] of [[100, '100'], [1000, '1k'], [10000, '10k']]) {
    const tx = el('text', { x: x(f) + 3, y: H - 4 });
    tx.textContent = label;
    grid.append(tx);
  }
  const area = el('path', { class: 'area' });
  const line = el('path', { class: 'line' });
  const dots = el('g');
  svg.append(grid, area, line, dots);
  let frame = 0;
  const draw = () => {
    frame = 0;
    const bs = bands(ch);
    let d = '';
    for (let i = 0; i <= 160; i++) {
      const f = 20 * Math.pow(1000, i / 160);
      d += (i ? 'L' : 'M') + x(f).toFixed(1) + ' ' + y(responseDb(bs, f)).toFixed(1);
    }
    line.setAttribute('d', d);
    area.setAttribute('d', d + `L${W} ${y(0)}L0 ${y(0)}Z`);
    dots.replaceChildren(...bs.filter((b) => b.type !== 'hpf').map((b) => el('circle', { cx: x(b.f), cy: y(responseDb(bs, b.f)), r: 5, style: 'fill:' + b.c })));
  };
  for (const p of PROPS) scope.bind(ch, p, () => { if (!frame) frame = requestAnimationFrame(draw); });
  return svg;
}
