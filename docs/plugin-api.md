# Plugin interface reference

Everything the host (`rcplugd`) promises a plugin and expects from it. To learn
plugin development step by step, start with [plugin-guide.md](plugin-guide.md);
for ready-made solutions see [cookbook.md](cookbook.md). Reading and controlling the
desk is described in [console-api.md](console-api.md).

| | |
|---|---|
| [Overview](#overview) | The contract on one page |
| [Directory and files](#directory-and-files) | Where a plugin lives and what is kept |
| [The manifest](#the-manifest-pluginjson) | Every field of `plugin.json` |
| [Process](#process) | Start, environment, output, signals, restarts, priority |
| [Operator settings](#operator-settings) | `config/<name>.json` |
| [Permissions](#permissions) | What a plugin declares it does |
| [HTTP](#http) | Port, proxy, headers, streaming, WebSockets, security |
| [Dashboard](#dashboard) | Navigation, start page, language, embedding |
| [Actions](#actions) | Entry points other UIs show |
| [Diagnostics](#diagnostics) | Contributing to the diagnostics bundle |
| [Updates](#updates) | Installing and updating plugins |
| [Host API](#host-api) | What a plugin may ask the host |
| [The desk](#the-desk) | Reading and controlling through the console |
| [Go SDK](#go-sdk) | `sdk`, `sdk/console`, `sdk/release` |
| [Compatibility](#compatibility) | Versions and what stays stable |
| [Checklist](#checklist) | Before you publish |

## Overview

| The host | The plugin |
|---|---|
| finds `plugins/<name>/plugin.json` and runs `exec` in that directory | exits within 3 s of SIGINT |
| passes `RCPLUG_*` variables and the operator's settings | keeps state in `RCPLUG_DATA_DIR` |
| assigns a loopback port to plugins with `httpProxy` | serves HTTP on `127.0.0.1:$RCPLUG_PORT` only |
| forwards `/plugins/<name>/…` to it, prefix removed | uses relative URLs in its pages |
| refuses foreign `Host` names and cross-site state changes | requires `X-Rcplug` on state-changing requests |
| restarts it after a crash, with growing pauses | does not depend on being started exactly once |
| keeps its output as a log | writes plain lines, never secrets |
| runs it at the lowest CPU priority | does not poll or spin |

Plugins run as **root** on the device, like the host. Nothing separates one plugin
from another or from the system: install only plugins you trust, and write plugins
that touch only what they need.

## Directory and files

```
<root>/                     /usr/local/rcplug on the device
  plugins/<name>/           the plugin: plugin.json, program, assets   replaced by updates
  data/<name>/              RCPLUG_DATA_DIR, created by the host        kept
  config/<name>.json        operator settings, optional                kept
  logs/<name>.log           the plugin's output, rotated at 512 KB     kept
  state.json                which plugins the user started or stopped  kept
```

- The directory name is the plugin's **id**: lower-case letters, digits, `-` and
  `_`, starting with a letter or digit (`^[a-z0-9][a-z0-9_-]*$`). It is also the URL
  segment. Other directories are skipped and listed on the dashboard.
- **Kept** means: across restarts, reboots, reinstalls with the toolkit and in-app
  updates. A **firmware update** of the RØDECaster removes the whole install
  directory.
- Uninstalling rcplug removes everything, including `data/`.

## The manifest: `plugin.json`

[`plugin.schema.json`](../plugin.schema.json) describes the format for editors. Set
`"$schema"` to the file's raw URL,
`https://raw.githubusercontent.com/Hustenreizjuengling/rcplug/main/plugin.schema.json`
(inside this repository: `../../plugin.schema.json`). Unknown fields are shown as a
warning on the dashboard and in the host's log; the plugin still loads.

| Field | Type | Default | Meaning |
|---|---|---|---|
| `exec` | string array | **required** | Command line, run in the plugin directory. A program of the plugin starts with `./` (`["./onair"]`); a bare name is looked up in `PATH` (`["/bin/sh", "./run.sh"]`). `{{port}}` becomes the assigned port, `{{dir}}` the plugin directory. |
| `title` | string | the id | Name in the navigation and the plugin list (English) |
| `description` | string | | One or two sentences for the plugin list (English). Say what the plugin changes, if anything. |
| `version` | string | | Shown on the dashboard. `X.Y.Z` (semantic versioning) is required for updates. |
| `httpProxy` | string | | Where the plugin's web server listens, normally `127.0.0.1:{{port}}`. Must be a loopback address (`127.x.x.x`, `::1`, `localhost`). Without it the plugin has no port and no page. |
| `autostart` | bool | `false` | Run the plugin unless the user stopped it |
| `primary` | bool | `false` | Open this plugin as the dashboard's start page (the first enabled one wins) |
| `order` | int | `0` | Position in the navigation and the plugin list, lower first; ties by id |
| `icon` | string | | A short glyph or emoji for the navigation (at most 8 characters) |
| `needsAudio` | bool | `false` | Shows an "audio" badge in the plugin list (older form of the permission `audio`, which it grants) |
| `permissions` | string array | | What the plugin does beyond its own pages: `desk:write`, `audio`, `network`, `device`, `screen`, see [Permissions](#permissions) |
| `translations` | object | | Localised `title` and `description` per language code: `{"de": {"title": "…", "description": "…"}}` |
| `actions` | array | | Entry points other UIs show, see [Actions](#actions). Needs `httpProxy`. |
| `update` | object | | Where updates come from, see [Updates](#updates) |
| `options` | array | | Settings users change in the dashboard, see [Options](#options) |
| `name` | string | | Ignored; the directory name is authoritative |
| `$schema` | string | | Ignored by the host |

A manifest that cannot be read, has no `exec`, a non-loopback `httpProxy` or an
invalid `actions`, `update`, `options` or `permissions` entry is skipped; the reason is listed on the
dashboard.

## Process

### Start

The host starts a plugin when it is enabled: by `autostart`, by the user on the
dashboard (remembered in `state.json`), and again at every start of the host if it
was enabled before.

- **Command:** `exec` with `{{port}}` and `{{dir}}` replaced, followed by `args` from
  the [operator settings](#operator-settings). There is no shell unless `exec` starts
  one.
- **Working directory:** the plugin directory.
- **Standard input:** `/dev/null`.
- **Process group:** a new one per plugin; children belong to it.
- **User:** root, like the host.
- Two instances of a plugin never run at the same time: a new start waits until the
  previous process is gone.

### Environment

The host's own environment, plus:

| Variable | Value |
|---|---|
| `RCPLUG_NAME` | the plugin's id |
| `RCPLUG_PORT` | the loopback port for the web server, `0` without `httpProxy` |
| `RCPLUG_PLUGIN_DIR` | the plugin directory. **Replaced on every update**: do not store anything here. |
| `RCPLUG_DATA_DIR` | `<root>/data/<name>/`, created before the start and kept across updates |
| `RCPLUG_HOST_URL` | the host on loopback, e.g. `http://127.0.0.1:8080`. Other plugins are at `$RCPLUG_HOST_URL/plugins/<name>/`. |
| `RCPLUG_VERSION` | the host's version |
| `RCPLUG_TOKEN` | proves the plugin to the host and, through it, to other plugins (header `X-Rcplug-Token`): for [events](#events), [audio](#audio) and [writes to the desk](#the-desk). New at every start, never logged. |

`env` from the operator settings comes last and wins over everything above.

### Output and logs

Everything the program writes to stdout and stderr becomes the plugin's log, line by
line:

- the dashboard shows the last 500 lines (**Plugins → Log**,
  `GET /api/plugins/<name>/logs`);
- `<root>/logs/<name>.log` keeps it on the device across reboots, rotated at 512 KB
  with one older file;
- every line gets the date and time; a timestamp in the default format of Go's `log`
  package at the start of a line is dropped, so it does not appear twice;
- the host adds its own lines (`started pid=…`, `exited (…), restarting in 4s`,
  `stopped by host`), with secret-looking arguments and the paths of URLs masked;
- lines longer than 64 KB are split into pieces of 64 KB;
- the logs go into the [diagnostics bundle](#diagnostics).

Write plain lines. Never write secrets: logs are shown to everyone who can open the
dashboard and end up in bug reports.

### Stop

The host stops a plugin when the user stops or restarts it, when it is removed or
updated, and when the host itself stops:

1. **SIGINT** to the plugin's process group;
2. after **3 seconds**, SIGKILL to the program;
3. once the program has exited, SIGKILL to whatever is left in its group (helpers
   that kept running).

Exit promptly on SIGINT (and SIGTERM). If the host dies without stopping its
plugins, the plugin's own process gets SIGTERM; helpers it started are not reached by
that, and the next start of the host removes whatever is left of the plugin's process
group. Shell scripts need a `trap` for SIGINT, because a
background `sleep` in a non-interactive shell ignores it; Python must flush its
output and handle SIGINT, see the [guide](plugin-guide.md#12-other-languages).

### Crashes and restarts

| State | Meaning |
|---|---|
| `starting` | the process is being started |
| `running` | the process runs |
| `backoff` | it exited while enabled and is restarted after a pause |
| `stopped` | not enabled |

The pause after an exit doubles from 1 s up to about 30 s; a process that ran for over
a minute starts again with 1 s. An exit with status 0 counts like a crash: a plugin
that is enabled is supposed to run. The dashboard shows the number of restarts and the
last error. A plugin with the permission `screen` is the exception: it runs once and
is never restarted (see [Permissions](#permissions)).

### Priority and resources

The host runs at nice 19 and its plugins inherit it, so audio on the device always
comes first. There are no other limits. The RØDECaster is an embedded device whose
memory and storage the firmware needs too: keep memory use small, avoid polling faster
than needed, never busy-wait, and bound everything that grows (logs, history,
caches). The dashboard's System page shows free memory and storage.

## Options

Users of a desk should never need a shell. Settings a user may change are declared as
`options`; the dashboard shows them under *Settings* on the plugin's card, the host
checks every value against the declaration, keeps it in `config/<name>.json` (below)
and restarts the plugin. When it starts the plugin, the host passes each value a user
has set as a command-line argument or an environment variable; for options nobody has
set, nothing is passed and the plugin's own default applies.

```json
"options": [
  {"id": "interval", "type": "number", "title": "Interval (seconds)", "min": 1, "max": 3600,
   "default": 5, "arg": "--interval={value}"},
  {"id": "style", "type": "choice", "title": "Style", "default": "big",
   "choices": [{"value": "big", "title": "Big clock"}, {"value": "small", "title": "Small"}],
   "arg": "--style={value}"},
  {"id": "quiet", "type": "bool", "title": "Quiet", "arg": "--quiet"},
  {"id": "label", "type": "text", "title": "Label", "maxLength": 40, "env": "MY_LABEL"}
]
```

| Field | Meaning |
|---|---|
| `id` | Unique, starts with a letter; letters, digits, `-`, `_` |
| `type` | `bool`, `choice`, `number` or `text` |
| `title`, `description` | Shown in the form (English; translations below) |
| `default` | The plugin's own default, shown as "default: …"; it is not passed |
| `choices` | `choice`: `[{"value", "title"}]` |
| `min`, `max` | `number`: bounds |
| `maxLength`, `pattern` | `text`: at most this many characters (default 256), and a regular expression the whole value must match; control characters are always refused |
| `arg` | `bool`: the flag added when the value is true (`--quiet`); otherwise one argument with `{value}` (`--style={value}`). Each value is exactly one argument, never split or passed through a shell. |
| `env` | Instead of `arg`: an environment variable set to the value (`1`/`0` for `bool`); not `RCPLUG_*` |

Translations go into `translations.<lang>.options.<id>`: `title`, `description` and
`choices` (value → title). A value stored for an option that a later version of the
plugin no longer has, or that no longer fits, is left out at the start and noted in the
plugin's log.

Options are as open as the rest of the dashboard: everyone who can reach the device
can change them. Never make secrets (PINs, tokens) or decisions only the owner may take
an option; those belong in the operator's `args`/`env` below. The API is
`GET`/`POST api/plugins/<name>/options` (`{"values": {"<id>": value}}`, `null` goes back
to the default).

## Operator settings

`<root>/config/<name>.json` adds arguments and environment variables without editing
the manifest, and survives updates (the dashboard keeps option values in it too, under
`"options"`):

```json
{
  "args": ["--read-only"],
  "env": {"HELLO_INTERVAL": "10", "CONSOLE_PIN": "4711"}
}
```

- `args` are appended to `exec` (after the options); `env` is added to the environment.
- The file is read at every start of the plugin: restart it after a change.
- Unknown fields and invalid JSON keep the plugin from starting; the reason is in its
  log.
- `env` values are never logged. In the command line the host logs, the value of every
  argument whose name starts with `pin`, `pass`, `token`, `secret` or `key`
  (`--pin 4711`, `--token=…`, `--password …`) is masked.

This is the place for anything only the operator may decide: PINs, tokens, webhook
URLs, options that change the desk. Everything a web page can change is open to
everyone on the network. The flags of the built-in plugins are in
[configuration.md](configuration.md).

## Permissions

A plugin declares in `plugin.json` what it does beyond its own pages; the dashboard
shows it on the plugin's card, so users know before they start it.

```json
"permissions": ["desk:write", "audio"]
```

| Permission | Means | Held to it by |
|---|---|---|
| `desk:write` | Changes the desk through the console: mixer, recorder, pads | the console: writes from plugins without it answer 403 `permission_desk_write` |
| `audio` | Hears the desk's inputs and outputs through the [shared audio taps](#audio) | the host: `api/audio/stream` answers 403 without it |
| `network` | Talks to other machines on the network or the internet | shown only |
| `device` | Uses the device's hardware, or files outside its own directories | shown only |
| `screen` | Takes over the device's screen while it runs (see below) | the host: it stops RØDE's screen before the plugin starts and starts it again when the plugin ends |

**`screen`: the device's screen.** While a plugin with this permission runs, RØDE's
screen program and the loop that restarts it are stopped, and the plugin may draw on
the display (`/dev/dri/card0`, 720 × 1280 portrait) and read the touchscreen
(`/dev/input/event0`). When the plugin ends, however it ends (stopped in the dashboard,
exited by itself, crashed, rcplug stopped or updated), the host starts RØDE's display
service again; a restarted rcplugd does the same if it finds the screen still away. Such
a plugin:

- never starts by itself: `autostart` must be false, its running state is not
  remembered and not restored from a backup; it starts only from the dashboard;
- runs once: when its process exits, it is not started again (to give the screen back,
  it simply exits);
- is not started again after its options change or after an update;
- is the only one with the screen: a second one does not start while one has it.

Meanwhile the console holds the mixer alone (it reads `/run/rcplug-screen`, where the
host writes the plugin's name) instead of waiting for RØDE's screen. The device's big
encoder is read by RØDE's screen program and does nothing while a plugin has the screen.

How the host knows who asks: every plugin gets a token (`$RCPLUG_TOKEN`). When a
plugin calls another plugin through the host (`$RCPLUG_HOST_URL/plugins/<name>/`) with
the token in `X-Rcplug-Token`, the host passes the call on with `X-Rcplug-Plugin: <its
name>` and `X-Rcplug-Permissions: <its permissions, comma-separated>`, and drops the
token. A call from the device without a token arrives as `X-Rcplug-Plugin: unknown`;
calls from the network and from a browser carry neither header, and whatever a client
sent in them is dropped. A plugin that offers something to other plugins can check
these headers the same way the console does.

Plugins run as root on the device. Permissions guard against mistakes and tell users
what a plugin is for; they do not contain a plugin that means harm. Install plugins
only from sources you trust.

## HTTP

### Port and proxy

A plugin with `httpProxy` gets a free loopback port, from `--base-port` (9100) up,
assigned when it is first loaded. It may change between host restarts: always take
it from `{{port}}` or `RCPLUG_PORT`.

The host forwards every request below `/plugins/<name>/` to the plugin:

- The prefix is removed: the plugin sees `/`, `/api/state`, … Query strings are kept.
- `/plugins/<name>` redirects to `/plugins/<name>/` (301, query kept), so relative URLs
  resolve. Encoded characters in the path (`%2F`) reach the plugin as they were sent.
- Request headers are passed through, with `X-Forwarded-For`, `X-Forwarded-Host`,
  `X-Forwarded-Proto` and `X-Forwarded-Prefix: /plugins/<name>`. The `Host` header is
  the plugin's own address.
- Responses are passed through and flushed at once: **Server-Sent Events**, MJPEG and
  other streams work. **WebSocket** upgrades work as well; the host refuses handshakes
  from pages of other sites (their `Origin`), because browsers cannot add `X-Rcplug` to
  them.
- With a dashboard password set, the host lets only logged-in visitors through (a
  page without a session goes to the login, an API request gets 401
  `login_required`); the plugin needs no login of its own. Calls from the device
  itself over the loopback interface (another plugin, `RCPLUG_HOST_URL`) are not
  asked. A plugin that forwards requests from the network to the host must set
  `X-Forwarded-For`, or they would count as the device's own.
- If the plugin does not answer (starting, stopped, crashed), the host answers 502;
  browsers get a page that tries again every two seconds, so a page opened while the
  plugin starts appears by itself. For an unknown plugin or one without `httpProxy`:
  404.
- The host has no timeouts for responses and no body size limit; protect your own
  handlers (`http.MaxBytesReader`).

**Use relative URLs** in pages (`api/state`, not `/api/state`), so they work behind the
host and when the plugin's port is opened directly during development. For absolute
links, prefix `X-Forwarded-Prefix`.

### Security

The host does this for every request, plugins included:

- **Host names.** Only IP addresses, `localhost`, the device's host name, local
  names (`.local`, `.lan`, `.home`, `.home.arpa`, `.internal`, `.localdomain`,
  `.fritz.box`) and names added by the operator (`RCPLUG_ALLOW_HOST`) are answered;
  others get 421. This stops DNS rebinding.
- **Cross-site requests.** State-changing requests (anything but GET, HEAD, OPTIONS)
  whose `Origin` belongs to another site are refused with 403.
- **Framing.** Responses carry `X-Frame-Options: SAMEORIGIN` and
  `Content-Security-Policy: frame-ancestors 'self'` (added to the plugin's own
  headers), `X-Content-Type-Options: nosniff` and `Referrer-Policy: same-origin`.
  Pages can be framed by the dashboard, not by other sites.

The plugin must:

- **listen on loopback only** (`127.0.0.1:$RCPLUG_PORT`), never on all interfaces;
- **refuse state-changing requests without `X-Rcplug`** (any value; the dashboard and
  the SDK send `1`) with 403. Browsers cannot add custom headers to cross-site
  requests without a CORS preflight, which rcplug never grants. `sdk.Serve` and
  `sdk.CSRF` do this;
- **never change anything on GET**;
- not send CORS headers that allow other origins.

There are no user accounts: everyone who can reach the device can use every plugin
page. Protect actions with a PIN or keep them in the operator settings where that
matters, as the console does.

## Dashboard

- **Navigation.** Every plugin with `httpProxy` gets an entry with its `icon` and
  title and a status dot, sorted by `order` and id. A click shows its page in an
  iframe below the navigation (`#/p/<name>`); **new tab ↗** on the Plugins page
  opens `/plugins/<name>/` alone.
- **Start page.** The first enabled plugin with `primary`, otherwise the first
  enabled plugin with a page, otherwise the Plugins page.
- **Plugins page.** Every plugin with title, description, version, state, restarts,
  last error, log, and the buttons Start, Stop, Restart, Update.
- **Not running.** Instead of the iframe the dashboard shows the plugin's state and a
  Start button.
- **Language.** The dashboard stores `de` or `en` in `localStorage` under
  `rcplug-lang` (same origin as your page). Read it, fall back to
  `navigator.language`, and leave switching to the dashboard while embedded.
- **Embedded or alone.** `window.self !== window.top` tells you that you are in the
  dashboard's iframe. The iframe allows full screen.
- **Deep links.** `#/p/<name>/<path>` or `#/p/<name>?<query>` opens the plugin's page
  at that path, which is how [actions](#actions) work. `..` is not allowed.

## Actions

A plugin can offer entry points that other UIs show where they fit. The console shows
every action with the context `channel` as a button on each channel strip and in the
channel dialog. Sonar uses this for its "◉ Sonar" button:

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
| `id` | Unique within the plugin (`^[a-z0-9][a-z0-9_-]*$`) |
| `context` | Where the action belongs. The console knows `channel`; UIs ignore contexts they do not know. |
| `title`, `icon`, `translations` | Button text, optional glyph (the plugin's icon otherwise) and localised titles |
| `path` | Relative to the plugin's page (no scheme, no leading `/`). Placeholders are filled in and URL-encoded. |

Placeholders for `channel`: `{strip}` the strip id (`physical1`, `virtual2`, …),
`{index}` its position (0-based), `{source}` the source id assigned to it (`combo1`,
`usb2`, …, empty if none) and `{label}` the source's display name.

Inside the dashboard, a click switches to the plugin and opens the path there.
Without the dashboard, the browser goes to `/plugins/<plugin>/<path>`. Only running
plugins' actions are shown.

## Diagnostics

**System → Download diagnostics** (and `rcplugd --diagnostics`, and the toolkit)
build a zip with the logs of the host and all plugins, versions and the device's
state. A plugin can add its own data: the host calls `GET /api/diagnostics` on every
running plugin with a page and stores the answer under `plugins/<name>/`:

| Content-Type | Stored as |
|---|---|
| `application/json` | `diagnostics.json` |
| `application/zip` | its entries, as they are |
| anything else | `diagnostics.txt` |

Answer within 5 seconds, with at most 20 MB. Report what helps to find a problem:
configuration, state, counters, what the plugin sees of the system. **Never include
secrets**; users attach the bundle to public bug reports.

## Updates

Installing a plugin means putting its directory into `plugins/`. The toolkit does
that (`rcplug-wizard.sh --plugin <dir>`, or **Add plugin** in the setup window): it
replaces the directory, keeps `data/` and `config/`, and restarts the plugin.

A plugin can also update from the dashboard. Add its source to `plugin.json`:

```json
"update": {"github": "owner/repository", "publicKey": "<base64 Ed25519 public key>"}
```

| Field | Meaning |
|---|---|
| `github` | **Required.** The GitHub repository whose latest release carries the plugin. It must be public. |
| `publicKey` | The key that signs the releases, trusted in addition to rcplug's built-in keys. Leave it out only for plugins released from rcplug's own repositories. |

**System → Check for updates** then reads the repository's latest release, checks its
signed `release.json` and offers a newer `version` of the plugin; **Install** (or
**Update** on the plugin's card) downloads it, checks its SHA-256, unpacks it into a
staging directory, stops the plugin, swaps the directories, and starts it again if it
was enabled. The data directory and the operator settings stay. How to publish such
releases: [updates.md](updates.md#publishing-a-plugin-with-updates) and
[the guide](plugin-guide.md#13-publish-with-updates).

A package is a `.tar.gz` with `plugin.json` at its root. `version` in the package's
`plugin.json` must be the one in `release.json`, and its `name` must be the plugin's
id. Files keep their permissions without write access for group and others and
without special bits; links and paths outside the package are refused.

A rescan (**Rescan** on the Plugins page, `POST /api/reload`, SIGHUP) picks up new and
removed plugins and changed manifests, but a running plugin keeps its process until it
is restarted. A plugin whose manifest has become invalid is stopped and listed with the
reason. Skipped plugins and unknown manifest fields are written to the host's log at
every rescan.

## Host API

The dashboard's API, open to plugins (`$RCPLUG_HOST_URL`) and scripts. POSTs need
`X-Rcplug: 1`.

| Request | Effect |
|---|---|
| `GET /api/plugins` | `{"plugins": [...], "invalid": [...]}`: per plugin `name`, `title`, `description`, `version`, `hasUI`, `needsAudio`, `permissions`, `removable`, `enabled`, `state`, `running`, `pid`, `restarts`, `lastErr`, `port`, `primary`, `order`, `icon`, `unknownFields`, `translations`, `actions` |
| `GET /api/plugins/<name>/logs` | `{"lines": [...]}`, the last 500 lines |
| `POST /api/plugins/<name>/enable`, `/disable`, `/restart` | Start and remember, stop and remember, restart |
| `POST /api/reload` | Rescan the plugins directory |
| `POST /api/plugins/<name>/remove` `{"confirm": "<name>", "purge": false}` | Stops the plugin and deletes its directory; `purge` also deletes its data, settings and logs. Not for plugins of rcplug's device package (`removable` is false) |
| `GET /api/catalog`, `GET /api/catalog?refresh=1` | The [plugin catalog](updates.md#plugin-catalog): `sources` and `plugins` (`name`, `title`, `description`, `icon`, `translations`, `permissions`, `version`, `size`, `repo`, `official`, `installed`) as last seen; `refresh=1` asks GitHub again (at most every 20 s) |
| `POST /api/catalog/install` `{"repo", "plugin"}` | Installs a plugin from the catalog; progress as for updates (`job`, target `install:<name>`) |
| `POST /api/catalog/sources` `{"github", "publicKey"}` or `{"github", "remove": true}` | Adds or removes a catalog |
| `GET /api/backup` | rcplug's own settings for a backup: `{"format": 1, "kind": "rcplug-settings", "created", "version", "plugins": [{"name", "title", "version", "enabled", "options", "leftOut"}], "autoCheck", "catalogs"}`. `enabled`: whether the plugin runs, switched on by hand or by its autostart (a backup of 0.8.5 has it only where the user chose; absent, a restore leaves the plugin as it is). Left out: the dashboard's password, text options whose id looks like a secret (listed in `leftOut`), the plugins' data and the logs, and the console: what the plugins of rcplug's device package hold goes with the device and its shows |
| `POST /api/backup/diff` with a backup | What restoring it would change: `{"changes": [{"plugin", "what": "enabled" \| "option" \| "autoCheck" \| "catalog", "id", "title", "from", "to"}], "missing": [plugins the device lacks], "skipped": [{"plugin", "id", "reason"}]}`; `null` is "not set" (an option's default) |
| `POST /api/backup/restore` with a backup | Carries that out (`{"plan", "errors"}`): plugins switched off first, then the options (a running plugin restarts with them), then plugins switched on. An option the backup does not have goes back to its default; plugins the backup does not know stay as they are (the console always), missing ones are only listed (install them from the catalog, then restore again) |
| `GET /api/system` | Host name, version, uptimes, load, memory (`memory`: `totalKB`, `availableKB`, `usedKB`, `programsKB`, `sharedKB`, `kernelKB`, `cacheKB`, `freeKB`), CPU temperature, free storage |
| `POST /api/uninstall` `{"confirm": "uninstall"}` | Removes rcplug from the device: stops it and every plugin, removes the init script and the install directory with all data (only one named `rcplug` that holds `rcplugd`) |
| `GET /api/processes?sort=cpu\|mem&limit=20&all=1` | The device's processes: `pid`, `name`, `command` (secrets masked), `cpu` (percent of one core since the previous request), `cpuTime`, `ownKB` (memory only the process holds, RssAnon), `sharedKB` (program files and shared memory, RssFile + RssShmem), `rssKB`, `threads`, `kernel`, `owner` (`rcplug` with the `plugin`, or `rode`); `all=1` includes kernel threads. Also the device's `cpu` (percent of the whole device: `total`, `user`, `system`, `irq`, `iowait`; from the second request on), `memory` as in `api/system`, `ownKB` of all processes and `kernelThreads` (`count`, `cpu`) |
| `GET /api/diagnostics` | The diagnostics bundle |
| `GET /ui/rcplug.css`, `GET /ui/icons.svg` | rcplug's shared look ([`sdk/ui`](plugin-guide.md#look-like-the-rest-of-rcplug)); also served without a login, for the login page |
| `GET /api/updates`, `POST /api/updates/check`, `POST /api/updates/install?target=` | Updates, see [updates.md](updates.md) |
| `GET /api/events`, `GET /api/events/stream`, `POST /api/events` | The event bus, see [Events](#events) |
| `GET /api/audio`, `GET /api/audio/stream` | The shared audio taps, see [Audio](#audio) |

## Events

Plugins work together through the host's event bus without knowing each other's
APIs: the console reports what happens on the desk, the host reports its own
changes, and any plugin can publish what it does (a segment started, a guest joined)
and follow what others publish.

```go
for e := range sdk.Events(ctx, "desk.recording.") { // reconnects and catches up by itself
	if e.Source == "console" && e.Type == "desk.recording.started" {
		_ = sdk.Publish(ctx, "onair.lamp", map[string]bool{"on": true})
	}
}
```

An event is `{"id", "time", "type", "source", "data"}`:

- `type` has two to six dot-separated parts of `a-z`, `0-9`, `-` and `_`. Start your own
  with your plugin's name (`onair.lamp`); `host.` is reserved for the host.
- `source` is set by the host: `host`, or the plugin that published it, proven by its
  `RCPLUG_TOKEN`. Check it before you act on an event (`desk.*` comes from `console`).
- `data` is a JSON object of at most 16 KB, or absent.

The host keeps the last 200 events in memory (nothing on disk; a restart of rcplug
starts over). Publishing needs the plugin's token, so browsers and the network can
only read.

| Request | Effect |
|---|---|
| `POST /api/events` `{"type", "data"}` with `X-Rcplug-Token: $RCPLUG_TOKEN` | Publish; answers `{"id"}` |
| `GET /api/events/stream?type=desk.,onair.` | Server-sent events (`event: rcplug`, the event as JSON in `data`, its `id` as the event id) whose type starts with one of the prefixes (all without `type`). With `Last-Event-ID` (or `?since=<id>`) the kept events after that id come first. A stream that does not keep up is closed; reconnect with `Last-Event-ID`. At most 32 streams |
| `GET /api/events?since=<id>&type=…` | `{"events": [...]}`: the kept events after `id` |

The Go SDK has `sdk.Publish(ctx, type, data)` and `sdk.Events(ctx, prefixes...)`.

**Events of the desk** (source `console`, while it has the desk's state):

| Type | Data | When |
|---|---|---|
| `desk.recording.started`, `.paused`, `.resumed`, `.stopped` | `state`, `label`, `timeMs` | The recorder changes state (0 ready, 1 paused, 2 recording, 3 no destination) |
| `desk.recording.state` | the same | Any other change of the recorder state |
| `desk.recording.marker` | `timeMs` | A marker is dropped |
| `desk.recording.saved` | `id` | A recording is finished and saved (`lastRecordingID`) |
| `desk.channel.mute`, `desk.channel.listen` | `strip`, `label`, `source`, `muted` / `listening` | A channel is muted or unmuted, listen switched |
| `desk.pad.pressed` | `pad`, `bank` | A SMART pad button is pressed |
| `desk.show.changed` | `name` | Another show is loaded |
| `desk.connection.live`, `desk.connection.lost` | `mode`, `code` | The console gets or loses the desk's state |

**Events of the host** (source `host`):

| Type | Data | When |
|---|---|---|
| `host.plugin.state` | `name`, `state` (`running`, `backoff`, `stopped`), `error` | A plugin starts, crashes or stops |
| `host.update.available` | `updates`: `target`, `name`, `current`, `latest` | A check finds updates (once per new set) |
| `host.update.installed`, `.restarting`, `.failed` | `target`, `version`, `error` | An update ends; rcplug itself restarts to finish |
| `host.plugin.installed`, `host.plugin.removed` | `name`, `version`, `repo`; `name`, `purged` | A plugin was installed from a catalog or removed |

Desk events are best effort: a plugin that must not miss a state (a lamp that has to
be right after its own restart) reads it from the console as well (`api/summary`).

## Audio

On the Duo the whole desk is a JACK graph: every input and output is a JACK port
(`system:capture_*` for the inputs, `JUCEJack:out_*` for the mixer's outputs, …). A
plugin that wants to hear some of them asks the host, which runs **one** bridge for all
plugins: the JACK client `rcplug-taps` into an ALSA loopback, read with `arecord`. It
is only as wide as the ports in use need (2, 4 or 8 channels; it restarts wider when a
stream needs more, and the running streams go on after a short gap) and resamples as
cheaply as it can: on a Duo, 8 channels at the default quality took about half a core
(13 % of the device). The bridge starts when the first plugin asks and stops ten
seconds after the last one hangs up; a port that two plugins want takes one channel.

```go
on, ports, err := sdk.AudioPorts(ctx) // available? which ports?
r, err := sdk.AudioTap(ctx, "system:capture_1", "system:capture_2")
defer r.Close()
// r: interleaved signed 16-bit little-endian samples at 48 kHz, here two channels
```

- There is no switch (since 0.8.0): declaring the permission `audio` is the choice,
  shown to users before they install the plugin. On a Duo the bridge costs about 1 % of
  the device while a plugin listens. (Hosts 0.6.11 to 0.7.4 had a switch and answered
  403 `audio_taps_off` while it was off.)
- Only plugins with the permission `audio` get audio (`X-Rcplug-Token: $RCPLUG_TOKEN`,
  which `sdk.AudioTap` sends); a plugin that sends it on to the network (listen in a
  browser) does so itself.
- Read without pause: a stream that falls a second behind is ended (open it again).
- `GET /api/audio` shows the switch, whether the bridge runs, the channels and the
  streams in use, and the dropouts the bridge reported (`problems`), with the ports
  that can be tapped. The dashboard shows the same, and the process list marks the
  bridge's programs.

## The desk

Plugins never talk to the desk's control port (TCP 2345) themselves: the desk serves
few clients, and its own screen is one of them. They use the console plugin at
`$RCPLUG_HOST_URL/plugins/console/`:

- `GET api/summary`: channels, levels, mutes, recorder, pads in a compact form;
- `GET api/tree` and `GET api/events`: the full state and its live changes;
- `POST api/set`, `api/press`, `api/record`, …: changes.

A plugin that changes the desk declares `"permissions": ["desk:write"]` and sends its
token (`X-Rcplug-Token: $RCPLUG_TOKEN`; the SDK's client does). The console then takes
its writes at the *Control* level's rules, without the PIN and whatever level the
browsers have; write protection stops it like everyone else, and plugins never change
the console's own settings (control level, PIN, write protection). Without the
permission a write answers 403 `permission_desk_write`, also for requests without a
token from the device. Say in your plugin's description what it changes.
Everything else is in [console-api.md](console-api.md); recipes are in the
[cookbook](cookbook.md#the-desk).

## Go SDK

Module `github.com/Hustenreizjuengling/rcplug`, standard library only:

```bash
go get github.com/Hustenreizjuengling/rcplug@latest
```

To work on the SDK and a plugin together, use a workspace:
`go work init ./rcplug ./myplugin`.

### Package `sdk`

| Function | Purpose |
|---|---|
| `Name()` | `RCPLUG_NAME`, or `""` when run alone |
| `ListenAddr(def)` | `127.0.0.1:$RCPLUG_PORT`, or `def` when run alone |
| `PluginDir()` | `RCPLUG_PLUGIN_DIR`, else `.` |
| `DataDir()` | `RCPLUG_DATA_DIR`, else the plugin directory |
| `HostURL()` | `RCPLUG_HOST_URL`, else `http://127.0.0.1:8080` |
| `StatePath(file)` | A path in the data directory; moves a file of that name from the plugin directory (older versions) there |
| `LoadJSON(path, &v)` | Reads JSON; a missing file is no error |
| `SaveJSON(path, v)` | Writes JSON atomically (temporary file, sync, rename) |
| `WriteFileAtomic(path, data, perm)` | The same for any data |
| `SignalContext()` | A context cancelled on SIGINT and SIGTERM |
| `Serve(ctx, addr, h)` | HTTP server with the `X-Rcplug` check, a header timeout and graceful shutdown when `ctx` ends; running requests see their context cancelled |
| `ServeListener(ctx, ln, h)` | The same on an existing listener |
| `CSRF(h)` | Only the `X-Rcplug` check, for your own server |
| `APIHeader` | `"X-Rcplug"` |
| `WriteJSON(w, status, v)` | JSON response with `Cache-Control: no-store` |
| `NewError(status, code, msg)`, `WriteError(w, err)` | Errors as `{"error": "…", "code": "…"}`; other errors become 500 `internal` |
| `Error(w, status, msg)` | A plain error with the code `error` |
| `LowPriority()` | Lowers all threads to nice 19 (the host has done that already) |
| `Publish(ctx, type, data)` | Puts an event on the host's bus, with this plugin as source; `ErrStandalone` without the host |
| `Events(ctx, prefixes...)` | A channel of the host's events whose type starts with one of the prefixes; reconnects and catches up by itself, closes when `ctx` ends |
| `Event` | `ID`, `Time`, `Type`, `Source`, `Data` (raw JSON) |
| `AudioPorts(ctx)` | Whether the shared audio taps are on, and the JACK ports they can hear |
| `AudioTap(ctx, ports...)` | A stream of those ports (S16_LE, 48 kHz, interleaved in the given order); see [Audio](#audio) |

### Package `sdk/console`

`capi.New(hostURL)` (`""` = `sdk.HostURL()`) returns a client for the console behind
the host; set `Base` to talk to a console running alone (`http://127.0.0.1:8082/`). It
sends the plugin's token with every request; `PIN` is only for programs that are not
plugins.

| Method | Request |
|---|---|
| `Status(ctx)` | `GET api/status`: connection, control level, epoch, model |
| `Summary(ctx)` | `GET api/summary` as `Summary` (strips, monitor, Bluetooth, recorder, pads) |
| `Tree(ctx)` | `GET api/tree` as raw JSON |
| `Events(ctx, fn)` | `GET api/events`: calls `fn` for every event until the stream ends |
| `Set(ctx, epoch, path, name, value)` | `POST api/set` |

Errors from the console are `*capi.Error` with `Status`, `Code` and `Message`.

### Package `sdk/release`

The signed release format (`release.json`) for plugin authors who publish their own
releases: `Parse`, `Sign`, `Verify`, `Describe`, `GenerateKey`, `ParseVersion`,
`Newer`. Most authors use the `rcplug-release` command instead, see
[updates.md](updates.md#release-format).

## Compatibility

- `RCPLUG_VERSION` is the host's version. Before 1.0, minor versions may change the
  interface; the [changelog](../CHANGELOG.md) says what changed.
- Additions are compatible: a new manifest field shows as "unknown" on an older host,
  which still loads the plugin and ignores the field.
- `sdk/`, `sdk/console` and `sdk/release` are the public Go interface and are kept
  compatible where possible. Everything under `internal/` may change at any time.
- The console's HTTP API is versioned with rcplug; its error codes are stable.

## Checklist

- [ ] `exec` works from the plugin directory, without a shell profile and without
      anything you installed by hand.
- [ ] The program exits within 3 s of SIGINT; shell scripts trap it.
- [ ] State lives in `$RCPLUG_DATA_DIR` and is written atomically.
- [ ] The web server listens on `127.0.0.1:$RCPLUG_PORT` only.
- [ ] State-changing requests require `X-Rcplug`; GET never changes anything.
- [ ] All URLs in pages are relative.
- [ ] Secrets come from `config/<name>.json`, never from a page, and are never logged
      or put into diagnostics.
- [ ] Nothing polls faster than needed; everything that grows is bounded.
- [ ] Errors of the console (not running yet, desk offline, control off) are shown,
      not fatal.
- [ ] The description says what the plugin changes on the desk, if anything, and
      `permissions` lists what it does.
- [ ] `version` follows `X.Y.Z`; with `update`, releases are signed.
