# Adding a model

rcplug finds the positions of all node families in the state tree at run time, so a
new model or firmware mostly needs two things: a **capture** of its tree to develop and
test against, and a **profile** with the facts the tree does not carry itself. A model
only becomes controllable after it has been checked on hardware.

## 1. Capture the state tree

On the device, with rcplug installed (`rccapture` is in the install directory):

```bash
ssh root@<device-ip> /usr/local/rcplug/rccapture --out /tmp/pro2-fw1.6.8.frame
scp root@<device-ip>:/tmp/pro2-fw1.6.8.frame .
```

Use your install directory if it differs; the wizard prints it.

`rccapture` joins the control port **read-only**, takes the first full sync and exits.
It never changes the desk's state; like the console, it sends the session-open
handshake (a protocol message, not a write) only if no full sync arrives on its own.
With the GUI guard on (the default) it stays two more seconds to see whether joining
pushed another client off the desk; if so, it still writes the capture, prints a
warning and exits with status 1. It prints the detected model, the firmware version
and the size of the tree.

The capture is **sanitized**: every string except structural ones (the firmware
version, routing levels) becomes `"<redacted>"`, and binary values are zeroed. Numbers
and booleans are kept, because the layout and the model detection depend on them.
`--raw` writes the unsanitized tree for your own debugging. It contains Wi-Fi keys,
names and serial numbers: never share or commit it.

Check what a capture contains before you share it, for example in the console's
explorer (step 2).

## 2. Run it in the simulator

```bash
go run ./cmd/rcsim --fixture pro2-fw1.6.8.frame --addr 127.0.0.1:2345 &
go run ./cmd/console --mixer 127.0.0.1:2345 --allow-untested-model
# open http://127.0.0.1:8082/
```

The simulator serves the captured tree, fills in plausible values where it knows the
properties, animates meters and reacts to writes like the desk (no echo to the writer).
Go through every tab of the console and the explorer, and note what is missing or
wrong.

## 3. Add the fixture

Put the file into `internal/rcsim/fixture/` and describe it in
`internal/rcsim/fixture/NOTICE` (model, firmware, who captured it, licence: the
repository's MIT licence unless stated otherwise). `TestFixturesAreSanitized` fails if a
fixture is not in sanitized form, does not round-trip byte for byte, or is not
mentioned in the NOTICE.

## 4. Add or update the profile

Model knowledge lives in [`internal/rcproto/model.go`](../internal/rcproto/model.go):

- **Detection.** `DetectModel` reads `SYSTEM.boardType` (0 = Pro II, 1 = Duo) and
  falls back to `SYSTEM.systemName`. It never guesses: an unknown model stays
  `unknown` and read-only.
- **Profile.** `Profiles[model]` holds the display name, the number of physical faders
  (the rest are virtual), pads per bank and the number of banks, the microphone inputs
  and headphones the device has (the tree has room for four of each on every model;
  the console shows only these), `Controllable` and
  `Basis`, a note on where the facts come from. Start with `Controllable: false`.
- **Names.** `Sources` and `Outputs` name the routing sources and outputs by index.
  If the new model numbers them differently, make the tables per model.

Then add a test next to `TestDuoLayout` in
[`internal/rcproto/rcproto_test.go`](../internal/rcproto/rcproto_test.go) that loads the
new fixture and checks the detected model and the families the console needs
(`CHANNEL`, `FADER`, `METER`, `MIX`, `INPUTSOURCE`, `RECORDER`, …). Run `make check`.

## 5. Check the write policy

The *Control* level only allows properties listed in `basicRules` in
[`internal/console/policy.go`](../internal/console/policy.go), with ranges taken from
the Duo. Compare the new tree with them: properties that do not exist are simply not
offered, but a property with a different meaning or range needs a model-specific rule.
New node types that can do damage (storage, network, updates) belong on the `denied`
list.

## 6. Confirm on hardware

Put `{"args": ["--allow-untested-model"]}` into `config/console.json` in the install
directory on the device and restart the console on the dashboard. Then follow the
careful first test in [device-notes.md](device-notes.md). Once mute, faders, routing
and recording behave as expected, set `Controllable: true` and describe the result in
the pull request.

## 7. Firmware (optional)

To let the setup tools fetch and check a model's stock firmware, add it to
`fwimage.Models` (`internal/fwimage/fwimage.go`): the manifest URL and release
path RØDE's app uses for it, and the hostname and device tree its stock image
carries. `rcplug-cfw --inspect os.tar.gz` prints both for an image you have.
Start with `DownloadOnly`; `Full` (adding a key) only once someone has confirmed
the whole procedure on that model.
