// System: connection, console settings (PIN, write protection), the device
// buttons the console uses while reading along, full control, and the
// device: updates, versions, audio engine, network, Bluetooth, wireless mics,
// CallMe, shows, display & buttons. The first cards work without the device
// state.
import { h } from '../dom.js';
import { t } from '../i18n.js';
import { single, run, runPath } from '../store.js';
import { slider, kv, card, valueText } from '../controls.js';
import { request, reconnect, setPin, report, forgetPin, pinReset, setSettings } from '../api.js';
import { confirmDialog } from '../dialogs.js';
import { controlLevel } from '../policy.js';
import { liveState } from '../live.js';

function connectionCard(scope) {
  const box = h('div');
  const upd = () => {
    const st = liveState.status && liveState.status.connection;
    if (!st) return;
    const s = st.stats || {};
    box.replaceChildren(h('dl', { class: 'kv' },
      h('dt', null, t('conn.state')), h('dd', null, t('state.' + st.state) + (st.error ? ' – ' + st.error : '')),
      h('dt', null, t('conn.addr')), h('dd', { class: 'mono' }, st.addr),
      h('dt', null, t('conn.guard')), h('dd', null, t('guard.' + st.guard)),
      h('dt', null, t('conn.rate')), h('dd', null, (st.changesPerSecond || 0).toFixed(1)),
      h('dt', null, t('conn.received')), h('dd', null, t('conn.receivedN', { n: s.framesIn, kib: ((s.bytesIn || 0) / 1024).toFixed(0) })),
      h('dt', null, t('conn.sent')), h('dd', null, String(s.framesOut)),
      h('dt', null, t('conn.sessions')), h('dd', null, String(s.sessions)),
      h('dt', null, t('conn.decodeErrors')), h('dd', null, String(s.decodeErrors ?? 0)),
      h('dt', null, t('conn.applyErrors')), h('dd', null, String(s.applyErrors ?? 0)),
      h('dt', null, t('conn.desyncs')), h('dd', null, String(s.desyncs ?? 0))));
  };
  upd();
  scope.interval(upd, 2000);
  const clients = h('div', { class: 'small muted' });
  const loadClients = () => request('api/clients').then((d) => {
    clients.replaceChildren(h('b', null, t('conn.clientsOf', { port: d.port })), d.error ? h('p', null, d.error) :
      h('ul', null, (d.clients || []).map((c) => h('li', { class: 'mono' }, `${c.remote}${c.comm ? ' – ' + c.comm + ' (PID ' + c.pid + ')' : ''}`))));
  }).catch(report);
  return card(t('conn.title'), box, h('div', { class: 'row', style: 'margin-top:8px' },
    h('button', { class: 'btn', type: 'button', onclick: () => reconnect(t('conn.reconnecting')) }, t('conn.reconnect')),
    h('button', { class: 'btn', type: 'button', onclick: loadClients }, t('conn.clients'))), clients);
}

// readOnlyRow switches the console's write protection (the --read-only flag
// cannot be switched off here).
function readOnlyRow(st, rerender) {
  const set = async (on) => {
    if (!on && !(await confirmDialog(t('ro.offConfirm'), t('ro.off')))) return;
    if (await setSettings({ readOnly: on })) rerender();
  };
  const btn = st.readOnlyFlag ? null : st.readOnly
    ? h('button', { class: 'btn', type: 'button', onclick: () => set(false) }, t('ro.off'))
    : h('button', { class: 'btn', type: 'button', onclick: () => set(true) }, t('ro.on'));
  return h('div', { class: 'row', style: 'margin:8px 0' },
    h('span', null, t('ro.label'), ': ', h('b', null, st.readOnly ? (st.readOnlyFlag ? t('ro.forced') : t('ro.isOn')) : t('ro.isOff'))), btn);
}

// pinResetBox resets a forgotten PIN by presses at the device.
function pinResetBox(scope, rerender) {
  const box = h('div', { class: 'small', role: 'status' });
  const start = async () => {
    if (!(await confirmDialog(t('pin.resetConfirm'), t('pin.resetStart')))) return;
    if (!(await pinReset())) return;
    const tick = () => request('api/status').then((s) => {
      const r = s.pinReset || {};
      if (r.done && !s.pinRequired) { box.replaceChildren(h('b', null, t('pin.resetDone'))); setTimeout(rerender, 2500); return true; }
      if (!r.active) { box.replaceChildren(t('pin.resetExpired')); return true; }
      box.replaceChildren(h('b', null, t('pin.resetPress', { n: r.presses || 0, need: r.need, s: r.seconds })));
      return false;
    });
    const id = scope.interval(async () => { if (await tick().catch(() => false)) clearInterval(id); }, 500);
    tick();
  };
  return h('div', null, h('button', { class: 'btn small', type: 'button', onclick: start }, t('pin.forgot')), box);
}

function consoleCard(scope, rerender) {
  const st = liveState.status || {};
  const current = h('input', { type: 'password', autocomplete: 'current-password', 'aria-label': t('pin.current'), placeholder: t('pin.current') });
  const next = h('input', { type: 'password', autocomplete: 'new-password', 'aria-label': t('pin.new'), placeholder: t('pin.newHint') });
  const save = async () => {
    if (!next.value && !(await confirmDialog(t('pin.removeConfirm')))) return;
    if (await setPin(current.value, next.value)) { forgetPin(); current.value = next.value = ''; }
  };
  return card(t('console.title'),
    h('dl', { class: 'kv' },
      h('dt', null, t('console.version')), h('dd', null, st.version || '—'),
      h('dt', null, t('console.pin')), h('dd', null, st.pinRequired ? t('console.pinSet') : t('console.pinNone'))),
    h('div', { class: 'pinrow' }, st.pinRequired ? current : null, next, h('button', { class: 'btn', type: 'button', onclick: save }, t('pin.save'))),
    h('p', { class: 'muted small' }, t('console.pinHint')),
    st.pinRequired ? pinResetBox(scope, rerender) : null,
    readOnlyRow(st, rerender),
    // Behind the host the dashboard offers the full diagnostics bundle.
    /\/plugins\/[^/]+\//.test(location.pathname) ? h('a', { class: 'btn', href: '../../api/diagnostics', download: '' }, t('console.diagnostics')) : null);
}

// panelCard switches the front-panel emulation: reading along, the console
// changes mute, listen and the physical faders by sending what the device's
// buttons send.
function panelCard(hw, rerender) {
  const p = hw.panel || {};
  const c = h('section', { class: 'card' }, h('h3', null, t('panel.title'), ' ', h('span', { class: 'tag amber' }, t('experimental'))));
  c.append(h('p', null, t('panel.intro')));
  if (p.error) c.append(h('div', { class: 'banner bad' }, p.error));
  if (!p.enabled) {
    c.append(h('button', { class: 'btn primary', type: 'button', onclick: async () => {
      if (await confirmDialog(t('panel.enableConfirm'), t('panel.enable')) && await setSettings({ panel: true })) rerender();
    } }, t('panel.enable')));
    return c;
  }
  c.append(h('p', null, h('b', null, t('panel.isOn')), ' ', controlLevel() === 'expert' ? '' : t('panel.levelNote')));
  if (p.forced) c.append(h('p', { class: 'muted small' }, t('panel.forced')));
  else c.append(h('button', { class: 'btn', type: 'button', onclick: async () => { if (await setSettings({ panel: false })) rerender(); } }, t('panel.disable')));
  return c;
}

// fullControlCard switches relay mode (the console takes the mixer's one
// connection and serves the device's screen through itself); only where the
// console works with the device's screen.
function fullControlCard(conn, rerender) {
  if (conn.mode !== 'tap' && conn.mode !== 'relay') return null;
  const c = h('section', { class: 'card' }, h('h3', null, t('full.title'), ' ', h('span', { class: 'tag amber' }, t('experimental'))));
  const expert = controlLevel() === 'expert';
  const on = async () => {
    if (await confirmDialog(t('full.enableConfirm'), t('full.enable')) && await setSettings({ fullControl: true })) setTimeout(rerender, 1500);
  };
  const off = async () => { if (await setSettings({ fullControl: false })) setTimeout(rerender, 800); };
  if (!conn.fullControl) {
    c.append(h('p', null, t('full.intro')),
      h('div', { class: 'row' }, h('button', { class: 'btn primary', type: 'button', disabled: !expert, onclick: on }, t('full.enable')),
        expert ? null : h('span', { class: 'muted small' }, t('full.expertOnly'))));
    if (conn.relayError) c.append(h('p', { class: 'muted small' }, t('full.lastEnd', { err: relayText(conn) })));
    return c;
  }
  const r = conn.relay;
  let text;
  if (conn.mode === 'relay' && r && r.screen) text = t('full.active', { time: new Date(r.screenSince).toLocaleTimeString() });
  else if (conn.mode === 'relay' && conn.state === 'live') text = t('full.noScreen');
  else if (conn.mode === 'relay') text = t('full.starting');
  else text = conn.relayError ? t('full.failed', { err: relayText(conn) }) : t('full.starting');
  c.append(h('p', { role: 'status' }, text));
  const row = h('div', { class: 'row' });
  if (conn.mode !== 'relay' && conn.relayError) row.append(h('button', { class: 'btn primary', type: 'button', disabled: !expert, onclick: on }, t('full.retry')));
  row.append(h('button', { class: 'btn', type: 'button', onclick: off }, t('full.disable')),
    h('button', { class: 'btn', type: 'button', onclick: rerender }, t('full.refresh')));
  c.append(row, h('p', { class: 'muted small' }, t('full.note')));
  return c;
}

function relayText(conn) {
  const k = 'code.' + conn.relayCode;
  const s = t(k);
  return s === k ? conn.relayError : s;
}

export const system = {
  id: 'system',
  title: 'tab.system',
  noTree: true, // the connection, console, button and full control cards work without the device state
  render(scope, rerender) {
    const sys = single('SYSTEM'), b = single('BUILD'), a = single('AUDIO'), net = single('NETWORK'), gui = single('GUI');
    const control = h('section', { class: 'card' }, h('p', { class: 'muted' }, t('loading')));
    const cards = [connectionCard(scope), consoleCard(scope, rerender), control];
    Promise.all([request('api/hw'), request('api/status')]).then(([hw, st]) => {
      if (scope.dead) return;
      const fc = fullControlCard(st.connection || {}, rerender);
      control.replaceWith(...[panelCard(hw, rerender), fc].filter(Boolean));
    }).catch(report);
    if (sys) cards.push(card(t('sys.device'), kv(scope, [
      [t('sys.name'), sys, 'systemName'], [t('sys.serial'), sys, 'systemSerialNumber'], [t('sys.firmware'), sys, 'systemFirmwareVersion'],
      [t('sys.board'), sys, 'boardType', (v) => (v === 1 ? 'Duo (1)' : v === 0 ? 'Pro II (0)' : String(v))], [t('sys.engine'), sys, 'engineMode'],
      [t('sys.midi'), sys, 'systemMidiControl'], [t('sys.selChannel'), sys, 'systemChannelSelected'], [t('sys.selMix'), sys, 'systemMixSelected'],
      [t('sys.haptic'), sys, 'systemHapticSetting'], [t('sys.recButton'), sys, 'systemRecButtonSetting'], [t('sys.timezone'), sys, 'systemDateTimezone'],
      [t('sys.h24'), sys, 'systemDateTime24h'], [t('sys.dst'), sys, 'systemDateTimeDaylightSavings'], [t('sys.beta'), sys, 'systemBetaMode'],
      [t('sys.usbHost'), sys, 'usbHostOnUsb2'], [t('sys.presenter'), sys, 'presenterMode'], ['Unify', sys, 'unifyMode'],
    ])));
    if (sys) cards.push(card(t('sys.updates'), kv(scope, [
      [t('sys.appUpdate'), sys, 'appUpdateAvailable'], [t('sys.osUpdate'), sys, 'osUpdateAvailable'], [t('sys.downloaded'), sys, 'updateDownloaded'],
      [t('sys.updateVersion'), sys, 'updateVersion'], [t('sys.download'), sys, 'updateDownloadProgress', (v) => v + ' %'], [t('sys.checking'), sys, 'updateChecking'],
    ]), h('p', { class: 'muted small' }, t('sys.updatesLocked'))));
    if (b) cards.push(card(t('sys.versions'), kv(scope, [[t('sys.mixerVersion'), b, 'buildMixerVersion'], ['GUI', b, 'buildGuiVersion'], ['CallMe', b, 'buildCallMeVersion'],
      [t('sys.mixerModules'), b, 'buildMixerModulesGitSha'], [t('sys.guiModules'), b, 'buildGuiModulesGitSha']])));
    if (a) cards.push(card(t('sys.audio'), kv(scope, [[t('sys.rate'), a, 'audioSampleRate', (v) => (v ? v / 1000 + ' kHz' : '—')], [t('sys.buffer'), a, 'audioBufferSize', (v) => t('sys.samples', { n: v })],
      [t('sys.inputs'), a, 'audioInputChannels'], [t('sys.outputs'), a, 'audioOutputChannels'], [t('sys.latIn'), a, 'audioInputLatency'], [t('sys.latOut'), a, 'audioOuputLatency']])));
    if (net) cards.push(card(t('sys.network'), kv(scope, [
      [t('sys.wired'), net, 'wiredConnected'], [t('sys.ip'), net, 'ipAddress'], [t('sys.mask'), net, 'subnetMask'], [t('sys.gateway'), net, 'gateway'],
      ['Wi-Fi', net, 'wifi'], ['SSID', net, 'wifiSSID'], [t('sys.wifiIp'), net, 'wifiIpAddress'], ['DNS', net, 'primaryDns'], [t('sys.wifiKey'), net, 'wifiPSK'],
      [t('sys.cell'), net, 'cellUSBFound'],
    ]), h('p', { class: 'muted small' }, t('sys.secretsNote'))));
    if (net) cards.push(card('Bluetooth', kv(scope, [[t('sys.btVisible'), net, 'btVisible'], [t('sys.btConnected'), net, 'btConnectedAddress'], [t('sys.btType'), net, 'btConnectedType']])));
    const tx = run('RADIO/RADIOTX');
    if (tx) {
      const g = h('div');
      for (let i = 0; i < tx.count; i++) {
        const p = runPath(tx, i);
        g.append(h('div', { style: 'margin-bottom:8px' }, h('b', null, t('sys.tx', { n: i + 1 })), kv(scope, [[t('connected'), p, 'txConnected'], [t('sys.serial'), p, 'txDeviceSN'],
          [t('sys.battery'), p, 'txBatteryLevel', (v) => v + ' %'], [t('sys.signal'), p, 'txSignalQuality'], ['RSSI', p, 'txRssi'], [t('sys.charging'), p, 'txChargingState'],
          [t('sys.txRec'), p, 'txRecord'], [t('sys.txMute'), p, 'txRemoteMute']])));
      }
      cards.push(card(t('sys.wireless'), g));
    }
    const sc = single('SIPCALLING');
    if (sc) cards.push(card('CallMe / SIP', kv(scope, [[t('sys.hosting'), sc, 'sipCallHostingEnabled'], [t('sys.licence'), sc, 'sipLicenceMode'], [t('sys.remaining'), sc, 'sipRemainingCalltime'],
      [t('sys.registration'), runPath(run('SIPCALLING/SIPREGISTRATION'), 0), 'sipRegistrationIsRegistered']])));
    const shows = run('SHOWS/SHOW');
    if (shows) {
      const tb = h('tbody');
      for (let i = 0; i < shows.count; i++) {
        const p = runPath(shows, i);
        tb.append(h('tr', null, h('td', null, valueText(scope, p, 'showName')), h('td', null, valueText(scope, p, 'showIcon')), h('td', null, valueText(scope, p, 'showSnapshotIndex'))));
      }
      cards.push(card(t('sys.shows'), kv(scope, [[t('sys.currentShow'), single('CURRENTSHOW'), 'currentShowName']]),
        h('div', { class: 'tablewrap' }, h('table', { class: 'props' }, h('thead', null, h('tr', null, h('th', null, t('sys.show')), h('th', null, t('sys.icon')), h('th', null, 'Snapshot'))), tb))));
    }
    if (gui) cards.push(card(t('sys.display'), slider(scope, gui, 'screenBrightness', { label: t('gui.screen'), min: 0, max: 100 }),
      slider(scope, gui, 'activeButtonsBrightness', { label: t('gui.buttonsActive'), min: 0, max: 100 }), slider(scope, gui, 'inactiveButtonsBrightness', { label: t('gui.buttonsInactive'), min: 0, max: 100 }),
      kv(scope, [[t('gui.lang'), gui, 'lang'], [t('gui.auto'), gui, 'autoBrightness'], [t('gui.dim'), gui, 'screenDimAfterSeconds'], ['Metering', gui, 'metering'], [t('gui.meters'), gui, 'broadcastMeters']])));
    return [h('div', { class: 'grid cols-3' }, cards)];
  },
};
