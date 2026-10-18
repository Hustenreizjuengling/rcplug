# Updates

rcplug and its plugins update themselves from signed releases on GitHub. The
dashboard checks only when you ask it to, and installs only what you click. A firmware
update of the RØDECaster still removes rcplug; install it again with the
[toolkit](https://github.com/Hustenreizjuengling/rcplug-toolkit) afterwards.

## Using it

**System → Updates → Check for updates** asks GitHub for the latest release of rcplug
and of every installed plugin that names an update source. Each entry shows the
installed and the latest version, the release notes and an **Install** button. A
plugin with an update also shows it on its card under **Plugins**.

- **A plugin** is replaced in place and restarted if it was running. Its settings
  (`config/<name>.json`) and data (`data/<name>/`) stay.
- **rcplug itself** is installed by the `install.sh` of the new package, the same
  script the toolkit runs. rcplug and all plugins restart, which takes about half a
  minute; the RØDECaster itself and its audio keep running. The dashboard reloads by
  itself when the new version answers. If the new version does not start, the
  installer puts the previous one back and starts it again. The System page shows the
  outcome of the last update; the installer's output is in `logs/update.log`.

Settings, plugin data, logs, plugins you installed yourself and the SSH key are kept.

Requirements:

- The RØDECaster can reach `api.github.com` and `github.com` over HTTPS (port 443).
- Its clock is roughly right, or HTTPS fails. The firmware sets it over NTP when the
  network comes up.
- Free storage of about three times the package size plus 20 MB.

To switch updates off, put `RCPLUG_UPDATES=off` into `config/rcplugd.env` and restart
rcplug (see [configuration.md](configuration.md)). The dashboard then offers none, and
rcplug never contacts GitHub.

Updating from the computer with the toolkit's wizard works as before and is the way
back after a firmware update.

## How updates are secured

- Every release carries `release.json`, which lists each package with its size and
  SHA-256, and `release.json.sig`, an Ed25519 signature over that file. rcplugd only
  offers a release whose signature comes from a trusted key, and installs a package
  only if its size and SHA-256 match.
- The trusted keys are built into rcplugd
  ([`sdk/release/keys.go`](../sdk/release/keys.go)). A plugin may name one more key
  for its own releases (`update.publicKey`). The private key never leaves the release
  workflow, where it is a repository secret.
- `release.json` names the project (it must be the repository's name) and the version
  (it must be the release tag without `v`). A release of one repository cannot pass as
  one of another, and an old signed release cannot pass as a new one.
- Only newer versions are offered; there are no downgrades.
- Downloads use HTTPS only and follow at most five redirects, all HTTPS. A package may
  be at most 200 MB and unpack to at most 400 MB.
- Packages are unpacked by rcplugd itself: regular files and directories only, no
  links, no absolute paths, no `..`.
- rcplug's own package must contain `install.sh` and a `VERSION` that matches the
  release; a plugin package must contain a `plugin.json` with the plugin's name and
  the version from `release.json`.
- Nothing happens on its own: no check, no download and no install without a click.

Like every other dashboard function, starting an update needs no password: anyone who
can open the dashboard can install the latest signed release (see
[safety.md](safety.md#limits)). They cannot install anything unsigned or older.

GitHub answers at most 60 unauthenticated API requests per hour from one address. A
check asks once for rcplug and once per plugin repository, and a second check within
20 seconds returns the first result.

## Release format

A release is a GitHub release with the tag `v<version>` (the latest release that is
neither a draft nor a pre-release counts). Next to the packages it has:

`release.json`:

```json
{
  "format": 1,
  "project": "rcplug-plugins",
  "version": "0.6.0",
  "published": "2026-10-18T02:39:13Z",
  "assets": [
    {
      "name": "sonar-0.6.0-aarch64.tar.gz",
      "size": 2496891,
      "sha256": "6b1f…",
      "kind": "plugin",
      "plugin": "sonar",
      "version": "0.6.0"
    }
  ]
}
```

`release.json.sig`: the base64 Ed25519 signature over the exact bytes of
`release.json`, followed by a newline.

`kind` is `platform` for a package with `install.sh` at its root (rcplug itself) and
`plugin` for one with `plugin.json` at its root; other files may be listed without a
kind and are never installed. One release can carry several plugins, one package
each; rcplugd picks the package whose `plugin` is the installed plugin's name.

`cmd/rcplug-release` writes and checks all of this:

```bash
go run ./cmd/rcplug-release keygen --out ~/rcplug-keys        # once: key pair (private file 0600)
export RCPLUG_SIGNING_KEY="$(cat ~/rcplug-keys/rcplug-release.key)"
go run ./cmd/rcplug-release sign --project <repository> --version 0.6.0 --out dist dist/*.tar.gz
go run ./cmd/rcplug-release verify dist                          # against rcplugd's built-in keys
go run ./cmd/rcplug-release verify --pub ~/rcplug-keys/rcplug-release.pub dist
```

`sign` classifies every package by its content and prints what it found; `verify`
checks the signature and every file.

## Publishing a plugin with updates

1. Add the source to `plugin.json`:

   ```json
   "update": {"github": "you/your-plugins"}
   ```

   Plugins in [rcplug-plugins](https://github.com/Hustenreizjuengling/rcplug-plugins)
   are signed with rcplug's key. For your own repository, create your own key pair
   and add `"publicKey": "<contents of rcplug-release.pub>"`. rcplugd trusts that key
   for this plugin in addition to its built-in ones; the key in the installed
   `plugin.json` checks the next update.
2. Package the plugin as `<name>-<version>-aarch64.tar.gz` with `plugin.json` at the
   root of the archive.
3. Sign: `rcplug-release sign --project <repository name> --version <tag without v>`.
4. Create a GitHub release with the tag `v<version>` and upload the packages,
   `release.json` and `release.json.sig`. The repository must be public: the
   RØDECaster cannot log in to GitHub.

The release workflow of rcplug-plugins (`make package sign`, then
`rcplug-release verify`) is a complete example.

## The project's signing key

rcplug and rcplug-plugins are signed in their release workflows with the repository
secret `RCPLUG_SIGNING_KEY` (the content of `rcplug-release.key`). The workflow fails
if the secret is missing or if the signature does not verify against the keys built
into rcplugd, so a release that devices would refuse is never published.

Replacing the key:

1. Create a new key pair and add the new public key to
   [`sdk/release/keys.go`](../sdk/release/keys.go), keeping the old one.
2. Release that version, still signed with the old key, so devices learn the new key.
3. Put the new private key into the secrets, and remove the old public key in a later
   release.

If the private key is lost, devices cannot verify later releases until they get a
version with a new key through the toolkit. If it leaks, replace it at once and remove
the old key from `keys.go`; until devices have that version, a release signed with the
leaked key would be accepted.

## Testing

`make update-test` installs a build as 0.8.0 into a sandbox, serves signed releases
from a fake GitHub (`tools/fakegithub`) and updates to 0.9.0 through the API, then
offers a 0.9.1 that cannot start and checks that the installer goes back to 0.9.0.
`make update-test DUO=<rodecaster-duo-…-os.tar.gz>` runs the same inside the
userland of a stock firmware image (an aarch64 chroot; needs `sudo` and
`qemu-user-static`/binfmt).

To point a host at a test server: `RCPLUG_UPDATE_API` (API base URL; plain HTTP only
to the loopback interface), `RCPLUG_UPDATE_REPO` (rcplug's repository) and
`RCPLUG_UPDATE_KEYS` (extra trusted public keys, comma-separated), see
[configuration.md](configuration.md).
