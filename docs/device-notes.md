# Device notes

What rcplug relies on about the RØDECaster Duo, and what still needs to be confirmed
on hardware. Corrections from people with a device are very welcome; please use the
"Device report" issue template.

## What rcplug relies on

Checked against the stock firmware images 1.7.6 and 1.7.3 (`os.tar.gz` from RØDE's
update server): their files, scripts and device tree, and static analysis of
`rc_audio_mixer` and `rc_gui_juce`. The installer, the init script and the setup's
device script were also run inside both images (an aarch64 chroot emulating a
Cortex-A53, with the images' own shell and tools). The two versions agree on
everything below.

| Topic | What the firmware has | Used by |
|---|---|---|
| CPU and OS | Amlogic AXG (A113D), 4× Cortex-A53, aarch64; Yocto (Poky), kernel 5.10.17 with PREEMPT_RT | static arm64 builds |
| Init system | SysV init, runlevel 5; `/etc/init.d`, Yocto's `update-rc.d`, BusyBox `start-stop-daemon` | installer (autostart `S05rcplug`, before RØDE's screen `S06rode-display-service`; `S20` before 0.6) |
| Shell tools | `/bin/sh` is bash; GNU coreutils, sed, grep, gawk, findutils; BusyBox 1.35 for `tar`, `ip`, `wget`, `nc` | installer, init and device scripts stay POSIX sh |
| Storage | only the root partition (ext4, rw, A/B: `mmcblk1p1`/`p2`) and RØDE's own vfat partitions `/Application/update-staging` and `/Application/emmc-data`; a firmware update writes a fresh root partition | installer: `/usr/local/rcplug`, removed by a firmware update |
| Access | OpenSSH: root login with a key only (`PasswordAuthentication no`, root's password is RØDE's); `authorized_keys` is empty in the stock image, so you add your key to the firmware once ([enable-ssh.md](https://github.com/Hustenreizjuengling/rcplug-toolkit/blob/main/docs/enable-ssh.md)). No firewall rules. rcplug ships no credentials. | wizard, setup |
| Desk state | `rc_audio_mixer` serves the state tree on **127.0.0.1:2345** to **one client at a time**; the touch-screen GUI `rc_gui_juce` connects at boot and holds it, so nobody else gets in (seen on a real Duo, see below and [protocol.md](protocol.md)) | console |
| Front panel | `rc_audio_mixer` talks to the fader/button MCU on `/dev/ttyAML3`, 115200 baud, 3-byte MIDI-style control-change messages (`0xBn`, control number, value), both ways | experimental panel emulation (its message table matches the mixer's; works on a real Duo, see below) |
| Display | `rc_gui_juce` renders in software into a DRM/KMS dumb buffer (XRGB8888) on `/dev/dri/card0`; it does not use `/dev/fb0` (only the boot splash does) | screen mirror (prefers DRM) |
| Audio | one ALSA card; `jackd` (JACK2 1.9.20) holds its capture (`hw:0,3`) and playback (`hw:0,0`) device; the mixer is a JACK client, and the desk's inputs and outputs are JACK ports (see [The JACK graph](#the-jack-graph)). JACK tools, `alsa_out`, `arecord` and the `snd-aloop` module are in the image | the shared audio taps ([plugin-api.md](plugin-api.md#audio)) |
| Ports | the mixer listens on 127.0.0.1:2345 and 127.0.0.1:2346 (internal) only until their one client (the screen, the call engine) has connected; the call engine `callme-t` on SIP 5060, RTP 15004–15515 and its own web ports; nothing uses 8080 | dashboard on :8080 |

## The JACK graph

How a Duo with firmware 1.7.6 wires its audio (from `jack_lsp -c -p`, which Sonar adds
to the diagnostics bundle while it runs). The mixer is the JACK client `JUCEJack` with
inputs `in_1` … `in_38` and outputs `out_1` … `out_46`:

| Ports | Connected to | What it is |
|---|---|---|
| `system:capture_1` … `_4` | `JUCEJack:in_1` … `_4` | the device's inputs (the microphone inputs) |
| `usb1in:capture_1`, `_2` | `in_7`, `in_8` | USB 1 from the computer |
| `usb2in:capture_1`, `_2` | `in_9`, `in_10` | USB 2 |
| `usb1-chat-in:capture_1`, `_2` | `in_11`, `in_12` | the chat channel |
| `sip-audio-in:capture_1` … `_6` | `in_29` … `in_34` | the three calls |
| `RodeRfSPI:TX1`, `TX2` | `in_35`, `in_36` | RØDE wireless |
| `JUCEJack:out_1` … `_8` | `system:playback_1` … `_8` | the headphone and speaker outputs |
| `JUCEJack:out_15`, `_16` | `usb1out:playback_1`, `_2` | to the computer over USB 1 |
| `JUCEJack:out_17`, `_18` | `usb1-chat-out` | the chat to the computer |
| `JUCEJack:out_19`, `_20` | `usb2out` | USB 2 to the computer |
| `JUCEJack:out_21` … `_26`, `_45`, `_46` | `sip-audio-out` | the calls |

The other mixer outputs (`out_9` … `_14`, `_27` … `_44`) are not connected; which
buses they carry is not known yet. The shared audio taps can listen to any output port
above; with rcplug 0.7.4 their bridge (two channels) costs about 1 % of the device
(0.7.2, with eight channels: 13 %).

## Processing values

The desk stores a channel's processing settings as 0–1 without units. What they mean
comes from the device's own screen app (`rc_gui_juce`, firmware 1.7.6): its parameter
classes turn a stored value *v* into the unit the screen shows, each with its range.
The console uses the same scales (`internal/console/web/js/scales.js`).

| Settings | Range | From *v* |
|---|---|---|
| EQ low / mid / high frequency | 20–200 Hz / 200–2000 Hz / 2–12 kHz | lo + (hi − lo) · v² |
| EQ gains | −12 to +12 dB | lo + (hi − lo) · v, v rounded to 0.01 |
| EQ Q | 0.1–10 | lo + (hi − lo) · v |
| High-pass frequency, slope | 20–200 Hz; 6–24 dB/octave | v²; linear, rounded (steps of 1/3) |
| Compressor threshold, ratio, make-up | −60 to 0 dB; 1.5–4.5 : 1; 0–9 dB | linear (0.01 steps); v²; linear |
| Compressor attack, release | 0.1–10 ms; 5–200 ms | lo · (hi / lo)^v |
| De-esser threshold, ratio, gain | −60 to 0 dB; 1–10 : 1; 0–9 dB | linear; v²; linear |
| De-esser frequency, attack, release | 1–8 kHz; 0.1–10 ms; 50–2000 ms | lo · (hi / lo)^v |
| Noise gate threshold | −60 to 0 dB | linear (0.01 steps) |
| Noise gate attack, hold, release | 0.1–1000 ms; 50–2000 ms; 50–2000 ms | lo · (hi / lo)^v |
| Noise gate range | 0–100 dB | 100 · √(1 − v), rounded |
| Noise gate hysteresis | 0–30 dB below the threshold | 30 · v (the screen's gate graph) |
| APHEX exciter tune, big bottom tune | 600–5000 Hz; 60–312 Hz | v² |

The defaults come out round: low shelf 100 Hz, mid 400 Hz, high 10 kHz, Q 0.3 and
0.5, high-pass 70 Hz at 12 dB/octave, compressor 2 : 1, gate release 200 ms.

The screen's graphs, which the console draws the same way:

- **EQ:** the three bands as bell filters (also the outer two, whose wide Q makes them
  look like shelves) at 48 kHz, A = 10^(gain/40), α = sin ω / 2Q, with the high-pass
  filter; frequencies from 20 Hz to 22 kHz.
- **Compressor:** output over input level from −60 to +6 dB, a hard knee drawn
  rounded, make-up gain added; a dot at the channel's level on the curve.
- **Noise gate:** the channel's level over about five seconds (+6 to −64 dB), open
  above the threshold and until it falls below the threshold minus the hysteresis,
  with both lines.
- **De-esser:** a bell (Q 1.7) at its frequency, as deep as the current reduction, at
  the height of the threshold.

## Firmware updates and flashing

The firmware format, the USB update path (`M`/`U` over HID, the update disk) and the
USB product IDs of each model and mode are described in the toolkit, which does the
flashing: [rcplug-toolkit/docs/firmware.md](https://github.com/Hustenreizjuengling/rcplug-toolkit/blob/main/docs/firmware.md).
A firmware update writes a fresh root partition, which removes rcplug and the SSH key.

## Assumptions to confirm on hardware

The firmware images answered most earlier questions (see the table above): the
mixer's address, the screen source, the install location, port 8080 and the audio
path; the first test on a real Duo answered the number of clients on port 2345 and
flashing from Windows (below). What is still open:

1. **Meters** (0.6.3). The mixer keeps them in shared memory, not on the control port
   ([protocol.md](protocol.md#meters-and-fader-positions)); their order (strips,
   master, the channel open on the screen, gain reductions), the linear scale and
   which value is the peak come from analysing the firmware. To confirm on the device:
   that the strips' meters move with their inputs, that the master follows the mix,
   and that a physical fader moved by hand shows at the same strip in the console.
2. **Integer meanings** of input type, microphone type and pad type. They are C++
   enums in the firmware, not readable tables; the UI always shows the raw value next
   to its interpretation.
3. **Gain ranges.** The real ranges of the input gains are not known; the *Control*
   level only lets them move in small steps until they are.
4. **The audio taps' bridge.** *Confirmed on a real Duo (0.7.2–0.7.4):* it works,
   Sonar hears the desk through it, and since 0.7.4 it costs about 1 % of the device
   without overruns; which port carries which input is in
   [The JACK graph](#the-jack-graph). Still open: which buses the unconnected mixer
   outputs carry.
5. **USB audio during an rcplug update.** During updates the computer's playback
   through the Duo went silent; once (0.6.1 → 0.6.4) the computer lost the Duo as a
   sound device until it was unplugged and plugged in again, while the desk itself
   kept working. Memory was not the cause (134 MB free throughout). The kernel log
   shows the Duo's USB interface stalling (`dwc2 … ep_sethalt`, earlier
   `ep_stop_xfr: timeout`) a few seconds before the installer started, i.e. while
   rcplugd downloaded and unpacked the package at full speed. Since 0.6.5 updates
   download at 256 KB/s, unpack slowly in small flushed pieces and run at the lowest
   I/O priority. The update to 0.6.5, still downloaded by 0.6.4 at full speed, stalled
   the interface again; the computer reset it about 45 seconds later (`ep_stop_xfr:
   timeout`, then a new enumeration), as after the earlier updates. To confirm with the
   first update installed *by* 0.6.5.
6. **Full control.** *Confirmed on a real Duo (0.7.4, firmware 1.7.6):* the takeover
   took four seconds (the session-open handshake was needed), the screen app worked
   normally through the console, and mute, listen, pads, routing and channel
   processing changed in the browser showed on the device; nothing was refused. Since
   0.8.0 it is how the console works on the device. Still to watch: the takeover at
   boot, and the screen finding the mixer again when the console stops (both behave as
   designed in the simulator).

## First test on a real Duo

On 2026-10-18 rcplug ran on a real Duo for the first time (firmware 1.7.3, flashed
from Windows to a patched 1.7.6):

- **Flashing from Windows works** as the toolkit describes it: the Duo appeared as USB
  product `0050`, after `M` as `004F` with an unlabelled FAT update disk of 273 MB, the
  archive was copied, `U` was acknowledged and the device came back with the new
  firmware.
- **SSH** with the key added to the image, **rcplug's installer** under the real init
  system, the **dashboard** on port 8080 and the plugins work.
- **The console cannot reach the mixer.** The mixer serves one client at a time
  ([protocol.md](protocol.md)) and the screen holds it, so the console's connection is
  refused. Its boot grace kept it from taking the screen's place at boot; it now shows
  *busy* and does not retry by itself. The mixer's D-Bus object is no way around this.
  Since 0.6 the console reads the screen's connection along instead
  ([protocol.md](protocol.md#reading-along)) and operates the desk through the front
  panel; reading along is tested against the simulator and still to be confirmed on
  the device.
- **The front-panel emulation works**: injecting *Listen 1* (`b0 19 00`, 50 ms,
  `b0 19 01` into `/dev/ttyAML3` with `TIOCSTI`) toggled it exactly like the button,
  LED included, and a second press turned it off again. It needs no connection to the
  mixer, so the console could operate the desk from its *Display* page although the
  screen holds the mixer's port (since 0.5.2; 0.5.1 needed `--panel`). Every button of
  the device, the wheel and the pads worked from the browser. (The *Display* page,
  with a screen mirror and operating the screen by touch, was removed in 0.6.7; the
  console now presses mute and listen and sets the physical faders right from its
  mixer; since 0.8.0 there is no switch for the device buttons.)
- **The screen mirror worked next to the GUI** (DRM, 2 frames per second; removed in
  0.6.7). The GUI draws its 1280×720 landscape screen into a 720×1280 buffer whose top
  is on the right.
- **A physical fader set from the console works** (0.6.2, expert level): the device
  follows the position sent through the front panel, and the real fader takes over
  again as soon as it is moved.
- **Reading along works** (0.6.0): after *Read the state again* (the screen app
  restarted, about three seconds) the console mirrored the whole desk; changes on the
  device arrive as described in [protocol.md](protocol.md#what-a-running-duo-sends).
  Since 0.6.1 physical faders follow (from the mix levels), and mute and listen of the
  four physical channels can be switched from the console's pages (pressed on the
  front panel); since 0.6.2 also the physical faders (at the expert level), and after
  an update the console goes on without *Read the state again*.
- **Diagnostics** now include the network and unix sockets with their processes and
  the names on the D-Bus system bus, which is what this needed.

## A careful first test

1. Install with the wizard and open the dashboard. Leave the console on *Read only*.
2. System → *Connection to the mixer*: the state should be `live`. *Show clients*
   should list both the device's GUI and the console. (With firmware 1.7.x the console
   shows *busy* for now; see above.)
3. Use the device as usual for a few minutes: the touch screen must keep working,
   audio must be unaffected. Press a mute on the device and watch it change in the
   browser.
4. Only then switch to *Control* and mute a channel from the browser.
5. Download the diagnostics (dashboard → System) and attach them to a device report
   together with what you saw. They contain what the console found for most of the
   assumptions above.

If anything looks wrong, the toolkit's setup window stops rcplug and all plugins at once
(*Diagnostics and more* → *Stop rcplug*, until it is started again or the device
restarts; on the command line `rcplug-setup stop`), and *Uninstall rcplug* removes it
completely. Neither needs the dashboard.

## Pro II

The RØDECaster Pro II uses the same protocol and a similar tree. rcplug recognises it
(`SYSTEM.boardType` 0) and shows it read-only, because no capture of its tree has been
checked yet. [adding-a-model.md](adding-a-model.md) describes how to change that.
