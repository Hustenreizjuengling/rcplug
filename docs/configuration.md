# Configuration reference

On the device, settings go into files under the install directory
(`/usr/local/rcplug` by default). Updates of rcplug keep them; a firmware update
removes them together with rcplug (the Duo has no partition that survives it):

| File | Applies to | Takes effect |
|---|---|---|
| `config/rcplugd.env` | the host: environment variables in `sh` syntax, e.g. `RCPLUG_ALLOW_HOST=studio.example` | `/etc/init.d/rcplug restart` |
| `config/<plugin>.json` | one plugin: `{"args": [...], "env": {...}}`, `args` are appended to the manifest's command line | restarting the plugin (dashboard → Plugins → Restart) |

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
| `--diagnostics FILE` | | | Write a diagnostics zip (`-` = stdout) and exit: from the running host if it answers at `--addr`, otherwise from the files on disk |
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
| `--boot-grace` | `75s` | Wait until the system has been up this long before connecting |
| `--handshake` | `auto` | Session-open message: `auto` (only if the desk stays silent), `always`, `never` |
| `--screen` | `auto` | Screen mirror source: `auto`, `fb`, `drm` or `off`. Users still have to switch it on. |
| `--fb`, `--drm` | `/dev/fb0`, `/dev/dri/card0` | Devices for the screen mirror |
| `--fps` | `2` | Maximum screen mirror frames per second |
| `--panel` | off | Experimental front-panel emulation (expert level only) |
| `--tty` | `/dev/ttyAML3` | Serial line of the front panel |
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
`--animate` (on), `--exclusive` (drop older clients like a single-client desk),
`--wait-handshake`.

## Installing and setup tools

The wizard, the setup window and the firmware tools are part of
[rcplug-toolkit](https://github.com/Hustenreizjuengling/rcplug-toolkit); their options are described
there ([docs/tools.md](https://github.com/Hustenreizjuengling/rcplug-toolkit/blob/main/docs/tools.md)).
