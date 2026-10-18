// Routing: which source ends up in which output mix.
import { h } from '../dom.js';
import { t } from '../i18n.js';
import { store, famPath, run } from '../store.js';
import { selectCtl } from '../controls.js';
import { sourceLabel, outputLabel, outputs, assignedSources, faders } from '../device.js';
import { mixCell } from './parts.js';

const KEY = 'rcplug-routing-used';

function onlyUsed() {
  try { return localStorage.getItem(KEY) !== '0'; } catch (e) { return true; }
}

export const routing = {
  id: 'routing',
  title: 'tab.routing',
  render(scope, rerender) {
    const assigned = assignedSources();
    for (let i = 0; i < faders().length; i++) scope.rerenderOn(famPath('CHANNEL', i), 'channelInputSource');
    const used = onlyUsed();
    const head = h('tr', null, h('th', { class: 'src', scope: 'col' }, t('routing.corner')));
    outputs().forEach((_, o) => head.append(h('th', { scope: 'col' }, outputLabel(o))));
    const tb = h('tbody');
    for (let s = 0; s < store.L.sourceCount; s++) {
      if (used && !assigned.has(s)) continue;
      const tr = h('tr', { class: assigned.has(s) ? '' : 'unused' },
        h('th', { class: 'src', scope: 'row', title: assigned.has(s) ? t('routing.on', { strips: assigned.get(s).join(', ') }) : t('routing.unassigned') }, sourceLabel(s)));
      outputs().forEach((_, o) => tr.append(h('td', null, mixCell(scope, s, o))));
      tb.append(tr);
    }
    const filter = h('label', { class: 'row small muted' },
      h('input', { type: 'checkbox', checked: used, onchange: (e) => { try { localStorage.setItem(KEY, e.target.checked ? '1' : '0'); } catch (x) { /* ignore */ } rerender(); } }),
      t('routing.onlyUsed'));
    const mm = run('MIXMINUSES');
    const mmGrid = h('div', { class: 'grid cols-4' });
    for (let i = 0; i < (mm ? mm.count : 0); i++) {
      const p = famPath('MIXMINUSES', i);
      mmGrid.append(h('div', { class: 'card' }, selectCtl(scope, p, 'outputMixMinus', [0, 1, 2].map((v) => [v, t('mm.' + v)]), { label: outputLabel(i) })));
    }
    return [
      h('div', { class: 'row' }, h('p', { class: 'muted', style: 'margin:0' }, t('routing.intro')), h('span', { class: 'spacer' }), filter),
      h('div', { class: 'legend' }, h('span', null, '● ' + t('mix.linked')), h('span', null, '◐ ' + t('mix.unlinked')), h('span', null, 'M = ' + t('mix.muted')), h('span', null, t('routing.hatched'))),
      h('div', { class: 'matrix-wrap' }, h('table', { class: 'matrix' }, h('thead', null, head), tb)),
      h('h2', { class: 'section-title' }, t('routing.mixMinus')),
      mmGrid,
    ];
  },
};
