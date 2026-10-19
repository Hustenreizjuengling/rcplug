// The console's processing scales match the device's screen app: its
// defaults (firmware 1.7.6) come out as round values (node --test tools/webtest).
import { test } from 'node:test';
import assert from 'node:assert/strict';

const { SCALES, formats } = await import('../../internal/console/web/js/scales.js');
const near = (got, want, what) => assert.ok(Math.abs(got - want) < Math.max(0.01, Math.abs(want) * 0.002), `${what}: ${got}, want ${want}`);

test('the screen app defaults are round values', () => {
  for (const [name, v, want] of [
    ['eqLowShelf', 2 / 3, 100], ['eqMidBell', 1 / 3, 400], ['eqHighBell', 0.89443, 10000],
    ['eqLowGain', 0.5, 0], ['eqLowQ', 2 / 99, 0.3], ['eqMidQ', 4 / 99, 0.5],
    ['hpfFrequency', 0.52705, 70], ['hpfSlope', 1 / 3, 12],
    // Gains are rounded to steps of 0.01 first, as on the device: its default
    // threshold 2/3 shows as −19.8 dB.
    ['compressorThreshold', 2 / 3, -19.8], ['compressorThreshold', 0.75, -15], ['compressorRatio', 0.40825, 2], ['compressorAttack', 0.42255, 0.7],
    ['compressorGain', 2 / 9, 1.98], ['deesserThreshold', 0.5, -30], ['deesserRatio', 0.4714, 3],
    ['noiseGateThreshold', 0.25, -45], ['noiseGateAttack', 0.61928, 30], ['noiseGateRelease', 0.3758, 200],
    ['aphexAETune', 0.81184, 3500], ['aphexBBTune', 0.34503, 90],
  ]) near(SCALES[name](v), want, name);
});

test('ranges end where the device ends them', () => {
  near(SCALES.eqHighBell(1), 12000, 'eqHighBell max');
  near(SCALES.compressorRatio(1), 4.5, 'compressorRatio max');
  near(SCALES.deesserFrequency(0), 1000, 'deesserFrequency min');
  near(SCALES.deesserFrequency(1), 8000, 'deesserFrequency max');
  assert.equal(SCALES.hpfSlope(1), 24);
  assert.equal(SCALES.noiseGateRange(1), 0);
  assert.equal(SCALES.eqLowShelf(-1), 20, 'clamped');
});

test('formats', () => {
  const f = formats('dB/oct');
  assert.equal(f.eqLowShelf(2 / 3), '100 Hz');
  assert.equal(f.eqHighBell(0.89443), '10.0 kHz');
  assert.equal(f.compressorRatio(0.40825), '2.0 : 1');
  assert.equal(f.compressorThreshold(2 / 3), '−19.8 dB');
  assert.equal(f.hpfSlope(2 / 3), '18 dB/oct');
  assert.equal(f.noiseGateRelease(0.3758), '200 ms');
});
