// Level meters. Updates arrive many times per second; they are applied once
// per animation frame and drawn with transforms (no layout work).
import { h } from './dom.js';
import { t } from './i18n.js';
import { val } from './store.js';

let dbScale = false; // set once a negative reading shows the desk reports dBFS
const pending = new Map();
let frame = 0;

function flush() {
  frame = 0;
  for (const [el, fn] of pending) fn(el);
  pending.clear();
}
function schedule(el, fn) {
  pending.set(el, fn);
  if (!frame) frame = requestAnimationFrame(flush);
}

// meterNorm maps a reading to 0..1 over a 60 dB window. The desk's scale is
// not documented: negative values are taken as dBFS, otherwise as linear
// amplitude.
export function meterNorm(v) {
  if (typeof v !== 'number' || !isFinite(v)) return 0;
  if (v < 0) dbScale = true;
  if (dbScale) return Math.min(1, Math.max(0, (v + 60) / 60));
  if (v <= 0) return 0;
  return Math.min(1, Math.max(0, (20 * Math.log10(v) + 60) / 60));
}

export function vMeter(scope, path, side) {
  const cv = h('div', { class: 'cv' }); // covers what the level does not reach
  const pk = h('div', { class: 'pk' });
  const el = h('div', { class: 'meter', role: 'meter', 'aria-label': t('meter.side', { side }), 'aria-valuemin': 0, 'aria-valuemax': 100 }, h('div', { class: 'lvs' }), cv, pk);
  scope.bind(path, 'meterLevel' + side, (v) => schedule(cv, (e) => {
    const n = meterNorm(v);
    e.style.transform = `scaleY(${(1 - n).toFixed(3)})`;
    el.setAttribute('aria-valuenow', Math.round(n * 100));
  }));
  scope.bind(path, 'meterPeak' + side, (v) => schedule(pk, (e) => { e.style.transform = `translateY(${((1 - meterNorm(v)) * 100).toFixed(1)}%)`; }));
  return el;
}

export function hMeter(scope, path, text) {
  const bar = h('i');
  const upd = () => schedule(bar, (e) => {
    const n = Math.max(meterNorm(val(path, 'meterLevelL')), meterNorm(val(path, 'meterLevelR')));
    e.style.transform = `scaleX(${(1 - n).toFixed(3)})`; // the cover over the colour scale
  });
  scope.bind(path, 'meterLevelL', upd);
  scope.bind(path, 'meterLevelR', upd);
  return h('div', { class: 'hmeter' }, h('span', null, text), h('div', { class: 'hbar lvl', role: 'meter', 'aria-label': text }, bar));
}

// reductionDb turns a compressor's gain (1.0 = no reduction) into dB of
// reduction.
export function reductionDb(v) {
  if (typeof v !== 'number' || !isFinite(v) || v <= 0 || v >= 1) return 0;
  return -20 * Math.log10(v);
}

// grMeter shows a strip's gain reduction growing down from the top, over
// 0..20 dB.
export function grMeter(scope, path) {
  const lv = h('div', { class: 'lv' });
  const el = h('div', { class: 'meter gr', role: 'meter', 'aria-label': t('meter.reduction'), 'aria-valuemin': 0, 'aria-valuemax': 20 }, lv);
  scope.bind(path, 'meterLevelL', (v) => schedule(lv, (e) => {
    const db = reductionDb(v);
    e.style.transform = `scaleY(${Math.min(1, db / 20).toFixed(3)})`;
    el.setAttribute('aria-valuenow', db.toFixed(1));
    el.title = t('meter.reductionDb', { db: db.toFixed(1) });
  }));
  return el;
}

// hReduction is grMeter as a labelled bar.
export function hReduction(scope, path, text) {
  const bar = h('i');
  const out = h('span', { class: 'grdb' });
  scope.bind(path, 'meterLevelL', (v) => schedule(bar, (e) => {
    const db = reductionDb(v);
    e.style.transform = `scaleX(${Math.min(1, db / 20).toFixed(3)})`;
    out.textContent = db >= 0.1 ? `−${db.toFixed(1)} dB` : '';
  }));
  return h('div', { class: 'hmeter' }, h('span', null, text, ' ', out), h('div', { class: 'hbar gr', role: 'meter', 'aria-label': text }, bar));
}
