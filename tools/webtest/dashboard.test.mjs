// The dashboard (cmd/rcplugd/web/index.html): German and English have the
// same keys, and every key the page uses literally exists
// (node --test tools/webtest).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const html = readFileSync(join(dirname(fileURLToPath(import.meta.url)), '../../cmd/rcplugd/web/index.html'), 'utf8');
const script = html.slice(html.indexOf('<script>') + 8, html.lastIndexOf('</script>'));
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
