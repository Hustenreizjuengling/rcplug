// The server-sent event stream. EventSource retries network errors itself,
// but gives up for good after an HTTP error (e.g. 502 while the host restarts
// the plugin) — then we reconnect with backoff.
import { applyBatch, reload, store } from './store.js';

const state = { open: false, status: null };
let handlers = {};
let delay = 1000;

export const liveState = state;

export function connect(h) {
  handlers = h;
  open();
}

function open() {
  const es = new EventSource('api/events');
  es.onopen = () => { delay = 1000; state.open = true; handlers.onState && handlers.onState(); };
  es.addEventListener('hello', (e) => {
    const d = JSON.parse(e.data);
    state.status = d;
    handlers.onStatus && handlers.onStatus(d);
    // A tree loaded before we subscribed may miss changes made in between; a
    // different epoch or a lower seq means a new console process.
    if (!store.loaded || d.epoch !== store.epoch || d.seq !== store.seq) reload();
  });
  es.addEventListener('changes', (e) => applyBatch(JSON.parse(e.data)));
  es.addEventListener('resync', () => reload());
  es.addEventListener('status', (e) => {
    state.status = { ...(state.status || {}), connection: JSON.parse(e.data) };
    handlers.onStatus && handlers.onStatus(state.status);
  });
  es.addEventListener('control', (e) => {
    state.status = JSON.parse(e.data);
    handlers.onStatus && handlers.onStatus(state.status);
    reload(); // writable set and settings changed
  });
  es.onerror = () => {
    state.open = false;
    handlers.onState && handlers.onState();
    if (es.readyState === EventSource.CLOSED) {
      setTimeout(open, delay);
      delay = Math.min(delay * 2, 10000);
    }
  };
}
