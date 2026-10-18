# Roadmap

Where rcplug is going, and plugins that would be worth building. Nothing here is a
promise; priorities follow what users need and what the first tests on hardware
show. Ideas and comments are welcome as issues.

**Today (0.7.2):**

- **Host:** dashboard and plugin supervisor, signed in-app updates with rollback,
  diagnostics, plugin settings in the dashboard, update notices, an optional dashboard
  password, an event bus for plugins, shared audio taps, plugin permissions, a plugin
  catalog to install and remove plugins, the device's processes with their CPU and RAM,
  and removing rcplug from the dashboard.
  - Updates download gently and show a progress bar.
- **Console:** the whole desk in the browser, reading along on the real Duo.
  - Live meters from the mixer's memory.
  - Mute, listen and the physical faders through the device's buttons.
  - Write protection and a PIN that can be reset at the device.
  - Experimental *full control* for every setting.
- **Sonar** as a plugin.
- **Toolkit** for SSH access, flashing (Windows, Linux), installing, stopping and
  uninstalling, and diagnostics.
- **For plugin authors:** an SDK, a guide and a cookbook.

**Confirmed on a real Duo** (firmware 1.7.6;
[device-notes.md](device-notes.md#first-test-on-a-real-duo)):

- Flashing from Windows, SSH, the installer, the dashboard and the plugin host.
- In-app updates. Since 0.6.5 they leave the computer's USB audio alone; the update to
  0.6.6 ran without a dropout.
- The console reads along, also after its own update without reading the state again.
- The meters and the hardware fader positions.
- Mute and listen through the device's buttons, and a physical fader set from the
  browser (the real fader takes over again as soon as it is moved).

## Now: the rest of the first run on hardware

1. **Full control** ([protocol.md](protocol.md#full-control), 0.6.4, experimental):
   - Does the device's screen work normally through the console?
   - Does a setting without a button, changed in the browser, show on the device?
   - Does the screen find the mixer again when it is switched off?
2. **A restart of the device:**
   - The console starts before the device's screen and reads along on its own.
   - With full control on, it takes the mixer's place at boot.
3. **Open values** ([device-notes.md](device-notes.md#assumptions-to-confirm-on-hardware)):
   - the real gain ranges;
   - the meaning of a few integer settings (the console's change log, 0.7.3, shows
     them when a setting is changed on the device's screen);
   - the CPU load of the audio taps since 0.7.4 (2 channels, cheapest resampling; with
     8 channels it was 13 % of the device in 0.7.2).

## Done: the core (0.6.8 – 0.6.11)

The host's own features, which every plugin builds on:

| Topic | What |
|---|---|
| **Update notices** | *0.6.8:* an opt-in daily check and a badge in the navigation; installing stays a click. |
| **Dashboard access** | *0.6.9:* an optional password for the dashboard and all plugin pages, with sessions that survive updates and a reset at the device. |
| **Event bus** | *0.6.10:* plugins publish and subscribe to events through the host; the console reports the desk (recording, markers, mutes, pads, show), the host its plugins and updates. |
| **Shared audio taps** | *0.6.11:* one JACK bridge run by the host: plugins ask for ports and get a stream, one opt-in, one place to watch it. Sonar 0.5.1 uses it. |

## Done: the platform (0.7.0 – 0.7.2)

Installing, removing and trusting plugins without a computer:

| Topic | What |
|---|---|
| **Uninstall from the dashboard** | *0.7.0:* *Remove rcplug* on the dashboard's System page. Nothing a user does needs a shell or a computer any more, except installing. |
| **Plugin permissions** | *0.7.0:* the manifest declares what a plugin does (`desk:write`, `audio`, `network`, `device`) and the dashboard shows it on the plugin's card. The console takes writes only from plugins with `desk:write`, named by their token, without the PIN and whatever level the browsers have; the audio taps stream only to plugins with `audio`. |
| **Plugin catalog** | *0.7.1:* the dashboard lists the plugins of signed releases (rcplug-plugins, and catalogs users add with their key) with description, size, source and permissions, installs them and removes plugins again. |
| **Resources** | *0.7.2:* processes with CPU and RAM that add up to the device's totals, and the device's resources once a minute in the diagnostics bundle. |

## Next: the whole device in the console

Everything the device's own screen does, from the browser. The console already shows
the whole desk and changes mixing, channel processing, effects and outputs; what is
missing:

1. **Settings shown as raw numbers** become real controls with named choices
   (multitrack and recording modes, headphone types, mix-minus, pad types and modes,
   input types and microphone presets), once the meaning of each value is known.
2. **SMART pads** configured from the browser (name, colour, play mode, loop, effect
   and mixer pads), not only played.
3. **Functions the screen app carries out** (switching shows, Bluetooth connections,
   recording destination, system settings): they need *full control*
   ([protocol.md](protocol.md#full-control)), which is still to be confirmed on a
   real Duo, and each one tested there before it is offered.

Reading along, the console changes what has a button on the device (mute, listen,
physical faders); everything else needs full control.

## Waiting for hardware

| Topic | What | Why |
|---|---|---|
| **RØDECaster Pro II** | A verified profile from a state capture ([adding-a-model.md](adding-a-model.md)), so control can be enabled. | Same platform, larger audience. |
| **Flashing from macOS** | The toolkit's flasher on macOS. | Windows and Linux only today. |

## On hold

- **Python SDK.** The firmware's Python 3.10 is a minimal build (about 60 modules,
  checked in the 1.7.6 image): no `socket`, `json`, `http`, `ssl` or `ctypes`. A plugin
  in it could neither serve a page nor call the host or the console, and bringing an
  interpreter is too big for the device. If scripts with pages are wanted, the way
  would be a CGI mode in the host (it runs a script per request, with the request in
  its environment and on stdin), which would serve shell scripts as well.

## Plugin ideas

Effort: **S** a few days, **M** one or two weeks, **L** more.

| Plugin | What it does | Builds on | Effort |
|---|---|---|---|
| **Recordings** | Browse, play, download and (after confirmation) delete recordings over the network, without taking out the card or switching to USB mode. Optionally copy finished recordings to a NAS or cloud folder (WebDAV). | The firmware's strings point to `/Application/sd-card-mount` (microSD) and `/Application/ssd-mount` (USB SSD); the tree lists `RECORDINGS` and the storage volumes. Read-only, lowest priority while recording. | M |
| **Scenes** | Save and recall mixer setups (levels, processing, routing) by name, per guest or show, with a preview of what a recall changes. | Console tree and writes at the *Control* level, within the write policy; on the Duo this needs full control. | M |
| **Remote control bridge** | Simple HTTP endpoints and OSC for Stream Deck, Bitfocus Companion, TouchOSC: mute toggles, pads, markers, record. | Console writes. First check what the firmware's own `OSCPropertyBridge` offers. | S–M |
| **Home Assistant / MQTT** | Recorder state, mutes and levels as MQTT entities with discovery, commands back: on-air lamps, room light, "do not disturb" on the door. | Summary and events; a small MQTT client in Go. | M |
| **On-air signal** | Rules like "when recording starts, a mic opens or a pad plays, call this URL". | The [webhook recipe](cookbook.md#call-a-url-when-recording-starts-or-stops). | S |
| **Loudness meter** | EBU R128 loudness (momentary, short-term, integrated, true peak) of the program mix with podcast and broadcast targets, and a history per recording. | Audio through the JACK bridge; DSP in Go, as Sonar does. | M |
| **Talk time** | Who spoke how long, live and per recording, from the channel meters; no audio access needed. | The channel meters (confirmed on the Duo in 0.6.3). | S–M |
| **Show rundown** | Segments with planned durations, a countdown to the next one, a marker at every change; a tablet view. | On air (the guide's example) and markers. | S |
| **Silent mic and clip alarm** | Warns on every open dashboard (and by webhook) when a mic stays muted or silent while recording, or a channel clips. | The meters (with peaks and gain reduction) and the recorder state. | S |
| **Listen remotely** | The program mix in the browser with low latency, for a producer in another room. | Audio through the JACK bridge, WebSockets through the host. About 1.5 Mbit/s uncompressed. | M |
| **Chapter export** | The markers of a recording as chapters (Podlove, MP4, YouTube timestamps), ready to paste. | Recordings; where the device stores markers is to be found out. | S after Recordings |
| **Scheduled recording** | Start and stop at set times or from a calendar (ICS). | `api/record`. | S |
| **Pad manager** | Prepare SMART pad sounds in the browser: upload, trim, normalise. | Pad files under `/Application/emmc-data/pads`; the mixer owns them, so read-only first. | L |
| **Settings export** | Keep a copy of RØDE's own show and pad configuration. | The tree; export only, restoring would go through the write policy. | M |
| **Sonar games** | Back to Sonar's origin: a hide-and-seek mode with rounds and scores. | Sonar. | S |

How to build any of them: [plugin-guide.md](plugin-guide.md), [cookbook.md](cookbook.md).

## Open research

- RØDE's own `OSCPropertyBridge` in `rc_audio_mixer`: what it exposes and whether it
  is reachable.
- The layout of recordings on the microSD, and where markers are stored.
- cgroups in the device's kernel, for memory limits per plugin.
- Why a fast download stalls the Duo's USB audio interface. The network, the storage
  and the USB interface share their interrupt handling at one real-time priority. Since
  0.6.5 updates avoid it by going slowly; plugins that move a lot of data should do
  the same.

## Removed

- The console's *Display* page (0.6.7): a mirror of the device's screen, operating it
  by touch, and a grid of the device's buttons. The mixer and full control replace it.

## Principles that stay

- The device comes first: audio, its screen and RØDE's own functions are never
  put at risk for a feature ([safety.md](safety.md)).
- Read-only by default; anything that changes the desk or touches the audio graph is
  opt-in and says so.
- Nothing in the firmware is changed except the one-time SSH key.
- A firmware update removes rcplug completely and is the way back to the stock device
  in an emergency. rcplug does not try to survive it; reinstalling afterwards stays a
  deliberate step with the toolkit.
- Go standard library only; small, static binaries; no background network traffic
  unless a user switches it on (update notices).
