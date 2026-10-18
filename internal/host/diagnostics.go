package host

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DiagnosticsPath is the optional endpoint of a plugin that contributes to
// the diagnostics bundle: JSON or text, or a zip whose entries are copied.
const DiagnosticsPath = "/api/diagnostics"

// diagLimit caps what a plugin may contribute and how much of a log is kept.
const (
	diagPluginLimit = 20 << 20
	diagLogLimit    = 2 << 20
)

// DiagnosticsInput is what goes into a diagnostics bundle. Without a running
// host (rcplugd --diagnostics while it is down) Started is zero, Memory and
// Fetch are nil and Note says so.
type DiagnosticsInput struct {
	Root    string
	Version string
	Started time.Time
	Plugins []Status
	Invalid []string
	Memory  map[string][]string
	Fetch   func(ctx context.Context, name string) (contentType string, body []byte, err error)
	Note    string
}

// WriteDiagnostics writes a zip with everything needed to analyse a problem:
// versions, system state, processes, kernel log, plugin states, masked
// settings, all logs and what the plugins contribute. Secrets are masked; the
// README inside asks to review the bundle before sharing it publicly.
func WriteDiagnostics(ctx context.Context, w io.Writer, in DiagnosticsInput) error {
	z := zip.NewWriter(w)
	now := time.Now()
	add := func(name string, data []byte) {
		f, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now})
		if err == nil {
			_, _ = f.Write(data)
		}
	}
	addJSON := func(name string, v interface{}) {
		raw, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			raw = []byte(err.Error())
		}
		add(name, append(raw, '\n'))
	}

	add("README.txt", []byte(diagReadme(now, in)))
	host, _ := os.Hostname()
	info := map[string]interface{}{
		"generated": now.Format(time.RFC3339), "version": in.Version, "go": runtime.Version(),
		"os": runtime.GOOS, "arch": runtime.GOARCH, "host": host, "root": in.Root,
		"plugins": in.Plugins, "invalid": in.Invalid, "health": health(in.Root),
	}
	if !in.Started.IsZero() {
		info["hostUptimeSec"] = int(now.Sub(in.Started).Seconds())
	}
	if in.Note != "" {
		info["note"] = in.Note
	}
	addJSON("rcplug.json", info)

	// System state (each best effort).
	for name, path := range map[string]string{
		"system/proc-version.txt": "/proc/version", "system/os-release.txt": "/etc/os-release",
		"system/meminfo.txt": "/proc/meminfo", "system/loadavg.txt": "/proc/loadavg",
		"system/uptime.txt": "/proc/uptime", "system/mounts.txt": "/proc/mounts",
		"system/cpuinfo.txt": "/proc/cpuinfo",
	} {
		if b, err := os.ReadFile(path); err == nil {
			add(name, limit(b, 64<<10))
		}
	}
	add("system/processes.txt", []byte(processTable()))
	add("system/sockets.txt", []byte(socketTable()))
	if t := soundState("/"); t != "" {
		add("system/sound.txt", []byte(t))
	}
	if t := dbusTable(ctx); t != "" {
		add("system/dbus.txt", []byte(t))
	}
	if out := commandOutput(ctx, 3*time.Second, "dmesg"); out != nil {
		add("system/dmesg.txt", tailLines(out, 400))
	}
	// The device's own logs: RØDE's mixer and screen app (a RØDECaster only).
	for _, l := range deviceLogs {
		if b, err := readTail(l.path, 256<<10); err == nil {
			add("device/"+l.name, maskDeviceLog(tailLines(b, 400)))
		}
	}

	// Settings, with secrets masked.
	if b, err := os.ReadFile(filepath.Join(in.Root, "state.json")); err == nil {
		add("config/state.json", b)
	}
	cfgs, _ := filepath.Glob(filepath.Join(in.Root, "config", "*"))
	for _, f := range cfgs {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if strings.HasSuffix(f, ".json") {
			b = maskUserConfig(b)
		} else {
			b = maskEnvFile(b)
		}
		add("config/"+filepath.Base(f), b)
	}

	// Logs: the files on disk, then what the host still holds in memory.
	logDir := filepath.Join(in.Root, "logs")
	files, _ := filepath.Glob(filepath.Join(logDir, "*"))
	sort.Strings(files)
	var problems strings.Builder
	for _, f := range files {
		b, err := readTail(f, diagLogLimit)
		if err != nil {
			continue
		}
		add("logs/"+filepath.Base(f), b)
		problems.WriteString(scanProblems(filepath.Base(f), b))
	}
	for name, lines := range in.Memory {
		add("logs/memory/"+name+".txt", []byte(strings.Join(lines, "\n")+"\n"))
	}
	if problems.Len() == 0 {
		problems.WriteString("No crashes, panics or restarts found in the logs.\n")
	}
	add("problems.txt", []byte(problems.String()))

	// Plugin contributions, asked for all at once (each may take up to 5 s).
	if in.Fetch != nil {
		type answer struct {
			ct   string
			body []byte
			err  error
		}
		answers := make([]*answer, len(in.Plugins))
		var wg sync.WaitGroup
		for i, p := range in.Plugins {
			if !p.Running || !p.HasUI {
				continue
			}
			answers[i] = &answer{}
			wg.Add(1)
			go func(a *answer, name string) {
				defer wg.Done()
				fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				a.ct, a.body, a.err = in.Fetch(fctx, name)
			}(answers[i], p.Name)
		}
		wg.Wait()
		for i, p := range in.Plugins {
			a := answers[i]
			if a == nil {
				continue
			}
			base := "plugins/" + p.Name + "/"
			switch {
			case a.err != nil:
				add(base+"error.txt", []byte(a.err.Error()+"\n"))
			case strings.HasPrefix(a.ct, "application/zip"):
				copyZip(a.body, base, add)
			case strings.HasPrefix(a.ct, "application/json"):
				add(base+"diagnostics.json", a.body)
			default:
				add(base+"diagnostics.txt", a.body)
			}
		}
	}
	return z.Close()
}

func diagReadme(now time.Time, in DiagnosticsInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "rcplug diagnostics, %s, rcplug %s\n\n", now.Format("2006-01-02 15:04:05 MST"), in.Version)
	if in.Note != "" {
		fmt.Fprintf(&b, "NOTE: %s\n\n", in.Note)
	}
	b.WriteString(`Contents
  problems.txt     crashes, panics and restarts found in the logs (start here)
  rcplug.json      versions, plugin states, device vitals
  system/          kernel, memory, mounts, processes, network and unix sockets
                   with their processes, D-Bus names, input devices, kernel log (dmesg)
  device/          the device's own logs (RØDE's mixer and screen app; serial
                   numbers, tokens, passwords and MAC addresses masked)
  config/          rcplug settings (passwords, PINs and keys masked)
  logs/            host and plugin logs (logs/memory: the host's in-memory copy)
  plugins/<name>/  what each running plugin reports about itself

Passwords, PINs, Wi-Fi keys and tokens are masked. The bundle can still contain
names, serial numbers and network addresses of your setup: review it before you
attach it to a public issue.
`)
	return b.String()
}

// problemLine matches what is worth a look in a log; policyRefusal is the
// console refusing a write the user's control level does not allow, which is
// the console doing its job.
var (
	problemLine   = regexp.MustCompile(`(?i)panic:|fatal error|SIGSEGV|segmentation|exited \(|restarting in|out of memory|guard:|desync|refused|failed`)
	policyRefusal = regexp.MustCompile(`console: write .* refused: `)
)

func scanProblems(name string, log []byte) string {
	var hits []string
	sc := bufio.NewScanner(bytes.NewReader(log))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if problemLine.MatchString(sc.Text()) && !policyRefusal.MatchString(sc.Text()) {
			hits = append(hits, sc.Text())
		}
	}
	if len(hits) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "== %s: %d line(s)\n", name, len(hits))
	if len(hits) > 30 {
		fmt.Fprintf(&b, "   (last 30 shown)\n")
		hits = hits[len(hits)-30:]
	}
	for _, h := range hits {
		b.WriteString("   " + h + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

// CheckLogs summarises problems in the persistent logs (rcplugd --check).
func CheckLogs(root string) string {
	files, _ := filepath.Glob(filepath.Join(root, "logs", "*"))
	sort.Strings(files)
	var b strings.Builder
	for _, f := range files {
		if strings.HasSuffix(f, ".1") {
			continue // older half; the current file is enough for a summary
		}
		data, err := readTail(f, diagLogLimit)
		if err == nil {
			b.WriteString(scanProblems(filepath.Base(f), data))
		}
	}
	return b.String()
}

func copyZip(body []byte, prefix string, add func(string, []byte)) {
	r, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		add(prefix+"error.txt", []byte("invalid zip: "+err.Error()+"\n"))
		return
	}
	for _, f := range r.File {
		name := filepath.ToSlash(filepath.Clean(f.Name))
		if strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") || f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(rc, diagPluginLimit))
		rc.Close()
		add(prefix+name, data)
	}
}

// secretKey matches setting names whose values are masked.
var secretKey = regexp.MustCompile(`(?i)pin|pass|token|secret|key|psk`)

// harmless matches setting values that cannot carry a secret: numbers and
// durations, switches, loopback addresses and plain paths. Operator settings
// may hold webhook URLs, tokens or names, and the bundle ends up in public
// bug reports, so every other value is masked.
var harmless = regexp.MustCompile(`(?i)^(-?[0-9]+(\.[0-9]+)?(ms|s|m|h|%|db|hz)?|true|false|on|off|yes|no|auto|always|never|` +
	`((127\.[0-9]+\.[0-9]+\.[0-9]+|localhost|\[::1\])?:[0-9]{1,5})|127\.[0-9]+\.[0-9]+\.[0-9]+|/[A-Za-z0-9._/-]*)?$`)

// shownSettings are rcplug's own host settings, which hold no secrets.
var shownSettings = map[string]bool{"RCPLUG_ALLOW_HOST": true, "RCPLUG_UPDATES": true, "RCPLUG_UPDATE_REPO": true,
	"RCPLUG_UPDATE_KEYS": true, "RCPLUG_MIXER": true, "RCPLUG_ROOT": true}

// maskValue shows a harmless value as it is, a URL with its scheme and host
// only, and anything else as ***.
func maskValue(v string) string {
	switch {
	case harmless.MatchString(v):
		return v
	case urlLike.FindString(v) == v:
		return maskURLs(v)
	default:
		return "***"
	}
}

func maskUserConfig(raw []byte) []byte {
	var c UserConfig
	if json.Unmarshal(raw, &c) != nil {
		return []byte("(unreadable, not included)\n")
	}
	c.Args = maskArgs(c.Args)
	for i, a := range c.Args {
		switch {
		case a == "***":
		case strings.HasPrefix(a, "-"):
			if k, v, ok := strings.Cut(a, "="); ok && v != "***" {
				c.Args[i] = k + "=" + maskValue(v)
			}
		default:
			c.Args[i] = maskValue(a)
		}
	}
	for k, v := range c.Env {
		if secretKey.MatchString(k) {
			c.Env[k] = "***"
		} else if !shownSettings[k] {
			c.Env[k] = maskValue(v)
		}
	}
	out, _ := json.MarshalIndent(c, "", "  ")
	return append(out, '\n')
}

func maskEnvFile(raw []byte) []byte {
	var b strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(k), "export "))
		switch {
		case !ok || strings.HasPrefix(k, "#"):
		case secretKey.MatchString(k):
			line = k + "=***"
		case !shownSettings[k]:
			line = k + "=" + maskValue(strings.Trim(strings.TrimSpace(v), `"'`))
		}
		b.WriteString(line + "\n")
	}
	return []byte(strings.TrimSuffix(b.String(), "\n"))
}

// processTable lists all processes with their state, CPU time, threads,
// memory and command line (secret-looking arguments masked).
func processTable() string {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return "(no /proc)\n"
	}
	tick := 100.0 // USER_HZ on Linux
	var b strings.Builder
	fmt.Fprintf(&b, "%-7s %-5s %-4s %8s %7s %9s  %s\n", "PID", "STATE", "NICE", "CPU s", "THREADS", "RSS KiB", "COMMAND")
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue
		}
		// The command name is in parentheses and may contain spaces.
		s := string(stat)
		end := strings.LastIndexByte(s, ')')
		if end < 0 {
			continue
		}
		f := strings.Fields(s[end+1:])
		if len(f) < 22 {
			continue
		}
		ut, _ := strconv.ParseFloat(f[11], 64)
		st, _ := strconv.ParseFloat(f[12], 64)
		rss, _ := strconv.Atoi(f[21])
		cmd := ""
		if raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil && len(raw) > 0 {
			cmd = strings.Join(maskArgs(strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")), " ")
		}
		if cmd == "" {
			cmd = "[" + s[strings.IndexByte(s, '(')+1:end] + "]"
		}
		if len(cmd) > 200 {
			cmd = cmd[:200] + "…"
		}
		fmt.Fprintf(&b, "%-7d %-5s %-4s %8.1f %7s %9d  %s\n", pid, f[0], f[16], (ut+st)/tick, f[17], rss*os.Getpagesize()/1024, cmd)
	}
	return b.String()
}

func commandOutput(ctx context.Context, timeout time.Duration, name string, args ...string) []byte {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil && len(out) == 0 {
		return nil
	}
	return out
}

func readTail(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("not a file")
	}
	if st.Size() > max {
		if _, err := f.Seek(st.Size()-max, io.SeekStart); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(io.LimitReader(f, max))
}

func tailLines(b []byte, n int) []byte {
	lines := bytes.Split(bytes.TrimRight(b, "\n"), []byte("\n"))
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return append(bytes.Join(lines, []byte("\n")), '\n')
}

func limit(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

// handleDiagnostics serves the bundle for the running host.
func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	invalid := append([]string(nil), s.invalid...)
	s.mu.Unlock()
	plugins := s.m.List()
	mem := map[string][]string{}
	for _, p := range plugins {
		mem[p.Name] = s.m.Logs(p.Name)
	}
	in := DiagnosticsInput{Root: s.m.Root(), Version: s.version, Started: s.started, Plugins: plugins,
		Invalid: invalid, Memory: mem, Fetch: s.fetchPluginDiagnostics}
	var buf bytes.Buffer
	if err := WriteDiagnostics(r.Context(), &buf, in); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	host, _ := os.Hostname()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, DiagnosticsFileName(host, time.Now())))
	_, _ = w.Write(buf.Bytes())
}

// DiagnosticsFileName is the suggested name of a bundle.
func DiagnosticsFileName(host string, t time.Time) string {
	host = regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(host, "-")
	return fmt.Sprintf("rcplug-diagnostics-%s-%s.zip", host, t.Format("20060102-150405"))
}

func (s *Server) fetchPluginDiagnostics(ctx context.Context, name string) (string, []byte, error) {
	p := s.m.get(name)
	if p == nil {
		return "", nil, fmt.Errorf("plugin %s not found", name)
	}
	target := expand(p.Manifest().HTTPProxy, p.Port(), p.Dir())
	if !strings.Contains(target, "://") {
		target = "http://" + target
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(target, "/")+DiagnosticsPath, nil)
	if err != nil {
		return "", nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil, fmt.Errorf("the plugin offers no diagnostics")
	}
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, diagPluginLimit))
	return resp.Header.Get("Content-Type"), body, err
}

// deviceLogs are the logs of RØDE's own programs on a RØDECaster (written by
// its start scripts, audio-start.sh and display-start.sh).
var deviceLogs = []struct{ name, path string }{
	{"rc_audio_mixer.log", "/var/volatile/log/rc_audio_mixer"},
	{"rc_gui_juce.log", "/var/log/rc_gui_juce"},
}

var (
	deviceSerial = regexp.MustCompile(`\b([A-Z]{2}[0-9]{2})[0-9]{5}\b`)
	deviceSecret = regexp.MustCompile(`(?i)\b(pass(word|phrase)?|psk|token|secret|api[_-]?key|auth[a-z]*)(["']?\s*[:=]\s*)("[^"]*"|\S+)`)
	deviceToken  = regexp.MustCompile(`\b[A-Za-z0-9+/_-]{32,}={0,2}`)
	deviceMAC    = regexp.MustCompile(`(?i)\b((?:[0-9a-f]{2}:){3})(?:[0-9a-f]{2}:){2}[0-9a-f]{2}\b`)
)

// maskDeviceLog hides what identifies the device or its owner in a log of
// RØDE's programs: serial numbers keep their first four characters, values of
// password/token-like keys and long token-like strings are replaced, MAC
// addresses keep their vendor half, URLs keep scheme and host.
func maskDeviceLog(b []byte) []byte {
	s := string(b)
	s = deviceSecret.ReplaceAllString(s, "${1}${3}***")
	s = deviceToken.ReplaceAllStringFunc(s, func(t string) string { return t[:4] + "…" })
	s = deviceSerial.ReplaceAllString(s, "${1}…")
	s = deviceMAC.ReplaceAllString(s, "${1}xx:xx:xx")
	return []byte(maskURLs(s))
}
