// Display: live picture of the desk's screen (opt-in) and the experimental
// front-panel emulation.
import { h } from '../dom.js';
import { t } from '../i18n.js';
import { request, setSettings, panel, touch, report } from '../api.js';
import { confirmDialog } from '../dialogs.js';
import { controlLevel } from '../policy.js';

function screenCard(scope, hw, rerender) {
  const c = h('section', { class: 'card' }, h('h3', null, t('display.screen')));
  const sc = hw.screen || {};
  if (!sc.available && !sc.enabled) {
    c.append(h('p', { class: 'muted' }, t('display.unavailable')));
    return c;
  }
  if (!sc.enabled) {
    c.append(h('p', null, t('display.optIn')), h('button', { class: 'btn primary', type: 'button', onclick: async () => {
      if (await confirmDialog(t('display.optInConfirm'), t('display.enable')) && await setSettings({ screen: true })) rerender();
    } }, t('display.enable')));
    return c;
  }
  const st = sc.status || {};
  if (!st.available) {
    c.append(h('p', { class: 'muted' }, t('display.error', { err: st.error || '?' })), h('button', { class: 'btn', type: 'button', onclick: async () => { if (await setSettings({ screen: false })) rerender(); } }, t('display.disable')));
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
  // The Duo draws its screen into a portrait buffer; "auto" turns it.
  const rot = h('select', { 'aria-label': t('display.rotate') }, ...['auto', '0', '90', '180', '270'].map((v) =>
    h('option', { value: v, selected: (st.rotate || 'auto') === v }, v === 'auto' ? t('display.rotateAuto') : v + '°')));
  rot.addEventListener('change', async () => { if (await setSettings({ screenRotate: rot.value })) rerender(); });
  c.append(touchArea(img, hw.touch || {}, rerender), h('div', { class: 'row', style: 'margin-top:8px' },
    h('a', { class: 'btn', href: 'api/screen.png', download: '' }, t('display.save')),
    h('button', { class: 'btn', type: 'button', onclick: async () => { if (await setSettings({ screen: false })) rerender(); } }, t('display.disable')),
    h('label', { class: 'small muted' }, t('display.rotate'), ' ', rot),
    h('span', { class: 'muted small' }, t('display.source', { detail: st.detail || st.source }))));
  return c;
}

// touchArea: with screen control on, a click on the mirror taps the device's
// touchscreen at that spot and a drag swipes (expert level); otherwise the
// picture as it is, with the switch below it.
function touchArea(img, tc, rerender) {
  const wrap = h('div', { class: 'screenwrap' }, img);
  if (!tc.available) return wrap;
  if (!tc.enabled) {
    return h('div', null, wrap, h('div', { class: 'row', style: 'margin-top:8px' },
      h('button', { class: 'btn', type: 'button', onclick: async () => {
        if (await confirmDialog(t('display.touchConfirm'), t('display.touchEnable')) && await setSettings({ screenControl: true })) rerender();
      } }, t('display.touchEnable')), h('span', { class: 'tag amber' }, t('experimental'))));
  }
  const flip = h('select', { 'aria-label': t('display.touchFlip') }, ...[['', 'display.flipNone'], ['x', 'display.flipX'], ['y', 'display.flipY'], ['xy', 'display.flipXY']].map(([v, k]) =>
    h('option', { value: v, selected: (tc.flip || '') === v }, t(k))));
  flip.addEventListener('change', async () => { if (await setSettings({ touchFlip: flip.value })) rerender(); });
  const controls = h('div', { class: 'row', style: 'margin-top:8px' },
    h('span', { class: 'muted small' }, controlLevel() === 'expert' ? t('display.touchOn') : t('display.touchExpert')),
    h('label', { class: 'small muted' }, t('display.touchFlip'), ' ', flip),
    h('button', { class: 'btn small', type: 'button', onclick: async () => { if (await setSettings({ screenControl: false })) rerender(); } }, t('display.touchOff')));
  if (controlLevel() !== 'expert') return h('div', null, wrap, controls);
  wrap.classList.add('touch');
  const at = (e) => { const r = img.getBoundingClientRect(); return { x: Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)), y: Math.min(1, Math.max(0, (e.clientY - r.top) / r.height)), t: performance.now() }; };
  let start = null;
  const mark = (p) => {
    const dot = h('span', { class: 'tapdot', style: `left:${p.x * 100}%;top:${p.y * 100}%` });
    wrap.append(dot);
    setTimeout(() => dot.remove(), 600);
  };
  wrap.addEventListener('pointerdown', (e) => { e.preventDefault(); start = at(e); wrap.setPointerCapture(e.pointerId); });
  wrap.addEventListener('pointerup', (e) => {
    if (!start) return;
    const end = at(e), s = start;
    start = null;
    mark(end);
    if (Math.hypot(end.x - s.x, end.y - s.y) < 0.02) touch({ x: end.x, y: end.y });
    else touch({ x: s.x, y: s.y, toX: end.x, toY: end.y, ms: Math.round(Math.min(2000, Math.max(80, end.t - s.t))) });
  });
  return h('div', null, wrap, controls);
}

function panelCard(hw, rerender) {
  const p = hw.panel || {};
  const c = h('section', { class: 'card' }, h('h3', null, t('panel.title'), p.enabled ? h('span', { class: 'tag amber' }, t('experimental')) : null));
  if (!p.enabled) {
    c.append(h('p', { class: 'muted' }, t('panel.disabled')), h('button', { class: 'btn primary', type: 'button', onclick: async () => {
      if (await confirmDialog(t('panel.enableConfirm'), t('panel.enable')) && await setSettings({ panel: true })) rerender();
    } }, t('panel.enable')));
    return c;
  }
  if (p.error) c.append(h('div', { class: 'banner bad' }, p.error));
  const disabled = controlLevel() !== 'expert';
  if (disabled) c.append(h('div', { class: 'banner' }, t('panel.expertOnly')));
  const groups = {};
  for (const ctl of p.controls) (groups[ctl.group] = groups[ctl.group] || []).push(ctl);
  for (const [g, list] of Object.entries(groups)) {
    const grid = h('div', { class: 'panelgrid' });
    for (const ctl of list) {
      const label = panelLabel(ctl);
      if (ctl.kind === 'button') grid.append(h('button', { class: 'btn', type: 'button', disabled, onclick: () => panel(ctl.id) }, label));
      else if (ctl.kind === 'wheel') grid.append(h('button', { class: 'btn', type: 'button', disabled, onclick: () => panel(ctl.id, -1) }, label + ' ◀'), h('button', { class: 'btn', type: 'button', disabled, onclick: () => panel(ctl.id, 1) }, label + ' ▶'));
      else if (ctl.kind === 'fader') {
        const inp = h('input', { type: 'range', min: 0, max: 127, value: 90, disabled, 'aria-label': label });
        inp.addEventListener('change', () => panel(ctl.id, Number(inp.value)));
        grid.append(h('label', { class: 'small muted' }, t('panel.faderLabel', { name: label }), inp));
      }
    }
    c.append(h('h4', { class: 'section-title' }, panelGroup(g)), grid);
  }
  c.append(h('p', { class: 'muted small' }, t('panel.note')));
  if (!p.forced) c.append(h('button', { class: 'btn', type: 'button', onclick: async () => { if (await setSettings({ panel: false })) rerender(); } }, t('panel.disable')));
  return c;
}

export const display = {
  id: 'display',
  title: 'tab.display',
  noTree: true, // works without the device state: the panel and the screen do not need it
  render(scope, rerender) {
    const wrap = h('div', { class: 'grid cols-2' }, h('section', { class: 'card' }, h('p', { class: 'muted' }, t('loading'))));
    request('api/hw').then((hw) => {
      if (scope.dead) return;
      wrap.replaceChildren(screenCard(scope, hw, rerender), panelCard(hw, rerender));
    }).catch(report);
    return [wrap];
  },
};

// The server names the panel's controls in English; known ones are
// translated by their id, others keep the server's name.
function panelLabel(ctl) {
  const m = /^(listen|mute|settings|fader|pad)([1-6])$/.exec(ctl.id);
  if (m) return t('pc.' + m[1], { n: m[2], pos: t('pc.pos' + m[2]) });
  if (['pageLeft', 'pageRight', 'wheel', 'wheelPress'].includes(ctl.id)) return t('pc.' + ctl.id);
  return ctl.label;
}

function panelGroup(g) {
  const m = /^Channel (\d+)$/.exec(g);
  if (m) return t('pc.groupChannel', { n: m[1] });
  return { Navigation: t('pc.groupNavigation'), 'SMART Pads': t('pc.groupPads') }[g] || g;
}
