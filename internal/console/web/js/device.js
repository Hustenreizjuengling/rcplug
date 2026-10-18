// What the UI knows about the desk beyond the raw tree: labels, processing
// sections, enumerations. Counts and positions always come from the layout
// the server discovered (store.L).
import { store, famPath, val } from './store.js';
import { label, t } from './i18n.js';
import { argbToCss } from './dom.js';

export const PAD_COLOURS = ['#ff3b30', '#ff9500', '#ffcc00', '#34c759', '#00c7be', '#32ade6', '#007aff', '#5856d6', '#af52de', '#ff2d55', '#ffffff', '#8e8e93'];

export const sources = () => store.meta.sources.slice(0, store.L.sourceCount || store.meta.sources.length);
export const outputs = () => store.meta.outputs.slice(0, store.L.mixPerSource);
export const faders = () => store.meta.faders;
export const unityFader = () => store.meta.unityFader || 90;

export function sourceLabel(i) {
  const s = store.meta.sources[i];
  return s ? label('src', s.id, s.label) : '—';
}
export function outputLabel(o) {
  const s = store.meta.outputs[o];
  return s ? label('out', s.id, s.label) : t('output.n', { n: o + 1 });
}
export function faderLabel(i) {
  const f = store.meta.faders[i];
  if (!f) return '';
  const m = f.id.match(/^(physical|virtual)(\d+)$/);
  return m ? t('fader.' + m[1], { n: m[2] }) : f.label;
}

// Combo inputs (XLR/jack, sources 0–6) have a preamp; 16–18 are CallMe returns.
export const isCombo = (src) => src >= 0 && src <= 6;
export const isCall = (src) => src >= 16 && src <= 18;

export function stripSource(i) {
  const v = val(famPath('CHANNEL', i), 'channelInputSource');
  return typeof v === 'number' ? v : -1;
}
export function sourceColour(src) {
  return argbToCss(val(famPath('INPUTSOURCE', src), 'inputColour'));
}
// assignedSources maps source index -> labels of the strips it sits on.
export function assignedSources() {
  const m = new Map();
  for (let i = 0; i < faders().length; i++) {
    const s = stripSource(i);
    if (s >= 0) m.set(s, (m.get(s) || []).concat(faderLabel(i)));
  }
  return m;
}

// faderDb approximates the level of a fader position relative to the unity
// detent. The desk publishes no taper: a display aid, not a measurement.
export function faderDb(pos) {
  if (pos <= 0) return '−∞';
  const db = 20 * Math.log10(pos / unityFader());
  return (db >= 0 ? '+' : '−') + Math.abs(db).toFixed(1);
}

export function mixLevel(s) {
  if (typeof s !== 'string') return null;
  const [a, b] = s.split('|').map(Number);
  return isFinite(a) && isFinite(b) ? { anchor: a, level: b } : null;
}

// Channel processing sections: [property, translation key].
export const PROC = [
  { chip: 'APHEX', title: 'proc.aphex', on: 'aphexOn', params: [['aphexAEMix', 'p.aeMix'], ['aphexAETune', 'p.aeTune'], ['aphexBBDrive', 'p.bbDrive'], ['aphexBBTune', 'p.bbTune']] },
  { chip: 'Gate', title: 'proc.gate', on: 'noiseGateOn', params: [['noiseGateThreshold', 'p.threshold'], ['noiseGateRange', 'p.range'], ['noiseGateAttack', 'p.attack'], ['noiseGateHold', 'p.hold'], ['noiseGateRelease', 'p.release'], ['noiseGateHysteresis', 'p.hysteresis']] },
  { chip: 'HPF', title: 'proc.hpf', on: 'hpfOn', params: [['hpfFrequency', 'p.frequency'], ['hpfSlope', 'p.slope']], toggles: [['hpfLowerOn', 'p.stageLow'], ['hpfHigherOn', 'p.stageHigh']] },
  { chip: 'De-Ess', title: 'proc.deess', on: 'deesserOn', params: [['deesserThreshold', 'p.threshold'], ['deesserRatio', 'p.ratio'], ['deesserFrequency', 'p.frequency'], ['deesserAttack', 'p.attack'], ['deesserRelease', 'p.release'], ['deesserGain', 'p.gain']] },
  { chip: 'Comp', title: 'proc.comp', on: 'compressorOn', params: [['compressorThreshold', 'p.threshold'], ['compressorRatio', 'p.ratio'], ['compressorAttack', 'p.attack'], ['compressorRelease', 'p.release'], ['compressorGain', 'p.makeup']] },
  { chip: 'EQ', title: 'proc.eq', on: 'eqOn', params: [['eqLowGain', 'p.lowGain'], ['eqLowShelf', 'p.lowFreq'], ['eqLowQ', 'p.lowQ'], ['eqMidGain', 'p.midGain'], ['eqMidBell', 'p.midFreq'], ['eqMidQ', 'p.midQ'], ['eqHighGain', 'p.highGain'], ['eqHighBell', 'p.highFreq'], ['eqHighQ', 'p.highQ']], toggles: [['eqLowOn', 'p.low'], ['eqMidOn', 'p.mid'], ['eqHighOn', 'p.high']] },
  { title: 'proc.tone', params: [['channelDepth', 'p.depth'], ['channelSparkle', 'p.sparkle'], ['channelPunch', 'p.punch'], ['channelPan', 'p.pan']], toggles: [['channelPanOn', 'p.panOn'], ['channelAdvancedProcessing', 'p.advanced'], ['channelTalkbackEnable', 'p.talkback'], ['channelWirelessMute', 'p.wirelessMute']] },
];

// Voice effects: [title key, on-property, [[property, key, slider opts]]].
export const FX = [
  ['fx.reverb', 'reverbOn', [['reverbMix', 'p.mix'], ['reverbModel', 'p.model', { min: 0, max: 5, step: 1, fmt: (v) => String(v) }], ['reverbLowCut', 'p.lowCut'], ['reverbHighCut', 'p.highCut']]],
  ['fx.echo', 'echoOn', [['echoMix', 'p.mix'], ['echoDelay', 'p.time'], ['echoDecay', 'p.decay'], ['echoLowCut', 'p.lowCut'], ['echoHighCut', 'p.highCut']]],
  ['fx.pitch', 'pitchShiftOn', [['pitchShiftSemitones', 'p.semitones', { min: -1, max: 1, step: 0.01, fmt: (v) => v.toFixed(2) }]]],
  ['fx.distortion', 'distortionOn', [['distortionIntensity', 'p.intensity']]],
  ['fx.robot', 'robotOn', [['robotLevel', 'p.level'], ['robotMix', 'p.mix']]],
  ['fx.disguise', 'voiceDisguiseOn', []],
];

export const recordStateText = (v) => (v >= 0 && v <= 3 ? t('rec.state' + v) : t('rec.stateN', { n: v }));
export const inputTypeText = (v) => t('input.type' + v) !== 'input.type' + v ? t('input.type' + v) : String(v);
export const padTypeText = (v) => t('pad.type' + v) !== 'pad.type' + v ? t('pad.type' + v) : t('pad.typeN', { n: v });
