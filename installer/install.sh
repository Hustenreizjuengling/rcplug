#!/bin/sh
# rcplug device-side installer. Runs ON the RØDECaster (the wizard pushes it
# there together with the payload). Installs the plugin host into one
# directory, registers a SysV init service and starts it. Safe to re-run.
# POSIX sh (on the Duo /bin/sh is bash, with coreutils and BusyBox 1.35).
#
# Layout of the install directory:
#   rcplugd               the plugin host
#   plugins/<name>/       plugin code (replaced on every install)
#   data/<name>/          plugin data (kept)
#   config/<name>.json    optional operator settings per plugin (kept), e.g.
#                         config/console.json: {"args": ["--read-only"]}
#   config/rcplugd.env    optional host settings (kept)
set -eu

ADDR=""
FORCE_DIR=""
# Where the SysV init script goes. Overridable for tests in a sandbox.
INITDIR="${RCPLUG_INITDIR:-/etc/init.d}"

fail() { echo "rcplug: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --addr) [ $# -ge 2 ] || fail "--addr needs a value"; ADDR="$2"; shift 2;;
    --dir)  [ $# -ge 2 ] || fail "--dir needs a value"; FORCE_DIR="$2"; shift 2;;
    *) echo "unknown argument: $1" >&2; exit 2;;
  esac
done

# Without --addr an existing installation keeps its address.
if [ -z "$ADDR" ] && [ -f "$INITDIR/rcplug" ]; then
  ADDR="$(sed -n 's/^RCPLUG_ADDR="\(.*\)"$/\1/p' "$INITDIR/rcplug")"
fi
ADDR="${ADDR:-:8080}"

# Both values end up in the init script, so only plain values are accepted.
printf '%s\n' "$ADDR" | grep -Eq '^([0-9]{1,3}(\.[0-9]{1,3}){3})?:[0-9]{1,5}$' \
  || fail "--addr must look like :8080 or 192.168.1.50:8080"
if [ -n "$FORCE_DIR" ]; then
  printf '%s\n' "$FORCE_DIR" | grep -Eq '^/[A-Za-z0-9._/-]+$' \
    || fail "--dir must be an absolute path of letters, digits, '.', '_', '-' and '/'"
fi

# The payload (rcplugd, plugins/, initd/) sits next to this script.
SRC="$(cd "$(dirname "$0")" && pwd)"
[ -f "$SRC/rcplugd" ] || fail "payload incomplete: $SRC/rcplugd missing"

arch="$(uname -m)"
case "$arch" in
  aarch64|arm64) : ;;
  *) echo "WARNING: expected aarch64, found $arch; continuing anyway." >&2;;
esac

# The install directory: --dir, else the one of an existing installation,
# else /usr/local/rcplug on the root partition. The Duo has no other place
# for it: its firmware (1.7.x) mounts only the root file system and RØDE's
# own vfat partitions (/Application/update-staging with the device's identity
# and the update image, /Application/emmc-data), and a firmware update writes
# a fresh root partition. So rcplug survives reboots, but a firmware update
# removes it, like the SSH key; reinstall afterwards.
pick_dir() {
  if [ -n "$FORCE_DIR" ]; then echo "$FORCE_DIR"; return; fi
  if [ -f "$INITDIR/rcplug" ]; then
    old="$(sed -n 's/^RCPLUG_DIR="\(.*\)"$/\1/p' "$INITDIR/rcplug")"
    if [ -n "$old" ] && [ -d "$old" ]; then echo "$old"; return; fi
  fi
  echo "/usr/local/rcplug"
}

DIR="$(pick_dir)"
echo "rcplug: installing to $DIR (arch $arch, dashboard $ADDR)"
mkdir -p "$DIR/plugins" "$DIR/data" "$DIR/config"

# Stage everything first, while the old version keeps running. The staging
# directory is outside plugins/, so the host never sees half-copied plugins.
STAGE="$DIR/.staging"
# What this install replaces is kept here until the new version runs, so any
# failure can be undone. In-app updates rely on this: there may be nobody with
# SSH at hand when one goes wrong.
PREV="$DIR/.previous"
STOPPED=0  # the running instance was stopped
SWAPPED=0  # files of the installation were replaced; $PREV holds the old ones

# restore_previous puts back the host, VERSION, rccapture, the init script and
# the plugins this install replaced, and removes the plugins it added.
# $PREV is kept if anything cannot be put back.
restore_previous() {
  [ -f "$PREV/rcplugd" ] || return 0 # a first installation has nothing to go back to
  echo "rcplug: going back to the previous version" >&2
  ok=1
  for f in rcplugd VERSION rccapture; do
    if [ -f "$PREV/$f" ]; then cp -p "$PREV/$f" "$DIR/$f" || ok=0; fi
  done
  if [ -f "$PREV/new-plugins" ]; then
    while read -r name; do
      if [ -n "$name" ]; then rm -rf "${DIR:?}/plugins/$name"; fi
    done < "$PREV/new-plugins"
  fi
  for p in "$PREV"/plugins/*/; do
    [ -d "$p" ] || continue
    name="$(basename "$p")"
    rm -rf "${DIR:?}/plugins/$name"
    mv "$p" "$DIR/plugins/$name" || ok=0
  done
  if [ -f "$PREV/initd" ]; then cp -p "$PREV/initd" "$INITDIR/rcplug" || ok=0; fi
  if [ $ok = 0 ]; then
    echo "rcplug: not everything could be put back; the previous files are in $PREV" >&2
    KEEP_PREV=1
  fi
}

KEEP_PREV=0
cleanup() {
  rc=$?
  rm -rf "$STAGE"
  if [ $rc -ne 0 ]; then
    if [ $SWAPPED = 1 ]; then restore_previous; fi
    if [ $STOPPED = 1 ] && [ -x "$INITDIR/rcplug" ]; then
      echo "rcplug: installation failed; starting the previous version again" >&2
      "$INITDIR/rcplug" start >&2 || true
    fi
  fi
  if [ $KEEP_PREV = 0 ]; then rm -rf "$PREV"; fi
  exit $rc
}
trap cleanup EXIT
rm -rf "$STAGE"
mkdir -p "$STAGE/plugins"
cp "$SRC/rcplugd" "$STAGE/rcplugd" || fail "copying rcplugd failed"
for p in "$SRC"/plugins/*/; do
  [ -d "$p" ] || continue
  cp -r "$p" "$STAGE/plugins/$(basename "$p")" || fail "copying plugin $p failed"
done
sed -e "s#__RCPLUG_DIR__#$DIR#g" -e "s#__RCPLUG_ADDR__#$ADDR#g" "$SRC/initd/rcplug" > "$STAGE/initd" \
  || fail "writing the init script failed"
grep -q "^RCPLUG_DIR=\"$DIR\"$" "$STAGE/initd" || fail "init script template not filled in"

# Stop a running instance before replacing its files.
if [ -x "$INITDIR/rcplug" ]; then
  STOPPED=1
  "$INITDIR/rcplug" stop >/dev/null 2>&1 || true
fi

# Keep what this install replaces (see restore_previous).
rm -rf "$PREV"
mkdir -p "$PREV/plugins" || fail "creating $PREV failed"
for f in rcplugd VERSION rccapture; do
  if [ -f "$DIR/$f" ]; then cp -p "$DIR/$f" "$PREV/$f" || fail "saving the previous $f failed"; fi
done
if [ -f "$INITDIR/rcplug" ]; then cp -p "$INITDIR/rcplug" "$PREV/initd" || fail "saving the previous init script failed"; fi
: > "$PREV/new-plugins" || fail "writing to $PREV failed"
SWAPPED=1

chmod 0755 "$STAGE/rcplugd"
mv -f "$STAGE/rcplugd" "$DIR/rcplugd" || fail "installing rcplugd failed"
# rccapture records the desk state for adding models (docs/adding-a-model.md).
if [ -f "$SRC/rccapture" ]; then
  { cp "$SRC/rccapture" "$DIR/rccapture.new" && chmod 0755 "$DIR/rccapture.new" && mv -f "$DIR/rccapture.new" "$DIR/rccapture"; } \
    || fail "installing rccapture failed"
fi
for f in VERSION LICENSE THIRD_PARTY_NOTICES.md; do
  if [ -f "$SRC/$f" ]; then cp "$SRC/$f" "$DIR/$f" || fail "copying $f failed"; fi
done

# Plugins shipped in the payload replace their previous version. Other
# plugins (installed with the wizard's --plugin) and all data stay.
for p in "$STAGE"/plugins/*/; do
  [ -d "$p" ] || continue
  name="$(basename "$p")"
  # v0.2 and v0.3 kept settings next to the code; move them to the data
  # directory before the old folder goes.
  for old in "$DIR/plugins/$name"/*.state.json; do
    [ -f "$old" ] || continue
    mkdir -p "$DIR/data/$name"
    if [ ! -e "$DIR/data/$name/$(basename "$old")" ]; then mv "$old" "$DIR/data/$name/"; fi
  done
  if [ -d "$DIR/plugins/$name" ]; then
    mv "$DIR/plugins/$name" "$PREV/plugins/$name" || fail "saving the previous plugin $name failed"
  else
    echo "$name" >> "$PREV/new-plugins"
  fi
  mv "$p" "$DIR/plugins/$name" || fail "installing plugin $name failed"
  if [ -f "$DIR/plugins/$name/$name" ]; then chmod 0755 "$DIR/plugins/$name/$name"; fi
done
find "$DIR/plugins" -type f -name '*.sh' -exec chmod 0755 {} + 2>/dev/null || true

# Install the init service with the chosen directory and address.
chmod 0755 "$STAGE/initd"
mv -f "$STAGE/initd" "$INITDIR/rcplug" || fail "installing the init script failed"

# Register for autostart (skipped in a test sandbox): start 05, before RØDE's
# screen (S06rode-display-service), so the console reads along from the
# screen's first connection to the mixer; stop 20. Earlier versions used
# S20, so the old links go first.
if [ -z "${RCPLUG_INITDIR:-}" ] && command -v update-rc.d >/dev/null 2>&1; then
  update-rc.d -f rcplug remove >/dev/null 2>&1 || true
  update-rc.d rcplug defaults 05 20 >/dev/null 2>&1 || true
fi

if ! "$INITDIR/rcplug" start; then
  # The cleanup puts the previous version back and starts it.
  "$INITDIR/rcplug" stop >/dev/null 2>&1 || true
  STOPPED=0
  if [ -f "$PREV/rcplugd" ]; then STOPPED=1; fi
  fail "the new version did not start, see $DIR/logs/"
fi
SWAPPED=0
rm -rf "$PREV"

# The address to open: the one rcplugd listens on, or this device's own.
ip="${ADDR%:*}"
case "$ip" in
  ""|0.0.0.0)
    ip="$(ip -4 addr show 2>/dev/null | awk '/inet /{print $2}' | cut -d/ -f1 | grep -v '^127\.' | head -1 || true)"
    [ -n "$ip" ] || ip="<device-ip>";;
esac
port="${ADDR##*:}"
ver="$(cat "$SRC/VERSION" 2>/dev/null || echo '?')"
echo ""
echo "rcplug $ver installed and started."
echo "  Dashboard:   http://$ip:$port/"
echo "  Install dir: $DIR   (logs: $DIR/logs/)"
echo "  Settings:    $DIR/config/   (see docs/configuration.md)"
echo "  A firmware update removes rcplug (and your SSH key); install again afterwards."
