// What the desk's 0..1 processing values mean, read out of the device's own
// screen app (rc_gui_juce, firmware 1.7.6): its parameter classes turn a
// stored value v into the unit the screen shows, with a range per setting.
//
//   frequency, ratio   lo + (hi − lo) · v²          (FrequencyProperty, RatioProperty)
//   gain, threshold    lo + (hi − lo) · v, v in steps of 0.01 (GainProperty)
//   Q, others          lo + (hi − lo) · v           (ParameterProperty)
//   times, de-esser    lo · (hi / lo)^v             (Attack-, Release-, DeesserFrequencyProperty)
//   slope              6 … 24 dB/octave, rounded    (SlopeProperty)
//   gate range         0 … 100, = 100 · √(1 − v), rounded (GateRangeProperty)
const clamp = (v) => Math.min(1, Math.max(0, Number(v) || 0));
const quad = (lo, hi) => (v) => lo + (hi - lo) * clamp(v) ** 2;
const lin = (lo, hi) => (v) => lo + (hi - lo) * clamp(v);
const gain = (lo, hi) => (v) => lo + (hi - lo) * Math.round(clamp(v) * 100) / 100;
const expo = (lo, hi) => (v) => lo * Math.pow(hi / lo, clamp(v));

export const SCALES = {
  eqLowShelf: quad(20, 200), eqMidBell: quad(200, 2000), eqHighBell: quad(2000, 12000),
  eqLowGain: gain(-12, 12), eqMidGain: gain(-12, 12), eqHighGain: gain(-12, 12),
  eqLowQ: lin(0.1, 10), eqMidQ: lin(0.1, 10), eqHighQ: lin(0.1, 10),
  hpfFrequency: quad(20, 200), hpfSlope: (v) => Math.round(lin(6, 24)(v)),
  compressorThreshold: gain(-60, 0), compressorRatio: quad(1.5, 4.5), compressorGain: gain(0, 9),
  compressorAttack: expo(0.1, 10), compressorRelease: expo(5, 200),
  deesserThreshold: gain(-60, 0), deesserRatio: quad(1, 10), deesserGain: gain(0, 9),
  deesserAttack: expo(0.1, 10), deesserRelease: expo(50, 2000), deesserFrequency: expo(1000, 8000),
  noiseGateThreshold: gain(-60, 0), noiseGateRange: (v) => Math.round(100 * Math.sqrt(1 - clamp(v))),
  noiseGateAttack: expo(0.1, 1000), noiseGateHold: expo(50, 2000), noiseGateRelease: expo(50, 2000),
  aphexAETune: quad(600, 5000), aphexBBTune: quad(60, 312),
};

export const fmtHz = (f) => (f >= 1000 ? (f / 1000).toFixed(f >= 10000 ? 1 : 2) + ' kHz' : f.toFixed(f >= 100 ? 0 : 1) + ' Hz');
export const fmtDb = (db) => (db > 0.05 ? '+' : db < -0.05 ? '−' : '') + Math.abs(db).toFixed(1) + ' dB';
export const fmtMs = (ms) => (ms >= 1000 ? (ms / 1000).toFixed(2) + ' s' : ms.toFixed(ms >= 100 ? 0 : ms >= 10 ? 1 : 2) + ' ms');
const fmtRatio = (r) => r.toFixed(1) + ' : 1';

// FORMATS show a stored value in the device's units (perOctave: the word for
// dB/octave in the page's language).
export function formats(perOctave) {
  const f = {};
  const use = (names, fn) => { for (const n of names) f[n] = (v) => fn(SCALES[n](v)); };
  use(['eqLowShelf', 'eqMidBell', 'eqHighBell', 'hpfFrequency', 'deesserFrequency', 'aphexAETune', 'aphexBBTune'], fmtHz);
  use(['eqLowGain', 'eqMidGain', 'eqHighGain', 'compressorThreshold', 'compressorGain', 'deesserThreshold', 'deesserGain', 'noiseGateThreshold'], fmtDb);
  use(['eqLowQ', 'eqMidQ', 'eqHighQ'], (q) => q.toFixed(2));
  use(['compressorRatio', 'deesserRatio'], fmtRatio);
  use(['compressorAttack', 'compressorRelease', 'deesserAttack', 'deesserRelease', 'noiseGateAttack', 'noiseGateHold', 'noiseGateRelease'], fmtMs);
  f.hpfSlope = (v) => SCALES.hpfSlope(v) + ' ' + perOctave;
  f.noiseGateRange = (v) => '−' + SCALES.noiseGateRange(v) + ' dB';
  f.noiseGateHysteresis = (v) => (30 * Math.min(1, Math.max(0, Number(v) || 0))).toFixed(1) + ' dB'; // the gate closes this far below the threshold
  return f;
}
