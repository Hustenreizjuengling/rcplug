package console

import (
	"net/http"
	"strings"

	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// Who changes the desk: a person in a browser, for whom the control level
// and the PIN apply, or a plugin on the device, which rcplugd names when it
// passes the request on (X-Rcplug-Plugin with X-Rcplug-Permissions, see the
// host's permissions.go). A plugin changes the desk only with the permission
// "desk:write" in its plugin.json, at the Control level's rules and without
// the PIN; write protection stops it like everyone else. Plugins never change
// the console's own settings.
type who struct {
	remote string
	plugin string // "" = a person
	perms  []string
}

func (c *Console) who(r *http.Request) who {
	w := who{remote: remoteOf(r)}
	switch {
	case r.Header.Get("X-Rcplug-Plugin") != "" && loopback(r.RemoteAddr):
		w.plugin = r.Header.Get("X-Rcplug-Plugin")
		for _, p := range strings.Split(r.Header.Get("X-Rcplug-Permissions"), ",") {
			if p = strings.TrimSpace(p); p != "" {
				w.perms = append(w.perms, p)
			}
		}
	case c.opts.Hosted && loopback(r.RemoteAddr) && r.Header.Get("X-Forwarded-For") == "" && r.Header.Get("Sec-Fetch-Mode") == "":
		// Straight to the console's port, past rcplugd, and not a
		// browser: something on the device.
		w.plugin = "unknown"
	}
	return w
}

func (w who) can(perm string) bool {
	for _, p := range w.perms {
		if p == perm {
			return true
		}
	}
	return false
}

// label is how the audit log names the writer.
func (w who) label() string {
	if w.plugin != "" {
		return "plugin " + w.plugin
	}
	return w.remote
}

var errPluginAdmin = sdk.NewError(http.StatusForbidden, "plugin_not_allowed", "plugins cannot change the console's settings, control level or PIN")

func errNoDeskWrite(plugin string) error {
	if plugin == "unknown" {
		return sdk.NewError(http.StatusForbidden, "permission_desk_write",
			"a plugin changes the desk through rcplugd with its token (X-Rcplug-Token from RCPLUG_TOKEN; the SDK sends it)")
	}
	return sdk.NewError(http.StatusForbidden, "permission_desk_write",
		plugin+` does not declare the permission "desk:write" in its plugin.json`)
}

// forPeople wraps the console's own settings: plugins may not use them.
func (c *Console) forPeople(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c.who(r).plugin != "" {
			sdk.WriteError(w, errPluginAdmin)
			return
		}
		h(w, r)
	}
}
