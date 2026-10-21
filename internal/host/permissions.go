package host

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"sort"
	"strings"
)

// Plugin permissions: a plugin declares in its manifest what it does beyond
// its own pages, the dashboard shows it, and the host and the console hold
// plugins to what the host controls:
//
//	desk:write  change the desk through the console (the console refuses
//	            writes from plugins without it)
//	audio       hear the desk through the shared audio taps (the host
//	            refuses the stream without it)
//	network     talk to other machines (shown, not enforced)
//	device      use the device's hardware or files outside its data
//	            directory (shown, not enforced)
//	screen      take over the device's screen: while the plugin runs, RØDE's
//	            screen program is stopped, and it is started again when the
//	            plugin ends (screen.go). Such a plugin never starts by
//	            itself: only from the dashboard, and only once (no restarts).
//	            With desk:write it writes at the console's expert level,
//	            as RØDE's screen sets everything
//
// Plugins run as root on the device, so this guards against mistakes and
// tells users what a plugin is for; it does not contain a plugin that means
// harm.

var knownPermissions = map[string]bool{"desk:write": true, "audio": true, "network": true, "device": true, "screen": true, "screen:request": true}

func validatePermissions(m *Manifest) error {
	seen := map[string]bool{}
	for _, p := range m.Permissions {
		if !knownPermissions[p] {
			known := make([]string, 0, len(knownPermissions))
			for k := range knownPermissions {
				known = append(known, k)
			}
			sort.Strings(known)
			return fmt.Errorf("unknown permission %q (known: %s)", p, strings.Join(known, ", "))
		}
		if seen[p] {
			return fmt.Errorf("permission %q listed twice", p)
		}
		seen[p] = true
	}
	return nil
}

// has reports whether a manifest declares a permission.
func (m *Manifest) has(perm string) bool {
	for _, p := range m.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

// pluginPermissions returns a running plugin's declared permissions.
func (s *Server) pluginPermissions(name string) []string {
	if p := s.m.get(name); p != nil {
		return p.Manifest().Permissions
	}
	return nil
}

// identify tells a plugin who sends a proxied request: requests from the
// device itself are other plugins, named by their token (X-Rcplug-Plugin,
// with their X-Rcplug-Permissions), or "unknown" without one; requests from
// the network, and a browser on the device itself (make run-local, an SSH
// tunnel), carry neither header. Whatever a client sent in these headers
// is dropped, and a token never travels on to another plugin.
func (s *Server) identify(pr *httputil.ProxyRequest) {
	h := pr.Out.Header
	h.Del("X-Rcplug-Plugin")
	h.Del("X-Rcplug-Permissions")
	token := pr.In.Header.Get("X-Rcplug-Token")
	h.Del("X-Rcplug-Token")
	if !fromDevice(pr.In) || (token == "" && browser(pr.In)) {
		return
	}
	name := "unknown"
	if s.Events != nil && token != "" {
		if n, ok := s.Events.pluginOf(token); ok {
			name = n
		}
	}
	h.Set("X-Rcplug-Plugin", name)
	if name != "unknown" {
		h.Set("X-Rcplug-Permissions", strings.Join(s.pluginPermissions(name), ","))
	}
}

// browser: browsers send Sec-Fetch-Mode with every request, HTTP clients in
// plugins do not.
func browser(r *http.Request) bool { return r.Header.Get("Sec-Fetch-Mode") != "" }
