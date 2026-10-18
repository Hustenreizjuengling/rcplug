package host

import (
	"bufio"
	"encoding/json"
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

	mu      sync.Mutex
	proxies map[string]*httputil.ReverseProxy // key: name@target
	invalid []string
}

// NewServer creates the HTTP front end. dashboard may be nil.
func NewServer(m *Manager, dashboard fs.FS, version string) *Server {
	return &Server{m: m, dashboard: dashboard, version: version, started: time.Now(),
		proxies: map[string]*httputil.ReverseProxy{}}
}

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
	mux.HandleFunc("GET /api/plugins/{name}/logs", s.handleLogs)
	mux.HandleFunc("GET /api/diagnostics", s.handleDiagnostics)
	mux.Handle("POST /api/plugins/{name}/{action}", sdk.CSRF(http.HandlerFunc(s.handleAction)))
	mux.Handle("POST /api/reload", sdk.CSRF(http.HandlerFunc(s.handleReload)))
	mux.HandleFunc("/plugins/{name}", s.handleProxy)
	mux.HandleFunc("/plugins/{name}/", s.handleProxy)
	if s.dashboard != nil {
		// Plain "/" (no method) so the more specific /plugins/… and /api/…
		// patterns take precedence; this serves everything else.
		mux.Handle("/", http.FileServer(http.FS(s.dashboard)))
	}
	return s.checkHost(securityHeaders(sameOrigin(mux)))
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
// pass; plugins add their own header check on top (see sdk.CSRF).
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
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
	invalid := append([]string(nil), s.invalid...)
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
	if f, err := os.Open("/proc/meminfo"); err == nil {
		sc := bufio.NewScanner(f)
		mem := map[string]int{}
		for sc.Scan() {
			parts := strings.Fields(sc.Text())
			if len(parts) >= 2 {
				if v, err := strconv.Atoi(parts[1]); err == nil {
					mem[strings.TrimSuffix(parts[0], ":")] = v
				}
			}
		}
		f.Close()
		if mem["MemTotal"] > 0 {
			out["memTotalKB"], out["memAvailableKB"] = mem["MemTotal"], mem["MemAvailable"]
		}
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

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	invalid, err := s.m.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.SetInvalid(invalid)
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
			pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, prefix)
			if pr.Out.URL.Path == "" {
				pr.Out.URL.Path = "/"
			}
			pr.Out.URL.RawPath = ""
			pr.Out.Header.Set("X-Forwarded-Prefix", prefix)
		},
		// Flush immediately so SSE/streaming responses from plugins work.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "plugin "+name+" is not reachable (starting or stopped)", http.StatusBadGateway)
		},
	}
	s.proxies[key] = rp
	return rp, nil
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
		http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
		return
	}
	rp, err := s.proxyFor(p)
	if err != nil {
		http.Error(w, "bad proxy target", http.StatusInternalServerError)
		return
	}
	rp.ServeHTTP(w, r)
}
