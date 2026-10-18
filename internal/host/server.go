package host

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// Server wires the manager to an HTTP mux: dashboard, JSON API and per-plugin
// reverse proxies.
type Server struct {
	m         *Manager
	dashboard fs.FS
	version   string
	started   time.Time

	// AllowHosts extends the Host-header allowlist (see hostAllowed).
	AllowHosts []string
	// Updates checks for and installs updates; nil switches the API off.
	Updates *Updater
	// Access guards everything with the optional dashboard password; nil:
	// no password possible (tests).
	Access *Access
	// Events is the event bus; nil switches its API off.
	Events *Bus
	// Taps are the shared audio taps; nil switches their API off.
	Taps *Taps
	// Rescan reloads the plugins for POST /api/reload (and logs what it
	// found); nil means Manager.Load.
	Rescan func() ([]string, error)

	procs procSampler
	// Resources keeps the minute history for the diagnostics bundle.
	Resources *ResourceLog

	mu      sync.Mutex
	proxies map[string]*httputil.ReverseProxy // key: name@target
	invalid []string
}

// NewServer creates the HTTP front end. dashboard may be nil.
func NewServer(m *Manager, dashboard fs.FS, version string) *Server {
	return &Server{m: m, dashboard: dashboard, version: version, started: time.Now(),
		proxies: map[string]*httputil.ReverseProxy{}, Resources: &ResourceLog{}}
}

// RunResources notes the device's resources once a minute for the
// diagnostics bundle until ctx ends (resources.go).
func (s *Server) RunResources(ctx context.Context) { s.Resources.Run(ctx, s.procGroups) }

// localSuffixes are DNS suffixes only a home network's own resolver answers
// for, so an attacker cannot point them at the device (DNS rebinding).
var localSuffixes = []string{".local", ".lan", ".home", ".home.arpa", ".fritz.box", ".internal", ".localdomain"}

// hostAllowed decides whether a request's Host header is acceptable: IP
// literals, localhost, this machine's hostname, local DNS suffixes and the
// operator's --allow-host entries (".example.com" allows a suffix). Anything
// else is refused, which defeats DNS-rebinding attacks from web pages.
func (s *Server) hostAllowed(hostport string) bool {
	h := hostport
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		h = host
	}
	h = strings.ToLower(strings.TrimSuffix(strings.Trim(h, "[]"), "."))
	if h == "" || net.ParseIP(h) != nil || h == "localhost" {
		return true
	}
	if name, err := os.Hostname(); err == nil && (h == strings.ToLower(name)) {
		return true
	}
	for _, suf := range localSuffixes {
		if strings.HasSuffix(h, suf) {
			return true
		}
	}
	for _, a := range s.AllowHosts {
		a = strings.ToLower(strings.TrimSpace(a))
		if a != "" && (h == a || (strings.HasPrefix(a, ".") && strings.HasSuffix(h, a))) {
			return true
		}
	}
	return false
}

// SetInvalid records plugins skipped at load time so the dashboard can show them.
func (s *Server) SetInvalid(invalid []string) {
	s.mu.Lock()
	s.invalid = invalid
	s.mu.Unlock()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/plugins", s.handlePlugins)
	mux.HandleFunc("GET /api/system", s.handleSystem)
	mux.HandleFunc("GET /api/processes", s.handleProcesses)
	mux.Handle("POST /api/uninstall", sdk.CSRF(http.HandlerFunc(s.handleUninstall)))
	mux.HandleFunc("GET /api/plugins/{name}/logs", s.handleLogs)
	mux.HandleFunc("GET /api/diagnostics", s.handleDiagnostics)
	mux.Handle("POST /api/plugins/{name}/{action}", sdk.CSRF(http.HandlerFunc(s.handleAction)))
	mux.HandleFunc("GET /api/plugins/{name}/options", s.handleOptions)
	mux.Handle("POST /api/plugins/{name}/options", sdk.CSRF(http.HandlerFunc(s.handleSetOptions)))
	mux.Handle("POST /api/reload", sdk.CSRF(http.HandlerFunc(s.handleReload)))
	mux.HandleFunc("GET /api/updates", s.handleUpdates)
	mux.Handle("POST /api/updates/check", sdk.CSRF(http.HandlerFunc(s.handleUpdateCheck)))
	mux.Handle("POST /api/updates/install", sdk.CSRF(http.HandlerFunc(s.handleUpdateInstall)))
	mux.Handle("POST /api/updates/auto", sdk.CSRF(http.HandlerFunc(s.handleUpdateAuto)))
	mux.HandleFunc("GET /api/catalog", s.handleCatalog)
	mux.Handle("POST /api/catalog/install", sdk.CSRF(http.HandlerFunc(s.handleCatalogInstall)))
	mux.Handle("POST /api/catalog/sources", sdk.CSRF(http.HandlerFunc(s.handleCatalogSource)))
	mux.Handle("POST /api/plugins/{name}/remove", sdk.CSRF(http.HandlerFunc(s.handleRemovePlugin)))
	if s.Access != nil {
		mux.HandleFunc("GET /api/access", s.handleAccess)
		mux.Handle("POST /api/login", sdk.CSRF(http.HandlerFunc(s.handleLogin)))
		mux.Handle("POST /api/logout", sdk.CSRF(http.HandlerFunc(s.handleLogout)))
		mux.Handle("POST /api/access/password", sdk.CSRF(http.HandlerFunc(s.handlePassword)))
		mux.Handle("POST /api/access/reset", sdk.CSRF(http.HandlerFunc(s.handleAccessReset)))
		mux.HandleFunc("GET /login", s.handleLoginPage)
	}
	if s.Events != nil {
		mux.HandleFunc("GET /api/events", s.handleEvents)
		mux.HandleFunc("GET /api/events/stream", s.handleEventStream)
		mux.Handle("POST /api/events", sdk.CSRF(http.HandlerFunc(s.handlePublish)))
	}
	if s.Taps != nil {
		mux.HandleFunc("GET /api/audio", s.handleTaps)
		mux.HandleFunc("GET /api/audio/stream", s.handleTapStream)
	}
	mux.HandleFunc("/plugins/{name}", s.handleProxy)
	mux.HandleFunc("/plugins/{name}/", s.handleProxy)
	if s.dashboard != nil {
		// Plain "/" (no method) so the more specific /plugins/… and /api/…
		// patterns take precedence; this serves everything else.
		mux.Handle("/", http.FileServer(http.FS(s.dashboard)))
	}
	var h http.Handler = mux
	if s.Access != nil {
		h = s.Access.Gate(h)
	}
	return s.checkHost(securityHeaders(sameOrigin(h)))
}

// securityHeaders keeps foreign pages from framing the dashboard or a plugin
// (clicks in a frame are same-origin and would pass the CSRF checks). The
// dashboard itself frames plugins from the same origin, which stays allowed.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Content-Security-Policy", "frame-ancestors 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) checkHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			http.Error(w, "unknown host name "+r.Host+"; open the device by IP address or add it with --allow-host", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin rejects state-changing requests that a browser sends on behalf
// of a foreign web page (CSRF): if an Origin header is present it must match
// the host the request was sent to. Requests without Origin (curl, scripts)
// pass; plugins add their own header check on top (see sdk.CSRF). WebSocket
// handshakes count as state-changing: browsers cannot add X-Rcplug to them,
// so the Origin is all that keeps a foreign page from opening one.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case (r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions) &&
			!strings.EqualFold(r.Header.Get("Upgrade"), "websocket"):
		default:
			if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				if err != nil || !strings.EqualFold(u.Host, r.Host) {
					http.Error(w, "cross-origin request refused", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	invalid := append([]string{}, s.invalid...)
	s.mu.Unlock()
	writeJSON(w, map[string]interface{}{"plugins": s.m.List(), "invalid": invalid})
}

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	host, _ := os.Hostname()
	doc := map[string]interface{}{
		"product":   "RØDECaster",
		"host":      host,
		"version":   s.version,
		"uptimeSec": int(time.Since(s.started).Seconds()),
		"arch":      runtime.GOARCH,
		"os":        runtime.GOOS,
		"time":      time.Now().Format(time.RFC3339),
	}
	for k, v := range health(s.m.Root()) {
		doc[k] = v
	}
	writeJSON(w, doc)
}

// health gathers cheap device vitals from /proc and /sys (best effort; keys
// are omitted when unavailable).
func health(root string) map[string]interface{} {
	out := map[string]interface{}{}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				out["systemUptimeSec"] = int(v)
			}
		}
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) >= 3 {
			out["load"] = f[:3]
		}
	}
	if m := memUsage(readMeminfo("/proc/meminfo")); m.TotalKB > 0 {
		out["memTotalKB"], out["memAvailableKB"] = m.TotalKB, m.AvailableKB
		out["memory"] = m // the same breakdown as api/processes
	}
	// Highest thermal zone reading (millidegrees).
	best := -1
	for i := 0; i < 8; i++ {
		b, err := os.ReadFile("/sys/class/thermal/thermal_zone" + strconv.Itoa(i) + "/temp")
		if err != nil {
			continue
		}
		if v, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && v > best {
			best = v
		}
	}
	if best >= 0 {
		out["cpuTempC"] = float64(best) / 1000
	}
	if free, total, ok := diskStats(root); ok {
		out["diskFreeMB"], out["diskTotalMB"] = free, total
	}
	return out
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.m.get(name) == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, map[string]interface{}{"lines": s.m.Logs(name)})
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var ok bool
	switch r.PathValue("action") {
	case "enable":
		ok = s.m.Enable(name)
	case "disable":
		ok = s.m.Disable(name)
	case "restart":
		ok = s.m.Restart(name)
	default:
		http.Error(w, "unknown action", http.StatusNotFound)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// handleOptions returns the option values set for a plugin (the options
// themselves are in api/plugins).
func (s *Server) handleOptions(w http.ResponseWriter, r *http.Request) {
	values, ok, err := s.m.Options(r.PathValue("name"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	doc := map[string]interface{}{"values": values}
	if err != nil {
		doc["error"] = err.Error()
	}
	writeJSON(w, doc)
}

// handleSetOptions stores option values ({"values": {id: value|null}}) and
// restarts the plugin if it runs.
func (s *Server) handleSetOptions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Values map[string]json.RawMessage `json:"values"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		sdk.WriteError(w, sdk.NewError(http.StatusBadRequest, "bad_request", "body must be {\"values\": {...}}"))
		return
	}
	name := r.PathValue("name")
	found, restarted, err := s.m.SetOptions(name, req.Values)
	switch {
	case !found:
		http.NotFound(w, r)
	case err != nil:
		sdk.WriteError(w, err)
	default:
		writeJSON(w, map[string]bool{"ok": true, "restarted": restarted})
	}
}

func (s *Server) handleUpdates(w http.ResponseWriter, r *http.Request) {
	if s.Updates == nil {
		writeJSON(w, UpdateStatus{Current: s.version, Targets: []UpdateTarget{}})
		return
	}
	writeJSON(w, s.Updates.Status())
}

// handleUpdateCheck asks GitHub for new releases (at most every 20 s).
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if s.Updates == nil {
		sdk.WriteError(w, sdk.NewError(http.StatusNotFound, "updates_off", "updates are switched off"))
		return
	}
	writeJSON(w, s.Updates.Check(r.Context()))
}

// handleUpdateAuto switches the daily check on or off: ?on=true|false.
func (s *Server) handleUpdateAuto(w http.ResponseWriter, r *http.Request) {
	if s.Updates == nil {
		sdk.WriteError(w, sdk.NewError(http.StatusNotFound, "updates_off", "updates are switched off"))
		return
	}
	on, err := strconv.ParseBool(r.URL.Query().Get("on"))
	if err != nil {
		sdk.WriteError(w, sdk.NewError(http.StatusBadRequest, "bad_request", "on must be true or false"))
		return
	}
	s.m.SetAutoCheck(on)
	writeJSON(w, s.Updates.Status())
}

// handleUpdateInstall installs what the last check found for ?target=
// (rcplug, or plugin:<name>).
func (s *Server) handleUpdateInstall(w http.ResponseWriter, r *http.Request) {
	if s.Updates == nil {
		sdk.WriteError(w, sdk.NewError(http.StatusNotFound, "updates_off", "updates are switched off"))
		return
	}
	if err := s.Updates.Install(r.URL.Query().Get("target")); err != nil {
		code := "update_failed"
		if err == ErrUpdateBusy {
			code = "update_busy"
		}
		sdk.WriteError(w, sdk.NewError(http.StatusConflict, code, err.Error()))
		return
	}
	writeJSON(w, s.Updates.Status())
}

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	var invalid []string
	var err error
	if s.Rescan != nil {
		invalid, err = s.Rescan()
	} else if invalid, err = s.m.Load(); err == nil {
		s.SetInvalid(invalid)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "invalid": invalid})
}

// proxyFor returns the cached reverse proxy for a plugin's loopback target.
func (s *Server) proxyFor(p *Plugin) (*httputil.ReverseProxy, error) {
	m := p.Manifest()
	name := m.Name
	target := expand(m.HTTPProxy, p.Port(), p.Dir())
	if !strings.Contains(target, "://") {
		target = "http://" + target
	}
	key := name + "@" + target // a changed manifest yields a new proxy
	s.mu.Lock()
	defer s.mu.Unlock()
	if rp, ok := s.proxies[key]; ok {
		return rp, nil
	}
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	prefix := "/plugins/" + name
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(u)
			pr.SetXForwarded() // plugins see the browser's address and host
			// Strip the prefix from the escaped path, so that an encoded
			// slash (%2F) reaches the plugin as it was sent.
			pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, prefix)
			pr.Out.URL.RawPath = strings.TrimPrefix(pr.In.URL.EscapedPath(), prefix)
			if pr.Out.URL.Path == "" {
				pr.Out.URL.Path, pr.Out.URL.RawPath = "/", ""
			}
			if pr.Out.URL.RawPath == pr.Out.URL.Path {
				pr.Out.URL.RawPath = ""
			}
			pr.Out.Header.Set("X-Forwarded-Prefix", prefix)
			s.identify(pr) // who asks, for the plugin's permission checks
		},
		// Flush immediately so SSE/streaming responses from plugins work.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			unreachable(w, r, name)
		},
	}
	s.proxies[key] = rp
	return rp, nil
}

// unreachable answers for a plugin that does not answer (starting, stopped,
// crashed). Browsers get a page in the dashboard's colours that tries again
// every two seconds, so a plugin page opens by itself once the plugin is up.
func unreachable(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet || !strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.Error(w, "plugin "+name+" is not reachable (starting or stopped)", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusBadGateway)
	fmt.Fprintf(w, `<!DOCTYPE html><meta charset="utf-8"><meta http-equiv="refresh" content="2">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>%[1]s</title>
<body style="margin:0;background:#0d1015;color:#9aa8b6;font:14px/1.5 system-ui,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;text-align:center">
<p><b style="color:#e7edf3">%[1]s</b> is not answering yet (starting, or stopped).<br>This page tries again every two seconds.<br><br>
<span lang="de"><b style="color:#e7edf3">%[1]s</b> antwortet noch nicht (startet oder ist gestoppt).<br>Die Seite versucht es alle zwei Sekunden erneut.</span></p>`,
		html.EscapeString(name))
}

// handleProxy reverse-proxies /plugins/<name>/... to the plugin's loopback
// HTTP target, stripping the /plugins/<name> prefix.
func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p := s.m.get(name)
	if p == nil || p.Manifest().HTTPProxy == "" {
		http.NotFound(w, r)
		return
	}
	// /plugins/<name> → /plugins/<name>/ so relative asset URLs resolve.
	if r.URL.Path == "/plugins/"+name {
		to := r.URL.Path + "/"
		if r.URL.RawQuery != "" {
			to += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, to, http.StatusMovedPermanently)
		return
	}
	rp, err := s.proxyFor(p)
	if err != nil {
		http.Error(w, "bad proxy target", http.StatusInternalServerError)
		return
	}
	rp.ServeHTTP(w, r)
}
