# Changelog

All notable changes. The format follows [Keep a Changelog](https://keepachangelog.com/),
versions follow [Semantic Versioning](https://semver.org/) (before 1.0, minor versions
may break compatibility).

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
