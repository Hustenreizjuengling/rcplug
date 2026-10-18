// Audit: every write the console sent to the desk, and the changes the
// device reported.
import { h } from '../dom.js';
import { t, errorText } from '../i18n.js';
import { request, report } from '../api.js';

export const audit = {
  id: 'audit',
  title: 'tab.audit',
  noTree: true,
  render(scope) {
    const box = h('div', { class: 'card' }, h('p', { class: 'muted' }, t('loading')));
    request('api/audit').then((d) => {
      if (scope.dead) return;
      const w = (d.writes || []).slice().reverse();
      if (!w.length) { box.replaceChildren(h('p', { class: 'muted' }, t('audit.empty'))); return; }
      const tb = h('tbody');
      for (const e of w) {
        tb.append(h('tr', null, h('td', null, e.time), h('td', { class: 'n' }, e.node + (Array.isArray(e.path) && e.path.length ? ' [' + e.path.join(',') + ']' : '')), h('td', { class: 'n' }, e.name), h('td', { class: 'v' }, e.value),
          h('td', { class: 'small muted' }, e.remote), h('td', null, e.error ? h('span', { class: 'tag red', title: errorText({ code: e.code, message: e.error }) }, t('audit.refused')) : h('span', { class: 'tag on' }, 'ok'))));
      }
      box.replaceChildren(h('div', { class: 'tablewrap' }, h('table', { class: 'props' },
        h('thead', null, h('tr', null, ...['audit.time', 'audit.node', 'audit.prop', 'audit.value', 'audit.from', 'audit.result'].map((k) => h('th', null, t(k))))), tb)));
    }).catch(report);
    return [h('p', { class: 'muted' }, t('audit.intro')), box, changesSection(scope)];
  },
};

// changesSection lists what the device reported, with the value before and
// after: change a setting on the device's screen to see its numbers.
function changesSection(scope) {
  const list = h('div', { class: 'card' }, h('p', { class: 'muted' }, t('loading')));
  let filter = '';
  let last = '';
  const show = (v) => (v === null || v === undefined ? '—' : typeof v === 'string' ? JSON.stringify(v) : String(v));
  const load = () => request('api/changes').then((d) => {
    if (scope.dead) return;
    const all = (d.changes || []).slice().reverse();
    const f = filter.toLowerCase();
    const rows = f ? all.filter((e) => (e.node + ' ' + e.name).toLowerCase().includes(f)) : all;
    const sig = JSON.stringify([rows.length, rows[0], f, d.dropped]);
    if (sig === last) return;
    last = sig;
    if (!rows.length) { list.replaceChildren(h('p', { class: 'muted' }, t(all.length ? 'changes.noMatch' : 'changes.empty'))); return; }
    const tb = h('tbody');
    for (const e of rows) {
      tb.append(h('tr', null, h('td', { class: 'small' }, e.time),
        h('td', { class: 'n' }, e.node + (e.index >= 0 ? ' #' + e.index : '')),
        h('td', { class: 'n' }, e.name), h('td', { class: 'v' }, show(e.old), ' → ', h('b', null, show(e.new)))));
    }
    list.replaceChildren(h('div', { class: 'tablewrap' }, h('table', { class: 'props' },
      h('thead', null, h('tr', null, ...['audit.time', 'audit.node', 'audit.prop', 'changes.values'].map((k) => h('th', null, t(k))))), tb)),
      d.dropped ? h('p', { class: 'small muted' }, t('changes.dropped', { n: d.dropped })) : null);
  }).catch(() => { /* the next round tries again */ });
  load();
  scope.interval(load, 2000);
  const search = h('input', { type: 'search', placeholder: t('changes.filter'), 'aria-label': t('changes.filter'), oninput: (e) => { filter = e.target.value; load(); } });
  const clear = h('button', { class: 'btn small', type: 'button', onclick: () => request('api/changes/clear', {}).then(load).catch(report) }, t('changes.clear'));
  return h('section', null, h('h2', { class: 'section-title' }, t('changes.title')), h('p', { class: 'muted' }, t('changes.intro')),
    h('div', { class: 'row', style: 'margin-bottom:8px' }, search, h('span', { class: 'spacer' }), clear), list);
}
