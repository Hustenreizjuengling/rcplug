# Changelog

All notable changes. The format follows [Keep a Changelog](https://keepachangelog.com/),
versions follow [Semantic Versioning](https://semver.org/) (before 1.0, minor versions
may break compatibility).

## 0.8.9 - Unreleased

### Fixed
- **Guessing the dashboard password in parallel:** checks still running count towards
  the lockout (5 wrong passwords a minute), so a burst of simultaneous guesses gets no
  more tries than one after the other. A password file that cannot be read keeps the
  dashboard closed instead of opening it (removing the password at the device still
  works), and a session needs a usable signing key.
- **Plugins calling rcplugd at the device's own address** (when rcplugd listens only
  on the LAN address, `--addr`) count as the device again: with a password they are
  no longer asked to log in, and the console sees which plugin writes.
- **The audio bridge** stops again after a request that failed (it ran on for nobody),
  and a failed restart for more channels ends the streams cleanly instead of leaving
  them hanging with a stale channel table.
- **Diagnostics:** plugin options that look like secrets (`apiToken`, `pin`, …) are
  masked in the bundle, URLs keep only scheme and host; command lines mask flags such
  as `--api-token` too.
- **A failed update of rcplug** no longer leaves the dashboard at “restarting”
  forever, refusing every later update and install: when the installer reports a
  failure, or rcplug is not replaced within 15 minutes, the update shows as failed.
- **Leftovers** of an update or removal interrupted by a power loss are removed at
  the next start; changes to a plugin's state and options happen one at a time.
- **Full control ends when the console's copy loses track** of the conversation
  between mixer and screen (a damaged frame, a change that does not fit): the console
  steps out and starts again with a full read, instead of showing and writing values
  that may be wrong. When the console stops, the screen gets the mixer back at once.
  Another program on the control port no longer takes the device's screen's place.
- **Mute and listen while the state is not current** are refused (409 `stale_state`)
  also when reading along, as with full control: a press could have done the
  opposite.
- **Held SMART pads are always let go:** the release (new `api/release`) needs no PIN
  and no level, so a hold ends even if control was turned off meanwhile, and after a
  change of the tree it finds the pad again instead of the node now at its old place.
- **The console's diagnostics** leave out the device's, the show's and the pads'
  names, text values in the write log, and the browsers' addresses.
- **On phones** no page is wider than the screen any more: the password card's state
  (now a short “No password”), the console's top bar (at 320–390 px, also with
  *Reconnect* showing) and long states in pills.
- **After an update** pages no longer run on the cached stylesheet and icons of the
  version before (`sdk/ui` files are checked again on every load, 304 while unchanged).
- **Dashboard:** an error while drawing no longer stops its updates until a reload; a
  page you switch to is not overwritten by the one you left; the plugin catalog shows
  at once with the *Plugins* page and keeps what you typed into its form.
- **Accessibility:** the closed phone menu is out of reach for the keyboard and screen
  readers and closes with Escape; the side bar's figures are no longer read out every
  few seconds; the console's connection state and its tool tabs keep their names on
  small screens (tool tabs show them on touch screens); disabled buttons show why on
  hover; the console's labels follow the language. The console's tab strip no longer
  jumps back while you swipe it.
- **Hostile data on the control port** (deeply nested or huge arrays) is refused
  before it uses up memory, and a TCP segment from far behind no longer crashes reading
  along. The change log names the node a change was made to, also when the tree
  changed before it was written down.

## 0.8.8 - 2026-10-19

### Changed
- **A new look, one application:** rcplug's dashboard, its console and its login page
  share one design (`sdk/ui`: colours, type, components and an icon set, about 24 KB)
  and plugins can use it too.
  - The **dashboard** has a side bar with the device, the plugins' pages and *Plugins*
    and *System*; a plugin's page fills the rest, without a second bar. On a phone the
    side bar folds into a menu. *System* is split into Overview (memory, storage,
    load and uptime at a glance), Processes, Updates, Access, Audio & events and
    Help; the plugin list has clearer cards with the main action first.
  - The **console** has one bar with the device, its views as tabs, the connection
    and the control level, instead of a header and a second side bar. Channel strips
    show their source in full and carry their colour at the top; settings show their
    name and value above the slider; recorder buttons have icons; cards keep their
    own height.
  - Fewer explanations in the way: long texts moved into hints and folds.

### Added
- **`sdk/ui`** serves the shared stylesheet and icons for plugins
  (`ui.Handler("/ui/")`); rcplugd serves them at `/ui/` too
  ([plugin guide](docs/plugin-guide.md#look-like-the-rest-of-rcplug)).

## 0.8.7 - 2026-10-19

### Added
- **The device's processing graphs in a channel's details:** the compressor's
  transfer curve with the channel's level as a dot on it, the noise gate's level
  history of the last five seconds with its threshold and hysteresis, the de-esser's
  dip at its frequency (as deep as the current reduction), and the high-pass filter's
  curve, drawn as the device's screen draws them.

### Changed
- **Exact units** for every processing setting: the scales come from the device's own
  screen app (firmware 1.7.6) instead of estimates, e.g. the EQ's low band is 20–200 Hz
  (quadratic), the compressor's attack 0.1–10 ms (logarithmic); the defaults come out
  round (100 Hz, 70 Hz high-pass, 2 : 1). The EQ curve uses the device's filters (three
  bells at 48 kHz). The gate's hysteresis shows in dB. Table in
  [device-notes.md](docs/device-notes.md#processing-values).

## 0.8.6 - 2026-10-18

### Changed
- **`/api/backup` leaves the console out**: its options, and on restore a console in a
  backup of 0.8.5 is left alone. What the console holds goes with the device and its
  shows, not with rcplug's backups.

### Fixed
- A backup records whether each plugin runs, also when its autostart started it: a
  plugin switched off by hand after the backup is switched on again by a restore
  (0.8.5 recorded only what the user had switched).

### Removed
- **Snapshots of the desk's settings** (the console's `api/snapshot`, `/diff` and
  `/apply`, new in 0.8.5). Backups cover rcplug's own settings only: the desk's
  settings are thousands of values whose meaning a firmware update can change, and the
  device keeps them itself (RØDE's shows). The *Backup* plugin 0.2.0 follows suit.

## 0.8.5 - 2026-10-18

### Added
- **Backups of rcplug's and the desk's settings**, for the new *Backup* plugin
  (rcplug-plugins): `GET /api/backup` reads rcplug's own settings (which plugins are
  installed and switched on, their options, the daily update check, the extra
  catalogs; never the dashboard's password, the console's PIN or options that look
  like secrets), `POST /api/backup/diff` shows what restoring one would change and
  `POST /api/backup/restore` carries it out. The console's `api/snapshot` lists the
  desk's settings (a few thousand: channels, processing, routing, inputs, pads,
  effects, outputs, the device's own settings), addressed by node family and index so
  a snapshot survives a firmware update that moves nodes; `api/snapshot/diff`
  compares one with the desk and marks what needs the expert level;
  `api/snapshot/apply` restores it through the usual checks, in passes (a microphone
  type applies its preset, so the restore looks again and writes what still
  differs), routing links with the device's sequence and capped values in allowed
  steps. Listening volumes are never part of it, so a restore cannot make the
  headphones loud, and neither are the routing levels of channels on a physical
  fader (they follow the fader).

## 0.8.4 - 2026-10-18

### Added
- **EQ curve** in a channel's details: the frequency response of the three EQ bands
  and the high-pass filter, drawn as a small SVG from the standard filter formulas and
  redrawn as the settings change, with a dot per band. The EQ and high-pass sliders
  show Hz, dB, Q and dB/octave instead of percent. The desk stores these settings as
  0–1 without units; the scales are estimates that fit a real Duo's values (gain 0.5
  is 0 dB, ±12 dB; logarithmic frequencies; Q 0.1–10) and live in one place
  (`curves.js`), to be confirmed against the device's own read-outs.

## 0.8.3 - 2026-10-18

### Changed
- **The console has a side bar** instead of a row of tabs: Mixer, Routing, Inputs,
  Effects, Outputs, SMART pads, Recording, Device, and *Tools* (Explorer, Log)
  folded away at the bottom; small inline icons (a few hundred bytes), no framework.
  On a phone it becomes a row to swipe. Channel strip names stay on one line.

### Fixed
- **SMART pads that play while held** work from the browser: a pad is pressed while
  the mouse button, finger or key is down and released when it comes up (before, a
  click was always a 150 ms press). A hold the browser never ends (a closed tab) is
  released by the console after 30 seconds, and only the newest press of a button is
  released that way. `api/press` takes `ms` up to 30 000.

## 0.8.2 - 2026-10-18

### Added
- **Headphone volume** in the console (*Outputs*), one slider per headphone. On a
  Duo the two headphone knobs are endless encoders whose position the mixer keeps in
  `POT` 0 and 1 (0–127), written at the *Control* level. Whether the mixer follows a
  written value is to be confirmed on the device; turning a knob shows in the change
  log, which no longer leaves the knobs out.

### Changed
- **Only what the device has:** the tree has room for four microphone inputs and four
  headphones on every model; the console now shows a Duo's two of each (inputs,
  routing matrix, mix-minus, headphones) and leaves out the tree's spare input nodes.
  The model profile has `MicInputs` and `Headphones` (layout `micInputs`,
  `headphones`). A source assigned to a channel strip is always shown.
- The console's *System* tab is called **Device**, so it is not mistaken for the
  dashboard's *System* page (rcplug and the operating system).
- **Level meters:** the colour scale stands still and a cover shrinks over it, so the
  top turns red only when the level gets there (before, the scale was squeezed into
  the bar and its top was always red). Green to about −17 dB, amber to about −2 dB.

## 0.8.1 - 2026-10-18

### Fixed
- **Full control after an update of the console**, seen on a real Duo: the old
  console restarted the device's screen app as it stopped, the new one took the free
  place at once, and the screen app, which tried to connect in the moment in between,
  did not try again; after 30 seconds the console stepped back and stayed reading
  along. Now the console restarts the screen app once if it has not connected after 8
  seconds (45 while the device boots), so it connects through the console; and if the
  console still has to step back, it takes the place over again two minutes later, at
  most three times. The console's *System* page shows when the next try is.

## 0.8.0 - 2026-10-18

### Changed
- **Full control is how the console works on the device.** Tried on a real Duo
  (firmware 1.7.6): the console took the mixer's connection in four seconds, the
  device's screen worked normally through it, and everything changed in the browser
  showed on the device. So it is no longer an experimental switch: on a RØDECaster the
  console takes the mixer's place by itself when it starts (the device's screen
  restarts once, about three seconds), and reads along only when that is not possible;
  it tries again when the mixer's place becomes free, never in a loop. The control
  levels still decide what a browser may change, and full control no longer needs
  the *Expert* level. The way back, should a firmware update break it: Plugins →
  Console → Settings → *Connection to the mixer: Read along only*.
- **The device buttons are no switch either:** the console uses the front-panel
  emulation wherever its serial line exists (physical faders, and mute, listen and the
  SMART pads while reading along). `--panel` is ignored.
- The console's *System* page shows how it reaches the desk (full control since …,
  reading along and why, or connected) instead of the two switch cards.
- **Audio taps:** no switch in the dashboard any more. A plugin with the permission
  `audio`, which users see before installing it, gets audio; the bridge costs about
  1 % of a Duo. `POST /api/audio/enable` is gone; `GET /api/audio` reports `enabled`
  as always true.

## 0.7.4 - 2026-10-18

### Fixed
- **Audio taps, measured on a real Duo:** the bridge resampled 8 channels at the default
  quality, which took about half a core (13 % of the device) while Sonar listened, and
  `arecord` overran for half a second whenever a stream started or stopped, because the
  reading loop waited while JACK's tools ran. The bridge is now only as wide as the
  ports in use need (2, 4 or 8 channels; it restarts wider when needed and the running
  streams go on), resamples as cheaply as it can (`alsa_out -q 0`: on a test machine a
  third of before), reads without ever waiting for those tools, and `arecord` keeps
  half a second of buffer. `GET /api/audio` shows the bridge's `width`.

## 0.7.3 - 2026-10-18

### Added
- Reading along, the console plays the **SMART pads** of the bank the device shows:
  a click on a pad presses it on the front panel (with the device buttons switched
  on). Other banks need the bank selected on the device first; the console says so.
- **Changes on the device** (console → *Log*): the last 500 changes the device
  reported, with the value before and after (meters, fader positions and progress left
  out). Change a setting on the device's screen to see which property it is and what
  its numbers mean; the diagnostics bundle carries the list too (`changes.json`).
  `GET api/changes`.
- **Mix-minus** per output (*Routing*) as a choice: main mix, mix-minus or custom
  (*Control* level).

### Fixed
- Value ranges checked against a real Duo (firmware 1.7.6): ducking depth and pad
  volume are in dB (−60 to 0), the screen and button brightness go up to 255, and
  automatic brightness is a switch. Before, the *Control* level refused real values.
- Input type 0 is a microphone (it was shown as "—"); types not seen on a real device
  show as numbers instead of guessed names.

## 0.7.2 - 2026-10-18

### Changed
- **Processes** (System page): the numbers add up now. CPU is the share of the whole
  device, with the device's total above the list (programs, kernel, interrupts,
  waiting for storage) and a line for the kernel threads, where the RT kernel runs the
  audio interrupts. Memory per process is split into its own RAM (RssAnon), which
  together is about the "programs" part of the device's memory, and shared RAM
  (program files, shared memory), which counts in every process that maps it. The
  system card breaks the memory down into programs, shared memory, kernel, file cache
  and free. Only counters are read, never a process's page tables, so the mixer's
  audio threads are never held up.

### Added
- **Resources in the diagnostics bundle:** `system/resources.txt` (CPU, RAM and
  storage now, what rcplug and RØDE's programs take, rcplug's files per plugin),
  `system/processes.txt` (every process with its CPU, own and shared RAM, threads)
  and `system/resources-history.txt` (the same once a minute for the last three
  hours, with the busiest processes).

### Fixed
- A test helper of the full-control relay could hang when a test ended while its fake
  screen was connecting (seen once in a release build).

## 0.7.1 - 2026-10-18

### Added
- **Plugin catalog** (Plugins → *More plugins*): the dashboard lists the plugins of
  the official catalog (the latest signed release of rcplug-plugins) and of catalogs
  users add with their public key, with description, size, source and permissions,
  and installs them with a click: downloaded gently, verified against the signed
  `release.json`, refused if the package asks for a permission the catalog did not
  show. `GET /api/catalog`, `POST /api/catalog/install`, `POST /api/catalog/sources`.
- **Remove a plugin** from its card (*Remove…*), optionally with its data, settings
  and log. `POST /api/plugins/<name>/remove`. The console belongs to rcplug and stays.
- `rcplug-release` records each plugin's title, description, icon, permissions and
  translations in `release.json` (`info`), for the catalog; older hosts ignore it.
- Events `host.plugin.installed` and `host.plugin.removed`.

## 0.7.0 - 2026-10-18

### Changed (breaking for plugins)
- **Plugin permissions.** A plugin declares in `plugin.json` what it does beyond its
  own pages: `"permissions": ["desk:write", "audio", "network", "device"]`. The
  dashboard shows them on the plugin's card. The console takes writes from a plugin
  only with `desk:write` (and the plugin's token, which the SDK's console client now
  sends), at the *Control* level's rules without the PIN, whatever level the browsers
  have; write protection still stops it, and plugins can no longer change the
  console's settings, control level or PIN. The shared audio taps stream only to
  plugins with `audio` (`"needsAudio": true` counts as `audio`, so Sonar 0.5.1 keeps
  its sound). **Plugins that write to the desk or tap audio need the
  permission in their manifest**; a write from the device without a token answers 403
  `permission_desk_write`. The host tells plugins who calls them (`X-Rcplug-Plugin`,
  `X-Rcplug-Permissions`). See docs/plugin-api.md#permissions.

### Fixed
- Dashboard → System → Processes: the memory column says RAM (German:
  Arbeitsspeicher), and the free space of rcplug's directory is called storage space.
- docs/console-api.md: error codes of the removed screen endpoints are gone; the codes
  for write protection, full control and the PIN reset are listed.

### Added
- **Remove rcplug** from the dashboard (System page, at the bottom): stops rcplug and
  every plugin and removes them with all data; the device keeps working. Only an
  install directory named `rcplug` is removed, never a development tree.
  `POST /api/uninstall`.

## 0.6.11 - 2026-10-18

### Added
- **Shared audio taps** (experimental, off by default; dashboard → System → *Audio
  taps*): one JACK bridge, `rcplug-taps` with 8 channels, run by the host for every
  plugin that needs sound. Plugins ask for ports and get a stream (`sdk.AudioTap`,
  `GET /api/audio/stream` with their token); the bridge runs only while one listens,
  and the System page shows what runs and the dropouts. See docs/plugin-api.md#audio.

## 0.6.10 - 2026-10-18

### Added
- **Event bus:** plugins publish and subscribe to events through the host
  (`POST /api/events` with the plugin's `RCPLUG_TOKEN`, `GET /api/events/stream`,
  `sdk.Publish`, `sdk.Events`). The console reports the desk: recording started,
  paused, resumed, stopped and saved, markers, mute and listen per channel, SMART pads,
  the show and its connection. The host reports plugins starting, crashing and
  stopping, and updates. The dashboard's System page lists the events live. See
  docs/plugin-api.md#events.
- **Processes** on the dashboard's System page: what runs on the device with its CPU
  (share of one core), memory and threads, sortable, with rcplug's plugins and RØDE's
  own programs marked (`GET /api/processes`).

## 0.6.9 - 2026-10-18

### Added
- **Dashboard password** (optional; dashboard → System → *Access*): with a password
  set, every page and API of the host, the plugins' pages included, needs a login.
  Sessions last 30 days and survive restarts and updates; a new password ends the
  others. Plugins on the device itself are not asked. A forgotten password is removed
  at the device: *Forgot the password?* on the login page, then four presses of
  channel 1's Listen button (the console confirms them to the host). Kept as a
  PBKDF2-SHA256 hash in `config/dashboard-access.json`.

### Fixed
- The in-app installer records the update's result only after it removed the unpacked
  package, so nothing is left to clean up once the result is there.

## 0.6.8 - 2026-10-18

### Added
- **Update notices:** *Look for updates every day* (dashboard → System → Updates, off
  by default) asks GitHub a few minutes after the start and then once a day; found
  updates show as a badge on *System* in the navigation. Nothing is installed without
  a click. `POST api/updates/auto?on=true|false`; `api/updates` reports `autoCheck`
  and `available`.

### Fixed
- The dashboard's plugin cards showed an empty divider below their buttons.

## 0.6.7 - 2026-10-18

### Added
- Updates show a **progress bar** with the amount and the time left while they
  download and unpack (in the dashboard's update card, and on the plugin card for a
  plugin update); the job reports `phase`, `done`, `total` and `eta`.

### Removed
- The console's **Display page**: the screen mirror, operating the device's screen by
  touch from it, and the grid of device buttons. The console's mixer shows and
  changes the desk itself, and full control covers the settings without a button.
  The switches for the device buttons and full control are on the *System* page now.
  `api/screen.*`, `api/touch` and `api/panel` are gone, `api/hw` reports the device
  buttons and the meters only; `--screen`, `--fb`, `--drm`, `--fps` and `--touch` are
  still accepted but do nothing. The diagnostics no longer list the input devices.

## 0.6.6 - 2026-10-18

### Changed
- Device notes: what the update logs of 0.6.4 and 0.6.5 showed about the USB audio
  dropout. This release mainly lets the paced updates of 0.6.5 be tried on a device.

## 0.6.5 - 2026-10-18

### Fixed
- **Updates no longer disturb USB audio** (to be confirmed on the device): during an
  update the computer lost the Duo as a sound device. The kernel log shows its USB
  interface stalling while the package was being downloaded and unpacked; the network,
  the storage and the USB audio share the interrupt handling at the same real-time
  priority. Updates now download at 256 KB per second with a small receive buffer,
  unpack at 1 MB per second, write in small flushed pieces and run at the lowest I/O
  priority (`RCPLUG_UPDATE_RATE`). This applies to updates installed by 0.6.5 and later.
- The console logged the start of the meters twice.

## 0.6.4 - 2026-10-18

### Added
- **Full control** (experimental, off by default): on the *Display* page, at the expert
  level, the console takes the mixer's one connection itself and serves the device's
  screen through it. Every setting can then be changed from the browser, not only
  those with a button, and the device's screen shows the change. Switching it on
  restarts the device's screen once. The console gives the place back when it is
  switched off, when the console stops or crashes, or when the screen does not
  connect, so the screen always finds the mixer again.
- **Plugin settings in the dashboard**: plugins declare `options` in their manifest
  (yes/no, a choice, a number or text, each passed as one argument or an environment
  variable); the dashboard shows them under *Settings* on the plugin's card, checks
  them and restarts the plugin. No shell needed. The console offers its meters, the
  screen mirror's source, operating the screen, the connection mode and control on
  untested models (docs/plugin-api.md#options).
- **Write protection** can be switched on and off on the console's *System* page
  (behind the PIN); `--read-only` still forces it.
- **Forgot the PIN?** removes the console's PIN at the device, without a shell: four
  presses of channel 1's Listen button within four seconds, in the minute after the
  request. The control level goes back to *Read only*.
- The simulator can serve one client like the Duo's mixer (`rcsim --one-client`) and
  send pings (`--ping`).

## 0.6.3 - 2026-10-18

### Added
- **Meters on the device.** The Duo's mixer never sends its level meters over the
  control port; it keeps them, with the hardware faders' positions, in shared memory
  that its screen reads. The console now reads that memory too (read-only, 20 times a
  second while someone watches) and shows every channel's level, its gain reduction,
  the master and the channel open on the device's screen. Physical faders moved by
  hand show at their real position. `--meters auto|off`.
- Diagnostics include the state of the sound devices' streams and of the USB device
  controller, to find out what silences USB audio during an update.

### Changed
- The mixer page shows the master and the channel open on the device's screen
  instead of thirteen "output" meters: the meter order assumed before was wrong.
  The layout's `outputMeters` is replaced by `masterMeter`, `focusMeters` and
  `reductionMeters`; the simulator follows the real order.
- The installer logs each step of an in-app update also when the update was started
  by a version before 0.6.2.

## 0.6.2 - 2026-10-18

### Added
- Reading along, the four physical faders can be moved from the console's mixer at the
  expert level: the position is set on the front panel (as the *Display* page does)
  until the real fader moves.
- The console goes on reading along after its own restart (an update, a restart of
  the plugin): it keeps the mirrored state and the screen's connection when it stops
  and continues from them, so no *Read the state again* is needed. Changes made in the
  seconds in between may be missing; the console says so.
- **Operate the device's screen from the mirror** (experimental, off by default): with
  *Operate the screen* switched on (Display page, expert level), a click on the
  mirrored screen taps the device's touchscreen there and a drag swipes. The console
  writes the touches into the touchscreen's input device, which RØDE's screen app
  reads directly; *Touch correction* mirrors an axis if taps land mirrored.
  `--touch auto|off|/dev/input/eventN`.
- Diagnostics include the device's own logs (RØDE's mixer and screen app, last 400
  lines) with serial numbers, tokens, passwords and MAC addresses masked, and the
  input devices with their axis ranges.
- The update log records each step of an in-app update with the time and the free
  memory, to find out what silences USB audio during an update.

### Changed
- The screen mirror reuses its frame buffers instead of allocating three new ones for
  every frame, and frees them 30 s after the last request: on the Duo the console
  needed about 37 MB while the mirror had been used and kept it; now it falls back to
  its usual size when nobody watches.

## 0.6.1 - 2026-10-18

### Added
- Reading along, mute and listen of the four physical channels can be switched on the
  console's pages again: the console presses the channel's button on the front panel
  (with the device buttons switched on, at a level that allows it) and the device
  reports the result. Other changes stay on the *Display* page.

### Fixed
- Physical faders did not move in the console on a real Duo: the device reports their
  position only as the level of the source's mixes (`mixLevelWithAnchor`), not as
  `faderLevel`. The mixer shows physical faders from there.
- An invalid port in the console's `--mixer` address (above 65535, zero, negative) falls back
  to 2345 instead of being cut down to another port (found by code scanning).

## 0.6.0 - 2026-10-18

### Added
- **The console reads along.** On a RØDECaster the mixer serves one client, the
  device's screen, so the console no longer connects: it reads the screen's
  connection on the loopback interface (a packet socket with a kernel filter, both
  TCP directions reassembled) and mirrors the whole desk from it, without ever
  sending anything. Changes go through the device buttons on the *Display* page. The
  state comes with the screen's connection: rcplug now starts before the screen at
  boot (`S05`, existing installs are moved from `S20`), and *Read the state again*
  restarts the screen app (about three seconds) when it was missed, e.g. after an
  update of rcplug. `--desk-mode auto|tap|connect` (default `auto`: reading along on
  a RØDECaster, connecting to the simulator or through a tunnel).
- `make test-root` (also in CI): reading along on the real loopback interface,
  against the simulator.

### Changed
- The init script does not wait for rcplugd at boot, so RØDE's screen is not delayed.

## 0.5.2 - 2026-10-18

### Added
- The console's *Display* page switches the front-panel emulation on and off itself
  (with a confirmation, saved across restarts), so no `config/console.json` is needed;
  `--panel` still switches it on for good. It works at the expert level only.
- The screen mirror's orientation: by default a portrait buffer is turned to
  landscape, which the Duo needs (its GUI draws the 1280×720 screen into a 720×1280
  buffer); *Display* offers automatic, 0°, 90°, 180° and 270°.
- When the mixer is busy, every page of the console points to *Display*, where the
  device can be operated through its buttons.

### Fixed
- Switching the screen mirror on or off without the device state needed a reload of
  the page before the picture appeared.

## 0.5.1 - 2026-10-18

### Added
- Diagnostics: `system/sockets.txt` lists the TCP and UDP sockets (listening and
  connected) and the named unix sockets with the processes owning them, both ends of
  a local connection named, public addresses cut short; `system/dbus.txt` lists the
  names on the D-Bus system bus with their processes.

### Changed
- The console says why the mixer refuses it: when its port is held by another client
  (on the Duo the device's own screen; the mixer serves one client at a time), the
  state is *busy* (`desk_busy`, with the holding process) instead of "not reachable",
  and the console no longer retries by itself, so it can never take the screen's
  place when the screen reconnects. *Reconnect* tries again.
- The console's *Display* page (front panel and screen mirror) and *Log* page work
  without the device state, so the desk can be operated through the front panel while
  the mixer's port is busy; the other pages say why they are empty. Without the state
  the control level comes from the console's status.
- The front-panel tty is opened with `O_NOCTTY`.
- Docs: the first test on a real Duo (flashing from Windows, SSH, installer,
  dashboard and plugins work; the mixer serves one client at a time) in
  docs/device-notes.md, docs/protocol.md, docs/safety.md, the READMEs and the roadmap;
  the front-panel emulation works on it (Listen 1 toggled like the button).

## 0.5.0 - 2026-10-18

rcplug is now three repositories: this one (host, console, SDK, simulator, device
installer), [rcplug-toolkit](https://github.com/Hustenreizjuengling/rcplug-toolkit) (setup,
firmware, flashing, install tools) and
[rcplug-plugins](https://github.com/Hustenreizjuengling/rcplug-plugins) (Sonar and other plugins).
Sonar is no longer part of the device package; install it from rcplug-plugins.

### Added
- Automation on GitHub: every push to `main` publishes the pre-release *Development
  build* (device package and examples, signed); releases can be made with one click
  (*Actions → Release → Run workflow*, `tools/release-version.sh` dates the changelog,
  sets the versions and starts the next one); `govulncheck` runs on every push and
  weekly, CodeQL once the repository is public; Dependabot proposes new versions of
  the GitHub Actions; `make docs-check` checks every link and anchor in the docs.
- Release files are built with the current Go release instead of the minimum version
  from `go.mod` (Go 1.22.0 has known vulnerabilities in `net/http` and `crypto/tls`);
  CI still tests the minimum version.
- In-app updates (dashboard → System → *Updates*, and an *Update* button on plugin
  cards): rcplug and plugins with an `update` source in plugin.json update from
  their latest GitHub release. Nothing is checked, downloaded or installed without a
  click. Releases carry a `release.json` with the SHA-256 of every package, signed
  with Ed25519; rcplugd only accepts keys built into it (plus a plugin's own
  `update.publicKey`), only newer versions, HTTPS only, and unpacks packages without
  links or paths outside their directory. rcplug updates through the new package's
  `install.sh`, started in its own session; the dashboard reloads by itself when the
  new version is up. `RCPLUG_UPDATES=off` switches updates off. See docs/updates.md.
- `rcplug-release`: creates the signing key, signs releases (`release.json` +
  `release.json.sig`) and verifies them; the release workflow signs every release
  with the repository secret `RCPLUG_SIGNING_KEY` and refuses keys rcplugd does not
  trust. `sdk/release` has the format for plugin authors who sign their own releases.
- `make update-test`: an update from 0.8.0 to 0.9.0 and the rollback of a 0.9.1 that
  cannot start, through the API against a fake GitHub; `DUO=<image>` runs it inside
  the stock firmware's userland.
- Documentation for plugin authors: a guide that builds a complete plugin step by
  step (docs/plugin-guide.md), a cookbook with tested recipes (docs/cookbook.md) and
  a reference of the whole plugin interface (docs/plugin-api.md, rewritten). A
  roadmap with plugin ideas (docs/roadmap.md).
- Example plugin `onair`, the result of the guide: a big recording clock for a
  tablet next to the desk, with Server-Sent Events, a setting and tests.
- Plugin actions: plugins declare entry points (`actions` in plugin.json) that other
  UIs show; the console shows `channel` actions on every strip and in the channel
  dialog. Sonar uses it: "◉ Sonar" on a strip opens Sonar for that channel.
- Persistent logs on the device (`<root>/logs/`, rotated) for the host and every plugin.
- Diagnostics bundle: dashboard → System → *Download diagnostics*, `rcplugd
  --diagnostics` and `rcplug-wizard.sh --diagnostics` (also when the web interface is
  down). Plugins can contribute through `GET /api/diagnostics`; the console adds its
  connection statistics and a sanitized copy of the desk state.

### Changed
- `install.sh` keeps the previous version until the new one has started, and puts it
  back (host, init script and the plugins it shipped) if the new one does not start.
- Everything rcplug assumes about the device was checked against the stock Duo
  firmware 1.7.6 and 1.7.3 (files, scripts, device tree, static analysis of
  `rc_audio_mixer` and `rc_gui_juce`), and the installer, init script and setup
  script were run inside both images (aarch64 chroot, the images' own shell and
  tools). docs/device-notes.md lists what the firmware has; the questions only a
  running device can answer are much fewer now.
- Port 2345 is documented from the firmware: it listens on 127.0.0.1 only and serves
  several clients at once, forwarding each change to the others.
- The effects page is an overview per slot with the parameters in a dialog.
- Controls adapt to narrow cards (labels above sliders and steppers); input colours
  are shown as swatches.
- rcplug installs to `/usr/local/rcplug`, the only place the Duo offers; a firmware
  update removes it (see the README).

### Fixed
Found by end-to-end tests of every option (host, dashboard, console, simulator, tools):
- `install.sh`: a failure after the old version was stopped (for example a plugin
  folder that cannot be moved) now puts the previous version back completely before
  starting it, instead of starting a half-updated installation; `.previous` no longer
  stays behind.
- A failed plugin update no longer leaves its download (up to hundreds of MB) in
  `.update/`. Updating a stopped plugin no longer claims it was restarted.
- The diagnostics bundle masks the values of operator settings unless they are plainly
  harmless, and shows URLs (also in command lines and logs) only with scheme and host;
  webhook URLs and tokens under other names used to appear in clear.
- The console's write policy refuses values only the desk writes (recorder state and
  time, progress, auto-mute states, a wireless microphone's serial number) and the
  fader and pot limits at every level, as safety.md always said.
- The GUI guard no longer steps back when another client of a multi-client desk left
  by itself while the console joined: it now looks at which side closed the
  connection.
- `--handshake auto` sends the handshake when no full sync arrives, also if the desk
  sends other messages first; `rccapture` waits for its guard check and reports a
  displaced client (exit status 1).
- Dashboard: a plugin page that is opened while the plugin starts shows a readable
  page that retries by itself; the page shown for a stopped plugin follows its state;
  percent-encoded `..` in deep links and action paths is refused; plugin states are
  dimmed while the host is unreachable.
- Proxy: cross-site WebSocket handshakes are refused; the redirect to
  `/plugins/<name>/` keeps the query; encoded characters in paths reach the plugin
  unchanged.
- If rcplugd itself is killed, helpers its plugins started no longer survive the next
  start (process groups are recorded in `.groups`).
- `state.json` records only the plugins the user started or stopped, so a new version
  can change a plugin's `autostart` default.
- `POST /api/reload` logs skipped plugins and unknown fields like SIGHUP; `invalid` is
  `[]`, not `null`; the dashboard port is taken before plugins get ports.
- Log lines of Go plugins no longer carry two timestamps; long lines are split at
  exactly 64 KB; `rcplugd --check` no longer reports the console refusing a write as
  a problem; plugin diagnostics are collected in parallel.
- `RCPLUG_UPDATES` also accepts `0`, `false` and `no`; `rcplug-release sign` refuses a
  public key given as the signing key; the init script names the right log file when
  rcplugd does not start.
- Console: changes of the control level, screen mirror, PIN (never its value),
  reconnects, panel input and refused PINs are in the audit log; `api/screen.mjpg`
  answers `capture_failed` when the screen cannot be read; unknown `api/` paths answer
  in JSON; `--screen` and `--handshake` refuse unknown values; a missing data directory
  is created; run on its own, the console answers only IP addresses and `localhost`.
- Console UI: meters no longer freeze at their last level when the desk is gone; front
  panel controls and four error messages are translated; strip buttons wrap instead of
  being cut off.
- rcsim no longer adds a property the desk does not have when a fader moves.
- `rcplugd --root` accepts a relative directory: plugins run in their own folder and
  got relative paths (`RCPLUG_DATA_DIR` and the like) that pointed nowhere there.
- Unpacking an update checks its paths with the standard library's `filepath.IsLocal`
  (same rules as before, and recognised by code scanning).
- rcplugd registers its signal handlers before it starts any plugin. Started with
  SIGINT ignored (as a background job of a shell is), it passed that on to the first
  plugins, which then could not stop cleanly and were killed after 3 seconds.
- The screen mirror's automatic mode reads the DRM buffer first. On the Duo the GUI
  draws through DRM, while `/dev/fb0` keeps the boot splash, which the old order
  (framebuffer first unless black) would have shown instead of the screen.
- A plugin whose loopback port was taken by another program restarted endlessly; the
  host now only hands out free ports.
- The gain steppers on the inputs page overflowed their cards when control was on.
- Values in narrow cards broke in the middle of words.

## 0.4.0 - 2026-10-17

A review of the whole project, preparing it for publication.

### Added
- English documentation in `docs/`, a German README, licence and third-party notices.
- Plugin SDK as a public package `sdk/` with a client for the console API
  (`sdk/console`), atomic state files and a server helper with CSRF protection.
- Plugin manifests can carry translations, are validated (`httpProxy` must be
  loopback) and are described by `plugin.schema.json`. Unknown fields are shown as a
  warning.
- Per-plugin data directories (`data/<name>/`) and operator settings
  (`config/<name>.json`) that survive updates.
- Example plugins: `examples/reclog` (Go, web UI, console API) and
  `examples/hello-sh` (POSIX sh).
- `rccapture` records a sanitized device state; `rcsim --fixture` serves it. Together
  they are the starting point for new models (`docs/adding-a-model.md`).
- Model detection with profiles. Models without a verified profile (Pro II) are
  read-only unless `--allow-untested-model` is given.
- Wizard: `--plugin` installs or updates single plugins, `--yes`, `--ask-password`.
- Dashboard, console and Sonar in German and English.
- Console: PIN set in the UI and stored on the device, with lockout after wrong
  attempts; the screen mirror is opt-in.
- CI (tests, race detector, arm64 under QEMU, cross builds, shellcheck, web tests,
  installer test) and a release workflow.

### Changed
- The console UI was rewritten as ES modules: it recovers from reconnects and
  structural changes, keeps no stale views, and is keyboard accessible.
- The *Control* level checks value ranges for every property and only lets values of
  unknown range move in small steps. File paths, power off and more are denied at
  every level.
- Writes are atomic with respect to the mirror and carry the tree's epoch.
- Plugins are stopped with SIGINT to their process group; leftover processes are
  removed.

### Fixed
- An open browser tab reloads after the console restarted (each process starts with a
  new epoch) instead of freezing.
- Installing from a release tarball works (the tarball check failed under `pipefail`,
  and the examples tarball could be picked).
- The installer stages everything before stopping the service, restarts the old
  state if the update fails, keeps the install directory and the dashboard address
  of an existing installation, and moves old state files before replacing a plugin.
- The init script reports failures, and a stale PID file can no longer make it
  signal an unrelated process.
- Sonar opens relative WAV paths inside its data directory; reclog closes a recording
  that ended while it was not running.
- Sonar no longer burns a CPU core with the test source and recovers from failing
  sources.
- The console UI no longer freezes after a structural change and no longer leaks
  screen streams.
- A plugin could run twice after a quick stop and start; the mirror could drift after
  errors; the GUI guard could be bypassed by a pending reconnect.

### Security
- The host answers only to IP addresses and local host names (DNS rebinding) and
  forbids other sites to frame its pages (clickjacking).
- The GUI guard also steps back when a single-client desk drops the console before
  the guard check ran (a GUI that reconnects within the settle time).
- The current show, presets and the phone book are denied; SIP account and
  registration details, streaming and proxy servers and Wi-Fi/Bluetooth scan results
  count as secrets.
- Requests without a PIN no longer count as wrong PIN attempts; PINs are printable
  ASCII.
- The console PIN is no longer passed on the command line; the wizard no longer takes
  passwords as arguments and verifies SSH host keys.

### Upgrade notes
- The console's control level starts at *Read only* again after the upgrade.
- A PIN given with the old `--pin` flag or `RCPLUG_CONSOLE_PIN` is taken over once
  and then stored on the device; set or change it in the console (System → Console)
  from now on.
- Settings that v0.2/v0.3 kept next to the plugin code move to `data/<plugin>/`.
- The wizard's `--password` is gone; use `--ask-password` or `RCPLUG_SSH_PASSWORD`.

## 0.3.0 - 2026-10-17

### Added
- Console plugin: the whole desk mirrored live in the browser, with control levels,
  a deny list, an audit log, a screen mirror and an experimental front-panel
  emulation.
- Simulator of the control port with a real Duo state capture.

## 0.2.0 - 2026-10-17

### Added
- Plugin host with supervision, reverse proxy and dashboard.
- Sonar plugin.
- SSH install wizard and device installer.
