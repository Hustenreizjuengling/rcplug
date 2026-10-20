// The dashboard (cmd/rcplugd/web/app.js): German and English have the
// same keys, and every key the page uses literally exists
// (node --test tools/webtest).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const script = readFileSync(join(dirname(fileURLToPath(import.meta.url)), '../../cmd/rcplugd/web/app.js'), 'utf8');
const block = script.slice(script.indexOf('const T = {'), script.indexOf('\n};\n') + 3);
const T = new Function(block.replace('const T =', 'return'))();

test('de and en define the same keys', () => {
  assert.deepEqual(Object.keys(T.de).filter((k) => !(k in T.en)), [], 'German-only keys');
  assert.deepEqual(Object.keys(T.en).filter((k) => !(k in T.de)), [], 'English-only keys');
});

test('every literal key used in the page exists', () => {
  const used = new Set();
  for (const m of script.matchAll(/\bt\('([a-zA-Z0-9_]+)'(?!\s*\+)/g)) used.add(m[1]);
  // t(cond ? 'a' : 'b')
  for (const m of script.matchAll(/\bt\([^()]*\?\s*'([a-zA-Z0-9_]+)'\s*:\s*'([a-zA-Z0-9_]+)'/g)) { used.add(m[1]); used.add(m[2]); }
  for (const st of ['running', 'starting', 'backoff', 'stopped']) used.add('st_' + st); // t('st_' + state)
  assert.deepEqual([...used].filter((k) => !(k in T.en)), []);
});

test('placeholders match between the languages', () => {
  const ph = (s) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort().join(',');
  const bad = Object.keys(T.en).filter((k) => k in T.de && ph(T.en[k]) !== ph(T.de[k]));
  assert.deepEqual(bad, []);
});

test('sizes are named in one unit, GB from 1024 MB on', () => {
  const helpers = script.slice(script.indexOf('const mbUnit ='), script.indexOf('\nasync function refresh()'));
  const { mbLabel, mbPair } = new Function(helpers + '\nreturn { mbLabel, mbPair };')();
  assert.equal(mbLabel(259.4), '259 MB');
  assert.equal(mbLabel(1023), '1023 MB');
  assert.equal(mbLabel(1024), '1.0 GB');
  assert.equal(mbLabel(1536), '1.5 GB');
  assert.equal(mbPair(212, 471), '212 / 471 MB');
  assert.equal(mbPair(1434, 3686), '1.4 / 3.6 GB');
  // the whole decides the unit, even for a small part
  assert.equal(mbPair(51, 3686), '0.0 / 3.6 GB');
});
