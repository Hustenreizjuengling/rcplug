// Inputs: preamp, phantom power, phase and type per source.
import { h } from '../dom.js';
import { t } from '../i18n.js';
import { run } from '../store.js';
import { assignedSources, sourceIndexes } from '../device.js';
import { inputCard } from './parts.js';

export const inputs = {
  id: 'inputs',
  title: 'tab.inputs',
  render(scope) {
    const assigned = assignedSources();
    const grid = h('div', { class: 'grid cols-3' });
    const r = run('INPUTSOURCE');
    // The sources this model has (not the tree's spare nodes beyond them).
    for (const s of sourceIndexes()) {
      if (!r || s >= r.count) continue;
      grid.append(inputCard(scope, s, assigned.has(s) ? h('span', { class: 'tag on' }, assigned.get(s).join(', ')) : null));
    }
    return [h('p', { class: 'muted' }, t('inputs.intro')), grid];
  },
};
