// The page frame: header (device, connection, control level, language),
// navigation generated from the view registry, and mounting views with a
// fresh scope whenever the tree is reloaded.
import { $, $$, h, toast } from './dom.js';
import { t, getLang, setLang } from './i18n.js';
import { store, on, single, val, isLoading } from './store.js';
import { setCapabilities, controlLevel } from './policy.js';
import { createScope } from './scope.js';
import { confirmDialog } from './dialogs.js';
import { setControl, reconnect, resync, request } from './api.js';
import { liveState } from './live.js';
import { views } from './views/index.js';

let current = null;
let scope = null;
let lastState = '';
let mounted = ''; // the view on the page while there is no device state
let lastError = null;

function tabFromHash() {
  const id = location.hash.replace(/^#\/?/, ''); // "#mixer" and "#/mixer" both work
  let saved = null;
  try { saved = localStorage.getItem('rcplug-tab'); } catch (e) { /* ignore */ }
  return views.find((v) => v.id === id) || views.find((v) => v.id === saved) || views[0];
}

export function render() {
  // Without the device state only views that do not need it (noTree) can
  // show something; the others say why there is nothing.
  if (!store.loaded && !current.noTree) {
    if (scope) scope.dispose();
    scope = null;
    mounted = '';
    $('#main').replaceChildren(...banners(), h('div', { class: 'empty' }, h('p', null, t('loading.none')), lastError ? h('p', { class: 'muted' }, lastError.message) : null));
    markTab();
    return;
  }
  if (store.loaded) setCapabilities(store.meta.control);
  mounted = store.loaded ? '' : current.id;
  if (scope) scope.dispose();
  scope = createScope(render);
  const main = $('#main');
  const y = window.scrollY;
  const focusId = document.activeElement && document.activeElement.id;
  let content;
  try {
    content = current.render(scope, render);
  } catch (e) {
    console.error(e);
    content = [h('div', { class: 'banner bad' }, t('render.error', { err: e.message }))];
  }
  main.replaceChildren(...banners(), ...content);
  window.scrollTo(0, y);
  if (focusId && document.getElementById(focusId)) document.getElementById(focusId).focus();
  markTab();
}

function markTab() {
  $$('#tabs a').forEach((a) => {
    const on = a.dataset.view === current.id;
    a.classList.toggle('on', on);
    if (on) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
  });
}

function banners() {
  const out = [];
  const st = liveState.status;
  const conn = st && st.connection;
  if (conn && conn.state !== 'live') out.push(h('div', { class: 'banner' + (conn.state === 'conflict' ? ' bad' : ''), role: 'status' }, connText(conn), store.loaded ? ' – ' + t('banner.lastKnown') : ''));
  // Reading along: the whole state comes only when the device's screen
  // connects; restarting the screen brings it.
  if (conn && conn.mode === 'tap' && conn.state === 'waiting') {
    out.push(h('div', { class: 'banner info' }, t('banner.tapWaiting'), ' ',
      h('button', { class: 'btn small', type: 'button', onclick: resyncNow }, t('banner.tapResync'))));
  } else if (conn && conn.mode === 'tap' && conn.state === 'live' && conn.code === 'resumed') {
    out.push(h('div', { class: 'banner info' }, t('banner.tapResumed'), ' ',
      h('button', { class: 'btn small', type: 'button', onclick: resyncNow }, t('banner.tapResync'))));
  } else if (conn && conn.mode === 'tap' && conn.state === 'live' && current.id !== 'system') {
    out.push(h('div', { class: 'banner info' }, t('banner.tapLive'), ' ', h('a', { href: '#system' }, t('banner.busyLink'))));
  }
  // Busy: the device's screen holds the mixer, but its buttons still work.
  if (conn && conn.code === 'desk_busy' && current.id !== 'system') out.push(h('div', { class: 'banner info' }, t('banner.busyHint'), ' ', h('a', { href: '#system' }, t('banner.busyLink'))));
  else if (store.stale) out.push(h('div', { class: 'banner', role: 'status' }, t('banner.lastKnown')));
  const limit = store.loaded ? store.meta.controlLimit : st && st.controlLimit;
  if (limit === 'untested_model' && store.loaded) out.push(h('div', { class: 'banner info' }, t('banner.untested', { model: store.L.modelName })));
  if (limit === 'read_only') out.push(h('div', { class: 'banner info' }, t('banner.readOnly')));
  return out;
}

export function connText(c) {
  switch (c.state) {
    case 'live': return c.mode === 'tap' ? t('state.liveTap') : c.mode === 'relay' ? t('state.liveRelay') : t('state.live');
    case 'waiting':
      if (c.code === 'tap_waiting') return t('state.tapWaiting');
      if (c.code === 'tap_lost') return t('state.tapLost');
      return t('state.waitingN', { n: Math.ceil(c.waitSeconds || 0) });
    case 'backoff': return t('state.backoff') + (c.nextRetrySeconds ? ' – ' + t('state.retryIn', { n: Math.ceil(c.nextRetrySeconds) }) : '') + (c.code ? ' (' + t('code.' + c.code) + ')' : '');
    case 'conflict': return t('state.conflict');
    default: return t('state.' + c.state);
  }
}

export function renderHeader() {
  const st = liveState.status;
  const conn = st && st.connection;
  const pill = $('#conn');
  const live = liveState.open && conn && conn.state === 'live';
  pill.className = 'conn ' + (!liveState.open ? 'bad' : !conn ? '' : live ? 'live' : conn.state === 'conflict' ? 'bad' : 'warn');
  // The banner explains a conflict at length; the pill stays short.
  $('.txt', pill).textContent = !liveState.open ? t('header.offline') : !conn ? '…' : live ? t(conn.mode === 'tap' ? 'header.liveTap' : conn.mode === 'relay' ? 'header.liveRelay' : 'header.live', { n: (conn.changesPerSecond || 0).toFixed(0) })
    : conn.state === 'conflict' ? t('code.guard_conflict') : connText(conn);
  // Without a live connection the meters would freeze at their last levels.
  document.body.classList.toggle('notlive', !live);
  $$('button', pill).forEach((b) => b.remove());
  if (conn && (conn.state === 'conflict' || conn.state === 'backoff')) pill.append(h('button', { class: 'btn small', type: 'button', onclick: reconnectNow }, t('conn.reconnect')));
  if (store.loaded) {
    $('#devName').textContent = val(single('SYSTEM'), 'systemName') || store.L.modelName;
    const show = val(single('CURRENTSHOW'), 'currentShowName');
    $('#devSub').textContent = [store.L.modelName, store.L.firmware && t('header.firmware', { v: store.L.firmware }), show && t('header.show', { name: show })].filter(Boolean).join(' · ');
  }
  // Without the device state the level comes from the status (the System
  // page's switches need it).
  if (!store.loaded && st && st.control !== controlLevel()) {
    setCapabilities({ level: st.control });
    if (current && current.noTree) render();
  }
  const level = st ? st.control : controlLevel();
  const locked = st && (st.readOnly || st.controlLimit);
  $$('.ctl button').forEach((b) => {
    const on = b.dataset.level === level;
    b.classList.toggle('on', on);
    b.setAttribute('aria-checked', String(on));
    b.disabled = !!locked && b.dataset.level !== 'off';
    if (locked) b.title = t(st.readOnly ? 'banner.readOnly' : 'banner.locked');
  });
  if (conn && conn.state !== lastState) {
    const was = lastState;
    lastState = conn.state;
    if (was && (was === 'live' || conn.state === 'live')) render();
  }
}

async function resyncNow() {
  if (!(await confirmDialog(t('banner.tapResyncConfirm'), t('banner.tapResync')))) return;
  resync(t('banner.tapResyncOk'));
}

async function reconnectNow() {
  const st = liveState.status && liveState.status.connection;
  if (st && st.state === 'conflict' && !(await confirmDialog(t('conn.conflictConfirm'), t('conn.reconnect')))) return;
  reconnect(t('conn.reconnecting'));
}

async function chooseLevel(lv) {
  if (lv === controlLevel()) return;
  if (lv === 'basic' && !(await confirmDialog(h('div', null, h('p', null, h('b', null, t('level.basicTitle'))), h('p', null, t('level.basicText'))), t('level.basicOk')))) return;
  if (lv === 'expert' && !(await confirmDialog(h('div', null, h('p', null, h('b', null, t('level.expertTitle'))), h('p', null, t('level.expertText'))), t('level.expertOk')))) return;
  if (await setControl(lv)) {
    toast(t('level.now.' + lv), 'ok');
    request('api/status').then((s) => { liveState.status = s; renderHeader(); }).catch(() => {});
  }
}

export function initShell() {
  const nav = $('#tabs');
  nav.replaceChildren(...views.map((v) => h('a', { href: '#' + v.id, 'data-view': v.id }, t(v.title))));
  $$('.ctl button').forEach((b) => {
    b.textContent = t('level.' + b.dataset.level);
    b.title = t('level.' + b.dataset.level + 'Hint');
    b.addEventListener('click', () => chooseLevel(b.dataset.level));
  });
  $('.ctl').setAttribute('aria-label', t('level.group'));
  const langBtn = $('#lang');
  langBtn.textContent = getLang() === 'de' ? 'EN' : 'DE';
  langBtn.title = t('header.language');
  langBtn.addEventListener('click', () => setLang(getLang() === 'de' ? 'en' : 'de'));
  document.title = t('page.title');
  // Inside the rcplug shell (an iframe) the shell already shows a header.
  if (window.self !== window.top) document.body.classList.add('embedded');
  const fixTabs = () => { $('#tabs').style.top = $('.top').offsetHeight + 'px'; };
  window.addEventListener('resize', fixTabs);
  fixTabs();

  current = tabFromHash();
  window.addEventListener('hashchange', () => {
    current = tabFromHash();
    try { localStorage.setItem('rcplug-tab', current.id); } catch (e) { /* ignore */ }
    render();
  });
  on('reloaded', () => { renderHeader(); render(); requestAnimationFrame(fixTabs); });
  on('error', (e) => {
    if (store.loaded) return;
    lastError = e;
    if (current.noTree && mounted === current.id) return; // keep it (and its screen stream) as it is
    render();
  });
  // Poll the status for the rate counter and retry countdowns.
  setInterval(() => {
    if (document.hidden || isLoading()) return;
    request('api/status').then((s) => { liveState.status = s; renderHeader(); }).catch(() => {});
  }, 4000);
}
