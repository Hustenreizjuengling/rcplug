// Small DOM and formatting helpers shared by all modules.
import { t } from './i18n.js';

export const $ = (s, r = document) => r.querySelector(s);
export const $$ = (s, r = document) => Array.from(r.querySelectorAll(s));

// h('div', {class: 'x', onclick: fn}, ...children) — children may be nodes,
// strings, numbers, arrays, or null/false (skipped).
export function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (v === undefined || v === null || v === false) continue;
      if (k === 'class') el.className = v;
      else if (k === 'style') el.style.cssText = v;
      else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
      else if (v === true) el.setAttribute(k, '');
      else el.setAttribute(k, v);
    }
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid === null || kid === undefined || kid === false) continue;
    el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

// icon is an icon of the shared set (ui/icons.svg, sdk/ui).
const SVG = 'http://www.w3.org/2000/svg';
export function icon(id, cls = '') {
  const svg = document.createElementNS(SVG, 'svg');
  svg.setAttribute('class', 'i' + (cls ? ' ' + cls : ''));
  svg.setAttribute('aria-hidden', 'true');
  const use = document.createElementNS(SVG, 'use');
  use.setAttribute('href', 'ui/icons.svg#' + id);
  svg.append(use);
  return svg;
}

export const clamp = (x, a, b) => Math.min(b, Math.max(a, x));
export const pct = (x) => Math.round(clamp(x, 0, 1) * 100) + '%';

export function argbToCss(s) {
  if (typeof s !== 'string') return null;
  const m = s.trim().replace(/^#/, '').match(/^([0-9a-f]{2})?([0-9a-f]{6})$/i);
  return m ? '#' + m[2] : null;
}

export function fmtMs(ms) {
  const s = Math.max(0, Math.floor(ms / 1000));
  const hh = Math.floor(s / 3600), mm = Math.floor((s % 3600) / 60), ss = s % 60;
  return (hh ? hh + ':' : '') + String(mm).padStart(2, '0') + ':' + String(ss).padStart(2, '0');
}

export function fmtSize(mb) { return mb >= 1024 ? (mb / 1024).toFixed(1) + ' GB' : Math.round(mb) + ' MB'; }

export function fmtValue(v) {
  if (v === true) return t('yes');
  if (v === false) return t('no');
  if (v === null || v === undefined) return '—';
  if (typeof v === 'number') return Number.isInteger(v) ? String(v) : (Math.abs(v) <= 1 ? v.toFixed(3) : v.toFixed(2));
  if (v === '') return t('empty');
  return String(v);
}

// Toasts are de-duplicated: the same message within a few seconds bumps a
// counter instead of stacking up (e.g. failed writes during a fader drag).
const toasts = new Map();
export function toast(msg, kind = '') {
  const box = $('#toasts');
  const known = toasts.get(msg);
  if (known && document.body.contains(known.el)) {
    known.count++;
    known.el.dataset.count = '×' + known.count;
    clearTimeout(known.timer);
    known.timer = setTimeout(() => { known.el.remove(); toasts.delete(msg); }, 5000);
    return;
  }
  while (box.children.length >= 4) box.firstElementChild.remove();
  const el = h('div', { class: 'toast ' + kind, role: kind === 'err' ? 'alert' : 'status' }, msg);
  box.append(el);
  const entry = { el, count: 1, timer: setTimeout(() => { el.remove(); toasts.delete(msg); }, kind === 'err' ? 7000 : 3500) };
  toasts.set(msg, entry);
}
