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
| Audio | one ALSA card; `jackd` (JACK2 1.9.20) holds its capture (`hw:0,3`) and playback (`hw:0,0`) device; the mixer is a JACK client, and the desk's inputs and outputs are JACK ports. JACK tools, `alsa_out`, `arecord` and the `snd-aloop` module are in the image | [Sonar](https://github.com/Hustenreizjuengling/rcplug-plugins/tree/main/sonar)'s opt-in JACK source |
| Ports | the mixer listens on 127.0.0.1:2345 and 127.0.0.1:2346 (internal) only until their one client (the screen, the call engine) has connected; the call engine `callme-t` on SIP 5060, RTP 15004–15515 and its own web ports; nothing uses 8080 | dashboard on :8080 |

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

1. **Meter mapping.** `METER` 0–8 are the channel strips and 9–21 the outputs (derived
   from their count). The meters are separate `meterLevelL`/`meterLevelR` and
   `meterPeakL`/`meterPeakR` properties.
2. **Meter scale.** The mixer stores them as numbers without a unit; linear 0–1 or
   dB, the UI detects either.
3. **Integer meanings** of input type, microphone type and pad type. They are C++
   enums in the firmware, not readable tables; the UI always shows the raw value next
   to its interpretation.
4. **Gain ranges.** The real ranges of the input gains are not known; the *Control*
   level only lets them move in small steps until they are.
5. **Sonar's JACK bridge.** CPU load and dropouts when `alsa_out` joins RØDE's audio
   graph, and which JACK port carries which input of the desk
   ([Sonar](https://github.com/Hustenreizjuengling/rcplug-plugins/blob/main/sonar/README.md)).
6. **Meters.** The mixer does not send meter values over the screen's connection
   ([protocol.md](protocol.md#what-a-running-duo-sends)); where the screen gets them
   is not known yet, so the console shows no meters on the device.
7. **USB audio during an rcplug update.** During each update so far the computer's
   playback through the Duo went silent until another output was chosen and the Duo
   again. The kernel log shows the Duo's USB device set up anew about half a minute
   after each update (`dwc2 … new device is high-speed`), most likely when the
   computer selected it again, so the silence starts earlier. The device has about
   120 MB of memory to spare; since 0.6.2 the update log records each step with the
   free memory, and the diagnostics include RØDE's own logs, to find the cause.
8. **Operating the screen** (0.6.2, experimental). RØDE's screen app reads its
   touchscreen from `/dev/input/event0`/`event1` (evdev, no libinput); which one is the
   touchscreen, its axis ranges and orientation are in the diagnostics
   (`system/input-devices.txt`). Whether taps land where clicked is to be confirmed
   on the device.

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
  mixer, so the console's *Display* page (front panel and screen mirror) can operate
  the desk although the screen holds the mixer's port: on the console's *Display*
  page choose *Switch on the device buttons* and the *Expert* level (since 0.5.2; 0.5.1
  needed `--panel`). Every button of the device, the wheel and the pads worked from
  the browser.
- **The screen mirror works next to the GUI** (DRM, 2 frames per second). The GUI
  draws its 1280×720 landscape screen into a 720×1280 buffer whose top is on the
  right; since 0.5.2 the mirror turns it.
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

If anything looks wrong, `ssh root@<ip> /etc/init.d/rcplug stop` stops rcplug and all
plugins at once (until the next boot), and `rcplug-wizard.sh --host <ip> --uninstall`
removes it completely.

## Pro II

The RØDECaster Pro II uses the same protocol and a similar tree. rcplug recognises it
(`SYSTEM.boardType` 0) and shows it read-only, because no capture of its tree has been
checked yet. [adding-a-model.md](adding-a-model.md) describes how to change that.
