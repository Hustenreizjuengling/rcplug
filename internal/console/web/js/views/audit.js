// Audit: every write the console sent to the desk.
import { h } from '../dom.js';
import { t, errorText } from '../i18n.js';
import { request, report } from '../api.js';

export const audit = {
  id: 'audit',
  title: 'tab.audit',
  render(scope) {
    const box = h('div', { class: 'card' }, h('p', { class: 'muted' }, t('loading')));
    request('api/audit').then((d) => {
      if (scope.dead) return;
      const w = (d.writes || []).slice().reverse();
      if (!w.length) { box.replaceChildren(h('p', { class: 'muted' }, t('audit.empty'))); return; }
      const tb = h('tbody');
      for (const e of w) {
        tb.append(h('tr', null, h('td', null, e.time), h('td', { class: 'n' }, e.node + ' [' + (Array.isArray(e.path) ? e.path.join(',') : '') + ']'), h('td', { class: 'n' }, e.name), h('td', { class: 'v' }, e.value),
          h('td', { class: 'small muted' }, e.remote), h('td', null, e.error ? h('span', { class: 'tag red', title: errorText({ code: e.code, message: e.error }) }, t('audit.refused')) : h('span', { class: 'tag on' }, 'ok'))));
      }
      box.replaceChildren(h('div', { class: 'tablewrap' }, h('table', { class: 'props' },
        h('thead', null, h('tr', null, ...['audit.time', 'audit.node', 'audit.prop', 'audit.value', 'audit.from', 'audit.result'].map((k) => h('th', null, t(k))))), tb)));
    }).catch(report);
    return [h('p', { class: 'muted' }, t('audit.intro')), box];
  },
};
