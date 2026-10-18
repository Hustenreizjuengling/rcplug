// Command rcplugd is the RØDECaster plugin host. It loads plugins from
// <root>/plugins, supervises them, and serves a dashboard plus a reverse proxy
// to each plugin's web UI. Pure Go standard library so it cross-compiles to a
// single static aarch64 binary.
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Hustenreizjuengling/rcplug/internal/host"
	"github.com/Hustenreizjuengling/rcplug/sdk"
	"github.com/Hustenreizjuengling/rcplug/sdk/release"
)

// version is set at build time via -ldflags "-X main.version=…".
var version = "dev"

//go:embed web
var webFS embed.FS

func main() {
	var (
		addr     = flag.String("addr", ":8080", "dashboard listen address")
		root     = flag.String("root", defaultRoot(), "rcplug root directory (contains plugins/)")
		basePort = flag.Int("base-port", 9100, "first loopback port assigned to plugins")
		lowPrio  = flag.Bool("low-prio", true, "run at lowest CPU priority (audio comes first)")
		allow    = flag.String("allow-host", os.Getenv("RCPLUG_ALLOW_HOST"), "extra host names accepted in the Host header, comma-separated (\".example.com\" allows a suffix)")
		showVer  = flag.Bool("version", false, "print version and exit")
		diag     = flag.String("diagnostics", "", "write a diagnostics bundle (zip) to this file (\"-\" = stdout) and exit; works without a running host")
		check    = flag.Bool("check", false, "print problems found in the logs and whether the host answers, then exit")
		call     = flag.String("call", "", "call the running host's API (\"POST /api/reload\") and print the answer, then exit; for scripts on the device")
		updates  = flag.Bool("updates", !isOff(os.Getenv("RCPLUG_UPDATES")), "offer updates of rcplug and its plugins from signed GitHub releases (env RCPLUG_UPDATES=off switches them off)")
		updRepo  = flag.String("update-repo", envOr("RCPLUG_UPDATE_REPO", host.DefaultUpdateRepo), "GitHub repository (owner/name) of rcplug's releases")
	)
	flag.Parse()
	if *showVer {
		fmt.Println(version)
		return
	}
	// Plugins run in their own directory, so every path handed to them (and
	// the leftover check, which compares working directories) is absolute.
	if abs, err := filepath.Abs(*root); err == nil {
		*root = abs
	}
	if *diag != "" {
		os.Exit(writeDiagnostics(*diag, *root, *addr))
	}
	if *check {
		os.Exit(checkInstall(*root, *addr))
	}
	if *call != "" {
		os.Exit(callAPI(*call, *addr))
	}
	// The host's own log goes to <root>/logs/rcplugd.log (rotated), and to
	// the terminal when there is one. Without a terminal (started by the init
	// script) stderr only receives what the Go runtime prints on a crash.
	hostLog := host.NewRotatingFile(filepath.Join(*root, "logs", "rcplugd.log"), host.LogFileMax)
	if isTerminal(os.Stderr) {
		log.SetOutput(io.MultiWriter(os.Stderr, hostLog))
	} else {
		log.SetOutput(hostLog)
	}

	if *lowPrio {
		// Audio handling on the device must always win; plugins inherit this.
		if err := sdk.LowPriority(); err != nil {
			log.Printf("warning: lowering priority: %v", err)
		}
	}

	log.SetFlags(log.LstdFlags)
	log.Printf("rcplug %s starting, root=%s addr=%s", version, *root, *addr)

	m := host.NewManager(*root, *basePort)
	m.HostURL, m.Version = loopbackURL(*addr), version
	dash, _ := fs.Sub(webFS, "web")
	srv := host.NewServer(m, dash, version)
	if *allow != "" {
		srv.AllowHosts = strings.Split(*allow, ",")
	}
	if *updates {
		cfg := host.UpdateConfig{API: os.Getenv("RCPLUG_UPDATE_API"), PlatformRepo: *updRepo}
		// Extra keys are for forks and tests; rcplugd.env is root's file.
		for _, k := range strings.Split(os.Getenv("RCPLUG_UPDATE_KEYS"), ",") {
			if strings.TrimSpace(k) == "" {
				continue
			}
			pk, err := release.ParsePublicKey(k)
			if err != nil {
				log.Printf("warning: RCPLUG_UPDATE_KEYS: %v", err)
				continue
			}
			cfg.ExtraKeys = append(cfg.ExtraKeys, pk)
		}
		srv.Updates = host.NewUpdater(m, version, cfg)
	}
	// Before any plugin starts: a signal handled here reaches plugins with
	// its default action even if rcplugd was started with it ignored (as by
	// a background job of a shell), so plugins can trap SIGINT and stop
	// cleanly instead of being killed 3 s later.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	// The dashboard's port first: plugins are then never handed it.
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen %s: %v", *addr, err)
	}

	// reload (re)scans the plugins: at start, on SIGHUP and from the API.
	reload := func() ([]string, error) {
		invalid, err := m.Load()
		if err != nil {
			log.Printf("warning: loading plugins: %v", err)
		}
		for _, msg := range invalid {
			log.Printf("skipped plugin %s", msg)
		}
		for _, st := range m.List() {
			if len(st.Unknown) > 0 {
				log.Printf("plugin %s: unknown plugin.json fields %v (typo, or written for a newer rcplug?)", st.Name, st.Unknown)
			}
		}
		srv.SetInvalid(invalid)
		return invalid, err
	}
	srv.Rescan = reload
	if left := m.ReapLeftovers(); len(left) > 0 {
		log.Printf("removed processes an earlier rcplugd left running for %s", strings.Join(left, ", "))
	}
	reload()

	// Requests (notably proxied SSE streams) see this context cancelled at
	// shutdown, so Shutdown does not wait out long-lived connections.
	baseCtx, cancelRequests := context.WithCancel(context.Background())
	httpSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context { return baseCtx }}
	go func() {
		if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
	}()
	log.Printf("dashboard listening on %s", ln.Addr())

	for s := range sig {
		if s == syscall.SIGHUP {
			log.Printf("SIGHUP: rescanning plugins")
			_, _ = reload()
			continue
		}
		break
	}
	log.Printf("shutting down, stopping plugins")
	cancelRequests()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	m.StopAll()
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// writeDiagnostics asks the running host for a complete bundle; if it does
// not answer, it builds one from the files on disk.
func writeDiagnostics(path, root, addr string) int {
	out := os.Stdout
	if path != "-" {
		f, err := os.Create(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer f.Close()
		out = f
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(loopbackURL(addr) + "/api/diagnostics")
	if err == nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		if _, err := io.Copy(out, resp.Body); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	reason := "no answer"
	if err != nil {
		reason = err.Error()
	} else {
		reason = resp.Status
		resp.Body.Close()
	}
	in := host.DiagnosticsInput{Root: root, Version: version,
		Note: fmt.Sprintf("rcplugd did not answer at %s (%s); this bundle was made from the files on disk and has no live plugin data", addr, reason)}
	ents, _ := os.ReadDir(filepath.Join(root, "plugins"))
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		st := host.Status{Name: e.Name(), State: "unknown"}
		if m, err := host.LoadManifest(filepath.Join(root, "plugins", e.Name())); err == nil {
			st.Title, st.Version, st.Unknown, st.HasUI = m.Title, m.Version, m.Unknown, m.HTTPProxy != ""
		} else {
			in.Invalid = append(in.Invalid, e.Name()+": "+err.Error())
		}
		in.Plugins = append(in.Plugins, st)
	}
	if err := host.WriteDiagnostics(context.Background(), out, in); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// callAPI sends one request to the running host's API, with the CSRF header,
// and prints the body. Exit status 0 for 2xx, 1 otherwise.
func callAPI(spec, addr string) int {
	method, path, ok := strings.Cut(strings.TrimSpace(spec), " ")
	if !ok || !strings.HasPrefix(path, "/") {
		fmt.Fprintln(os.Stderr, `--call needs "METHOD /path"`)
		return 2
	}
	req, err := http.NewRequest(strings.ToUpper(method), loopbackURL(addr)+path, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	req.Header.Set(sdk.APIHeader, "1")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer resp.Body.Close()
	_, _ = io.Copy(os.Stdout, resp.Body)
	if resp.StatusCode/100 != 2 {
		fmt.Fprintln(os.Stderr, resp.Status)
		return 1
	}
	return 0
}

// checkInstall prints a short health report for the wizard and setup tools.
// The first line is machine-readable: "host: up" or "host: down (...)".
func checkInstall(root, addr string) int {
	client := &http.Client{Timeout: 5 * time.Second}
	if resp, err := client.Get(loopbackURL(addr) + "/api/system"); err == nil {
		resp.Body.Close()
		fmt.Printf("host: up (%s)\n", resp.Status)
	} else {
		fmt.Printf("host: down (%v)\n", err)
	}
	if p := host.CheckLogs(root); p != "" {
		fmt.Print(p)
		return 2
	}
	fmt.Println("logs: no problems found")
	return 0
}

// loopbackURL is the URL plugins use to reach the host (RCPLUG_HOST_URL).
func loopbackURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://127.0.0.1:8080"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func defaultRoot() string {
	if v := os.Getenv("RCPLUG_ROOT"); v != "" {
		return v
	}
	// Prefer the directory the binary lives in so a self-contained install
	// (binary + plugins/ next to it) just works.
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	return "."
}

// isOff reports whether an environment setting switches something off.
func isOff(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "0", "false", "no":
		return true
	}
	return false
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
