# Safety

rcplug runs as root on a device that people use live on air. Its defaults are chosen
so that installing it changes nothing about how the desk behaves, and so that
mistakes, other web pages and other people on the network cannot easily do harm.
This page explains each safeguard and its limits.

## Audio comes first

- The host lowers all of its threads to the lowest CPU priority (nice 19) before it
  starts any plugin, and plugins inherit it.
- The console never blocks the desk: it reads the control port in its own loop and
  hands changes to browsers in batches every 60 ms, dropping intermediate values.
- The screen mirror captures at most 2 frames per second, shared by all viewers, with
  at most 2 MJPEG streams at once. It is off until a user switches it on.

## The device's own screen: the GUI guard

The touch screen is a client of the same control port the console uses, and the
firmware serves **one client at a time** (1.7.3 and 1.7.6, seen on a real Duo; see
[protocol.md](protocol.md)): while the screen is connected nobody else gets in, and a
client that connects before the screen would lock it out. On a RØDECaster the console
therefore **does not connect at all**: it reads along with the screen's connection
(`--desk-mode auto`, see [protocol.md](protocol.md#reading-along)), which cannot
disturb it, and changes the desk only through the front panel – unless a user switches
on the experimental *full control* (below). *Read the state
again* restarts the screen app once (about three seconds; RØDE's own scripts restart
it the same way, and the mixer keeps running); it is rate-limited and needs the PIN
if one is set. To go on after its own restart, the console keeps the mirrored tree in
its data directory, readable by root only, and uses it once. Where the console does connect (`--desk-mode connect`, e.g. through a
tunnel), it:

1. **never retries a busy desk by itself**: when the port refuses connections while
   another client holds it, the console shows *busy* (`desk_busy`, naming the
   process, normally `rc_gui_juce`) and waits for an explicit *Reconnect*. A retry
   could otherwise take the slot in the moment the screen reconnects after a restart;
2. **waits after boot** until the system has been up for 75 s, so the screen is
   connected first (`--boot-grace`);
3. **compares connections**: before connecting it reads all established connections
   to port 2345 from `/proc/net/tcp`; two seconds after the full sync it reads them
   again;
4. **steps back for good** if a connection that existed before has disappeared: it
   disconnects at once and enters the `conflict` state. The same check runs at once
   if the desk drops the console before the two seconds are over (a single-client desk
   whose GUI reconnected). The console does **not** retry on its own. It shows the
   reason; "Reconnect" (rate-limited, PIN-protected) tries again.

System → *Connection to the mixer* → *Show clients* lists the processes connected to the
port. The guard only works when the console runs on the device itself (it needs both
ends of the connections in `/proc/net/tcp`). `--guard=false` turns it off, which is not
recommended.

## What the console may change

### Control levels

| Level | Allows |
|---|---|
| **Read only** (default) | No change to the desk. The console's own switches stay available: the screen mirror and reconnecting (both protected by the PIN, if one is set). |
| **Control** | A curated list of everyday controls, each with a type and a valid range (see below). |
| **Expert** | Every existing property that is not on the deny list, with type checks and the node's own limits (such as a fader's min and max) but without the curated ranges. |

The level is stored on the device, shared by all browsers and kept across restarts. A
fresh install starts at *Read only*. Switching to *Control* or *Expert* asks for
confirmation (and the PIN, if one is set).

*Write protection* on the console's *System* page (behind the PIN, if one is set)
turns off all writes regardless of the level; `--read-only` (for example in
`config/console.json`: `{"args": ["--read-only"]}`, then restart the console) does the
same for good, and the page cannot switch it off. Models without a verified profile (currently
everything except the Duo) are read-only unless the console is started with
`--allow-untested-model`.

### Always refused

At every level, including *Expert*:

- firmware update, download, reset and reboot requests, power off, disabling all
  physical buttons;
- the serial number, board type, firmware version and other identity values;
- storage volumes, recordings, shows, the current show and show management (delete,
  format, import, export), effect and streaming presets, the phone book, clearing the
  pad recorder;
- network, proxy, Wi-Fi, accounts (OAuth), calls (SIP), wireless microphone pairing,
  factory test modes, build information, app and audio-engine settings;
- every property whose name contains `path` (the desk runs as root, so a writable path
  could point anywhere);
- every secret (see below);
- meter readings and other values only the desk writes (recorder state and time,
  playback and pad progress, auto-mute states, a wireless microphone's serial
  number);
- the limits a node checks values against (a fader's or pot's minimum and maximum),
  since writing them would lift the limits.

The list is `denied` in [`internal/console/policy.go`](../internal/console/policy.go).

### The *Control* level

Allowed, with ranges: channel mute, listen, source, processing (APHEX, compressor,
gate, de-esser, EQ, high-pass) and pan; the **virtual** faders (0–127; physical faders
are not motorised); routing cells (mute, enable, level, link); input gain in steps of
at most 10, phantom power and phase; monitor, Bluetooth and headphone levels and mutes;
the master compressor and delay (0–2 s); ducking; emergency mute; recording (stop,
pause, record) and markers; effects; pad presses, pad gain and the pad bank; screen
and button brightness.

Values must have the right type (numbers are not accepted as strings) and lie within
the property's range. Where the real range of a value is not known, the console only
lets it move in small steps.

### Never new properties

The desk creates any property a client writes, and keeps it. The console only writes
properties that exist on the node, in the type they already have, so it cannot leave
anything behind in the device's state.

### Stale paths

Paths address nodes by child index. A write carries the tree's epoch, and is refused
(`stale_epoch`) if the tree's structure changed in between, for example because a show
was loaded. It can therefore never hit the wrong node.

### Audit log

Every write to the desk, allowed or refused, and every change to the console itself
(control level, screen mirror, PIN, reconnect, front panel) is listed on the console's
*Log* tab with time, node, property, value and client address; PINs are never
recorded. Requests refused before they reach the console's checks (a missing
`X-Rcplug` header) are not listed. The log keeps the last 300 entries in memory and
starts empty when the console restarts; the plugin log on the dashboard has a line for
each write as well.

The PIN lockout (five wrong attempts within five minutes) applies to everyone: someone
on the network who tries wrong PINs locks everybody out for a minute. That is the price
of having no user accounts.

## Secrets

Properties that hold credentials or personal data are never sent to a browser and
never written: anything named like a password, passphrase, PSK, secret, token, user
name, pairing code, user code, call details or call address, paired phone numbers, the
account and server details of calls, streaming and proxies, the Wi-Fi and Bluetooth
scan results (other people's networks and phones) and the OAuth verification URL. The
API sends `"••••••"` instead. The list is
`rcproto.Secret` in [`internal/rcproto/sanitize.go`](../internal/rcproto/sanitize.go).

Captures made with `rccapture` replace every string except structural ones, see
[adding-a-model.md](adding-a-model.md).

The **diagnostics bundle** is meant to be attached to bug reports, also public ones.
It masks the values of the operator settings (`config/*.json`, `config/rcplugd.env`)
unless they are plainly harmless (numbers, durations, switches, loopback addresses,
paths) or one of rcplug's own host settings, shows URLs only with their scheme and
host, everywhere including the process list, and leaves out secret-looking command-line
arguments. Review a bundle before you post it anyway.

## Web security

- **No cross-site control.** Every state-changing request needs the header
  `X-Rcplug`. Browsers do not send custom headers to another site without a CORS
  preflight, and rcplug never answers one. In addition the host refuses
  state-changing requests whose `Origin` belongs to another site.
- **No DNS rebinding.** The host only answers requests whose `Host` is an IP address,
  `localhost`, the device's host name, a local name (`.local`, `.lan`, `.home`,
  `.home.arpa`, `.internal`, `.localdomain`, `.fritz.box`) or a name added with
  `RCPLUG_ALLOW_HOST` (in `config/rcplugd.env`, see
  [configuration.md](configuration.md)). A foreign domain that resolves to the device
  gets 421.
- **Plugins listen on loopback only**; the host is the single entry point.
- **WebSockets.** Browsers cannot add `X-Rcplug` to a WebSocket handshake, so the host
  refuses handshakes whose `Origin` belongs to another site.
- **The console run on its own** (during development, without rcplugd in front)
  answers only requests addressed to an IP address or `localhost`.
- **PIN.** An optional PIN protects every change in the console, including the control
  level and reconnecting. It is stored in the console's data directory with mode 0600,
  compared in constant time, and five wrong attempts within five minutes lock it for
  a minute (requests that carry no PIN at all do not count). PINs consist of printable
  ASCII characters. Use more than four digits. A forgotten PIN is removed at the
  device, without a shell: *Forgot the PIN?* on the *System* page opens a minute in
  which pressing channel 1's Listen button four times within four seconds removes the
  PIN and sets the level to *Read only*. Over the network those buttons can only be
  pressed with the PIN (front panel, touchscreen, writes), and four presses in so
  short a time do not happen by chance; every reset is in the console's log.
  (Without the console running, deleting `<install dir>/data/console/pin` and
  restarting the console still works.)

### Limits

There are **no user accounts**. The PIN protects changes to the desk, nothing else:
everyone who can reach the device on the network can read the whole state (including
the serial number, network addresses and the names of paired devices), watch the
screen mirror once it is switched on, start, stop or restart plugins on the
dashboard, and install the latest signed release of rcplug or a plugin (never
anything unsigned or older, see [updates.md](updates.md)). The traffic is plain HTTP. rcplug is made for a home or studio network you
trust. Do not expose port 8080 to the internet.

## Experimental features

- **Front-panel emulation** injects the same messages that the panel's microcontroller
  sends into the input queue of its serial line (`/dev/ttyAML3`, with `TIOCSTI`), so
  buttons, the encoder and faders can be operated remotely. It is off until a user
  switches it on (*Display* page, with a confirmation) or the console runs with
  `--panel`, and it works only at the *Expert* level. Every press is in the console's
  log. An emulated fader jumps back when the real one moves.
- **Operating the screen** from the mirror writes taps and swipes into the
  touchscreen's input device (`/dev/input/event*`), which RØDE's screen app reads
  directly, exactly as a finger's would arrive. It is off until a user switches it on
  (*Display* page, with a warning), needs the screen mirror on and the *Expert* level,
  allows one gesture every 80 ms, and every gesture is in the console's log. A tap
  that lands beside its target triggers whatever is there: try it on a harmless spot
  first; *Touch correction* mirrors an axis if taps land mirrored.
- **Screen mirror** reads the DRM scanout buffer the GUI draws into (`/dev/dri/card0`)
  or, if that cannot be read or is black, `/dev/fb0` (on the Duo that only holds the
  boot splash). The Duo's GUI draws its landscape screen into a portrait buffer, so
  the mirror turns such a picture by 90° (the orientation can be changed on the
  *Display* page). It only reads, never draws. A user must switch it on (*Display* tab), since the
  screen can show private information.
- **Full control** lets the console take the mixer's one connection and serve the
  device's screen through itself, so every setting can be changed
  ([protocol.md](protocol.md#full-control)). It is off until a user switches it on at
  the *Expert* level (*Display* page, with a warning); switching it on restarts the
  screen app once. The console always closes its own listener before it lets go of the
  mixer, also when it crashes, so the mixer can listen again and the screen finds it;
  a screen that does not keep up is disconnected rather than ever holding up the
  mixer; without the screen for 30 seconds the console steps back. Audio does not pass
  through this connection. Switching it off (or `--desk-mode connect`) returns to
  reading along.
- **Meters** come from the shared memory in which the mixer keeps them for its screen
  ([protocol.md](protocol.md#meters-and-fader-positions)). The console attaches it
  read-only, like the screen app, and only copies it; it is on by default because it
  cannot change anything, and `--meters off` switches it off.

## Updates

rcplug contacts GitHub only when someone presses *Check for updates*, and installs
only what is clicked. Releases are accepted only with a valid signature by a key built
into rcplugd, every file must match its SHA-256 in the signed manifest, only newer
versions are offered, and packages are unpacked without links or paths outside their
directory. An update of rcplug that does not start is rolled back by its installer.
`RCPLUG_UPDATES=off` switches the function off. Details: [updates.md](updates.md).

## Installing and removing

Installing is done by [rcplug-toolkit](https://github.com/Hustenreizjuengling/rcplug-toolkit):

- The wizard asks before it changes anything and shows what it will do
  (`--dry-run` only checks). It never stores the device's password or puts it on a
  command line: with `--ask-password` it is read from the terminal and handed to
  `sshpass` through the environment.
- The SSH host key is remembered on first use and checked afterwards.
- Everything rcplug installs lives in one directory (including its logs) plus
  `/etc/init.d/rcplug`. `--uninstall` removes all of it, including plugin data.
