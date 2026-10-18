# Development

## Requirements

- **Go 1.22** or newer. Nothing else is needed to build; rcplug uses only the standard
  library.
- Optional, for the full checks: **Node 18+** (web UI tests), **shellcheck** and
  **qemu-user** (`qemu-aarch64`, arm64 tests).

`make help` lists all targets. The important ones:

| Target | Does |
|---|---|
| `make run-local` | Host with the console and the examples on `http://127.0.0.1:8080`, the console connected to the simulator. `EXTRA_PLUGINS=dir…` adds plugin directories (`make run-local` in rcplug-plugins does that for its plugins), `LOCAL_ADDR` and `LOCAL_MIXER` change the ports. |
| `make check` | gofmt, vet, race tests, web tests, shellcheck: what CI runs |
| `make test-arm64` | The test suite as arm64 binaries under qemu-user |
| `make fuzz` | Fuzzes the protocol decoder for 30 s |
| `make package` | The release files in `dist/`: device tarball, examples, SHA256SUMS |
| `make package-host` | The device tree for this machine in `dist/payload-host`, used by the toolkit's installer test |
| `make payload-arm64` | The device tree in `dist/payload-arm64` (`payload-<goarch>` for other architectures) |
| `make update-test` | In-app update end to end against a fake GitHub; `DUO=<stock os.tar.gz>` runs it in the device's own userland (see [updates.md](updates.md#testing)) |
| `make sign` | Signs the files from `make package` (`release.json` + `.sig`); the key comes from `RCPLUG_SIGNING_KEY` |

Run a single program from source:

```bash
go run ./cmd/console --sim                    # console with a built-in simulated Duo, :8082
go run ./cmd/rcsim --addr 127.0.0.1:2345      # only the simulated desk
```

## Repository layout

```
cmd/rcplugd/         plugin host and its dashboard (web/)
cmd/console/         console plugin: wiring and flags
cmd/rcsim/           simulator of the desk's control port
cmd/rccapture/       captures a sanitized device state
internal/host/       manifests, supervisor, HTTP server and proxy
internal/rcproto/    protocol: values, trees, changes, frames, layout, models, sanitizing
internal/desk/       connection to the desk, mirror, GUI guard, JSON encoding
internal/console/    console API, write policy, PIN, settings, web UI (web/)
internal/hw/         screen mirror, front-panel emulation
internal/rcsim/      simulator and the Duo fixture
sdk/                 public helpers for Go plugins; sdk/console is the console client
plugins/<name>/      manifests of the built-in plugins (the console)
examples/            example plugins (Go and sh)
installer/           device installer and init script (part of the device tarball)
tools/webtest/       web UI tests
docs/                documentation
```

`internal/` packages may change at any time. `sdk/` is the interface for plugin
authors; keep it small and compatible.

## Tests

- **Unit tests** next to the code. The protocol tests decode the real Duo capture and
  re-encode it byte for byte.
- **Integration tests** run the console against the simulator in-process: mirroring,
  every write path, the policy, the GUI guard against a single-client desk, reconnects,
  the event stream. The host tests start real plugin processes (shell scripts).
- **Web tests** (`tools/webtest`, `node --test`) cover the console's store (loading,
  batches during a load, epoch changes) and check, for the console and the dashboard,
  that both languages have the same keys and that every key used in the code exists.
- **Update test** (`make update-test`): a build installed as 0.8.0 in a sandbox is
  updated to a signed 0.9.0 from a fake GitHub through the API; a 0.9.1 that cannot
  start must be rolled back. With `DUO=` it runs in an aarch64 chroot of a stock
  firmware image, with the device's BusyBox and shell.
- **Installer test:** the toolkit's `make installer-test` installs this repository's
  device tree with the wizard in a sandbox (a fake `ssh` runs the remote commands
  locally): install, update, add a plugin, uninstall.

Tests must not depend on timing luck: wait for a condition with a deadline instead of
sleeping, and run new tests a few times with `-race -count=10`.

## Conventions

- Go standard library only, `gofmt`, `go vet` clean.
- Code, comments and documentation in English. User interfaces in German and English.
- Errors returned by HTTP APIs are `{"error", "code"}` with a stable `code`
  (`sdk.NewError`); UIs translate the code, not the message.
- No build step for web UIs: plain ES modules embedded with `go:embed`, no third-party
  code, all URLs relative.
- Shell scripts are POSIX sh where they run on the device (bash as `sh` there, with
  coreutils and BusyBox 1.35) and pass shellcheck.

## Working on the console UI

The UI lives in `internal/console/web`; see [architecture.md](architecture.md#web-ui)
for the modules. To add a tab:

1. Create `js/views/<name>.js` exporting `{id, title, render(scope)}`. `render` returns
   DOM nodes. Use `scope.bind(path, prop, fn)` for live values and
   `scope.interval`/`scope.timeout` for timers; everything is released when the view
   is left or the tree reloads.
2. Register it in `js/views/index.js`.
3. Add its texts to both `js/i18n/en.js` and `js/i18n/de.js`.

`go run ./cmd/console --sim` serves the files from the binary, so restart it after
changes. `window.__rcplug` in the browser console exposes the store for debugging.

## Releasing

1. Update `VERSION` and the `version` of the manifests in `plugins/` (a test checks
   they match), and add a section to `CHANGELOG.md`. Its text becomes the release
   notes, which the dashboard shows next to the update.
2. Commit, tag `v<version>` and push the tag. The release workflow checks that the tag
   matches `VERSION`, runs the tests, builds the package, signs it with the repository
   secret `RCPLUG_SIGNING_KEY`, checks the signature against the keys built into
   rcplugd and publishes a GitHub release with the notes from the changelog.
   Devices offer it as an update from then on (see [updates.md](updates.md)).

## The three repositories

rcplug is developed in three repositories that are checked out side by side:

| Repository | Contains |
|---|---|
| [rcplug](https://github.com/Hustenreizjuengling/rcplug) (this one) | host, dashboard, console, SDK, simulator, device installer |
| [rcplug-plugins](https://github.com/Hustenreizjuengling/rcplug-plugins) | plugins such as Sonar; they use the SDK from here |
| [rcplug-toolkit](https://github.com/Hustenreizjuengling/rcplug-toolkit) | the computer-side tools: setup, firmware, flashing, install, diagnostics |

Plugins import the SDK as `github.com/Hustenreizjuengling/rcplug/sdk`. To work on the SDK and a
plugin at the same time, put a `go.work` next to the checkouts (it is not committed):

```bash
go work init ./rcplug ./rcplug-plugins ./rcplug-toolkit
```
