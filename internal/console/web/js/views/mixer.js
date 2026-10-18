// Mixer: recorder, monitor, device info, the channel strips and the level meters.
import { h } from '../dom.js';
import { t } from '../i18n.js';
import { store, single, famPath, val, runPath, mixPath } from '../store.js';
import { canWrite } from '../policy.js';
import { toggle, slider, vfader, selectCtl, chip, kv, card } from '../controls.js';
import { hMeter, vMeter, grMeter, hReduction } from '../meters.js';
import { openDrawer } from '../dialogs.js';
import { PROC, faders, faderLabel, faderDb, sourceLabel, sourceColour, sources, stripSource, unityFader, mixLevel } from '../device.js';
import { recorderCard, monitorCard, inputCard, routingRow } from './parts.js';
import { actionButtons } from '../plugins.js';

function showCard(scope) {
  const cs = single('CURRENTSHOW'), sys = single('SYSTEM'), b = single('BUILD');
  return card(t('mixer.device'), kv(scope, [
    [t('mixer.show'), cs, 'currentShowName'],
    [t('sys.name'), sys, 'systemName'],
    [t('sys.firmware'), sys, 'systemFirmwareVersion'],
    [t('sys.mixerVersion'), b, 'buildMixerVersion'],
    ['USB 1', sys, 'usb1Connected', (v) => (v ? t('connected') : t('disconnected'))],
  ]));
}

function quickCard(scope) {
  const d = single('DUCKER'), gui = single('GUI'), o = single('OUTPUT');
  return card(t('mixer.quick'),
    o ? slider(scope, o, 'outputBTLevel', { label: 'Bluetooth' }) : null,
    d ? slider(scope, d, 'duckerDepth', { label: t('out.ducking'), min: -60, max: 0, step: 1, fmt: (v) => Math.round(v) + ' dB' }) : null,
    gui ? slider(scope, gui, 'screenBrightness', { label: t('gui.screen'), min: 0, max: 255, step: 1 }) : null,
    gui ? slider(scope, gui, 'activeButtonsBrightness', { label: t('gui.buttons'), min: 0, max: 255, step: 1 }) : null);
}

// sourceLevel: the device reports a physical fader's movement only as the
// level of its source's mixes ("anchor|level", level = position / max), not
// as the FADER's faderLevel. The first linked mix carries it.
function sourceLevel(src, max) {
  if (src < 0) return null;
  let path = null;
  for (let o = 0; o < store.L.mixPerSource; o++) {
    const p = mixPath(src, o);
    if (p && val(p, 'mixLink') === true) { path = p; break; }
  }
  if (!path) return null;
  return { path, name: 'mixLevelWithAnchor', map: (v) => { const m = mixLevel(v); return m ? Math.round(m.level * max) : null; } };
}

export function sourceOptions() {
  return [[-1, t('mixer.noSource')], ...sources().map((s, i) => [i, sourceLabel(i)])];
}

function strip(scope, i) {
  const physical = i < store.L.physicalFaders;
  const ch = famPath('CHANNEL', i), fd = famPath('FADER', i);
  const src = stripSource(i);
  const colour = src >= 0 ? sourceColour(src) : null;
  const name = faderLabel(i);
  scope.rerenderOn(ch, 'channelInputSource');
  const el = h('section', { class: 'strip' + (physical ? '' : ' virtual') + (src < 0 ? ' emptysrc' : ''), 'aria-label': name });
  el.append(h('div', { class: 'sh' }, h('b', null, name), h('span', { class: 'spacer' }), h('span', { class: 'tag' }, physical ? t('mixer.physical') : t('mixer.virtual'))));
  el.append(h('div', { class: 'colorbar', style: colour ? 'background:' + colour : '' }));
  if (canWrite(ch, 'channelInputSource')) el.append(selectCtl(scope, ch, 'channelInputSource', sourceOptions(), { raw: true, label: t('mixer.source'), confirm: t('mixer.sourceConfirm') }));
  else el.append(h('div', { class: 'srcname', title: 'channelInputSource' }, src >= 0 ? sourceLabel(src) : t('mixer.empty')));
  const mp = runPath(store.L.stripMeters, i);
  const fz = h('div', { class: 'fz' });
  if (mp) {
    fz.append(vMeter(scope, mp, 'L'));
    if (val(mp, 'meterStereo') !== false) fz.append(vMeter(scope, mp, 'R'));
  }
  const gp = runPath(store.L.reductionMeters, i);
  if (gp) fz.append(grMeter(scope, gp));
  const fmax = Number(val(fd, 'faderMax')) || 127;
  const fs = vfader(scope, fd, 'faderLevel', { min: Number(val(fd, 'faderMin')) || 0, max: fmax, unity: unityFader(), label: name, fmt: (v) => `${v} · ≈${faderDb(v)} dB`, follow: physical ? sourceLevel(src, fmax) : null, hint: physical && store.meta.passive ? t('fader.panelHint') : null });
  fz.append(fs.el);
  el.append(fz, h('div', { class: 'db', title: t('mixer.faderHint', { max: fmax }) }, fs.out));
  el.append(h('div', { class: 'btns' }, toggle(scope, ch, 'channelOutputMute', t('mute'), 'mute'), toggle(scope, ch, 'channelCueEnable', t('listen'), 'cue')));
  const chips = h('div', { class: 'chips' });
  for (const p of PROC) if (p.chip && val(ch, p.on) !== undefined) chips.append(chip(scope, ch, p.on, p.chip));
  chips.append(chip(scope, ch, 'channelBypassProcessing', 'Bypass'));
  el.append(chips, h('button', { class: 'btn small', type: 'button', onclick: () => openChannel(i) }, t('details')));
  const acts = actionButtons('channel', channelVars(i));
  if (acts) el.append(acts);
  return el;
}

// channelVars are the placeholders plugin actions get for strip i.
export function channelVars(i) {
  const src = stripSource(i);
  return { strip: (faders()[i] || {}).id || 'strip' + i, index: i, source: src >= 0 ? (store.meta.sources[src] || {}).id || '' : '', label: src >= 0 ? sourceLabel(src) : faderLabel(i) };
}

export function openChannel(i) {
  const src = stripSource(i);
  openDrawer(faderLabel(i) + (src >= 0 ? ' · ' + sourceLabel(src) : ''), (scope) => {
    const ch = famPath('CHANNEL', i);
    scope.rerenderOn(ch, 'channelInputSource');
    const body = h('div');
    body.append(h('div', { class: 'row' }, toggle(scope, ch, 'channelOutputMute', t('mute'), 'mute'), toggle(scope, ch, 'channelCueEnable', t('listen'), 'cue'), toggle(scope, ch, 'channelBypassProcessing', t('proc.bypass'), 'mute'),
      actionButtons('channel', channelVars(i))));
    const grid = h('div', { class: 'grid cols-2', style: 'margin-top:12px' });
    for (const p of PROC) {
      const head = h('h3', null, t(p.title));
      if (p.on && val(ch, p.on) !== undefined) head.append(h('span', { class: 'spacer' }), toggle(scope, ch, p.on, t('on')));
      const c = h('section', { class: 'card' }, head);
      for (const [prop, key] of p.toggles || []) if (val(ch, prop) !== undefined) c.append(h('div', { class: 'row', style: 'margin:4px 0' }, toggle(scope, ch, prop, t(key))));
      for (const [prop, key] of p.params) if (val(ch, prop) !== undefined) c.append(slider(scope, ch, prop, { label: t(key) }));
      grid.append(c);
    }
    body.append(grid);
    const s = stripSource(i);
    if (s >= 0) body.append(h('h4', { class: 'section-title' }, t('mixer.inputOf', { src: sourceLabel(s) })), inputCard(scope, s), h('h4', { class: 'section-title' }, t('mixer.sendsOf')), routingRow(scope, s));
    body.append(h('p', { class: 'muted small' }, t('mixer.normalised')));
    return body;
  });
}

export const mixer = {
  id: 'mixer',
  title: 'tab.mixer',
  render(scope) {
    const out = [h('div', { class: 'grid cols-4' }, recorderCard(scope), monitorCard(scope), showCard(scope), quickCard(scope))];
    const strips = h('div', { class: 'strips' });
    for (let i = 0; i < faders().length; i++) strips.append(strip(scope, i));
    out.push(h('h2', { class: 'section-title' }, t('mixer.channels')), strips);
    const mm = store.L.masterMeter, fm = store.L.focusMeters;
    if (mm && mm.count) {
      const box = h('div', { class: 'outmeters' }, hMeter(scope, runPath(mm, 0), t('mixer.master')));
      const card = h('div', { class: 'card' }, box);
      if (fm && fm.count === 3) {
        box.append(hMeter(scope, runPath(fm, 0), t('mixer.focusIn')), hMeter(scope, runPath(fm, 1), t('mixer.focusOut')), hReduction(scope, runPath(fm, 2), t('mixer.focusReduction')));
        card.append(h('p', { class: 'muted small' }, t('mixer.focusNote')));
      }
      out.push(h('h2', { class: 'section-title' }, t('mixer.levels')), card);
    }
    if (store.L.physicalFaders) out.push(h('p', { class: 'muted small' }, t('mixer.physicalNote')));
    return out;
  },
};
