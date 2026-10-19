# Console API

The console plugin offers the desk's state and controls over HTTP, for its own web UI,
for other plugins and for scripts.

- From the network: `http://<device>:8080/plugins/console/`
- From a plugin on the device: `$RCPLUG_HOST_URL/plugins/console/`
- From Go: the client in [`sdk/console`](../sdk/console)

Responses are JSON, except the screen images. Errors look like `{"error": "human readable text", "code":
"machine_readable"}` with a matching HTTP status. The codes are listed
[at the end](#error-codes).

## Reading

### `GET api/status`

```json
{
  "connection": {"state": "live", "addr": "127.0.0.1:2345", "changesPerSecond": 1363.2, "guard": "active", "...": "..."},
  "control": "off",
  "controlLimit": "",
  "readOnly": false,
  "readOnlyFlag": false,
  "pinRequired": false,
  "pinReset": {"active": false, "need": 4},
  "panel": true,
  "version": "0.4.0",
  "epoch": 1,
  "seq": 48210,
  "stale": false,
  "model": "duo",
  "modelName": "RØDECaster Duo",
  "firmware": "1.7.4",
  "supported": true
}
```

| Field | Meaning |
|---|---|
| `connection.state` | `waiting` (boot grace; or, reading along, waiting for a full sync: `code` `tap_waiting` or `tap_lost`), `connecting`, `syncing`, `live` (`code` `resumed`: going on with the state kept at its last stop; changes made in between may be missing), `backoff` (retrying; `tap_failed` if reading along is impossible, e.g. without root), `conflict` (the GUI guard stepped back, see [safety.md](safety.md)), `stopped` |
| `connection.mode` | `tap` (reading along with the device's screen), `relay` (full control: the device's screen runs through the console) or `connect` |
| `connection.fullControl` | Full control is wanted (on the device, unless the console's connection setting is *Read along only*); `connection.relay` (`screen`, `screenSince`, `screenFrames`) describes it while it runs, `relayCode`/`relayError` why the last attempt ended (`relay_busy`, `relay_race`, `relay_no_mixer`, `relay_no_screen`, `relay_failed`), `relayRetry` when it is tried again |
| `passive` | `true` while reading along: the tree cannot be written (`meta.control.writable` is empty), the front panel can |
| `meters` | `true` while the console reads the meters and hardware fader positions from the mixer's shared memory (on the device; elsewhere meters arrive as tree changes) |
| `control` | The current control level: `off`, `basic` or `expert` |
| `controlLimit` | Why control is limited even if a level is set: `read_only` (write protection on, or started with `--read-only`), `untested_model` (no verified profile), or empty |
| `readOnly` / `readOnlyFlag` | Write protection is on (switched on the *System* page or forced) / forced by `--read-only` |
| `pinReset` | A PIN reset at the device in progress (see `api/pin/reset`) |
| `epoch` | Changes whenever the tree's structure changes (it starts at a random value; the examples here use 1). Writes must carry it. |
| `seq` | Increases with every change the console has applied |
| `stale` | `true` while the console is disconnected and shows the last known state |
| `supported` | Whether the model has a verified profile that allows control |

### `GET api/summary`

A compact, semantic view for plugins that do not want to walk the tree. The Go type is
`console.Summary` in [`sdk/console/types.go`](../sdk/console/types.go).

```json
{
  "model": "duo", "modelName": "RØDECaster Duo", "name": "…", "firmware": "1.7.4",
  "show": "Episode 7", "stale": false,
  "strips": [
    {"index": 0, "id": "physical1", "label": "Fader 1", "physical": true,
     "source": 0, "sourceId": "combo1", "sourceLabel": "Mic 1",
     "fader": 90, "faderDb": 0, "muted": false, "cue": false, "meterL": 0.42, "meterR": 0.40}
  ],
  "monitor": {"level": 0.55, "muted": false},
  "bluetooth": {"level": 0.7, "muted": false},
  "recorder": {"state": 0, "label": "ready", "timeMs": 0},
  "padBank": 0,
  "pads": [{"index": 0, "bank": 0, "slot": 0, "name": "Applause", "colour": 4, "active": false}]
}
```

`recorder.state` is 0 ready, 1 paused, 2 recording, 3 no destination. `faderDb` is an
approximation relative to the unity position (90 of 127).

### `GET api/tree`

The whole state:

```json
{
  "epoch": 1, "seq": 48210, "stale": false,
  "meta": {
    "layout": {"model": "duo", "runs": {"CHANNEL": {"parent": [], "first": 28, "count": 10}, "...": {}}},
    "sources": [{"id": "combo1", "label": "Mic 1"}],
    "outputs": [{"id": "headphone1", "label": "Headphones 1"}],
    "faders": [{"id": "physical1", "label": "Fader 1"}],
    "control": {"level": "off", "writable": {}},
    "controlLimit": "",
    "unityFader": 90
  },
  "root": {"t": "Rodecaster", "p": {}, "y": "", "c": [
    {"t": "CHANNEL", "p": {"channelOutputMute": false, "channelInputSource": 0}, "y": "bi", "c": []}
  ]}
}
```

Nodes are `{"t": type, "p": {property: value}, "y": types, "c": [children]}`. `y` has
one letter per property, in property order: `i` int32, `l` int64, `b` bool, `d`
double, `s` string, `a` array, `x` binary (the value is hex), `v` void, `r` redacted.
Secrets (passwords, keys, codes, account names) are always sent as `"••••••"` (an empty secret stays `""`) with the type letter `r`.

`meta.layout.runs` tells where each family of nodes lives: `parent` is the path of the
parent node, the family's members are its children `first` … `first+count-1`. Use it
instead of hard-coding indices; firmware versions may reorder the tree.

`meta.control.writable` lists, per node type, the properties writable at the current
level. It is empty when control is off. Some nodes of a listed type still refuse values
at *Control*: `FADER.faderLevel` is listed for the virtual faders, and a physical one
answers `out_of_range` (they are not motorised; *Expert* allows it).

A **path** is the list of child indices from the root, e.g. `[28]` for the first
`CHANNEL` above. Paths are only valid for the epoch they were read in.

### `GET api/events`

Server-Sent Events. The stream starts with `hello` and then sends:

| Event | Data |
|---|---|
| `hello` | The status document. If its `epoch` differs from your tree, or its `seq` is ahead of it, reload `api/tree`. |
| `changes` | `{"epoch": 1, "seq": 48230, "ch": [[[28], "channelOutputMute", true, "b"], …]}`: path, property, value, type letter. At most every 60 ms, with only the latest value per property. |
| `resync` | `{"epoch", "seq"}`: changes were dropped (your client was too slow, or the tree changed structurally). Reload `api/tree`. |
| `status` | The `connection` object of the status document after a connection change |
| `control` | The status document after the control level changed |

Comments (`: ping`) keep idle connections alive. At most 32 streams are served at a
time.

### Other reads

| Request | Content |
|---|---|
| `GET api/clients` | `{"port", "clients": [...]}`: connections to the desk's control port with the process behind each (`pid`, `comm`), e.g. the device's GUI and the console |
| `GET api/changes` | `{"changes": [...], "dropped": n}`: the last 500 property changes the desk reported, oldest first, each with `time`, `node`, `index` (in its family, -1 for a single node), `path`, `name`, `old` and `new` (secrets redacted). Meters, fader positions, mix levels, progress and the recording time are left out. `POST api/changes/clear` empties it (not for plugins). The diagnostics bundle carries it as `changes.json`, without string values |
| `GET api/audit` | `{"writes": [...]}`: the last 300 writes and changes to the console itself (node `console`: control level, device buttons, PIN without its value, reconnect, panel), with time, node type, path, property, value, client address and, for refused ones, the error code |
| `GET api/diagnostics` | A zip with the console's connection statistics, status, clients, audit log, summary, layout and a sanitized copy of the tree; the host adds it to its diagnostics bundle |
| `GET api/hw` | The device buttons (`panel`: `enabled` where their serial line exists, `error`) and the meter memory (`meters`: `available`, `live`) |

## Writing

Every write:

- is a `POST` with `Content-Type: application/json` and the header `X-Rcplug: 1`,
- from a browser: carries `X-Rcplug-Pin: <pin>` if a PIN is set;
- from a plugin: carries its token, `X-Rcplug-Token: $RCPLUG_TOKEN` (the SDK's client
  sends it), and the plugin declares `"permissions": ["desk:write"]` in its
  `plugin.json`. It then writes at the *Control* level's rules without the PIN, whatever
  level the browsers have; write protection stops it, and `api/control`,
  `api/settings`, `api/pin`, `api/pin/reset`, `api/reconnect` and `api/resync` are for
  people only (`plugin_not_allowed`). See
  [plugin-api.md](plugin-api.md#permissions),
- carries the `epoch` the path was computed against,
- is checked against the control level and the write policy (see
  [safety.md](safety.md)) and recorded in the audit log.

| Request | Body | Effect |
|---|---|---|
| `api/set` | `{"epoch", "path", "name", "value"}` | Sets an existing property. The value is converted to the property's type; numeric strings are refused. |
| `api/press` | `{"epoch", "path", "name", "ms"}` | A button press: `true`, then `false` after `ms` (default 150, at most 30 000; e.g. `PADBUTTON.padButtonPressed`). To hold a button, press with a long `ms` and release it yourself with `api/release`; a later press of the same button is not released by an earlier one's timer. |
| `api/release` | `{"epoch", "path", "name"}` as the press had them | Lets go of a button the console holds from `api/press`; `{"ok": true, "released": …}`. Needs no PIN and no level (it can only let go of what the console pressed), so a hold always ends, even if control was turned off meanwhile; after a structural change it finds the button again by its place in its family. |
| `api/trigger` | `{"epoch", "path", "name"}` | A binary trigger pulse (e.g. `MIX.mixLinkRequest`) |
| `api/link` | `{"epoch", "source", "output", "link"}` | Links or unlinks a routing cell with the sequence the device expects |
| `api/record` | `{"epoch", "value"}` | Recorder: 0 stop, 1 pause, 2 record |
| `api/control` | `{"level": "off" \| "basic" \| "expert"}` | Changes the control level (refused with `read_only` or `untested_model` where control is not possible) |
| `api/settings` | `{"readOnly": true}` | Switches write protection on or off (`read_only_forced` when started with `--read-only`). Saved across restarts. Full control and the device buttons are no settings since 0.8.0 ([protocol.md](protocol.md#full-control)); `panel` and `fullControl` are ignored. |
| `api/pin` | `{"current", "new"}` | Sets, changes (`current` required) or removes (`new: ""`) the PIN |
| `api/pin/reset` | `{}` | No PIN needed: starts a minute in which four presses of channel 1's Listen (or Mute) button at the device within four seconds remove the PIN and set the level to `off`; progress in `api/status` → `pinReset` (`active`, `presses`, `need`, `seconds`, `done`). `no_pin` if none is set |
| `api/reconnect` | `{}` | Reconnects to the desk, also after the GUI guard stepped back. At most every 10 s. |
| `api/resync` | `{}` | Reading along only: restarts the device's screen app, whose new connection brings a full sync. At most every 10 s. |

A **marker** is `api/set` on `RECORDER.requestDropMarker` with the current time in
milliseconds (an int64), as the console's UI does.

A successful write answers `{"ok": true}` (`api/set` adds the `value` as stored). The desk does not echo writes back to the
client that made them; the console applies them to its mirror at once and sends them
to all other browsers.

### Example with curl

```bash
B=http://192.168.1.50:8080/plugins/console
E=$(curl -s $B/api/status | jq .epoch)
curl -s -X POST $B/api/control -H 'X-Rcplug: 1' -H 'Content-Type: application/json' -d '{"level":"basic"}'
# Mute the first channel strip. Its path comes from meta.layout.runs.CHANNEL.
P=$(curl -s $B/api/tree | jq -c '.meta.layout.runs.CHANNEL | .parent + [.first]')
curl -s -X POST $B/api/set -H 'X-Rcplug: 1' -H 'Content-Type: application/json' \
     -d "{\"epoch\":$E,\"path\":$P,\"name\":\"channelOutputMute\",\"value\":true}"
```

### Example in Go

```go
c := capi.New("") // $RCPLUG_HOST_URL, or http://127.0.0.1:8080
s, err := c.Summary(ctx)
if err != nil {
	return err
}
log.Printf("%s is recording: %v", s.Show, s.Recorder.State == 2)

// Find the first channel strip in the layout instead of hard-coding its path.
raw, err := c.Tree(ctx)
if err != nil {
	return err
}
var tree struct {
	Epoch uint64
	Meta  struct {
		Layout struct {
			Runs map[string]struct {
				Parent []int
				First  int
			}
		}
	}
}
if err := json.Unmarshal(raw, &tree); err != nil {
	return err
}
ch := tree.Meta.Layout.Runs["CHANNEL"]
path := append(append([]int{}, ch.Parent...), ch.First)

err = c.Set(ctx, tree.Epoch, path, "channelOutputMute", true)
var apiErr *capi.Error
if errors.As(err, &apiErr) && apiErr.Code == "permission_desk_write" {
	log.Print(`plugin.json lacks "permissions": ["desk:write"]`)
}
```

## Error codes

| Code | Status | Meaning |
|---|---|---|
| `bad_request` | 400 | Malformed body or parameters |
| `no_node` | 400 | No node at this path |
| `unknown_property` | 400 | The node has no such property (the console never creates properties) |
| `invalid_value` | 400 | The value does not fit the property's type |
| `out_of_range` | 400 | Outside the allowed range: the curated ranges at `basic`, the node's own limits (e.g. fader min/max) at both levels |
| `invalid_change` | 400 | The change could not be encoded |
| `invalid_level` | 400 | Unknown control level |
| `invalid_pin` | 400 | The new PIN is longer than 64 characters or contains characters other than printable ASCII |
| `pin_required` | 401 | A PIN is set and was missing or wrong |
| `control_off` | 403 | The control level is `off` |
| `denied` | 403 | The property is not writable at this level, or never |
| `read_only` | 403 | Write protection is on, or the console runs with `--read-only` |
| `permission_desk_write` | 403 | A plugin without the permission `desk:write`, or a request from the device without a plugin's token |
| `plugin_not_allowed` | 403 | Plugins cannot change the console's settings, control level or PIN |
| `untested_model` | 403 | The model has no verified profile; control needs `--allow-untested-model` |
| `expert_required` | 403 | Needs the expert level |
| `csrf` | 403 | The `X-Rcplug` header is missing |
| `read_only_forced` | 409 | Write protection cannot be switched off: the console was started with `--read-only` |
| `no_pin` | 409 | A PIN reset without a PIN set |
| `stale_epoch` | 409 | The tree changed since you read the path. Reload and retry. |
| `pin_locked` | 429 | Too many wrong PINs; wait a minute |
| `rate_limited` | 429 | Try again in a few seconds |
| `not_synced` | 503 | The console has no state yet (reads; writes before the first sync answer `offline`) |
| `not_found` | 404 | No such endpoint, or the wrong method for it |
| `internal` | 500 | An unexpected error, for example the data directory cannot be written |
| `offline` | 503 | The console is not connected to the desk |
| `send_failed` | 503 | The connection broke while sending |
| `too_many_clients` | 503 | Too many live event streams |
