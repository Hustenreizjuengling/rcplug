package host

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ringLog keeps the last N log lines of a plugin in memory for the dashboard
// and appends every line, with date and time, to the plugin's log file.
type ringLog struct {
	mu    sync.Mutex
	lines []string
	max   int
	file  *RotatingFile // nil = memory only
}

func newRingLog(max int, file *RotatingFile) *ringLog { return &ringLog{max: max, file: file} }

func (r *ringLog) add(line string) {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, now.Format("15:04:05 ")+line)
	if len(r.lines) > r.max {
		r.lines = r.lines[len(r.lines)-r.max:]
	}
	if r.file != nil {
		_, _ = r.file.Write([]byte(now.Format("2006-01-02 15:04:05 ") + line + "\n"))
	}
}

func (r *ringLog) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...)
}

// Plugin states reported to the dashboard.
const (
	StateStopped  = "stopped"  // disabled, no process
	StateStarting = "starting" // enabled, process being launched
	StateRunning  = "running"  // process alive
	StateBackoff  = "backoff"  // process exited, waiting to restart
)

// Plugin is a loaded plugin together with its runtime state. All fields are
// guarded by mu; use the accessors from other goroutines.
type Plugin struct {
	dir  string
	host *Manager
	log  *ringLog

	mu       sync.Mutex
	manifest *Manifest // replaced (never mutated) on reload
	port     int       // assigned loopback port, 0 if none
	enabled  bool
	state    string
	pid      int
	lastErr  string
	restarts int
	cancel   context.CancelFunc
	// loop is closed when the latest supervision loop has exited. A new
	// start waits for the previous loop, so two processes never overlap.
	loop chan struct{}
}

// Manifest returns the current manifest (do not modify it).
func (p *Plugin) Manifest() *Manifest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.manifest
}

// Port returns the assigned loopback port (0 if the plugin has no web UI).
func (p *Plugin) Port() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.port
}

// Dir is the plugin's code directory.
func (p *Plugin) Dir() string { return p.dir }

// Status is a JSON-friendly snapshot of a plugin's state for the dashboard API.
type Status struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	HasUI       bool     `json:"hasUI"`
	NeedsAudio  bool     `json:"needsAudio"`
	Enabled     bool     `json:"enabled"`
	State       string   `json:"state"`
	Running     bool     `json:"running"`
	PID         int      `json:"pid,omitempty"`
	Restarts    int      `json:"restarts"`
	LastErr     string   `json:"lastErr"`
	Port        int      `json:"port"`
	Primary     bool     `json:"primary"`
	Order       int      `json:"order"`
	Icon        string   `json:"icon"`
	Unknown     []string `json:"unknownFields,omitempty"`
	// Translations are the manifest's localised titles and descriptions.
	Translations map[string]Translation `json:"translations,omitempty"`
	// Actions are the plugin's entry points for other UIs.
	Actions []Action `json:"actions,omitempty"`
	// Options are the settings users change in the dashboard.
	Options []Option `json:"options,omitempty"`
}

func (p *Plugin) status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.state
	if st == "" {
		st = StateStopped
	}
	m := p.manifest
	return Status{
		Name: m.Name, Title: m.Title, Description: m.Description, Version: m.Version,
		HasUI: m.HTTPProxy != "", NeedsAudio: m.NeedsAudio, Enabled: p.enabled, State: st,
		Running: st == StateRunning, PID: p.pid, Restarts: p.restarts, LastErr: p.lastErr,
		Port: p.port, Primary: m.Primary, Order: m.Order, Icon: m.Icon, Unknown: m.Unknown,
		Translations: m.Translations, Actions: m.Actions, Options: m.Options,
	}
}

func (p *Plugin) setState(s string, pid int) {
	p.mu.Lock()
	changed := p.state != s
	p.state, p.pid = s, pid
	name, lastErr := p.manifest.Name, p.lastErr
	p.mu.Unlock()
	if changed && s != StateStarting {
		data := map[string]interface{}{"name": name, "state": s}
		if s == StateBackoff && lastErr != "" {
			data["error"] = lastErr
		}
		p.host.hostEvent("host.plugin.state", data)
	}
}

// start launches the supervision loop, which keeps the plugin process alive
// (restarting with capped backoff) until stop is called. Calling start on a
// supervised plugin is a no-op.
func (p *Plugin) start() {
	p.mu.Lock()
	if p.cancel != nil {
		p.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	prev, done := p.loop, make(chan struct{})
	p.cancel, p.loop = cancel, done
	p.enabled, p.state, p.pid = true, StateStarting, 0
	p.mu.Unlock()

	go func() {
		defer close(done)
		if prev != nil {
			<-prev // an earlier loop may still be stopping its process
		}
		p.supervise(ctx)
	}()
}

// stop terminates the plugin and waits for its supervision loop to exit.
func (p *Plugin) stop() {
	p.mu.Lock()
	cancel, done := p.cancel, p.loop
	p.cancel, p.enabled = nil, false
	p.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
	p.mu.Lock()
	if p.cancel == nil { // not restarted meanwhile
		p.state, p.pid = StateStopped, 0
	}
	p.mu.Unlock()
	p.log.add("stopped by host")
}

func (p *Plugin) supervise(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		began := time.Now()
		err := p.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		// A process that ran for a while before dying gets a fresh backoff.
		if time.Since(began) > time.Minute {
			backoff = time.Second
		}
		msg := "exited"
		if err != nil {
			msg = err.Error()
		}
		p.mu.Lock()
		p.restarts++
		p.state, p.pid, p.lastErr = StateBackoff, 0, msg
		p.mu.Unlock()
		p.log.add(fmt.Sprintf("exited (%s), restarting in %s", msg, backoff))
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// command builds the process for one run from the manifest, the user config
// and the host environment.
func (p *Plugin) command(ctx context.Context) (*exec.Cmd, []string, error) {
	p.mu.Lock()
	m, port := p.manifest, p.port
	p.mu.Unlock()
	h := p.host
	cfg, err := LoadUserConfig(h.root, m.Name)
	if err != nil {
		return nil, nil, err
	}
	args := make([]string, 0, len(m.Exec)+len(cfg.Args))
	for _, a := range m.Exec {
		args = append(args, expand(a, port, p.dir))
	}
	optArgs, optEnv, problems := optionArgs(m, cfg)
	for _, msg := range problems {
		p.log.add(msg)
	}
	args = append(args, optArgs...)
	args = append(args, cfg.Args...)
	dataDir := h.dataDir(m.Name)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, nil, err
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = p.dir
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("RCPLUG_PORT=%d", port),
		"RCPLUG_PLUGIN_DIR="+p.dir,
		"RCPLUG_DATA_DIR="+dataDir,
		"RCPLUG_NAME="+m.Name,
		"RCPLUG_HOST_URL="+h.HostURL,
		"RCPLUG_VERSION="+h.Version,
	)
	if h.Events != nil {
		cmd.Env = append(cmd.Env, "RCPLUG_TOKEN="+h.Events.Token(m.Name))
	}
	cmd.Env = append(cmd.Env, optEnv...)
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	setProcessGroup(cmd)
	return cmd, args, nil
}

func (p *Plugin) runOnce(ctx context.Context) error {
	cmd, args, err := p.command(ctx)
	if err != nil {
		return err
	}
	// Ask the plugin (and its children) to exit cleanly first; force-kill
	// only if it lingers.
	cmd.Cancel = func() error { return interruptGroup(cmd) }
	cmd.WaitDelay = 3 * time.Second
	// Writers (not pipes) so a grandchild holding the output open can't block
	// Wait; WaitDelay bounds the copy after the plugin itself has exited.
	out := &lineWriter{log: p.log}
	cmd.Stdout, cmd.Stderr = out, out

	p.setState(StateStarting, 0)
	if err := cmd.Start(); err != nil {
		return err
	}
	p.setState(StateRunning, cmd.Process.Pid)
	p.host.groupStarted(p.Manifest().Name, cmd.Process.Pid) // its process group
	p.log.add(fmt.Sprintf("started pid=%d: %s", cmd.Process.Pid, strings.Join(maskArgs(args), " ")))
	err = cmd.Wait()
	killGroup(cmd)
	p.host.groupEnded(p.Manifest().Name)
	out.flush()
	return err
}

// lineWriter splits process output into log lines. Shared by stdout and
// stderr, so writes are serialised.
type lineWriter struct {
	mu  sync.Mutex
	log *ringLog
	buf bytes.Buffer
}

// maxLine is the longest log line; longer output is split into pieces.
const maxLine = 64 * 1024

// goLogStamp is the date and time Go's log package puts in front of every
// line by default. The host stamps each line itself, so it is dropped.
var goLogStamp = regexp.MustCompile(`^[0-9]{4}/[0-9]{2}/[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)? `)

func (w *lineWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(b)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil { // no full line yet; keep the remainder
			w.buf.Reset()
			w.buf.WriteString(line)
			break
		}
		w.addLine(strings.TrimRight(line, "\r\n"))
	}
	for w.buf.Len() > maxLine { // a runaway line without newline
		w.addLine(string(w.buf.Next(maxLine)))
	}
	return len(b), nil
}

// addLine adds one line of output, in pieces of maxLine if it is longer.
func (w *lineWriter) addLine(line string) {
	line = goLogStamp.ReplaceAllString(line, "")
	for len(line) > maxLine {
		w.log.add(line[:maxLine])
		line = line[maxLine:]
	}
	w.log.add(line)
}

func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() > 0 {
		w.addLine(w.buf.String())
		w.buf.Reset()
	}
}
