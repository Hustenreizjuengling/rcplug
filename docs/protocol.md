# The desk's control protocol

The audio process on the device, `rc_audio_mixer`, keeps the complete state of the
desk in a JUCE `ValueTree` and shares it over **TCP port 2345** with
`juce::ValueTreeSynchroniser` messages. The device's own touch screen
(`rc_gui_juce`) is a client of this port. The RØDE desktop app uses the same
messages over USB HID. rcplug's implementation is in
[`internal/rcproto`](../internal/rcproto) (pure encoding and decoding, no I/O) and
[`internal/desk`](../internal/desk) (the connection).

What the server does, read from `rc_audio_mixer` in firmware 1.7.6 and 1.7.3
(static analysis; a JUCE `InterprocessConnectionServer` subclass,
`TCPSyncTreeServerConnection`) and seen on a real Duo with 1.7.6:

- It listens on **127.0.0.1:2345 only**, so a client runs on the device (as rcplug
  does) or comes through an SSH tunnel.
- It serves **one client at a time**. When a client connects, the server stops its
  accept thread and closes the listening socket; only when that client disconnects
  does it listen again. The device's own screen (`rc_gui_juce`) connects at boot and
  holds the connection, so any other client gets "connection refused", and a client
  that came first would lock the screen out. There is no handshake or
  authentication. (An earlier reading of the code took the connection slot for a list;
  the first test on hardware showed otherwise.)
- On connect it sends the full tree at once.
- A second server of the same kind on 127.0.0.1:2346 is an internal link to the
  call engine (`callme-t`), which holds it; rcplug does not use it.
- The mixer also registers an object `/org/rodemicrophones/rodecaster/rcui` on the
  D-Bus system bus. It is not a way to the state: no bus policy lets anyone call it,
  the binary cannot emit the signals its interface declares, and the methods that
  would return the tree are commented out in it.

## What a running Duo sends

Read along on a Duo with firmware 1.7.6 (the screen's connection):

- The full sync when the screen connects: about 200 KB, 905 nodes, 8734 properties.
  The tree differs from the 1.7.4 capture the simulator plays: 390 `MIX` nodes in
  several runs (the first run, 12 sources × 13 outputs, is what the layout uses),
  30 `INPUTSOURCE`, new types such as `RCSYNCMIX` and `STREAMERXSTREAMMIX`.
- `ping` (a 4-byte body) every few seconds, in both directions.
- A **physical fader** moving: only `MIX.mixLevelWithAnchor` of the 13 mixes of the
  channel's source, `"anchor|level"` with level = position / 127 (`0.708661` = 90 =
  unity). The `FADER` node's `faderLevel` is *not* updated; it keeps the value of the
  last full sync. The mixer keeps the position in its meter memory
  ([below](#meters-and-fader-positions)), from which the console takes it.
- **Mute** pressed on the device: `mutePressed` true/false on a node under
  `PHYSICALINTERFACE`, then `CHANNEL.channelOutputMute` and `mixMute` of the
  source's mixes.
- From the screen to the mixer: what the user does there, e.g. `screenTouched`.
- **No meter values**: the `METER` nodes stay at 0 and no level changes pass this
  connection; the screen reads its meters from shared memory
  ([below](#meters-and-fader-positions)).
- A message's 8-byte header and its body usually arrive as two TCP segments.

## Reading along

Because the mixer serves one client and the screen holds it, the console on the
device does not connect. It opens a packet socket on the loopback interface with a
kernel filter for the mixer's port, puts both directions of the screen's TCP
connection back together and applies the messages: from the mixer the full sync and
every change, from the screen every change it sends (the mixer does not echo them;
a full sync from the screen's side is ignored). It never sends anything. Lost
packets, a lost frame boundary or the end of the connection mark the mirror stale
until the screen connects again; the console's *Read the state again* restarts the
screen app for that (`rc_gui_juce`, which RØDE's `display-start.sh` starts again
within about three seconds). When the console stops (an update, a restart of the
plugin) it keeps the mirrored tree and the screen's connection in its data directory
(root only); started again in the same boot within ten minutes, it goes on from there
as soon as that connection carries anything (the Duo pings every few seconds),
aligning on the next message header. Changes made while it was not running may be
missing, which the console says. Implemented in
[`internal/desk/tap.go`](../internal/desk/tap.go), tested against the simulator on the
loopback interface (`make test-root`).

## Full control

Reading along can only watch, so changes go through the front panel, which only
reaches what has a button. *Full control* (experimental, off by default, switched on
at the expert level on the console's *System* page) uses the one-client behaviour
instead: the console takes the mixer's place itself and then serves the device's
screen on the mixer's port, which the mixer does not listen on while it has a client.

1. **Taking the place.** If the mixer listens (at boot, before the screen app has
   connected), the console connects at once. If the screen holds the place, the
   console restarts the screen app (as *Read the state again* does) and connects in
   the moment the mixer listens again, before the screen app is back (about three
   seconds). Once connected it binds the mixer's address (`127.0.0.1:2345`) as soon
   as the mixer has closed its listener; if the mixer keeps listening (another
   firmware), it gives the place back at once.
2. **Relaying.** The screen app connects to the console as it would to the mixer and
   gets the mixer's state as a full sync, then every message passes through unchanged
   in both directions, pings included. A change the console makes goes to the mixer
   and to the screen, which shows it like any other change; so every setting can be
   written, within the console's control levels. Frames for the screen are queued, and
   a screen that does not keep up is disconnected (it reconnects and gets the state
   again); the mixer connection is never held up.
3. **Giving the place back.** When full control is switched off, the console stops
   or crashes, or the screen has not connected for 30 seconds, the console's listener
   closes first and the mixer connection after it, so the mixer can listen again.
   Then the console restarts the screen app (except after a crash), so it connects to
   the mixer afresh, whatever it would do about a lost connection. A crash cannot reverse that order: a duplicate of the
   mixer connection is kept above every other file descriptor, and a process's
   descriptors are closed in ascending order when it ends.

The setting is kept; at the next start the console waits for the mixer and takes the
place before the screen app connects. Implemented in
[`internal/desk/relay.go`](../internal/desk/relay.go), tested against the simulator in
its one-client mode with a screen that reconnects like the device's (`rcsim
--one-client`).

## Transport

Both directions use the same frame:

```
u32 LE  magic 0xF2B49E2C
u32 LE  length of the payload
…       payload: one synchroniser message
```

On TCP no handshake is needed: the desk sends a full sync right after the connection
is opened. USB HID clients start a session with the payload `AD 10 A7 B0`; the console
sends this once only if a TCP desk stays silent (`--handshake auto`).

If a frame starts with the wrong magic, the reader slides forward byte by byte to the
next magic. rcplug treats this, a decoding error or a failed apply on a live session
as a desync: it drops the session and starts over with a fresh full sync.

## Messages

The first payload byte is the message type:

| Type | Name | Body |
|---|---|---|
| 1 | property changed | path, name, value |
| 2 | full sync | the whole tree |
| 3 | child added | path of the parent, index, subtree |
| 4 | child removed | path of the parent, index |
| 5 | child moved | path of the parent, old index, new index |
| 6 | property removed | path, name |

Unknown types are skipped and counted.

## Encodings

**Compressed int** (`juce::InputStream::readCompressedInt`): one size byte, whose low
7 bits give the number of value bytes (0 to 4) and whose high bit marks a negative
number, then the value bytes little-endian.

**Path**: a compressed count, then one compressed child index per level, starting at
the root. An empty path is the root itself.

**String**: UTF-8 followed by a NUL byte.

**Node** (`juce::ValueTree::writeToStream`): type name (string), compressed property
count, per property its name (string) and value, compressed child count, children.

**Value** (`juce::var::writeToStream`): a compressed length that counts the marker
byte and the payload, then:

| Marker | Type | Payload |
|---|---|---|
| 1 | int32 | 4 bytes LE |
| 2 | **true** | none |
| 3 | **false** | none |
| 4 | double | 8 bytes IEEE 754 LE |
| 5 | string | UTF-8 + NUL |
| 6 | int64 | 8 bytes LE |
| 7 | array | compressed count, then values |
| 8 | binary | the remaining bytes |
| 9 | void | none |

A length of 0 is a void value as well. Note that true is 2 and false is 3; at least
one public write-up has them swapped.

rcplug's decoder and encoder reproduce a captured full sync of a Duo (about 156 KB,
870 nodes, 6923 properties) byte for byte, and the decoder is fuzzed
(`make fuzz`).

## Behaviour of the desk

These points matter for any client that writes:

- **No echo.** The desk sends a client's own changes only to the *other* clients.
  A writer has to apply its changes to its own copy itself.
- **Never write properties a node does not have.** The desk creates them, and they
  stay. rcplug only writes existing properties, in their existing type.
- **Only real changes count.** Writing the value a property already has does nothing.
  For actions such as starting a recording again, the console first writes the
  current state and then the requested one.
- **Triggers.** Properties like `mixLinkRequest` or `mixUnlinkRequest` are booleans
  in the tree but are triggered with the binary value `01 01 02 01 01 02`.
- **Physical faders** are not motorised; writing their level has no lasting effect.
  The virtual (on-screen) faders can be written.
- **Keep reading.** A client that stops reading may stall the desk. The console reads
  in its own loop that never blocks and buffers changes for slow browsers.

## Meters and fader positions

The mixer publishes its level meters and the hardware faders' positions in a System V
shared memory segment, key `0x4D173451`, 712 bytes, that the screen app attaches
read-only and reads every 50 ms. Found by analysing the 1.7.6 binaries
(`rc_audio_mixer` creates it, `rc_gui_juce` attaches it with `SHM_RDONLY`); the
segment is also visible as `/SYSV4d173451` in the mixer's `/proc/<pid>/maps`.

| Offset | Content |
|---|---|
| `0x000` | 9 fader records of 12 bytes; the `int32` at +8 is the position 0–127, written when the front panel reports a move |
| `0x06C` | 22 meter records of 20 bytes: `u8` stereo, 3 bytes padding, `float32` peak L, peak R, level L, level R; linear, 1.0 = 0 dBFS |
| `0x224` | Counters the mixer keeps for itself |

The meter records are, in order: the 9 channel strips (0–8), the master (9), the
input, output and gain reduction of the channel open on the device's screen (10–12),
and each strip's gain reduction (13–21; 1.0 = no reduction). Records 0–11 are stereo,
12–21 mono, exactly like the tree's 22 `METER` nodes and their `meterStereo` flags, so
record *k* belongs to `METER` *k*. Nothing locks the segment; the screen app does not
either.

The console attaches the segment read-only (`--meters auto`), reads it 20 times a
second while a browser is watching and once a second otherwise, and writes the
readings into the tree as if the mixer had sent them: each `METER` node's peaks,
levels and stereo flag (a mono meter's reading on both sides, rounded to four
decimals) and the physical `FADER` nodes' `faderLevel`. It applies nothing unless the
tree has one `METER` node per record, finds a restarted mixer's new segment within
seconds, and never writes to the segment (an attachment with `SHM_RDONLY` cannot).
Implemented in [`internal/hw/meters.go`](../internal/hw/meters.go) and
[`internal/console/meters.go`](../internal/console/meters.go).

## The Duo's tree

Firmware 1.7.4, from the capture in `internal/rcsim/fixture`. The root has 702
children. The most important families:

| Node | Count | Notes |
|---|---|---|
| `CHANNEL` | 10 | Channel strip *i* belongs to fader *i*. Duo: 0–3 physical, 4–8 virtual. |
| `PHYSICALINTERFACE` → `FADER` | 9 | `faderLevel` 0–127, unity at about 90 |
| `PHYSICALINTERFACE` → `METER` | 22 | 0–8 channel strips, 9 master, 10–12 the channel open on the screen (input, output, gain reduction), 13–21 gain reduction per strip ([meters](#meters-and-fader-positions)) |
| `MIX` | 247 | 19 sources × 13 outputs, source-major |
| `INPUTSOURCE` | 19 | Gain, phantom power, input type per source |
| `HEADPHONE` | 4 | |
| `RECORDER`, `PLAYER` | 1 each | `requestRecordState` / `recordState`: 0 stop, 1 pause, 2 record |
| `SOUNDPADS` → `PAD` | 21 | Pads in use; `PADBUTTON` are the physical buttons |
| `EFFECTS_PARAMETERS` | 11 | |
| `SYSTEM`, `NETWORK`, `AUDIO`, `BUILD`, `GUI`, `OAUTH`, `SIP*`, `RADIO` … | | System, connectivity, accounts, calls, wireless microphones |

Sources (index → meaning) and outputs are named in
[`internal/rcproto/model.go`](../internal/rcproto/model.go). The positions of all
families are found at run time by `rcproto.DiscoverLayout`, not hard-coded.

## Sources

The wire format is JUCE's (`juce_Variant.cpp`, `juce_ValueTreeSynchroniser.cpp`);
rcplug's implementation is written independently. Facts about the RØDECaster came
from these projects, cross-checked against the capture:

- [Yeradon/rodecaster-protocol](https://github.com/Yeradon/rodecaster-protocol), also
  the origin of the Duo capture
- [gcdkgrnvle/companion-module-rode-rodecaster2](https://github.com/gcdkgrnvle/companion-module-rode-rodecaster2)
- [seanheiney/rodey](https://github.com/seanheiney/rodey)
- [cristalcorp/rodecasterpro2-linux](https://github.com/cristalcorp/rodecasterpro2-linux)
- [phihag/rc2http](https://github.com/phihag/rc2http) (front panel and device)
