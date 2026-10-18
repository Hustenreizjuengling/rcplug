// Translation coverage: German and English have the same keys, and every key
// the console code uses literally exists (node --test tools/webtest).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const dir = join(dirname(fileURLToPath(import.meta.url)), '../../internal/console/web/js');
const de = (await import(join(dir, 'i18n/de.js'))).default;
const en = (await import(join(dir, 'i18n/en.js'))).default;

test('de and en define the same keys (except entity labels)', () => {
  const entity = (k) => /^(src|out)\./.test(k); // labels that fall back to the server's English names
  assert.deepEqual(Object.keys(de).filter((k) => !entity(k) && !(k in en)), [], 'German-only keys');
  assert.deepEqual(Object.keys(en).filter((k) => !(k in de)), [], 'English-only keys');
});

test('every literal key used in the code exists', () => {
  const files = [
    ...readdirSync(dir).filter((f) => f.endsWith('.js')).map((f) => join(dir, f)),
    ...readdirSync(join(dir, 'views')).map((f) => join(dir, 'views', f)),
  ];
  const missing = new Set();
  for (const f of files) {
    const src = readFileSync(f, 'utf8');
    // t('key') but not t('prefix' + x)
    for (const m of src.matchAll(/\bt\('([a-zA-Z0-9_.]+)'(?!\s*\+)/g)) if (!(m[1] in en)) missing.add(m[1]);
    // keys stored in tables: 'proc.x', 'p.x', 'fx.x', 'tab.x'
    for (const m of src.matchAll(/'((?:proc|p|fx|tab)\.[a-zA-Z]+)'/g)) if (!(m[1] in en)) missing.add(m[1]);
  }
  assert.deepEqual([...missing], []);
});
