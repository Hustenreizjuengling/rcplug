// Display: live picture of the desk's screen (opt-in) and the experimental
// front-panel emulation.
import { h } from '../dom.js';
import { t } from '../i18n.js';
import { request, setSettings, panel, report } from '../api.js';
import { confirmDialog } from '../dialogs.js';
import { controlLevel } from '../policy.js';

function screenCard(scope, hw) {
  const c = h('section', { class: 'card' }, h('h3', null, t('display.screen')));
  const sc = hw.screen || {};
  if (!sc.available && !sc.enabled) {
    c.append(h('p', { class: 'muted' }, t('display.unavailable')));
    return c;
  }
  if (!sc.enabled) {
    c.append(h('p', null, t('display.optIn')), h('button', { class: 'btn primary', type: 'button', onclick: async () => {
      if (await confirmDialog(t('display.optInConfirm'), t('display.enable'))) setSettings({ screen: true });
    } }, t('display.enable')));
    return c;
  }
  const st = sc.status || {};
  if (!st.available) {
    c.append(h('p', { class: 'muted' }, t('display.error', { err: st.error || '?' })), h('button', { class: 'btn', type: 'button', onclick: () => setSettings({ screen: false }) }, t('display.disable')));
    return c;
  }
  const src = 'api/screen.mjpg';
  const img = h('img', { class: 'screen', alt: t('display.alt'), src: src + '?t=' + Date.now() });
  // An <img> keeps streaming after it leaves the page; stop it explicitly, and
  // pause while the tab is hidden. After an error the image is gone for good
  // (the stream slots are limited), so it is not restarted.
  let failed = false;
  img.addEventListener('error', () => {
    failed = true;
    img.removeAttribute('src');
    img.replaceWith(h('p', { class: 'muted' }, t('display.streamLost')));
  });
  const onVis = () => {
    if (failed) return;
    if (document.hidden) img.removeAttribute('src'); else img.src = src + '?t=' + Date.now();
  };
  document.addEventListener('visibilitychange', onVis);
  scope.onDispose(() => { img.removeAttribute('src'); document.removeEventListener('visibilitychange', onVis); });
  c.querySelector('h3').append(h('span', { class: 'tag' }, `${st.width}×${st.height} · ${st.source === 'drm' ? 'DRM' : 'Framebuffer'} · ${st.fps} fps`));
  if (st.blank) c.append(h('div', { class: 'banner' }, t('display.blank', { detail: st.detail || '' })));
  c.append(img, h('div', { class: 'row', style: 'margin-top:8px' },
    h('a', { class: 'btn', href: 'api/screen.png', download: '' }, t('display.save')),
    h('button', { class: 'btn', type: 'button', onclick: () => setSettings({ screen: false }) }, t('display.disable')),
    h('span', { class: 'muted small' }, t('display.source', { detail: st.detail || st.source }))));
  return c;
}

function panelCard(hw) {
  const p = hw.panel || {};
  const c = h('section', { class: 'card' }, h('h3', null, t('panel.title'), p.enabled ? h('span', { class: 'tag amber' }, t('experimental')) : null));
  if (!p.enabled) { c.append(h('p', { class: 'muted' }, t('panel.disabled'))); return c; }
  if (p.error) c.append(h('div', { class: 'banner bad' }, p.error));
  const disabled = controlLevel() !== 'expert';
  if (disabled) c.append(h('div', { class: 'banner' }, t('panel.expertOnly')));
  const groups = {};
  for (const ctl of p.controls) (groups[ctl.group] = groups[ctl.group] || []).push(ctl);
  for (const [g, list] of Object.entries(groups)) {
    const grid = h('div', { class: 'panelgrid' });
    for (const ctl of list) {
      if (ctl.kind === 'button') grid.append(h('button', { class: 'btn', type: 'button', disabled, onclick: () => panel(ctl.id) }, ctl.label));
      else if (ctl.kind === 'wheel') grid.append(h('button', { class: 'btn', type: 'button', disabled, onclick: () => panel(ctl.id, -1) }, ctl.label + ' ◀'), h('button', { class: 'btn', type: 'button', disabled, onclick: () => panel(ctl.id, 1) }, ctl.label + ' ▶'));
      else if (ctl.kind === 'fader') {
        const inp = h('input', { type: 'range', min: 0, max: 127, value: 90, disabled, 'aria-label': ctl.label });
        inp.addEventListener('change', () => panel(ctl.id, Number(inp.value)));
        grid.append(h('label', { class: 'small muted' }, t('panel.faderLabel', { name: ctl.label }), inp));
      }
    }
    c.append(h('h4', { class: 'section-title' }, g), grid);
  }
  c.append(h('p', { class: 'muted small' }, t('panel.note')));
  return c;
}

export const display = {
  id: 'display',
  title: 'tab.display',
  render(scope) {
    const wrap = h('div', { class: 'grid cols-2' }, h('section', { class: 'card' }, h('p', { class: 'muted' }, t('loading'))));
    request('api/hw').then((hw) => {
      if (scope.dead) return;
      wrap.replaceChildren(screenCard(scope, hw), panelCard(hw));
    }).catch(report);
    return [wrap];
  },
};
