package sdk

import (
	"os"
	"path/filepath"
)

// Name returns the plugin name (RCPLUG_NAME), or "" when run standalone.
func Name() string { return os.Getenv("RCPLUG_NAME") }

// ListenAddr returns the loopback address the host assigned (RCPLUG_PORT),
// or def when the plugin runs standalone.
func ListenAddr(def string) string {
	if p := os.Getenv("RCPLUG_PORT"); p != "" && p != "0" {
		return "127.0.0.1:" + p
	}
	return def
}

// PluginDir returns the plugin's code directory (RCPLUG_PLUGIN_DIR, else ".").
// It is replaced when the plugin is reinstalled; keep state in DataDir.
func PluginDir() string {
	if d := os.Getenv("RCPLUG_PLUGIN_DIR"); d != "" {
		return d
	}
	return "."
}

// DataDir returns the plugin's persistent data directory (RCPLUG_DATA_DIR),
// falling back to PluginDir. The host creates it; it survives reinstalls.
func DataDir() string {
	if d := os.Getenv("RCPLUG_DATA_DIR"); d != "" {
		return d
	}
	return PluginDir()
}

// HostURL returns the base URL of the rcplug host (RCPLUG_HOST_URL), e.g. to
// reach other plugins at HostURL()+"/plugins/<name>/".
func HostURL() string {
	if u := os.Getenv("RCPLUG_HOST_URL"); u != "" {
		return u
	}
	return "http://127.0.0.1:8080"
}

// StatePath returns the path of a state file in the plugin's data directory.
// A file of that name left in the plugin directory by an older version is
// moved over on first use, so upgrades keep their settings.
func StatePath(name string) string {
	p := filepath.Join(DataDir(), name)
	if old := filepath.Join(PluginDir(), name); old != p {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			if _, err := os.Stat(old); err == nil {
				_ = os.MkdirAll(filepath.Dir(p), 0755)
				_ = os.Rename(old, p)
			}
		}
	}
	return p
}
