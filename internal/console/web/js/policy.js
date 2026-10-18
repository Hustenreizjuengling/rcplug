// Which properties the current control level lets this UI write. The server
// sends the exact list (meta.control.writable) and enforces it again on every
// write; this only decides which controls are shown as live.
import { store, nodeAt, typeOf, famIndex } from './store.js';

let level = 'off';
let writable = {};

export function setCapabilities(control) {
  level = (control && control.level) || 'off';
  writable = {};
  for (const [type, props] of Object.entries((control && control.writable) || {})) writable[type] = new Set(props);
}

export const controlLevel = () => level;

export function canWrite(path, name) {
  if (level === 'off' || !path || store.stale) return false;
  const n = nodeAt(path);
  if (!n || !(name in n.p) || typeOf(n, name) === 'r') return false;
  if (!writable[n.t] || !writable[n.t].has(name)) return false;
  if (level === 'basic' && n.t === 'FADER' && name === 'faderLevel' && famIndex('FADER', path) < store.L.physicalFaders) return false;
  // Reading along, the server writes on the front panel: the buttons and
  // faders of the physical channels, and the pads of the bank the device shows.
  if (store.meta.passive && !(((n.t === 'CHANNEL' || n.t === 'FADER') && famIndex(n.t, path) < store.L.physicalFaders)
    || (n.t === 'PADBUTTON' && famIndex(n.t, path) < (store.L.padsPerBank || 6)))) return false;
  return true;
}
