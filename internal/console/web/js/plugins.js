// Actions of other plugins ("actions" in their plugin.json), read from the
// host's plugin list. A plugin can offer, for example, a button on every
// channel strip that opens it for that channel. Without the host (console
// run standalone) there are none.
import { h } from './dom.js';
import { getLang } from './i18n.js';

const self = (location.pathname.match(/\/plugins\/([^/]+)\//) || [])[1] || null;
let actions = [];
let changed = () => {};

// onActionsChanged registers the callback that re-renders the UI.
export function onActionsChanged(fn) { changed = fn; }

export async function loadActions() {
  if (!self) return;
  let next = [];
  try {
    const r = await fetch('../../api/plugins', { cache: 'no-store' });
    if (!r.ok) return;
    const d = await r.json();
    for (const p of d.plugins || []) {
      if (!p.running || p.name === self) continue;
      for (const a of p.actions || []) next.push({ ...a, plugin: p.name, pluginIcon: p.icon || '' });
    }
  } catch (e) {
    return; // keep what we have
  }
  const same = JSON.stringify(next) === JSON.stringify(actions);
  actions = next;
  if (!same) changed();
}

export function startActions() {
  loadActions();
  setInterval(loadActions, 30000);
}

export const actionsFor = (context) => actions.filter((a) => a.context === context);

const title = (a) => (a.translations && a.translations[getLang()] && a.translations[getLang()].title) || a.title;

// openAction shows the plugin with the action's path, filled with vars. Inside
// the dashboard the dashboard switches to the plugin; standalone the browser
// navigates there.
export function openAction(a, vars) {
  const path = (a.path || '').replace(/\{(\w+)\}/g, (m, k) => (k in vars ? encodeURIComponent(String(vars[k])) : ''));
  const plugin = encodeURIComponent(a.plugin);
  try {
    if (window.top !== window.self && window.top.location.origin === location.origin) {
      window.top.location.hash = '#/p/' + plugin + (path ? (path.startsWith('?') ? path : '/' + path) : '');
      return;
    }
  } catch (e) { /* not our dashboard */ }
  location.href = '../' + plugin + '/' + path;
}

// actionButtons renders the actions of a context as small buttons.
export function actionButtons(context, vars) {
  const list = actionsFor(context);
  if (!list.length) return null;
  return h('div', { class: 'actions' }, ...list.map((a) => h('button', {
    class: 'btn small action', type: 'button', title: title(a), onclick: () => openAction(a, vars),
  }, (a.icon || a.pluginIcon) ? (a.icon || a.pluginIcon) + ' ' : '', title(a))));
}
