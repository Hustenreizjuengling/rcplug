// Recording: transport, storage, recordings, player, pad recorder.
import { h, pct, fmtMs, fmtSize } from '../dom.js';
import { t } from '../i18n.js';
import { single, run, runPath, val } from '../store.js';
import { kv, card, valueText } from '../controls.js';
import { recorderCard } from './parts.js';

function playerCard(scope) {
  const p = single('PLAYER');
  if (!p) return card(t('player.title'), h('p', { class: 'muted' }, '—'));
  const bar = h('i');
  scope.bind(p, 'playerProgress', (v) => (bar.style.transform = `scaleX(${Math.max(0, Math.min(1, Number(v) || 0))})`));
  return card(t('player.title'), h('div', { class: 'hbar big' }, bar), kv(scope, [[t('player.state'), p, 'playerState'], [t('player.file'), p, 'playerFilePath'],
    [t('player.position'), p, 'playerCurrentPositionTime', (v) => fmtMs(v * 1000)], [t('player.speed'), p, 'playerSpeed']]));
}

export const recorder = {
  id: 'recorder',
  title: 'tab.recorder',
  render(scope) {
    const out = [h('div', { class: 'grid cols-2' }, recorderCard(scope), playerCard(scope))];
    const vols = run('SYSTEM/STORAGEVOLUME');
    if (vols) {
      const g = h('div', { class: 'grid cols-3' });
      for (let i = 0; i < vols.count; i++) {
        const p = runPath(vols, i);
        const bar = h('i');
        const usage = h('span', { class: 'small muted' });
        const upd = () => {
          const cap = Number(val(p, 'storageVolumeCapacity')) || 0, free = Number(val(p, 'storageVolumeFree')) || 0;
          bar.style.transform = `scaleX(${cap ? (cap - free) / cap : 0})`;
          usage.textContent = cap ? t('storage.used', { used: fmtSize(cap - free), total: fmtSize(cap) }) : t('storage.unknown');
        };
        scope.bind(p, 'storageVolumeCapacity', upd);
        scope.bind(p, 'storageVolumeFree', upd);
        g.append(h('section', { class: 'card' },
          h('h3', null, valueText(scope, p, 'storageVolumeName', (v) => v || t('storage.n', { n: i + 1 })), h('span', { class: 'spacer' }), h('span', { class: 'tag' }, valueText(scope, p, 'storageVolumeRecDestination', (v) => (v ? t('storage.recDest') : t('storage.storage'))))),
          h('div', { class: 'hbar big' }, bar), usage,
          kv(scope, [[t('storage.inserted'), p, 'storageVolumeInserted'], [t('storage.mounted'), p, 'storageVolumeMounted'], [t('storage.formatted'), p, 'storageVolumeFormatted'], [t('storage.state'), p, 'storageVolumeState']])));
      }
      out.push(h('h2', { class: 'section-title' }, t('storage.title')), g);
    }
    const recs = single('RECORDINGS');
    const rr = run('RECORDINGS/RECORDING');
    if (recs) {
      const tb = h('tbody');
      for (let i = 0; i < (rr ? rr.count : 0); i++) {
        const p = runPath(rr, i);
        tb.append(h('tr', null, h('td', null, i + 1), h('td', { class: 'v' }, valueText(scope, p, 'recordingUID')), h('td', { class: 'v' }, valueText(scope, p, 'recordingContent'))));
      }
      out.push(h('h2', { class: 'section-title' }, t('recs.title')), h('div', { class: 'card' },
        kv(scope, [[t('recs.count'), recs, 'recordingTotalCount'], [t('recs.duration'), recs, 'recordingTotalDuration', (v) => fmtMs(v * 1000)]]),
        h('div', { class: 'tablewrap' }, h('table', { class: 'props' }, h('thead', null, h('tr', null, h('th', null, '#'), h('th', null, 'UID'), h('th', null, t('recs.content')))), tb))));
    }
    const pr = single('PADRECORDER');
    if (pr) out.push(h('h2', { class: 'section-title' }, t('padrec.title')), h('div', { class: 'card' }, kv(scope, [[t('padrec.state'), pr, 'padRecordState'], [t('padrec.pad'), pr, 'padRecordIdx'], [t('padrec.seconds'), pr, 'padRecordSeconds']])));
    return out;
  },
};
