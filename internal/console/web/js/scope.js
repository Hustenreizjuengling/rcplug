// A scope owns everything one rendered view (or drawer) set up: live
// bindings, re-render triggers, timers and clean-up callbacks, plus the tree
// epoch it was built for. Writes use scope.epoch, so a control rendered for an
// older tree can never write through a shifted path.
import { store, val, bindKey, registerScope, unregisterScope } from './store.js';

export function createScope(onRerender) {
  const binds = new Map();
  const timers = new Set();
  const disposers = [];
  let rerenderQueued = false;
  const scope = {
    epoch: store.epoch,
    dead: false,
    bind(path, name, fn) {
      if (!path) return;
      const k = bindKey(path, name);
      if (!binds.has(k)) binds.set(k, new Set());
      binds.get(k).add(fn);
      fn(val(path, name));
    },
    // rerenderOn rebuilds the whole view when a structural property changes
    // (e.g. the source assigned to a strip).
    rerenderOn(path, name) {
      if (!path) return;
      const k = bindKey(path, name);
      if (!binds.has(k)) binds.set(k, new Set());
      binds.get(k).add(() => {
        if (rerenderQueued || !onRerender) return;
        rerenderQueued = true;
        requestAnimationFrame(() => { rerenderQueued = false; if (!scope.dead) onRerender(); });
      });
    },
    timeout(fn, ms) {
      const id = setTimeout(() => { timers.delete(id); if (!scope.dead) fn(); }, ms);
      timers.add(id);
      return id;
    },
    interval(fn, ms) {
      const id = setInterval(() => { if (!scope.dead) fn(); }, ms);
      timers.add(id);
      return id;
    },
    onDispose(fn) { disposers.push(fn); },
    // child returns a sub-scope that is disposed together with this one.
    child() {
      const c = createScope(onRerender);
      c.epoch = scope.epoch;
      disposers.push(() => c.dispose());
      return c;
    },
    dispose() {
      if (scope.dead) return;
      scope.dead = true;
      unregisterScope(scope);
      for (const id of timers) { clearTimeout(id); clearInterval(id); }
      for (const fn of disposers) { try { fn(); } catch (e) { console.error(e); } }
      binds.clear();
    },
    _fire(k, v) {
      const fns = binds.get(k);
      if (fns) for (const fn of fns) { try { fn(v); } catch (e) { console.error(e); } }
    },
  };
  registerScope(scope);
  return scope;
}
