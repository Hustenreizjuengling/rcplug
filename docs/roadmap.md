# Roadmap

Where rcplug is going, and plugins that would be worth building. Nothing here is a
promise; priorities follow what users need and what the first tests on hardware
show. Ideas and comments are welcome as issues.

**Today (0.5):** the host with dashboard, in-app updates and diagnostics; the console
(the whole desk in the browser); Sonar; the toolkit for SSH access, flashing,
installing and diagnostics on Windows and Linux; an SDK, a guide and a cookbook for
plugin authors. Everything is tested against the stock firmware images, a simulator
and under emulation, and the first test on a real Duo passed for flashing, the host
and the plugins ([device-notes.md](device-notes.md#first-test-on-a-real-duo)).

## Now: from "tested offline" to "works on a Duo"

1. **The console on a single-client mixer.** On the real Duo the mixer serves one
   client and the device's screen holds it. Plan: (a) a mirror that reads the
   screen's connection alongside it on the loopback interface, without being a
   client, started before the screen at boot (read-only, cannot disturb the desk):
   **works since 0.6.0/0.6.1**;
   (b) control through the front-panel emulation (mute, listen, pads, pages, the
   wheel), whose effect the mirror then shows: **works since 0.5.1/0.5.2** on the
   *Display* page, together with the screen mirror; (c) experimental, a relay that
   holds the mixer's slot and serves the screen, for settings without a button
   ([full control](protocol.md#full-control)): **implemented in 0.6.4**, to be tested
   on hardware.
2. **The rest of the first run on hardware**: the meters from the mixer's memory
   (**implemented in 0.6.3**, to be confirmed on hardware), gain ranges,
   CPU load of the JACK bridge
   ([device-notes.md](device-notes.md#assumptions-to-confirm-on-hardware)).
3. **First in-app update on hardware** (0.5.0 → 0.5.1): HTTPS with the firmware's CA
   store and clock, the installer and its rollback under the real init system.

## Next: the platform

| Topic | What | Why |
|---|---|---|
| **Survive firmware updates** | The toolkit saves `config/` and `data/` before a firmware flash and offers "reinstall with my settings" afterwards; it notices on connect that rcplug is gone. | A firmware update wipes rcplug today, which users will hit with every RØDE release. |
| **Plugin catalog** | A signed index of known plugins: install from the dashboard, not only update. Shows author, source, size and what the plugin may do. Other catalogs can be added with their key. | Installing a plugin should not need a computer and SSH. |
| **Plugin permissions** | The manifest declares what a plugin needs (`desk:write`, `audio`, `network`, `storage`); the dashboard shows it before installing; the console accepts writes only from plugins granted `desk:write` (a token per plugin instead of one control level for all). | With third-party plugins, "everything runs as root and may write" is too coarse. |
| **Everything without SSH** | Every option and recovery step in the dashboard, the console or the setup window: plugin options (today `config/<plugin>.json`), the console's read-only switch and PIN reset, stopping rcplug in an emergency, uninstalling. Manual SSH stays for developers. | Users of a desk are podcasters, not Linux admins; no step may need a shell on the device. |
| **Dashboard access** | An optional password for the dashboard and all plugin pages; sessions instead of only the console PIN. | Today everyone on the network can use every page. |
| **Update notices** | An opt-in daily check and a badge in the navigation; installing stays a click. | Users miss updates they never look for. |
| **Shared audio taps** | One JACK bridge run by the host: plugins ask for ports and get a stream, one opt-in, one place to watch CPU and dropouts. | Every audio plugin would otherwise add its own client to RØDE's graph. |
| **Event bus** | Plugins publish and subscribe to events through the host (*marker dropped*, *segment started*, *recording saved*). | Plugins can work together without knowing each other's APIs. |
| **Python SDK** | One file with the environment helpers, a server with the `X-Rcplug` check and a console client. The firmware ships Python 3.10. | Many makers write Python, and nothing needs to be cross-compiled. |
| **RØDECaster Pro II** | A verified profile from a state capture ([adding-a-model.md](adding-a-model.md)), so control can be enabled. | Same platform, larger audience. |
| **Flashing from macOS** | The toolkit's flasher on macOS. | Windows and Linux only today. |

## Plugin ideas

Effort: **S** a few days, **M** one or two weeks, **L** more.

| Plugin | What it does | Builds on | Effort |
|---|---|---|---|
| **Recordings** | Browse, play, download and (after confirmation) delete recordings over the network, without taking out the card or switching to USB mode. Optionally copy finished recordings to a NAS or cloud folder (WebDAV). | The firmware's strings point to `/Application/sd-card-mount` (microSD) and `/Application/ssd-mount` (USB SSD); the tree lists `RECORDINGS` and the storage volumes. Read-only, lowest priority while recording. | M |
| **Scenes** | Save and recall mixer setups (levels, processing, routing) by name, per guest or show, with a preview of what a recall changes. | Console tree and writes at the *Control* level, within the write policy. | M |
| **Remote control bridge** | Simple HTTP endpoints and OSC for Stream Deck, Bitfocus Companion, TouchOSC: mute toggles, pads, markers, record. | Console writes. First check what the firmware's own `OSCPropertyBridge` offers. | S–M |
| **Home Assistant / MQTT** | Recorder state, mutes and levels as MQTT entities with discovery, commands back: on-air lamps, room light, "do not disturb" on the door. | Summary and events; a small MQTT client in Go. | M |
| **On-air signal** | Rules like "when recording starts, a mic opens or a pad plays, call this URL". | The [webhook recipe](cookbook.md#call-a-url-when-recording-starts-or-stops). | S |
| **Loudness meter** | EBU R128 loudness (momentary, short-term, integrated, true peak) of the program mix with podcast and broadcast targets, and a history per recording. | Audio through the JACK bridge; DSP in Go, as Sonar does. | M |
| **Talk time** | Who spoke how long, live and per recording, from the channel meters; no audio access needed. | Summary meters (mapping to confirm on hardware). | S–M |
| **Show rundown** | Segments with planned durations, a countdown to the next one, a marker at every change; a tablet view. | On air (the guide's example) and markers. | S |
| **Silent mic and clip alarm** | Warns on every open dashboard (and by webhook) when a mic stays muted or silent while recording, or a channel clips. | Meters and recorder state. | S |
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
- Whether the screen can be read while the GUI is the DRM master
  ([device-notes.md](device-notes.md#assumptions-to-confirm-on-hardware), item 5).

## Principles that stay

- The device comes first: audio, the touch screen and RØDE's own functions are never
  put at risk for a feature ([safety.md](safety.md)).
- Read-only by default; anything that changes the desk or touches the audio graph is
  opt-in and says so.
- Nothing in the firmware is changed except the one-time SSH key.
- Go standard library only; small, static binaries; no background network traffic.
