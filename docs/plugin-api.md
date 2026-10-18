# Writing plugins

A plugin is a directory with a `plugin.json` and a program. The host starts the
program, restarts it when it crashes, shows its output on the dashboard and, if it
has a web UI, makes that UI reachable under `/plugins/<name>/`.

Plugins can be written in any language that runs on the device (aarch64 Linux).
Go is the easiest choice: it produces one static binary, and the small
[`sdk`](../sdk) package handles the details below. Two complete examples:

| Example | What it shows |
|---|---|
| [`examples/hello-sh`](../examples/hello-sh) | The smallest plugin: POSIX sh, no web UI, output in the log, a counter in the data directory |
| [`examples/reclog`](../examples/reclog) | A Go plugin with a web UI that watches the recorder through the console API and keeps a log in its data directory, with tests |

## A minimal plugin

```
plugins/hello/
  plugin.json
  run.sh
```

```json
{
  "title": "Hello",
  "description": "Says hello once a minute.",
  "version": "0.1.0",
  "exec": ["/bin/sh", "./run.sh"],
  "autostart": true
}
```

```sh
#!/bin/sh
# A background sleep ignores SIGINT, so the trap stops it explicitly.
trap 'kill "$pid" 2>/dev/null; exit 0' INT TERM
while :; do echo "hello"; sleep 60 & pid=$!; wait "$pid"; done
```

Copy the directory to `<root>/plugins/` on the device and press **Rescan** on the
dashboard's Plugins page (or run `/etc/init.d/rcplug reload`). From your computer, the
wizard does it for you: `rcplug-wizard.sh --host <ip> --plugin ./hello`.

A rescan picks up new and removed plugins and reads changed manifests, but a plugin
that is already running keeps its process. After replacing a plugin by hand, press
**Restart** on its card. The wizard restarts updated plugins itself.

## The manifest: `plugin.json`

The directory name is the plugin's id: lower-case letters, digits, `-` and `_`. The
host skips other directories and lists them on the dashboard.
[`plugin.schema.json`](../plugin.schema.json) describes the format for editors: inside
this repository add `"$schema": "../../plugin.schema.json"`, elsewhere point it at the
file's raw URL in the published repository. The host lists unknown fields as a warning
on the dashboard.

| Field | Type | Meaning |
|---|---|---|
| `exec` | string array, **required** | Command line, run in the plugin directory. Start a program of the plugin with `./` (`["./hello"]`); a bare name is looked up in `PATH` (`["/bin/sh", "./run.sh"]`). `{{port}}` becomes the assigned port, `{{dir}}` the plugin directory. |
| `title` | string | Name in the navigation and the plugin list (English). Defaults to the id. |
| `description` | string | One or two sentences for the plugin list (English). |
| `version` | string | Version of the plugin, shown on the dashboard. |
| `httpProxy` | string | Address of the plugin's web server, normally `127.0.0.1:{{port}}`. It must be a loopback address. Leave it out for a plugin without a UI. |
| `autostart` | bool | Run the plugin by default. The user's choice on the dashboard overrides it. |
| `primary` | bool | Open this plugin as the dashboard's start page. |
| `order` | int | Position in the navigation, lower first. |
| `icon` | string | A short glyph or emoji for the navigation. |
| `needsAudio` | bool | Shows an "audio" badge in the plugin list. |
| `translations` | object | Localised `title` and `description` per language, e.g. `{"de": {"title": "Hallo"}}`. |
| `actions` | array | Entry points that other UIs show for this plugin, see [Actions](#actions). Needs `httpProxy`. |
| `name` | string | Ignored; the directory name is authoritative. |

## Environment

The host starts the program in its plugin directory with these variables:

| Variable | Value |
|---|---|
| `RCPLUG_NAME` | the plugin id |
| `RCPLUG_PORT` | the loopback port for the web UI (`0` without `httpProxy`) |
| `RCPLUG_PLUGIN_DIR` | the plugin directory. **Replaced on every update**: do not store anything here. |
| `RCPLUG_DATA_DIR` | `<root>/data/<name>/`, created by the host and kept across updates. Store settings and state here. |
| `RCPLUG_HOST_URL` | the host's base URL on loopback, e.g. `http://127.0.0.1:8080`. Other plugins are at `$RCPLUG_HOST_URL/plugins/<name>/`. |
| `RCPLUG_VERSION` | the host's version |

The rest of the host's environment is passed through.

### Operator settings

`<root>/config/<name>.json` adds arguments and environment variables without editing
the manifest, so the settings survive updates:

```json
{
  "args": ["--read-only"],
  "env": {"HELLO_INTERVAL": "10"}
}
```

`args` are appended to `exec`. The file is read whenever the plugin starts, so restart
the plugin after changing it. Unknown fields are an error that shows up in the plugin's
log. Arguments that look like secrets (`--pin`, `--password`, `--token`, …) are masked
in the log; values in `env` are never logged. All flags of the built-in plugins are in
[configuration.md](configuration.md).

## Lifecycle

- **Start.** When the plugin is enabled (on the dashboard, by `autostart`, or at boot
  if it was enabled before).
- **Output.** Everything the program writes to stdout and stderr appears in the
  plugin's log on the dashboard (the last 500 lines) and in `<root>/logs/<name>.log`
  on the device (rotated at 512 KB, one older file kept). Write plain lines; a Go panic
  with its stack trace lands there too.
- **Crash.** If the program exits while enabled, it is started again after a pause
  that doubles from 1 s to about 30 s. After a minute of running the pause resets.
- **Stop.** The host sends **SIGINT** to the plugin's process group, waits up to 3 s,
  then sends SIGKILL and removes any processes left in the group. Exit promptly on
  SIGINT and SIGTERM.
- **Priority.** Plugins inherit the lowest CPU priority (nice 19). Audio on the
  device always comes first. Avoid busy loops and polling faster than you need.
- **Update.** A new version replaces the plugin directory. The data directory and
  the operator settings stay.

## Web UI

A plugin with `httpProxy` serves HTTP on `127.0.0.1:$RCPLUG_PORT`. The host forwards
`/plugins/<name>/…` to it with the prefix removed, so the plugin sees `/`, `/api/…`
and so on.

- **Listen on loopback only.** The host is the single entry point and checks the
  `Host` header against DNS rebinding.
- **Use relative URLs** (`api/state`, not `/api/state`), so the page works both
  behind the host and when you open the plugin's port directly during development.
  `X-Forwarded-Prefix` carries `/plugins/<name>` if you need absolute links.
- **Protect state changes.** Browsers cannot add custom headers to cross-site
  requests without a CORS preflight, which rcplug never grants. Require the header
  `X-Rcplug: 1` on every POST, PUT, PATCH and DELETE and reject requests without it
  with 403. `sdk.Serve` does this for Go plugins. Your own pages send the header
  with `fetch(url, {method: 'POST', headers: {'X-Rcplug': '1'}})`. The host
  additionally refuses state-changing requests whose `Origin` is foreign.
- **Streaming** (Server-Sent Events, MJPEG) works; the proxy flushes immediately.
- **Embedding.** The dashboard shows the UI in an iframe. `window.self !==
  window.top` tells you that you are embedded, for example to hide your own header.
- **Language.** The dashboard stores the user's language (`de` or `en`) in
  `localStorage` under `rcplug-lang`. Read it to match the dashboard, and fall back to
  `navigator.language`. Leave the language switch to the dashboard when embedded.

## Actions

A plugin can offer entry points that other UIs show where they fit. The console, for
example, shows every action with the context `channel` as a button on each channel
strip and in the channel dialog. Sonar uses this for its "◉ Sonar" button:

```json
"actions": [{
  "id": "channel",
  "context": "channel",
  "title": "Sonar",
  "icon": "◉",
  "path": "?strip={strip}&source={source}&label={label}",
  "translations": {"de": {"title": "Sonar"}}
}]
```

| Field | Meaning |
|---|---|
| `id` | Unique within the plugin (`a-z`, `0-9`, `-`, `_`) |
| `context` | Where the action belongs. The console knows `channel`; other contexts are accepted and ignored by UIs that do not know them. |
| `title`, `icon`, `translations` | Button text, optional glyph (the plugin's icon otherwise) and localised titles |
| `path` | Relative to the plugin's UI (no scheme, no leading `/`). Placeholders are filled in and URL-encoded. |

Placeholders for `channel`: `{strip}` the strip id (`physical1`, `virtual2`, …), `{index}`
its position (0-based), `{source}` the source id assigned to it (`combo1`, `usb2`, …, empty
if none) and `{label}` the source's display name.

Inside the dashboard, a click switches the dashboard to the plugin and opens the path
there (the dashboard route is `#/p/<plugin><path>`). Without the dashboard, the browser
navigates to `/plugins/<plugin>/<path>`. Only running plugins' actions are shown.

## Diagnostics

A plugin can add its own data to the diagnostics bundle (the dashboard's *Download
diagnostics*, or `rcplugd --diagnostics` on the device). The host calls
`GET /api/diagnostics` on every running plugin with a web UI and stores the answer
under `plugins/<name>/` in the zip:

- `application/json` → `diagnostics.json`, any other text → `diagnostics.txt`
- `application/zip` → its entries are copied as they are

Report what helps to find a problem (configuration, state, counters, what the plugin
sees of the system), at most 20 MB, within 5 seconds. **Never include secrets.** Your
log output is in the bundle anyway (`logs/<name>.log`).

## Reading and controlling the desk

Plugins never talk to the desk's control port themselves. They use the console
plugin's HTTP API at `$RCPLUG_HOST_URL/plugins/console/`:

- `GET api/summary`: a compact view of channels, levels, mutes, recorder and pads.
- `GET api/tree` and `GET api/events`: the full state and its live changes.
- `POST api/set` and friends: changes, subject to the user's control level and PIN.

The full reference is in [console-api.md](console-api.md). Writes are refused while the
console is read-only, which is the default. A plugin that wants to change something
must say so in its description, and the user decides by setting the control level. If
the user set a PIN, the plugin needs it too: let the user put it into the plugin's
`config/<name>.json` as an environment variable and pass it on as `Client.PIN`.

## Go SDK

```go
import (
	"rcplug/sdk"
	capi "rcplug/sdk/console"
)
```

| Function | Purpose |
|---|---|
| `sdk.ListenAddr(def)` | `127.0.0.1:$RCPLUG_PORT`, or `def` when run standalone |
| `sdk.DataDir()`, `sdk.PluginDir()`, `sdk.Name()`, `sdk.HostURL()` | The environment, with fallbacks for running standalone |
| `sdk.StatePath(file)` | A path in the data directory; moves an old file from the plugin directory there |
| `sdk.LoadJSON`, `sdk.SaveJSON`, `sdk.WriteFileAtomic` | State files that survive power cuts (write to a temporary file, sync, rename) |
| `sdk.SignalContext()` | A context cancelled on SIGINT and SIGTERM |
| `sdk.Serve(ctx, addr, handler)` | HTTP server with the `X-Rcplug` check, timeouts and graceful shutdown when `ctx` ends |
| `sdk.CSRF(handler)` | Only the header check, for your own server |
| `sdk.WriteJSON`, `sdk.WriteError`, `sdk.NewError` | JSON responses and errors as `{"error": "...", "code": "..."}` |
| `sdk.LowPriority()` | Lowers all threads to nice 19 (already done when started by the host) |
| `capi.New("")` | Client for the console API: `Status`, `Summary`, `Tree`, `Events`, `Set` |

[`examples/reclog/main.go`](../examples/reclog/main.go) is a complete plugin with all of
these in about 150 lines.

### Using the SDK outside this repository

The module is called `rcplug` and is not fetched with `go get` (see
[development.md](development.md#module-path)). Check out this repository next to your
plugin and point your `go.mod` at it:

```
module example.com/myplugin

go 1.22

require rcplug v0.0.0

replace rcplug => ../rcplug
```

The SDK uses only the standard library, so this adds no other dependencies.

## Building and installing

The device is aarch64 Linux. For Go:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o hello ./path/to/hello
```

Other languages need a static aarch64 build or must use what the device already has
(bash as `sh`, GNU coreutils, BusyBox 1.35, Python 3.10, Perl, the JACK and ALSA
tools, in firmware 1.7.x). The device has no package manager.

Installing:

1. Put the program and `plugin.json` into one directory, named like the plugin.
2. Run `rcplug-wizard.sh --host <ip> --plugin <dir>` from your computer. It copies
   the directory to `<root>/plugins/<name>/`, keeps the plugin's data, rescans and
   restarts the plugin (through the dashboard API, or by restarting rcplug if the
   dashboard cannot be reached from your computer).

To try a plugin locally first, `make run-local EXTRA_PLUGINS=path/to/plugin` runs it in
a local host next to the built-in plugins and the simulator.

Plugins inside this repository: put the program in `cmd/<name>/` and the manifest in
`plugins/<name>/plugin.json`. `make package` then builds and ships it with the host.
The version in a built-in manifest must match the `VERSION` file (a test checks this).

## Checklist

- [ ] `exec` works from the plugin directory, without a shell profile.
- [ ] The program exits within 3 s of SIGINT.
- [ ] State lives in `$RCPLUG_DATA_DIR`, not next to the program.
- [ ] The web server listens on `127.0.0.1:$RCPLUG_PORT` and requires `X-Rcplug` on
      state-changing requests.
- [ ] All URLs in the UI are relative.
- [ ] Nothing polls faster than necessary.
