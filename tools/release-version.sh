#!/bin/sh
# The version steps of a release, used by the release workflow:
#
#   tools/release-version.sh prepare X.Y.Z   date the changelog's "Unreleased"
#                                            section as X.Y.Z, set VERSION and
#                                            the built-in plugins' manifests
#   tools/release-version.sh next            start the next patch version: a new
#                                            "Unreleased" section, VERSION, manifests
#
# Run from anywhere; it changes files only (no git).
set -eu
cd "$(dirname "$0")/.."

# set_manifests writes the version into the manifests shipped with the host
# (a test checks they match VERSION).
set_manifests() {
  for m in plugins/*/plugin.json; do
    [ -f "$m" ] || continue
    sed -i "s/^\(  \"version\": \"\)[^\"]*\(\",\)\$/\1$1\2/" "$m"
    grep -q "^  \"version\": \"$1\",\$" "$m" || { echo "could not set the version in $m" >&2; exit 1; }
  done
}

case "${1:-}" in
  prepare)
    v="${2:-}"
    printf '%s\n' "$v" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || { echo "usage: $0 prepare X.Y.Z" >&2; exit 2; }
    head="$(grep -m1 -n '^## ' CHANGELOG.md)" || { echo "CHANGELOG.md has no sections" >&2; exit 1; }
    line="${head%%:*}"
    case "${head#*:}" in
      *" - Unreleased") ;;
      *) echo "the newest section of CHANGELOG.md is not '## <version> - Unreleased'" >&2; exit 1 ;;
    esac
    entries="$(sed -n "$((line + 1)),\$p" CHANGELOG.md | sed '/^## /,$d' | grep -c '^- ' || true)"
    [ "$entries" -gt 0 ] || { echo "the Unreleased section of CHANGELOG.md has no entries" >&2; exit 1; }
    sed -i "${line}s/.*/## $v - $(date -u +%Y-%m-%d)/" CHANGELOG.md
    echo "$v" > VERSION
    set_manifests "$v"
    echo "prepared $v"
    ;;
  next)
    cur="$(cat VERSION)"
    next="$(printf '%s\n' "$cur" | awk -F. '{ printf "%d.%d.%d", $1, $2, $3 + 1 }')"
    awk -v s="## $next - Unreleased" 'd == 0 && /^## / { print s; print ""; d = 1 } { print }' CHANGELOG.md > CHANGELOG.md.tmp
    mv CHANGELOG.md.tmp CHANGELOG.md
    echo "$next" > VERSION
    set_manifests "$next"
    echo "next version: $next"
    ;;
  *)
    echo "usage: $0 prepare X.Y.Z | next" >&2
    exit 2
    ;;
esac
