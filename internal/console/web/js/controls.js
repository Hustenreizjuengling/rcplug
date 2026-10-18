// Bound controls: each reflects one property live and, when the control level
// allows it, writes it. All take the owning scope first.
import { h, pct, clamp, fmtValue, argbToCss } from './dom.js';
import { t } from './i18n.js';
import { val, nodeAt, typeOf } from './store.js';
import { canWrite } from './policy.js';
import { setProp } from './api.js';
import { confirmDialog } from './dialogs.js';

function activate(el, fn) {
  el.addEventListener('click', fn);
  el.addEventListener('keydown', (e) => { if (e.key === ' ' || e.key === 'Enter') { e.preventDefault(); fn(); } });
}

// toggle shows a bool property as a switch. opts.invert shows "on" for false
// (e.g. "active" for mixDisabled), opts.confirm asks first.
export function toggle(scope, path, name, text, cls = '', opts = {}) {
  const can = canWrite(path, name);
  const el = h('span', { class: 'tog ' + cls + (can ? '' : ' ro'), role: 'switch', tabindex: can ? 0 : -1, 'aria-disabled': can ? null : 'true', title: can ? (opts.title || '') : t('readonly.hint') },
    h('span', { class: 'ind', 'aria-hidden': 'true' }), text);
  scope.bind(path, name, (v) => {
    const on = opts.invert ? !v : !!v;
    el.classList.toggle('on', on);
    el.setAttribute('aria-checked', String(on));
  });
  if (can) {
    activate(el, async () => {
      if (opts.confirm && !(await confirmDialog(opts.confirm))) return;
      setProp(scope, path, name, !val(path, name));
    });
  }
  return el;
}

// chip is a compact toggle (processing badges on a strip).
export function chip(scope, path, name, text) {
  const can = canWrite(path, name);
  const el = h(can ? 'button' : 'span', { class: 'chip' + (can ? ' can' : ''), title: name, type: can ? 'button' : null }, text);
  scope.bind(path, name, (v) => { el.classList.toggle('on', !!v); if (can) el.setAttribute('aria-pressed', String(!!v)); });
  if (can) el.addEventListener('click', () => setProp(scope, path, name, !val(path, name)));
  return el;
}

// throttled sends at most every 90 ms while dragging, then the final value.
function throttled(send) {
  let last = 0, timer = 0, sent;
  const put = (v) => { last = performance.now(); sent = v; send(v); };
  return {
    push(v) { clearTimeout(timer); if (performance.now() - last > 90) put(v); else timer = setTimeout(() => put(v), 90); },
    end(v) { clearTimeout(timer); if (v !== sent) put(v); },
  };
}

// slider for a numeric property; opts: min, max, step, fmt, label, raw.
export function slider(scope, path, name, opts = {}) {
  const n = path && nodeAt(path);
  const ty = n && typeOf(n, name);
  const min = opts.min ?? 0, max = opts.max ?? (ty === 'd' ? 1 : 127);
  const step = opts.step ?? (ty === 'd' ? 0.001 : 1);
  const can = canWrite(path, name);
  const lab = opts.label || name;
  const inp = h('input', { type: 'range', min, max, step, disabled: !can, 'aria-label': lab });
  const out = h('span', { class: 'val' });
  const fmt = opts.fmt || ((v) => (ty === 'd' && max === 1 ? pct(v) : String(v)));
  let dragging = false;
  const fill = () => inp.style.setProperty('--p', ((inp.value - min) / (max - min || 1) * 100).toFixed(1) + '%');
  scope.bind(path, name, (v) => {
    if (typeof v !== 'number') { out.textContent = v === undefined ? '—' : String(v); return; }
    if (!dragging) { inp.value = v; fill(); }
    out.textContent = fmt(v);
    inp.setAttribute('aria-valuetext', fmt(v));
  });
  const parse = () => (ty === 'd' ? parseFloat(inp.value) : Math.round(parseFloat(inp.value)));
  const send = throttled((v) => setProp(scope, path, name, v));
  inp.addEventListener('input', () => { dragging = true; const v = parse(); out.textContent = fmt(v); fill(); send.push(v); });
  inp.addEventListener('change', () => { send.end(parse()); dragging = false; });
  inp.addEventListener('blur', () => { dragging = false; });
  return opts.raw ? { inp, out } : h('div', { class: 'ctlrow' }, h('label', { title: name }, lab), inp, out);
}

// vfader is a channel fader (0..127) with a unity mark at opts.unity.
export function vfader(scope, path, name, opts = {}) {
  const min = opts.min ?? 0, max = opts.max ?? 127;
  const can = canWrite(path, name);
  const fill = h('div', { class: 'fill' });
  const cap = h('div', { class: 'cap' });
  const el = h('div', {
    class: 'fader' + (can ? ' can' : ''), role: 'slider', tabindex: can ? 0 : -1,
    'aria-label': opts.label || name, 'aria-valuemin': min, 'aria-valuemax': max, 'aria-readonly': can ? null : 'true',
    title: can ? t('fader.dragHint') : t('fader.physicalHint'),
  }, h('div', { class: 'trk' }), fill,
  h('div', { class: 'unity', style: `bottom:calc(6px + (100% - 12px) * ${(opts.unity - min) / (max - min)})` }), cap);
  const out = h('span');
  let cur = 0, dragging = false;
  const fmt = opts.fmt || String;
  const show = (v) => {
    cur = v;
    const f = clamp((v - min) / (max - min), 0, 1);
    cap.style.bottom = `calc(6px + (100% - 12px) * ${f})`;
    fill.style.height = `calc((100% - 12px) * ${f})`;
    el.setAttribute('aria-valuenow', v);
    el.setAttribute('aria-valuetext', fmt(v));
    out.textContent = fmt(v);
  };
  scope.bind(path, name, (v) => { if (typeof v === 'number' && !dragging) show(v); });
  // follow: another property that carries the position (a physical fader's
  // level shows in its source's mix, see mixer.js).
  if (opts.follow) scope.bind(opts.follow.path, opts.follow.name, (v) => { const x = opts.follow.map(v); if (x != null && !dragging) show(x); });
  if (can) {
    const send = throttled((v) => setProp(scope, path, name, v));
    const fromY = (e) => {
      const r = el.getBoundingClientRect();
      return Math.round(min + clamp((r.bottom - 6 - e.clientY) / (r.height - 12), 0, 1) * (max - min));
    };
    el.addEventListener('pointerdown', (e) => { dragging = true; el.setPointerCapture(e.pointerId); const v = fromY(e); show(v); send.push(v); });
    el.addEventListener('pointermove', (e) => { if (!dragging) return; const v = fromY(e); if (v !== cur) { show(v); send.push(v); } });
    const end = () => { if (!dragging) return; dragging = false; send.end(cur); };
    el.addEventListener('pointerup', end);
    el.addEventListener('pointercancel', end);
    el.addEventListener('keydown', (e) => {
      const d = { ArrowUp: 1, ArrowRight: 1, ArrowDown: -1, ArrowLeft: -1, PageUp: 10, PageDown: -10 }[e.key];
      if (d === undefined) return;
      e.preventDefault();
      const v = clamp(cur + d, min, max);
      show(v);
      send.push(v); // throttled while a key auto-repeats; the final value goes out on keyup
    });
    el.addEventListener('keyup', () => send.end(cur));
    el.addEventListener('blur', () => send.end(cur));
  }
  return { el, out };
}

// selectCtl shows an enumerated property as a <select>.
export function selectCtl(scope, path, name, options, opts = {}) {
  const can = canWrite(path, name);
  const sel = h('select', { disabled: !can, title: name, 'aria-label': opts.label || name });
  for (const [v, text] of options) sel.append(h('option', { value: v }, text));
  scope.bind(path, name, (v) => {
    if (v !== undefined && ![...sel.options].some((o) => o.value === String(v))) sel.append(h('option', { value: v }, String(v)));
    sel.value = String(v);
  });
  sel.addEventListener('change', async () => {
    const ty = typeOf(nodeAt(path), name);
    const v = ty === 's' ? sel.value : Number(sel.value);
    if (opts.confirm && !(await confirmDialog(opts.confirm))) { sel.value = String(val(path, name)); return; }
    setProp(scope, path, name, v);
  });
  return opts.raw ? sel : h('div', { class: 'ctlrow' }, h('label', null, opts.label || name), sel, h('span'));
}

// stepper nudges an integer whose real range is unknown (gains): the server
// only accepts small steps for those.
export function stepper(scope, path, name, text, opts = {}) {
  const can = canWrite(path, name);
  const out = h('span', { class: 'val' });
  scope.bind(path, name, (v) => (out.textContent = v === undefined ? '—' : (opts.fmt ? opts.fmt(v) : String(v))));
  if (!can) return h('div', { class: 'ctlrow' }, h('label', { title: name }, text), h('span'), out);
  const mk = (d, sym) => h('button', {
    class: 'btn step', type: 'button', 'aria-label': `${text} ${d > 0 ? '+' : '−'}${Math.abs(d)}`,
    onclick: () => {
      let v = (Number(val(path, name)) || 0) + d;
      if (opts.min !== undefined) v = Math.max(opts.min, v);
      if (opts.max !== undefined) v = Math.min(opts.max, v);
      setProp(scope, path, name, v);
    },
  }, sym);
  return h('div', { class: 'ctlrow' }, h('label', { title: name }, text), h('div', { class: 'steps' }, mk(-5, '−5'), mk(-1, '−'), out, mk(1, '+'), mk(5, '+5')), h('span'));
}

// valueText shows a property; fmt may return text or a DOM node.
export function valueText(scope, path, name, fmt) {
  const el = h('span');
  scope.bind(path, name, (v) => {
    const out = v === undefined ? '—' : fmt ? fmt(v) : fmtValue(v);
    if (out instanceof Node) el.replaceChildren(out); else el.textContent = out;
  });
  return el;
}

// colourValue renders an ARGB colour property as a swatch with its code.
export function colourValue(v) {
  const css = argbToCss(v);
  if (!css) return v ? String(v) : '—';
  return h('span', { class: 'colour' }, h('span', { class: 'swatch', style: 'background:' + css }), h('span', { class: 'mono small muted' }, css));
}

export function card(title, ...body) { return h('section', { class: 'card' }, h('h3', null, title), ...body); }

// kv renders [label, path, prop, fmt?] rows, skipping properties the tree lacks.
export function kv(scope, rows) {
  const dl = h('dl', { class: 'kv' });
  for (const [text, path, name, fmt] of rows) {
    const n = path && nodeAt(path);
    if (!n || !(name in n.p)) continue;
    dl.append(h('dt', { title: name }, text), h('dd', null, valueText(scope, path, name, fmt)));
  }
  return dl;
}
