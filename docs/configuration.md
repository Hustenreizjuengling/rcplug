# Configuration reference

On the device, settings go into files under the install directory
(`/usr/local/rcplug` by default). Updates of rcplug keep them; a firmware update
removes them together with rcplug (the Duo has no partition that survives it):

| File | Applies to | Takes effect |
|---|---|---|
| `config/rcplugd.env` | the host: environment variables in `sh` syntax, e.g. `RCPLUG_ALLOW_HOST=studio.example` | `/etc/init.d/rcplug restart` |
| `config/<plugin>.json` | one plugin: `{"args": [...], "env": {...}}`, `args` are appended to the manifest's command line; `"options"` holds what users set in the dashboard (Plugins → *Settings*) | restarting the plugin (dashboard → Plugins → Restart); *Settings* restarts it itself |

Example: allow control on a Pro II for testing.

```json
{"args": ["--allow-untested-model"]}
```

saved as `config/console.json`, then restart the console.

## Host: `rcplugd`

| Flag | Environment | Default | Meaning |
|---|---|---|---|
| `--addr` | | `:8080` | Dashboard address. The installer's `--addr` sets it in the init script. |
| `--root` | `RCPLUG_ROOT` | directory of the binary | Install directory with `plugins/`, `data/`, `config/` |
| `--allow-host` | `RCPLUG_ALLOW_HOST` | | Extra host names accepted in the `Host` header, comma-separated; `.example.com` allows a suffix |
| `--base-port` | | `9100` | First loopback port assigned to plugins |
| `--low-prio` | | `true` | Run everything at nice 19 |
| `--updates` | `RCPLUG_UPDATES` | on | Offer updates of rcplug and its plugins from signed GitHub releases; `RCPLUG_UPDATES=off` (or `0`, `false`, `no`) switches them off. See [updates.md](updates.md) |
| `--update-repo` | `RCPLUG_UPDATE_REPO` | `Hustenreizjuengling/rcplug` | GitHub repository (`owner/name`) of rcplug's own releases |
| | `RCPLUG_UPDATE_API` | `https://api.github.com` | GitHub API base URL, for tests and mirrors. Plain `http://` only to the loopback interface |
| | `RCPLUG_UPDATE_KEYS` | | Release keys trusted in addition to the built-in ones (base64 Ed25519, comma-separated), for forks and tests |
| `--diagnostics FILE` | | | Write a diagnostics zip (`-` = stdout) and exit: from the running host if it answers at `--addr`, otherwise from the files on disk |
| `--call "METHOD /path"` | | | Call the running host's API (e.g. `POST /api/reload`) and print the answer, then exit; for scripts on the device. Exit status 0 on success, 1 on an error answer, 2 on a bad call |
| `--check` | | | Print whether the host answers and the problems found in the logs, then exit (used by the wizard) |
| `--version` | | | Print the version |

`SIGHUP` (or `/etc/init.d/rcplug reload`) rescans the plugins directory.

## Console plugin

| Flag | Default | Meaning |
|---|---|---|
| `--mixer` (env `RCPLUG_MIXER`) | `127.0.0.1:2345` | The desk's control port |
| `--read-only` | off | Refuse every write, whatever the control level |
| `--allow-untested-model` | off | Offer control on models without a verified profile |
| `--guard` | on | The GUI guard, see [safety.md](safety.md) |
| `--desk-mode` | `auto` | How the console gets the state: `tap` reads along with the device's screen (needs root; no writes, control through the front panel), `connect` is a client of the mixer's port, `auto` reads along on a RØDECaster (where `/usr/bin/rc_audio_mixer` exists and the mixer is local) and connects anywhere else (the simulator, an SSH tunnel) |
| `--boot-grace` | `75s` | Wait until the system has been up this long before connecting (only with the guard on and a desk on this machine, where the device's own screen may still be connecting) |
| `--handshake` | `auto` | Session-open message: `auto` (only if no full sync arrives within 3 s), `always`, `never` |
| `--screen` | `auto` | Screen mirror source: `auto`, `fb`, `drm` or `off`. Users still have to switch it on. |
| `--fb`, `--drm` | `/dev/fb0`, `/dev/dri/card0` | Devices for the screen mirror |
| `--fps` | `2` | Maximum screen mirror frames per second |
| `--panel` | off | Switches the experimental front-panel emulation on for good. Without it, users switch it on and off on the console's *Display* page; either way it works only at the expert level. |
| `--tty` | `/dev/ttyAML3` | Serial line of the front panel |
| `--meters` | `auto` | Reads the level meters and the hardware faders' positions from the mixer's shared memory (read-only), where the Duo keeps them instead of sending them; `off` switches that off. |
| `--touch` | `auto` | Touchscreen event device for operating the screen from the mirror: `auto` (found by its axes), `off`, or `/dev/input/eventN`. Users still have to switch it on. |
| `--sim` | off | Start a built-in simulated Duo and connect to it |
| `--addr` | `127.0.0.1:8082` | Listen address when run standalone (the host passes its own) |
| `--pin` (env `RCPLUG_CONSOLE_PIN`) | | Deprecated. Taken over once as the PIN if none is stored; set the PIN in the UI instead |

The control level, the screen mirror switch and the PIN are set in the web UI and
stored in the console's data directory (`data/console/settings.json` and
`data/console/pin`). Run standalone without `RCPLUG_DATA_DIR`, the console keeps them
in memory only.

## Plugins from rcplug-plugins

Sonar and other plugins have their own settings, described next to their code in
[rcplug-plugins](https://github.com/Hustenreizjuengling/rcplug-plugins).

## Tools

`rccapture`: `--out <file>` (required), `--mixer`, `--raw` (unsanitized, keep it
private), `--timeout` (`30s`), `--guard` (on). See [adding-a-model.md](adding-a-model.md).

`rcsim`: `--addr` (`127.0.0.1:2345`), `--fixture <file>`, `--meter-hz` (`15`),
`--animate` (on), `--exclusive` (drop older clients), `--one-client` (serve one client
like the Duo's mixer: stop listening while a client is connected, listen again when it
leaves), `--ping <interval>` (send `ping` frames like the desk), `--wait-handshake`
(withhold the full sync until the client sends the session-open handshake; meter
changes flow before it).

## Installing and setup tools

The wizard, the setup window and the firmware tools are part of
[rcplug-toolkit](https://github.com/Hustenreizjuengling/rcplug-toolkit); their options are described
there ([docs/tools.md](https://github.com/Hustenreizjuengling/rcplug-toolkit/blob/main/docs/tools.md)).
