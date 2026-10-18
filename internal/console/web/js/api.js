// Requests to the console API. Writes carry the CSRF header and, if the
// console has one, the PIN; errors come back as {error, code}.
import { toast } from './dom.js';
import { errorText, t } from './i18n.js';
import { store, applyLocal, refresh, reload, bindKey } from './store.js';
import { askPin } from './dialogs.js';

let pin = '';
try { pin = sessionStorage.getItem('rcplug-pin') || ''; } catch (e) { /* ignore */ }

export class ApiError extends Error {
  constructor(message, code, status) { super(message); this.code = code; this.status = status; }
}

async function send(path, body) {
  const opt = body === undefined ? { cache: 'no-store' } : {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Rcplug': '1', ...(pin ? { 'X-Rcplug-Pin': pin } : {}) },
    body: JSON.stringify(body),
  };
  let r;
  try {
    r = await fetch(path, opt);
  } catch (e) {
    throw new ApiError(t('err.network'), 'network', 0);
  }
  let data = null;
  try { data = await r.json(); } catch (e) { /* not JSON */ }
  return { r, data };
}

const needsPin = ({ r, data }) => r.status === 401 && data && data.code === 'pin_required';

// pinRound is set while the user is asked for the PIN. Requests that hit
// pin_required meanwhile wait for it and are only sent again once the
// entered PIN worked, so a typo costs one attempt, not one per request.
let pinRound = null;

async function withPin(path, body, res) {
  if (pinRound) {
    return (await pinRound) ? send(path, body) : res;
  }
  let done;
  pinRound = new Promise((resolve) => { done = resolve; });
  try {
    const entered = await askPin();
    if (!entered) { done(false); return res; }
    pin = entered;
    const again = await send(path, body);
    const ok = !needsPin(again) && again.r.status !== 429;
    if (ok) {
      try { sessionStorage.setItem('rcplug-pin', pin); } catch (e) { /* ignore */ }
    } else forgetPin();
    done(ok);
    return again;
  } finally {
    pinRound = null;
  }
}

export async function request(path, body) {
  let res = await send(path, body);
  // api/pin checks the current PIN from its body, not from the header.
  if (body !== undefined && path !== 'api/pin' && needsPin(res)) res = await withPin(path, body, res);
  const { r, data } = res;
  if (!r.ok) {
    const err = new ApiError((data && data.error) || 'HTTP ' + r.status, data && data.code, r.status);
    if (err.code === 'stale_epoch') reload();
    throw err;
  }
  return data;
}

export function report(err) { toast(errorText(err), 'err'); }

// Only the newest response for a property may update the model: during a drag
// an older answer can arrive after a newer one.
const latest = new Map();

export async function setProp(scope, path, name, value) {
  const k = bindKey(path, name);
  const mine = (latest.get(k) || 0) + 1;
  latest.set(k, mine);
  try {
    const r = await request('api/set', { epoch: scope.epoch, path, name, value });
    // An answer that arrives after the tree was reloaded belongs to the old one.
    if (latest.get(k) === mine && scope.epoch === store.epoch) applyLocal(path, name, r.value);
    return true;
  } catch (e) {
    report(e);
    if (latest.get(k) === mine) refresh(path, name);
    return false;
  }
}

async function post(path, body, okMsg) {
  try {
    const r = await request(path, body);
    if (okMsg) toast(okMsg, 'ok');
    return r || true;
  } catch (e) {
    report(e);
    return false;
  }
}

export const press = (scope, path, name, ms) => post('api/press', { epoch: scope.epoch, path, name, ms: ms || 150 });
export const record = (scope, value) => post('api/record', { epoch: scope.epoch, value });
export const link = (scope, source, output, on, okMsg) => post('api/link', { epoch: scope.epoch, source, output, link: on }, okMsg);
export const setControl = (level) => post('api/control', { level });
export const setSettings = (settings) => post('api/settings', settings);
export const setPin = (current, next) => post('api/pin', { current, new: next });
export const pinReset = () => post('api/pin/reset', {});
export const reconnect = (okMsg) => post('api/reconnect', {}, okMsg);
export const resync = (okMsg) => post('api/resync', {}, okMsg);
export const panel = (control, value) => post('api/panel', { control, value: value || 0 });
export const touch = (g) => post('api/touch', g);
export function forgetPin() { pin = ''; try { sessionStorage.removeItem('rcplug-pin'); } catch (e) { /* ignore */ } }
