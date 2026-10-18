#!/usr/bin/env bash
# Tests the device installer (installer/install.sh) in a sandbox: a fresh
# install, a reinstall that keeps settings and data, a reinstall that fails
# halfway (the previous version must come back and run), a version that does
# not start (rolled back), and a first install that does not start.
#
#   tools/install-test.sh        (make install-test)
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
W="$(mktemp -d "${TMPDIR:-/tmp}/rcplug-install-test.XXXXXX")"
PORT="${RCPLUG_TEST_PORT:-18582}"
export RCPLUG_INITDIR="$W/initd" RCPLUG_PIDFILE="$W/rcplug.pid" RCPLUG_LOG="$W/rcplug.out"
cleanup() {
  chmod -R u+w "$W" 2>/dev/null || true
  if [ -x "$W/initd/rcplug" ]; then "$W/initd/rcplug" stop >/dev/null 2>&1 || true; fi
  rm -rf "$W"
}
trap cleanup EXIT
command -v start-stop-daemon >/dev/null 2>&1 || { echo "SKIP: start-stop-daemon not available"; exit 0; }

D="$W/rcplug"
fail() { echo "FAIL: $*" >&2; exit 1; }
version() { curl -fsS --max-time 5 "http://127.0.0.1:$PORT/api/system" 2>/dev/null | sed -n 's/.*"version":"\([^"]*\)".*/\1/p'; }
wait_version() {
  for _ in $(seq 1 100); do [ "$(version)" = "$1" ] && return 0; sleep 0.1; done
  fail "rcplug does not answer as $1 (it is '$(version)')"
}
install() { sh "$W/$1/install.sh" --dir "$D" --addr "127.0.0.1:$PORT" > "$W/install.log" 2>&1; }

echo "== build 1.0.0 and 1.1.0"
for v in 1.0.0 1.1.0; do
  make -C "$REPO" --no-print-directory -s DIST="$W/dist" VERSION="$v" "payload-$(go env GOARCH)" >/dev/null
  mv "$W/dist/payload-$(go env GOARCH)" "$W/v$v"
done
mkdir -p "$W/initd"

echo "== a first install that does not start"
cp -r "$W/v1.0.0" "$W/broken"
printf '#!/bin/sh\nexit 1\n' > "$W/broken/rcplugd"
if install broken; then fail "a broken first install succeeded"; fi
[ ! -e "$D/.previous" ] || fail ".previous left after a failed first install"
rm -rf "$D" "$W/initd/rcplug"

echo "== fresh install, settings and data"
install v1.0.0 || { cat "$W/install.log"; fail "install"; }
wait_version 1.0.0
grep -q "http://127.0.0.1:$PORT/" "$W/install.log" || fail "the dashboard address printed is not the one rcplugd listens on"
mkdir -p "$D/data/console" "$D/plugins/mine"
echo '{"args": []}' > "$D/config/console.json"
echo kept > "$D/data/console/marker"
printf '{"exec": ["/bin/sh", "-c", "sleep 1000"]}\n' > "$D/plugins/mine/plugin.json"

echo "== reinstall 1.1.0 keeps them"
install v1.1.0 || { cat "$W/install.log"; fail "reinstall"; }
wait_version 1.1.0
for f in config/console.json data/console/marker plugins/mine/plugin.json; do
  [ -f "$D/$f" ] || fail "$f was lost"
done
[ ! -e "$D/.previous" ] || fail ".previous left after a successful install"

echo "== a reinstall that fails halfway goes back to 1.1.0"
chmod 0555 "$D/plugins"        # the shipped plugins cannot be moved away
if install v1.0.0; then fail "the reinstall did not fail"; fi
chmod 0755 "$D/plugins"
grep -q 'going back to the previous version' "$W/install.log" || { cat "$W/install.log"; fail "no restore"; }
wait_version 1.1.0
[ "$(cat "$D/VERSION")" = 1.1.0 ] || fail "VERSION is $(cat "$D/VERSION")"
cmp -s "$D/rcplugd" "$W/v1.1.0/rcplugd" || fail "rcplugd was not restored"
[ ! -e "$D/.previous" ] || fail ".previous left after a restore"

echo "== a new version that does not start is rolled back"
cp -r "$W/v1.0.0" "$W/nostart"
printf '#!/bin/sh\nexit 1\n' > "$W/nostart/rcplugd"
if install nostart; then fail "a version that does not start was accepted"; fi
grep -q 'going back to the previous version' "$W/install.log" || { cat "$W/install.log"; fail "no rollback"; }
wait_version 1.1.0
[ -f "$D/plugins/console/plugin.json" ] || fail "the console plugin was not restored"
[ ! -e "$D/.previous" ] || fail ".previous left after a rollback"

echo "OK"
