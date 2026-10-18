// The local copy of the desk's state tree and its change feed.
//
// The tree comes from GET api/tree ({epoch, seq, stale, meta, root}); nodes
// are {t: type, p: {prop: value}, y: "type letters", c: [children]} and are
// addressed by index paths from the root, exactly as on the wire. Change
// batches from SSE carry the server's epoch and seq:
//  - a batch for another epoch means paths moved: the tree is reloaded;
//  - a batch with seq <= the tree's seq is already contained in it.
// Only one load runs at a time; batches that arrive meanwhile are buffered
// and replayed onto the new tree, so no update is lost or applied twice.

export const store = { epoch: 0, seq: 0, root: null, meta: null, L: null, stale: false, loaded: false };

const scopes = new Set();
const listeners = new Map();

export function on(event, fn) {
  if (!listeners.has(event)) listeners.set(event, new Set());
  listeners.get(event).add(fn);
  return () => listeners.get(event).delete(fn);
}
function emit(event, arg) {
  for (const fn of listeners.get(event) || []) {
    try { fn(arg); } catch (e) { console.error(e); }
  }
}

// --- tree access
export function nodeAt(path) {
  let n = store.root;
  for (const i of path || []) { if (!n || !n.c) return null; n = n.c[i]; }
  return n || null;
}
export function typeOf(n, name) {
  if (!n._ty) { n._ty = {}; Object.keys(n.p).forEach((k, i) => (n._ty[k] = n.y[i])); }
  return n._ty[name];
}
export function val(path, name) { const n = path && nodeAt(path); return n ? n.p[name] : undefined; }
export function run(fam) { return store.L && store.L.runs[fam]; }
export function famPath(fam, i) {
  const r = run(fam);
  if (!r || i < 0 || i >= r.count) return null;
  return [...r.parent, r.first + i];
}
export function famIndex(fam, path) {
  const r = run(fam);
  if (!r || !path || path.length !== r.parent.length + 1) return -1;
  for (let k = 0; k < r.parent.length; k++) if (path[k] !== r.parent[k]) return -1;
  const i = path[path.length - 1] - r.first;
  return i >= 0 && i < r.count ? i : -1;
}
export const single = (fam) => famPath(fam, 0);
export function mixPath(src, out) {
  const per = store.L.mixPerSource;
  return src < store.L.sourceCount && out < per ? famPath('MIX', src * per + out) : null;
}
export function runPath(r, i) { return r && i >= 0 && i < r.count ? [...r.parent, r.first + i] : null; }

// --- change delivery to view scopes
export const bindKey = (path, name) => path.join('.') + '|' + name;
export function registerScope(s) { scopes.add(s); }
export function unregisterScope(s) { scopes.delete(s); }
function fire(path, name, v) {
  const k = bindKey(path, name);
  for (const s of scopes) s._fire(k, v);
}

// applyLocal records the server's answer to our own write (the desk does not
// echo a client's writes).
export function applyLocal(path, name, value) {
  const n = nodeAt(path);
  if (n && name in n.p) { n.p[name] = value; fire(path, name, value); }
}
// refresh re-delivers the current value (e.g. after a refused write).
export function refresh(path, name) { fire(path, name, val(path, name)); }

// --- loading
let loading = null;
let again = false;
let buffer = [];
let retryDelay = 1000;
let retryTimer = null;

export function reload() {
  if (loading) { again = true; return loading; }
  clearTimeout(retryTimer);
  loading = (async () => {
    try {
      const r = await fetch('api/tree', { cache: 'no-store' });
      let d = null;
      try { d = await r.json(); } catch (e) { /* not JSON */ }
      if (!r.ok) throw Object.assign(new Error((d && d.error) || 'HTTP ' + r.status), { code: d && d.code });
      install(d);
      retryDelay = 1000;
    } catch (e) {
      emit('error', e);
      retryTimer = setTimeout(reload, retryDelay);
      retryDelay = Math.min(retryDelay * 2, 10000);
    } finally {
      loading = null;
      if (again) { again = false; reload(); }
    }
  })();
  return loading;
}

function install(d) {
  const epochChanged = d.epoch !== store.epoch || !store.loaded;
  Object.assign(store, { epoch: d.epoch, seq: d.seq, root: d.root, meta: d.meta, L: d.meta.layout, stale: !!d.stale, loaded: true });
  const pending = buffer;
  buffer = [];
  emit('reloaded', { epochChanged });
  for (const b of pending) applyNow(b);
}

export function isLoading() { return !!loading; }

export function applyBatch(d) {
  if (loading || !store.loaded) { buffer.push(d); return; }
  applyNow(d);
}

function applyNow(d) {
  if (d.epoch !== store.epoch) { reload(); return; }
  if (d.seq <= store.seq) return;
  store.seq = d.seq;
  for (const [path, name, value, ty] of d.ch) {
    const n = nodeAt(path);
    if (!n) continue;
    if (!(name in n.p)) { n.y += ty; n._ty = null; }
    n.p[name] = value;
    fire(path, name, value);
  }
}
