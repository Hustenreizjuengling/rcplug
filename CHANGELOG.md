# Changelog

All notable changes. The format follows [Keep a Changelog](https://keepachangelog.com/),
versions follow [Semantic Versioning](https://semver.org/) (before 1.0, minor versions
may break compatibility).

## 0.5.0 - Unreleased

rcplug is now three repositories: this one (host, console, SDK, simulator, device
installer), [rcplug-toolkit](https://github.com/Hustenreizjuengling/rcplug-toolkit) (setup,
firmware, flashing, install tools) and
[rcplug-plugins](https://github.com/Hustenreizjuengling/rcplug-plugins) (Sonar and other plugins).
Sonar is no longer part of the device package; install it from rcplug-plugins.

### Added
- Plugin actions: plugins declare entry points (`actions` in plugin.json) that other
  UIs show; the console shows `channel` actions on every strip and in the channel
  dialog. Sonar uses it: "◉ Sonar" on a strip opens Sonar for that channel.
- Persistent logs on the device (`<root>/logs/`, rotated) for the host and every plugin.
- Diagnostics bundle: dashboard → System → *Download diagnostics*, `rcplugd
  --diagnostics` and `rcplug-wizard.sh --diagnostics` (also when the web interface is
  down). Plugins can contribute through `GET /api/diagnostics`; the console adds its
  connection statistics and a sanitized copy of the desk state.

### Changed
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
