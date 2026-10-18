#!/bin/sh
# hello-sh: the smallest useful rcplug plugin, written in POSIX sh.
#
# It has no web UI (no httpProxy in plugin.json). Everything it prints ends
# up in the plugin log on the dashboard. Once a minute it logs the CPU
# temperature and, if wget is available, the console's connection state.
#
# The host passes its environment (see docs/plugin-api.md):
#   RCPLUG_NAME, RCPLUG_DATA_DIR, RCPLUG_HOST_URL, RCPLUG_VERSION, …
set -u

interval=${HELLO_INTERVAL:-60}
data=${RCPLUG_DATA_DIR:-.}
host=${RCPLUG_HOST_URL:-http://127.0.0.1:8080}

# The host stops plugins with SIGINT to the whole process group. A background
# job ignores SIGINT in a non-interactive shell, so the trap ends the sleep.
trap 'kill "$sleeper" 2>/dev/null; echo "bye"; exit 0' INT TERM
sleeper=""

echo "hello from ${RCPLUG_NAME:-hello-sh} on rcplug ${RCPLUG_VERSION:-dev}, data in $data"

# A counter in the data directory survives restarts and reinstalls.
count=0
[ -f "$data/starts" ] && count=$(cat "$data/starts")
count=$((count + 1))
echo "$count" > "$data/starts"
echo "started $count time(s)"

while :; do
	temp=""
	if [ -r /sys/class/thermal/thermal_zone0/temp ]; then
		temp=$(( $(cat /sys/class/thermal/thermal_zone0/temp) / 1000 ))
	fi
	desk=""
	if command -v wget >/dev/null 2>&1; then
		# GET requests need no header; POSTs need "X-Rcplug: 1".
		desk=$(wget -q -O - "$host/plugins/console/api/status" 2>/dev/null |
			sed -n 's/.*"connection":{"state":"\([a-z]*\)".*/\1/p')
	fi
	echo "cpu=${temp:-?}°C console=${desk:-unknown}"
	sleep "$interval" &
	sleeper=$!
	wait "$sleeper"
done
