# Contributing

Thanks for helping! rcplug is a hobby project for people who own a RØDECaster.

## Reports

- **Bugs:** use the bug report template. Include the rcplug version, the device and
  firmware, and the log from the dashboard's Plugins page.
- **Devices:** a device report says how rcplug behaves on a model or firmware that is
  not listed yet. It is the most useful contribution if you own a device.
- **Never post secrets.** The console never shows passwords or keys, but screenshots of
  the System tab or the explorer can contain the serial number, names and addresses. Captures for reports
  must be made with `rccapture` without `--raw`.
- **Security problems:** report them privately, see [SECURITY.md](SECURITY.md).

## Code

1. Read [docs/development.md](docs/development.md) and
   [docs/architecture.md](docs/architecture.md).
2. Keep changes focused, and add tests for new behaviour. Run `make check` before
   you push; CI runs it as well, plus the arm64 tests. The installer test lives in
   [rcplug-toolkit](https://github.com/Hustenreizjuengling/rcplug-toolkit).
3. Go standard library only. Web UIs without third-party code or build steps. Code,
   comments and docs in English; UI texts in English **and** German.
4. Changes that write to the desk need extra care: only existing properties, typed and
   range-checked, never anything on the deny list. Explain in the pull request how you
   verified the behaviour (simulator, hardware, protocol source).
5. Describe user-visible changes in `CHANGELOG.md` under an "Unreleased" heading.

Plugins do not need to live in this repository: ours are in
[rcplug-plugins](https://github.com/Hustenreizjuengling/rcplug-plugins), and yours can live anywhere.
If you wrote one, a link in an issue or discussion is welcome.

## Testing on hardware

Never test write paths on a device during a live show. Start with the careful first test
in [docs/device-notes.md](docs/device-notes.md), and keep the console on *Read only*
until reading works.

## Licence

By contributing you agree that your contribution is published under the MIT licence of
this repository.
