// Unit tests for the console UI's tree store (node --test tools/webtest).
import { test } from 'node:test';
import assert from 'node:assert/strict';

// A controllable fetch: each call returns a promise the test resolves.
const calls = [];
globalThis.fetch = (url) => new Promise((resolve) => calls.push({ url, resolve }));
const answer = (doc) => calls.shift().resolve({ ok: true, status: 200, json: async () => doc });

const tree = (epoch, seq, mute) => ({
  epoch, seq, stale: false,
  meta: { layout: { runs: { CHANNEL: { parent: [], first: 0, count: 1 } }, mixPerSource: 13, sourceCount: 19, physicalFaders: 4 } },
  root: { t: 'Rodecaster', p: {}, y: '', c: [{ t: 'CHANNEL', p: { channelOutputMute: mute }, y: 'b', c: [] }] },
});
const batch = (epoch, seq, mute) => ({ epoch, seq, ch: [[[0], 'channelOutputMute', mute, 'b']] });

const { store, reload, applyBatch, registerScope, val, on } = await import('../../internal/console/web/js/store.js');
const fired = [];
registerScope({ _fire: (k, v) => fired.push([k, v]) });
const settle = () => new Promise((r) => setTimeout(r, 0));

test('batches arriving while the tree loads are replayed, not lost', async () => {
  const p = reload();
  applyBatch(batch(1, 5, true)); // older than the snapshot: already contained
  applyBatch(batch(1, 12, true)); // newer: must survive the load
  answer(tree(1, 10, false));
  await p;
  assert.equal(val([0], 'channelOutputMute'), true);
  assert.equal(store.seq, 12);
  assert.ok(fired.some(([k, v]) => k === '0|channelOutputMute' && v === true));
});

test('old batches are ignored, new ones applied', async () => {
  applyBatch(batch(1, 11, false));
  assert.equal(val([0], 'channelOutputMute'), true);
  applyBatch(batch(1, 13, false));
  assert.equal(val([0], 'channelOutputMute'), false);
});

test('an epoch change reloads exactly once, even under a stream of batches', async () => {
  let reloads = 0;
  const off = on('reloaded', () => reloads++);
  for (let i = 0; i < 20; i++) applyBatch(batch(2, 20 + i, true)); // all for the new epoch
  await settle();
  assert.equal(calls.length, 1, 'one tree request in flight');
  answer(tree(2, 25, false));
  await settle(); await settle();
  // The buffered batches 26..39 of epoch 2 are replayed onto the new tree.
  assert.equal(store.epoch, 2);
  assert.equal(store.seq, 39);
  assert.equal(val([0], 'channelOutputMute'), true);
  assert.equal(reloads, 1);
  off();
});

test('a reload requested during a load runs once more afterwards', async () => {
  const p1 = reload();
  const p2 = reload();
  assert.equal(p1, p2, 'single flight');
  answer(tree(3, 50, false));
  await p1;
  await settle();
  assert.equal(calls.length, 1, 'follow-up load started');
  answer(tree(3, 51, true));
  await settle(); await settle();
  assert.equal(store.seq, 51);
});
