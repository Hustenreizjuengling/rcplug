// Building blocks shared by several views.
import { h, pct, fmtMs, fmtValue } from '../dom.js';
import { t } from '../i18n.js';
import { single, famPath, mixPath, nodeAt, typeOf, val } from '../store.js';
import { canWrite } from '../policy.js';
import { toggle, slider, stepper, kv, card, valueText, colourValue } from '../controls.js';
import { record, setProp, link } from '../api.js';
import { openDrawer, confirmDialog } from '../dialogs.js';
import { sourceLabel, outputLabel, sourceColour, isCombo, isCall, mixLevel, inputTypeText, recordStateText, outputs } from '../device.js';

export function recorderCard(scope) {
  const rp = single('RECORDER');
  if (!rp) return card(t('rec.title'), h('p', { class: 'muted' }, t('rec.none')));
  const dot = h('span', { class: 'recdot', 'aria-hidden': 'true' });
  const state = h('span');
  const time = h('span', { class: 'rectime', 'aria-live': 'off' }, '00:00');
  // The desk does not push the elapsed time continuously; count locally and
  // prefer the desk's value when it sends one.
  let since = null, base = 0, devMs = null;
  scope.bind(rp, 'recordState', (v) => {
    dot.className = 'recdot' + (v === 2 ? ' on' : v === 1 ? ' paused' : '');
    state.textContent = recordStateText(v);
    if (v === 2 && since === null) since = Date.now();
    if (v !== 2 && since !== null) { base += Date.now() - since; since = null; }
    if (v === 0) { base = 0; devMs = null; }
  });
  scope.bind(rp, 'recordTimeMs', (v) => { if (typeof v === 'number' && v > 0) devMs = v; });
  const tick = () => {
    const local = base + (since ? Date.now() - since : 0);
    time.textContent = fmtMs(devMs ? Math.max(devMs, local) : local);
  };
  tick();
  scope.interval(tick, 500);
  const can = canWrite(rp, 'requestRecordState');
  const btn = (text, v, hint) => h('button', { class: 'btn', type: 'button', disabled: !can, title: hint, onclick: () => record(scope, v) }, text);
  return card(t('rec.title'),
    h('div', { class: 'rec' }, dot, time, state),
    h('div', { class: 'row', style: 'margin-top:8px' },
      btn('● ' + t('rec.record'), 2, t('rec.recordHint')), btn('❚❚ ' + t('rec.pause'), 1), btn('■ ' + t('rec.stop'), 0),
      h('button', { class: 'btn', type: 'button', disabled: !canWrite(rp, 'requestDropMarker'), title: t('rec.markerHint'), onclick: () => setProp(scope, rp, 'requestDropMarker', Date.now()) }, '⚑ ' + t('rec.marker'))));
}

export function monitorCard(scope) {
  const o = single('OUTPUT');
  const em = single('EMERGENCYMUTE');
  return card(t('mon.title'),
    o ? slider(scope, o, 'outputMonLevel', { label: t('mon.level') }) : null,
    o ? h('div', { class: 'row' }, toggle(scope, o, 'outputMonMute', t('mute'), 'mute'), toggle(scope, o, 'outputMonAutoMute', t('mon.autoMute'))) : null,
    em ? h('div', { class: 'row', style: 'margin-top:8px' }, toggle(scope, em, 'emergencyMuteActive', t('mon.emergency'), 'mute', { confirm: t('mon.emergencyConfirm') })) : null);
}

export function phantomToggle(scope, p) {
  const can = canWrite(p, 'inputPower');
  const el = h('span', { class: 'tog' + (can ? '' : ' ro'), role: 'switch', tabindex: can ? 0 : -1, title: 'inputPower' }, h('span', { class: 'ind', 'aria-hidden': 'true' }), '48 V');
  scope.bind(p, 'inputPower', (v) => { el.classList.toggle('on', !!v); el.setAttribute('aria-checked', String(!!v)); });
  const act = async () => {
    const on = !!val(p, 'inputPower');
    if (!on && !(await confirmDialog(t('input.phantomConfirm'), t('input.phantomOn')))) return;
    setProp(scope, p, 'inputPower', on ? 0 : 1);
  };
  if (can) {
    el.addEventListener('click', act);
    el.addEventListener('keydown', (e) => { if (e.key === ' ' || e.key === 'Enter') { e.preventDefault(); act(); } });
  }
  return el;
}

export function inputCard(scope, s, tag) {
  const p = famPath('INPUTSOURCE', s);
  const colour = sourceColour(s);
  const head = h('h3', null, h('span', { class: 'swatch', style: 'background:' + (colour || 'var(--dim)') }), sourceLabel(s), tag || null);
  const c = h('section', { class: 'card' }, head);
  if (!p) return c;
  const combo = isCombo(s); // only combo inputs have a preamp
  if (combo) c.append(stepper(scope, p, 'inputMicrophoneGain', t('input.micGain'), { min: 0 }), stepper(scope, p, 'inputInstrumentGain', t('input.instGain'), { min: 0 }));
  if (combo || Number(val(p, 'inputDigitalGain'))) c.append(stepper(scope, p, 'inputDigitalGain', t('input.digitalGain')));
  c.append(h('div', { class: 'row', style: 'margin:6px 0' }, combo ? phantomToggle(scope, p) : null, toggle(scope, p, 'inputPhaseFlip', t('input.phase'))));
  const rows = [[t('input.type'), p, 'inputType', inputTypeText]];
  if (combo) rows.push([t('input.micType'), p, 'inputMicrophoneType']);
  rows.push([t('input.colour'), p, 'inputColour', colourValue]);
  if (val(p, 'inputWirelessSN')) rows.push([t('input.wireless'), p, 'inputWirelessSN']);
  if (isCall(s)) rows.push([t('input.callSlot'), p, 'inputSipCallSlot']);
  c.append(kv(scope, rows));
  return c;
}

export function mixCell(scope, s, o) {
  const p = mixPath(s, o);
  if (!p) return h('div', { class: 'cell' });
  const st = h('span', { class: 'st', 'aria-hidden': 'true' });
  const pc = h('span', { class: 'pct' });
  const lv = h('i', { class: 'lvl' });
  const el = h('button', { class: 'cell', type: 'button' }, st, pc, lv);
  const upd = () => {
    const dis = !!val(p, 'mixDisabled'), mute = !!val(p, 'mixMute'), linked = val(p, 'mixLink') !== false;
    const m = mixLevel(val(p, 'mixLevelWithAnchor'));
    el.classList.toggle('disabled', dis);
    el.classList.toggle('muted', mute && !dis);
    st.textContent = dis ? '–' : mute ? 'M' : linked ? '●' : '◐';
    const level = m ? m.level : 0;
    pc.textContent = dis ? '' : pct(level);
    lv.style.transform = `scaleX(${dis ? 0 : Math.max(0, Math.min(1, level))})`;
    const stateText = dis ? t('mix.disabled') : (mute ? t('mix.muted') + ', ' : '') + (linked ? t('mix.linked') : t('mix.unlinked')) + (m ? ' · ' + t('mix.levelAnchor', { level: pct(m.level), anchor: pct(m.anchor) }) : '');
    el.title = `${sourceLabel(s)} → ${outputLabel(o)}\n${stateText}`;
    el.setAttribute('aria-label', `${sourceLabel(s)} → ${outputLabel(o)}: ${stateText}`);
  };
  for (const n of ['mixDisabled', 'mixMute', 'mixLink', 'mixLevelWithAnchor']) scope.bind(p, n, upd);
  el.addEventListener('click', () => openDrawer(`${sourceLabel(s)} → ${outputLabel(o)}`, (ds) => mixDetails(ds, s, o)));
  return el;
}

function mixDetails(scope, s, o) {
  const p = mixPath(s, o);
  const body = h('div');
  body.append(h('div', { class: 'row' },
    toggle(scope, p, 'mixDisabled', t('mix.active'), '', { invert: true }),
    toggle(scope, p, 'mixMute', t('mix.mute'), 'mute'),
    h('span', { class: 'tag' }, valueText(scope, p, 'mixLink', (v) => (v === false ? t('mix.unlinked') : t('mix.linked'))))));
  body.append(h('div', { class: 'row', style: 'margin:10px 0' },
    h('button', { class: 'btn', type: 'button', disabled: !canWrite(p, 'mixLinkRequest'), onclick: () => link(scope, s, o, true, t('mix.linkRequested')) }, '⛓ ' + t('mix.link')),
    h('button', { class: 'btn', type: 'button', disabled: !canWrite(p, 'mixUnlinkRequest'), onclick: () => link(scope, s, o, false, t('mix.unlinkRequested')) }, t('mix.unlink'))));
  const can = canWrite(p, 'mixLevelWithAnchor');
  const part = (which, text) => {
    const inp = h('input', { type: 'range', min: 0, max: 1, step: 0.001, disabled: !can, 'aria-label': text });
    const out = h('span', { class: 'val' });
    scope.bind(p, 'mixLevelWithAnchor', (v) => {
      const m = mixLevel(v);
      if (!m) return;
      if (document.activeElement !== inp) { inp.value = m[which]; inp.style.setProperty('--p', pct(m[which])); }
      out.textContent = pct(m[which]);
    });
    inp.addEventListener('change', () => {
      const m = mixLevel(val(p, 'mixLevelWithAnchor')) || { anchor: 0, level: 0 };
      m[which] = parseFloat(inp.value);
      setProp(scope, p, 'mixLevelWithAnchor', `${m.anchor.toFixed(6)}|${m.level.toFixed(6)}`);
    });
    return h('div', { class: 'ctlrow' }, h('label', null, text), inp, out);
  };
  body.append(part('level', t('mix.level')), part('anchor', t('mix.anchor')), h('p', { class: 'muted small' }, t('mix.explain')));
  return body;
}

export function routingRow(scope, src) {
  const row = h('div', { class: 'outmeters' });
  outputs().forEach((_, o) => row.append(h('div', null, h('div', { class: 'small muted' }, outputLabel(o)), mixCell(scope, src, o))));
  return row;
}

const TYPE_NAMES = { i: 'int', l: 'int64', b: 'bool', d: 'double', s: 'text', a: 'array', x: 'binary', v: 'void', r: 'secret', u: '?' };

// propsTable lists properties of the node at path with live values; writable
// ones become inputs.
export function propsTable(scope, path, names) {
  const n = nodeAt(path);
  const table = h('table', { class: 'props' }, h('thead', null, h('tr', null, h('th', null, t('explorer.prop')), h('th', null, t('explorer.type')), h('th', null, t('explorer.value')))));
  const tb = h('tbody');
  for (const name of names) {
    const ty = typeOf(n, name);
    const tr = h('tr');
    const cell = h('td', { class: 'v' });
    const flash = () => cell.animate([{ background: 'var(--flash)' }, { background: 'transparent' }], { duration: 600 });
    const can = canWrite(path, name) && !['x', 'a', 'r'].includes(ty);
    if (can && ty === 'b') {
      cell.append(toggle(scope, path, name, ''));
    } else if (can) {
      const inp = h('input', { type: ty === 's' ? 'text' : 'number', step: ty === 'd' ? 'any' : '1', 'aria-label': name });
      scope.bind(path, name, (v) => { if (document.activeElement !== inp) inp.value = v ?? ''; flash(); });
      inp.addEventListener('change', () => {
        if (ty !== 's' && inp.value.trim() === '') { inp.value = val(path, name) ?? ''; return; } // empty is not 0
        setProp(scope, path, name, ty === 's' ? inp.value : Number(inp.value));
      });
      cell.append(inp);
    } else {
      scope.bind(path, name, (v) => { cell.textContent = ty === 'x' ? (v ? '0x' + v : t('empty')) : fmtValue(v); flash(); });
    }
    tr.append(h('td', { class: 'n' }, name), h('td', { class: 't', title: TYPE_NAMES[ty] }, ty), cell);
    tb.append(tr);
  }
  table.append(tb);
  return h('div', { class: 'tablewrap' }, table);
}
