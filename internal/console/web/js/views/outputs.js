// Outputs & master: monitor, Bluetooth, headphones, master bus, ducking.
import { h, argbToCss } from '../dom.js';
import { t } from '../i18n.js';
import { single, run, famPath } from '../store.js';
import { toggle, slider, kv, card, valueText } from '../controls.js';

export const outputsView = {
  id: 'outputs',
  title: 'tab.outputs',
  render(scope) {
    const o = single('OUTPUT'), m = single('MASTERCHANNEL'), d = single('DUCKER'), sys = single('SYSTEM'), em = single('EMERGENCYMUTE');
    const cards = [];
    if (o) {
      cards.push(card(t('mon.title'), slider(scope, o, 'outputMonLevel', { label: t('mon.level') }),
        h('div', { class: 'row' }, toggle(scope, o, 'outputMonMute', t('mute'), 'mute'), toggle(scope, o, 'outputMonAutoMute', t('mon.autoMute')), toggle(scope, o, 'outputMonFixed', t('out.fixed'))),
        kv(scope, [[t('out.autoMuteActive'), o, 'outputMonAutoMuteActive']])));
      cards.push(card(t('out.bt'), slider(scope, o, 'outputBTLevel', { label: t('mon.level') }),
        h('div', { class: 'row' }, toggle(scope, o, 'outputBTMute', t('mute'), 'mute'), toggle(scope, o, 'outputBTAutoMute', t('mon.autoMute'))),
        kv(scope, [[t('out.autoMuteActive'), o, 'outputBTAutoMuteActive']])));
      cards.push(card(t('out.multitrack'), kv(scope, [[t('out.multiMode'), o, 'outputMultiMode'], [t('out.multiBypass'), o, 'outputMultiBypass'],
        [t('out.prefader'), o, 'outputPrefader'], [t('out.trackMode'), o, 'recordingMultitrackMode'], [t('out.compression'), o, 'recordingCompressionQuality'],
        [t('out.procBypass'), o, 'recordingProcessingBypass'], [t('out.hp1Level'), o, 'headphone1Level']])));
    }
    const hp = run('HEADPHONE');
    if (hp) {
      const g = h('div');
      for (let i = 0; i < hp.count; i++) {
        const p = famPath('HEADPHONE', i);
        const sw = h('span', { class: 'swatch' });
        scope.bind(p, 'headphoneColour', (v) => (sw.style.background = argbToCss(v) || 'var(--dim)'));
        g.append(h('div', { class: 'row', style: 'margin:4px 0' }, sw, h('b', null, t('out.headphone', { n: i + 1 })), h('span', { class: 'muted small' }, t('out.hpType'), ' ', valueText(scope, p, 'headphoneType'))));
      }
      if (sys) g.append(h('div', { class: 'row', style: 'margin-top:8px' }, toggle(scope, sys, 'disableAllHeadphoneOutputs', t('out.allHpOff'), 'mute'), toggle(scope, sys, 'disableAllLineoutOutputs', t('out.lineOff'), 'mute')));
      cards.push(card(t('out.headphones'), g));
    }
    if (m) {
      cards.push(card(t('out.master'), h('div', { class: 'row' }, toggle(scope, m, 'masterCompellorOn', 'Compellor'), toggle(scope, m, 'masterDelayOn', 'Delay')),
        slider(scope, m, 'masterCompellorThreshold', { label: t('p.threshold') }), slider(scope, m, 'masterCompellorAttack', { label: t('p.attack') }),
        slider(scope, m, 'masterCompellorRelease', { label: t('p.release') }), slider(scope, m, 'masterCompellorGain', { label: t('p.gain') }),
        slider(scope, m, 'masterDelaySeconds', { label: t('out.delay'), min: 0, max: 2, step: 0.01, fmt: (v) => v.toFixed(2) + ' s' })));
    }
    if (d) cards.push(card(t('out.ducking'), slider(scope, d, 'duckerDepth', { label: t('out.depth') })));
    if (em) cards.push(card(t('mon.emergency'), toggle(scope, em, 'emergencyMuteActive', t('out.allMuted'), 'mute', { confirm: t('mon.emergencyConfirm') })));
    return [h('div', { class: 'grid cols-3' }, cards)];
  },
};
