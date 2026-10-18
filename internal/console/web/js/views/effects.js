// Voice effects per channel slot and for the SMART pads; FX presets.
import { h } from '../dom.js';
import { t } from '../i18n.js';
import { run, famPath, runPath, val } from '../store.js';
import { toggle, slider, kv, card } from '../controls.js';
import { FX, faderLabel, faders } from '../device.js';
import { openDrawer } from '../dialogs.js';

// fxCard is the compact overview of one effect slot: which effects are on.
// The parameters are in a drawer, so eleven slots stay readable.
function fxCard(scope, p, title, tag) {
  const chips = h('div', { class: 'row fxchips' });
  for (const [key, on] of FX) if (val(p, on) !== undefined) chips.append(toggle(scope, p, on, t(key)));
  return h('section', { class: 'card' },
    h('h3', null, title, tag ? h('span', { class: 'tag on' }, tag) : null),
    h('div', { class: 'row', style: 'margin-bottom:8px' }, toggle(scope, p, 'channelOutputMute', t('mute'), 'mute')),
    chips,
    h('button', { class: 'btn small', type: 'button', style: 'margin-top:10px', onclick: () => openDrawer(tag ? `${title} · ${tag}` : title, (ds) => fxDetails(ds, p)) }, t('details')));
}

function fxDetails(scope, p) {
  const box = h('div');
  for (const [key, on, params] of FX) {
    if (val(p, on) === undefined) continue;
    const sec = h('section', { class: 'fxsec' }, h('div', { class: 'row', style: 'margin:4px 0' }, toggle(scope, p, on, t(key))));
    for (const [prop, pkey, opts] of params) if (val(p, prop) !== undefined) sec.append(slider(scope, p, prop, { label: t(pkey), ...(opts || {}) }));
    box.append(sec);
  }
  return box;
}

export const effects = {
  id: 'effects',
  title: 'tab.effects',
  render(scope) {
    const r = run('EFFECTS_PARAMETERS');
    const grid = h('div', { class: 'grid cols-4' });
    for (let i = 0; i < (r ? r.count : 0); i++) {
      grid.append(fxCard(scope, famPath('EFFECTS_PARAMETERS', i), t('fx.slot', { n: i + 1 }), i < faders().length ? faderLabel(i) : null));
    }
    const out = [h('p', { class: 'muted' }, t('fx.intro')), grid];
    const pr = run('PADEFFECTS/EFFECTS_PARAMETERS');
    if (pr) {
      const pg = h('div', { class: 'grid cols-4' });
      for (let i = 0; i < pr.count; i++) pg.append(fxCard(scope, runPath(pr, i), t('fx.padSlot', { n: i + 1 })));
      out.push(h('h2', { class: 'section-title' }, t('fx.padTitle')), pg);
    }
    const fp = run('FXPRESETS/FXPRESET');
    if (fp) {
      const l = h('div', { class: 'grid cols-3' });
      for (let i = 0; i < fp.count; i++) {
        const p = runPath(fp, i);
        l.append(card(t('fx.preset', { n: i + 1 }), kv(scope, [[t('fx.index'), p, 'fxPresetIdx'], [t('fx.content'), p, 'fxPresetContents', (v) => (v ? t('fx.chars', { n: v.length }) : t('empty'))]])));
      }
      out.push(h('h2', { class: 'section-title' }, t('fx.presets')), l);
    }
    return out;
  },
};
