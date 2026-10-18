# The desk's control protocol

The audio process on the device, `rc_audio_mixer`, keeps the complete state of the
desk in a JUCE `ValueTree` and shares it over **TCP port 2345** with
`juce::ValueTreeSynchroniser` messages. The device's own touch screen
(`rc_gui_juce`) is a client of this port. The RØDE desktop app uses the same
messages over USB HID. rcplug's implementation is in
[`internal/rcproto`](../internal/rcproto) (pure encoding and decoding, no I/O) and
[`internal/desk`](../internal/desk) (the connection).

What the server does, read from `rc_audio_mixer` in firmware 1.7.6 and 1.7.3
(static analysis; a JUCE `InterprocessConnectionServer` subclass, `SyncMultiServer`):

- It listens on **127.0.0.1:2345 only**, so a client runs on the device (as rcplug
  does) or comes through an SSH tunnel.
- It serves **several clients at once**: every accepted connection is added to a list
  and gets its own synchroniser, and each change is forwarded to every other
  connection. There is no client limit, handshake or authentication.
- On connect it sends the full tree at once.
- A second server of the same kind on 127.0.0.1:2346 is an internal link to the
  call engine (`callme-t`); rcplug does not use it.

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

## The Duo's tree

Firmware 1.7.4, from the capture in `internal/rcsim/fixture`. The root has 702
children. The most important families:

| Node | Count | Notes |
|---|---|---|
| `CHANNEL` | 10 | Channel strip *i* belongs to fader *i*. Duo: 0–3 physical, 4–8 virtual. |
| `PHYSICALINTERFACE` → `FADER` | 9 | `faderLevel` 0–127, unity at about 90 |
| `PHYSICALINTERFACE` → `METER` | 22 | Assumed: 0–8 channel strips, 9–21 outputs |
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
