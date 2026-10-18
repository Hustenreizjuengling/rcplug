#!/usr/bin/env bash
# End-to-end test of rcplug's in-app update, without a device or GitHub:
# installs a build as 0.8.0 into a sandbox, publishes a signed 0.9.0 on a fake
# GitHub (tools/fakegithub) and updates through the dashboard API, then
# publishes a 0.9.1 that cannot start and checks that the installer goes back
# to 0.9.0.
#
#   tools/update-test.sh                  on this machine (make update-test)
#   tools/update-test.sh --duo OS.tar.gz  in the userland of a stock Duo
#                                         firmware image (an aarch64 chroot;
#                                         needs sudo and qemu-user binfmt)
set -euo pipefail

PORT="${RCPLUG_TEST_PORT:-18581}"
GHPORT="${RCPLUG_TEST_GH_PORT:-18590}"
SELF="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"

# prepare <dir> <goarch>: builds and signs everything the test needs.
prepare() {
  local w="$1" arch="$2" repo v rel
  repo="$(cd "$(dirname "$SELF")/.." && pwd)"
  mkdir -p "$w/releases"
  cp "$SELF" "$w/update-test.sh"
  (cd "$repo" && go build -o "$w/rcplug-release" ./cmd/rcplug-release &&
    CGO_ENABLED=0 GOARCH="$arch" go build -o "$w/fakegithub" ./tools/fakegithub)
  "$w/rcplug-release" keygen --out "$w/keys" >/dev/null
  for v in 0.8.0 0.9.0 0.9.1; do
    make -C "$repo" --no-print-directory -s DIST="$w/dist" VERSION="$v" "payload-$arch" >/dev/null
    mv "$w/dist/payload-$arch" "$w/v$v"
  done
  rm -rf "$w/dist"
  printf '#!/bin/sh\necho "this build cannot start" >&2\nexit 1\n' > "$w/v0.9.1/rcplugd"
  chmod 0755 "$w/v0.9.1/rcplugd"
  for v in 0.9.0 0.9.1; do
    rel="$w/releases/$v"
    mkdir -p "$rel"
    tar -C "$w/v$v" -czf "$rel/rcplug-$v-aarch64.tar.gz" .
    RCPLUG_SIGNING_KEY="$(cat "$w/keys/rcplug-release.key")" \
      "$w/rcplug-release" sign --project rcplug --version "$v" --out "$rel" "$rel/rcplug-$v-aarch64.tar.gz" >/dev/null
    echo "v$v" > "$rel/TAG"
  done
}

# run <dir>: the test itself, from a prepared directory. Uses only what the
# device has (bash, curl, BusyBox start-stop-daemon), so it also runs in the
# chroot.
run() {
  W="$1" SB="$1/sandbox" GHPID=""
  export RCPLUG_INITDIR="$SB/initd" RCPLUG_PIDFILE="$SB/rcplug.pid" RCPLUG_LOG="$SB/rcplug.out"
  command -v start-stop-daemon >/dev/null 2>&1 || { echo "SKIP: start-stop-daemon not available"; exit 0; }
  trap stop EXIT
  rm -rf "$SB"
  mkdir -p "$SB/initd" "$SB/gh/tester"

  echo "== install 0.8.0 and point it at the fake GitHub"
  "$W/fakegithub" --addr "127.0.0.1:$GHPORT" --dir "$SB/gh" 2>"$SB/fakegithub.log" &
  GHPID=$!
  sh "$W/v0.8.0/install.sh" --dir "$SB/rcplug" --addr "127.0.0.1:$PORT" >/dev/null
  mkdir -p "$SB/rcplug/config"
  cat > "$SB/rcplug/config/rcplugd.env" <<EOF
RCPLUG_UPDATE_API=http://127.0.0.1:$GHPORT
RCPLUG_UPDATE_REPO=tester/rcplug
RCPLUG_UPDATE_KEYS=$(cat "$W/keys/rcplug-release.pub")
RCPLUG_UPDATE_RATE=2048
EOF
  "$SB/initd/rcplug" restart >/dev/null
  wait_version 0.8.0

  echo "== no release yet"
  post /api/updates/check | grep -q 'no release yet' || fail "a missing release is not reported"

  echo "== update to 0.9.0 from the dashboard API"
  publish 0.9.0
  sleep 21 # a check within 20 s returns the previous one
  out="$(post /api/updates/check)"
  grep -q '"target":"rcplug"[^}]*"latest":"0.9.0"[^}]*"available":true' <<<"$out" || fail "0.9.0 not offered: $out"
  post '/api/updates/install?target=rcplug' >/dev/null
  wait_version 0.9.0
  wait_result ""
  grep -q '^0 ' "$SB/rcplug/logs/update.result" || fail "the update was not recorded as successful"
  [ ! -e "$SB/rcplug/.update" ] || fail ".update left behind"
  api /api/updates | grep -q '"last":{"ok":true' || fail "the dashboard does not show the successful update"
  [ -f "$SB/rcplug/config/rcplugd.env" ] || fail "settings lost in the update"
  grep -Eq 'rcplug: [0-9]{2}:[0-9]{2}:[0-9]{2} files in place, starting the new version \(free memory [0-9?]+ MB\)' "$SB/rcplug/logs/update.log" \
    || fail "the installer's steps are not in the update log"

  echo "== 0.9.1 does not start: the installer goes back to 0.9.0"
  publish 0.9.1
  sleep 21
  out="$(post /api/updates/check)"
  grep -q '"latest":"0.9.1"[^}]*"available":true' <<<"$out" || fail "0.9.1 not offered: $out"
  prev="$(result)"
  post '/api/updates/install?target=rcplug' >/dev/null
  wait_result "$prev"
  grep -qv '^0 ' "$SB/rcplug/logs/update.result" || fail "the failed update was not recorded"
  wait_version 0.9.0
  grep -q 'going back to the previous version' "$SB/rcplug/logs/update.log" || fail "no rollback in the log"
  api /api/updates | grep -q '"last":{"ok":false' || fail "the dashboard does not show the failed update"

  echo "OK"
}

stop() {
  # Let a running installer finish first, or it may start rcplugd again.
  for _ in $(seq 1 100); do pgrep -f "$SB/rcplug/.update/payload" >/dev/null || break; sleep 0.2; done
  if [ -x "$SB/initd/rcplug" ]; then "$SB/initd/rcplug" stop >/dev/null 2>&1 || true; fi
  if [ -n "$GHPID" ]; then kill "$GHPID" 2>/dev/null || true; fi
}
fail() { echo "FAIL: $*" >&2; tail -30 "$SB/rcplug/logs/update.log" 2>/dev/null >&2 || true; exit 1; }
api() { curl -fsS --max-time 10 "http://127.0.0.1:$PORT$1"; }
post() { curl -fsS --max-time 60 -X POST -H 'X-Rcplug: 1' "http://127.0.0.1:$PORT$1"; }
version() { api /api/system 2>/dev/null | sed -n 's/.*"version":"\([^"]*\)".*/\1/p'; }
wait_version() {
  for _ in $(seq 1 150); do [ "$(version)" = "$1" ] && return 0; sleep 0.2; done
  fail "rcplug did not come up as $1 (it is '$(version)')"
}
result() { cat "$SB/rcplug/logs/update.result" 2>/dev/null || true; }
wait_result() { # wait_result <previous>: the installer writes a new update.result after install.sh has exited
  local r
  for _ in $(seq 1 150); do r="$(result)"; [ -n "$r" ] && [ "$r" != "$1" ] && return 0; sleep 0.2; done
  fail "the installer did not finish"
}
publish() { rm -rf "$SB/gh/tester/rcplug"; cp -r "$W/releases/$1" "$SB/gh/tester/rcplug"; }

case "${1:-}" in
  --run)
    run "$2" ;;
  --duo)
    image="$(cd "$(dirname "$2")" && pwd)/$(basename "$2")"
    [ -f "$image" ] || { echo "no such image: $2" >&2; exit 2; }
    WORK="$(mktemp -d "${TMPDIR:-/tmp}/rcplug-update-duo.XXXXXX")"
    trap 'sudo rm -rf --one-file-system "$WORK"' EXIT
    echo "== build for the device and unpack $(basename "$image")"
    prepare "$WORK/t" arm64
    mkdir "$WORK/root"
    sudo tar --numeric-owner -xzpf "$image" -C "$WORK/root"
    sudo cp -a "$WORK/t" "$WORK/root/rcplug-update-test"
    # Own network (only loopback) and mount namespace: nothing reaches the
    # outside, and /proc and /dev disappear with the test.
    rc=0
    sudo unshare -n -m sh -c 'mount --bind /dev "$1/dev" && mount -t proc proc "$1/proc" && ip link set lo up &&
      QEMU_CPU=cortex-a53 chroot "$1" /bin/bash /rcplug-update-test/update-test.sh --run /rcplug-update-test' \
      sh "$WORK/root" || rc=$?
    exit $rc ;;
  "")
    WORK="$(mktemp -d "${TMPDIR:-/tmp}/rcplug-update-test.XXXXXX")"
    trap 'rm -rf "$WORK"' EXIT
    echo "== build"
    prepare "$WORK" "$(go env GOARCH)"
    rc=0
    (run "$WORK") || rc=$?
    exit $rc ;;
  *)
    echo "usage: $0 [--duo OS.tar.gz]" >&2; exit 2 ;;
esac
