// SMART pads: banks of pads with names and colours; tap to trigger.
import { h, pct } from '../dom.js';
import { t } from '../i18n.js';
import { store, single, run, runPath, famPath, val, nodeAt } from '../store.js';
import { canWrite } from '../policy.js';
import { slider } from '../controls.js';
import { setProp, press, report } from '../api.js';
import { openDrawer } from '../dialogs.js';
import { PAD_COLOURS, padTypeText } from '../device.js';
import { propsTable } from './parts.js';

const KEY = 'rcplug-padbank';

function padEl(scope, bank, slot, p, rerender) {
  const per = store.L.padsPerBank || 6;
  if (!p) return h('div', { class: 'pad empty' }, h('div', { class: 'pn' }, t('pads.pad', { n: slot + 1 })), h('div', { class: 'pt' }, t('pads.unused')));
  const colour = PAD_COLOURS[Number(val(p, 'padColourIndex'))] || '#444';
  const name = val(p, 'padName') || t('pads.pad', { n: slot + 1 });
  const btnPath = famPath('PADBUTTON', slot);
  const gui = single('GUI');
  const can = btnPath && canWrite(btnPath, 'padButtonPressed');
  const prog = h('div', { class: 'prog' });
  const el = h(can ? 'button' : 'div', {
    class: 'pad' + (can ? ' can' : ''), type: can ? 'button' : null,
    style: `border-color:${colour};--pc:${colour};background:linear-gradient(160deg,${colour}33,var(--panel2))`,
    'aria-label': can ? t('pads.trigger', { name }) : name,
  }, h('div', { class: 'pn' }, name), h('div', { class: 'pt' }, padTypeText(val(p, 'padType')), ' · #', bank * per + slot + 1), prog);
  scope.bind(p, 'padActive', (v) => el.classList.toggle('active', !!v));
  scope.bind(p, 'padProgress', (v) => (prog.style.transform = `scaleX(${Math.max(0, Math.min(1, Number(v) || 0))})`));
  if (can) {
    el.addEventListener('click', async () => {
      if (bank !== Number(val(gui, 'selectedBank'))) {
        if (!canWrite(gui, 'selectedBank')) { report({ message: t(store.meta.passive ? 'pads.bankOnDevice' : 'pads.bankDenied', { n: bank + 1 }) }); return; }
        if (!(await setProp(scope, gui, 'selectedBank', bank))) return;
        await new Promise((r) => setTimeout(r, 120));
      }
      press(scope, btnPath, 'padButtonPressed', 150);
    });
  }
  const info = h('button', { class: 'padinfo', type: 'button', 'aria-label': t('pads.details', { name }), onclick: (e) => { e.stopPropagation(); padDrawer(p); } }, 'ⓘ');
  return h('div', { class: 'padwrap' }, el, info);
}

function padDrawer(p) {
  openDrawer(t('pads.title', { name: val(p, 'padName') || '#' + val(p, 'padIdx') }), (scope) =>
    h('div', null, slider(scope, p, 'padGain', { label: t('pads.gain'), min: -60, max: 0, step: 0.5, fmt: (v) => Number(v).toFixed(1) + ' dB' }), propsTable(scope, p, Object.keys(nodeAt(p).p))));
}

export const pads = {
  id: 'pads',
  title: 'tab.pads',
  render(scope, rerender) {
    const gui = single('GUI');
    const per = store.L.padsPerBank || 6, banks = store.L.padBanks || 8;
    scope.rerenderOn(gui, 'selectedBank');
    const cur = Number(val(gui, 'selectedBank')) || 0;
    let view = cur;
    try { const s = sessionStorage.getItem(KEY); if (s !== null) view = Number(s); } catch (e) { /* ignore */ }
    if (!(view >= 0 && view < banks)) view = cur;
    const byIdx = new Map();
    const r = run('SOUNDPADS/PAD');
    for (let i = 0; i < (r ? r.count : 0); i++) {
      const p = runPath(r, i);
      byIdx.set(Number(val(p, 'padIdx')), p);
      scope.rerenderOn(p, 'padIdx'); scope.rerenderOn(p, 'padName'); scope.rerenderOn(p, 'padColourIndex');
    }
    const bankBar = h('div', { class: 'banks', role: 'tablist' });
    for (let b = 0; b < banks; b++) {
      bankBar.append(h('button', { class: 'btn' + (b === view ? ' on' : ''), type: 'button', role: 'tab', 'aria-selected': String(b === view),
        onclick: () => { try { sessionStorage.setItem(KEY, b); } catch (e) { /* ignore */ } rerender(); } }, t('pads.bank', { n: b + 1 }) + (b === cur ? ' ●' : '')));
    }
    const grid = h('div', { class: 'pads' });
    for (let s = 0; s < per; s++) grid.append(padEl(scope, view, s, byIdx.get(view * per + s), rerender));
    const select = canWrite(gui, 'selectedBank') && view !== cur
      ? h('button', { class: 'btn', type: 'button', onclick: () => setProp(scope, gui, 'selectedBank', view) }, t('pads.selectBank', { n: view + 1 })) : null;
    return [h('div', { class: 'row' }, h('p', { class: 'muted', style: 'margin:0' }, t('pads.intro')), h('span', { class: 'spacer' }), select), bankBar, grid];
  },
};
