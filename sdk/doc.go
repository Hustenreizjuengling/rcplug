// Package sdk is the small helper library for rcplug plugins written in Go.
//
// A plugin is a directory under <root>/plugins/<name>/ with a plugin.json and
// an executable. The host (rcplugd) starts it with these environment
// variables (see docs/plugin-api.md for the full contract):
//
//	RCPLUG_NAME        plugin name (the directory name)
//	RCPLUG_PORT        loopback port to serve the web UI on ("0" if the plugin has none)
//	RCPLUG_PLUGIN_DIR  the plugin's code directory (replaced on reinstall)
//	RCPLUG_DATA_DIR    the plugin's data directory (kept across reinstalls)
//	RCPLUG_HOST_URL    base URL of the host, e.g. http://127.0.0.1:8080
//	RCPLUG_VERSION     version of the host
//
// The helpers here read those variables with sensible fallbacks, so a plugin
// also runs standalone during development.
package sdk
