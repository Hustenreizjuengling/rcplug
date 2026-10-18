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
| Init system | SysV init, runlevel 5; `/etc/init.d`, Yocto's `update-rc.d`, BusyBox `start-stop-daemon` | installer (autostart `S20rcplug`) |
| Shell tools | `/bin/sh` is bash; GNU coreutils, sed, grep, gawk, findutils; BusyBox 1.35 for `tar`, `ip`, `wget`, `nc` | installer, init and device scripts stay POSIX sh |
| Storage | only the root partition (ext4, rw, A/B: `mmcblk1p1`/`p2`) and RØDE's own vfat partitions `/Application/update-staging` and `/Application/emmc-data`; a firmware update writes a fresh root partition | installer: `/usr/local/rcplug`, removed by a firmware update |
| Access | OpenSSH: root login with a key only (`PasswordAuthentication no`, root's password is RØDE's); `authorized_keys` is empty in the stock image, so you add your key to the firmware once ([enable-ssh.md](https://github.com/Hustenreizjuengling/rcplug-toolkit/blob/main/docs/enable-ssh.md)). No firewall rules. rcplug ships no credentials. | wizard, setup |
| Desk state | `rc_audio_mixer` serves the state tree on **127.0.0.1:2345**, to several clients at once; the touch-screen GUI `rc_gui_juce` is one of them ([protocol.md](protocol.md)) | console |
| Front panel | `rc_audio_mixer` talks to the fader/button MCU on `/dev/ttyAML3`, 115200 baud, 3-byte MIDI-style control-change messages (`0xBn`, control number, value), both ways | experimental panel emulation (its message table matches the mixer's) |
| Display | `rc_gui_juce` renders in software into a DRM/KMS dumb buffer (XRGB8888) on `/dev/dri/card0`; it does not use `/dev/fb0` (only the boot splash does) | screen mirror (prefers DRM) |
| Audio | one ALSA card; `jackd` (JACK2 1.9.20) holds its capture (`hw:0,3`) and playback (`hw:0,0`) device; the mixer is a JACK client, and the desk's inputs and outputs are JACK ports. JACK tools, `alsa_out`, `arecord` and the `snd-aloop` module are in the image | [Sonar](https://github.com/Hustenreizjuengling/rcplug-plugins/tree/main/sonar)'s opt-in JACK source |
| Ports | the mixer listens on 127.0.0.1:2345 and 127.0.0.1:2346 (internal); the call engine `callme-t` on SIP 5060, RTP 15004–15515 and its own web ports; nothing uses 8080 | dashboard on :8080 |

## Firmware updates and flashing

The firmware format, the USB update path (`M`/`U` over HID, the update disk) and the
USB product IDs of each model and mode are described in the toolkit, which does the
flashing: [rcplug-toolkit/docs/firmware.md](https://github.com/Hustenreizjuengling/rcplug-toolkit/blob/main/docs/firmware.md).
A firmware update writes a fresh root partition, which removes rcplug and the SSH key.

## Assumptions to confirm on hardware

The firmware images answered most earlier questions (see the table above): several
clients on port 2345, its address, the screen source, the install location, port
8080 and the audio path. What only a running device can show:

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
5. **Screen capture as a second DRM client.** rcplug reads the GUI's scanout buffer
   as root; whether the kernel hands it over while the GUI is DRM master is untested.
6. **Sonar's JACK bridge.** CPU load and dropouts when `alsa_out` joins RØDE's audio
   graph, and which JACK port carries which input of the desk
   ([Sonar](https://github.com/Hustenreizjuengling/rcplug-plugins/blob/main/sonar/README.md)).
7. **Flashing from Windows** (toolkit). That Windows gives the update disk a drive letter after
   `M`, that rcplug's check finds the Duo's USB device above it, and that the
   update-mode product ID is `004f` (all as the firmware suggests; see above). If the
   drive check is wrong, `rcplug-flash.exe --drive` names the drive by hand. A run's
   log shows the drive with the USB device it is on (or why a drive was ignored), and
   a flash run the HID interface `U` goes to.

## A careful first test

1. Install with the wizard and open the dashboard. Leave the console on *Read only*.
2. System → *Connection to the mixer*: the state should be `live`. *Show clients*
   should list both the device's GUI and the console.
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
