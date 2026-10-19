package host

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// RØDE's own services that rcplug can switch off, and on again: CallMe (SIP
// calls), rpcbind (an NFS helper the Duo does not use, listening on port
// 111) and ModemManager (the Duo has no modem). Switching one off lays a
// script that does nothing over the file that starts it, with a bind mount:
// the service's init script, so that neither the boot nor RØDE's own restarts
// (audio-start.sh restarts CallMe whenever audio restarts) start it, or the
// program itself for one that D-Bus starts. Then what runs is stopped.
//
// No file of RØDE's is changed. A bind mount lives in memory; rcplug's init
// script makes it again at every boot (rcplugd --services apply, before
// RØDE's services start), uninstalling rcplug undoes it (rcplugd --services
// restore) and without rcplug the device is as RØDE made it. rcplug covers
// only files it knows, by their SHA-256 in the firmware versions it was
// tested with; anything else stays on, and the dashboard says why.

// Service is one of RØDE's services rcplug can switch off.
type Service struct {
	ID string `json:"id"`
	// Path is the file covered: an init script (Script) or a program that
	// D-Bus starts on demand.
	Path   string `json:"path"`
	Script bool   `json:"-"`
	// Procs are the process names (comm) it runs as.
	Procs []string `json:"procs"`
	// Known are the SHA-256 sums of Path in the firmware versions rcplug
	// knows (1.7.3 and 1.7.6 ship the same files).
	Known []string `json:"-"`
}

var defaultServices = []Service{
	{ID: "callme", Path: "/etc/init.d/callme-t-service", Script: true, Procs: []string{"callme-t"},
		Known: []string{"18d4b4f0e0b4f077393f1c6cfd9102a5d2ee4c71ddc9a82a32ae86b974909c14"}},
	{ID: "rpcbind", Path: "/etc/init.d/rpcbind", Script: true, Procs: []string{"rpcbind"},
		Known: []string{"90409ef609dc061c689811e8c2fae803e55ff3f23cc745c5cf498d5ef05392e3"}},
	{ID: "modemmanager", Path: "/usr/sbin/ModemManager", Procs: []string{"ModemManager"},
		Known: []string{"a9c6c8eca768acd4ea8ce368e17fa57f2da65fe3c31174537146455140711bce"}},
}

// offScript replaces a switched-off service's starter: it says so and does
// nothing.
const offScript = "#!/bin/sh\n# rcplug: this service is switched off in rcplug's dashboard (System, RØDE services).\necho \"$(basename \"$0\"): switched off by rcplug\"\nexit 0\n"

// mounter makes and removes the bind mounts; tests use a fake.
type mounter interface {
	bind(src, dst string) error
	unbind(dst string) error
	mounted(dst string) bool
}

// Services switches RØDE's services off and on.
type Services struct {
	root string // rcplug's root: state.json, lib/service-off
	// Prefix is where the device's files are: "" on the device, a folder
	// in tests (RCPLUG_SERVICES_PREFIX).
	Prefix   string
	procRoot string
	catalog  []Service
	mnt      mounter
	logf     func(string, ...interface{})
	// run runs a service's script (stop) and waits; start runs it detached.
	run   func(ctx context.Context, script, arg string) error
	start func(path string, script bool) error
	mu    sync.Mutex
}

// NewServices returns the controller for the device's services.
func NewServices(root string, logf func(string, ...interface{})) *Services {
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	return &Services{root: root, procRoot: "/proc", catalog: defaultServices, mnt: sysMounter{}, logf: logf,
		run: runScript, start: startService}
}

func runScript(ctx context.Context, script, arg string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/sh", script, arg).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", script, arg, err, bytes.TrimSpace(out))
	}
	return nil
}

// startService starts a service again, detached: its init script, or the
// program.
func startService(path string, script bool) error {
	if script {
		return spawnDetached(`exec /bin/sh "$1" start >/dev/null 2>&1`, path)
	}
	return spawnDetached(`exec "$1" >/dev/null 2>&1`, path)
}

func (s *Services) lookup(id string) (Service, bool) {
	for _, sv := range s.catalog {
		if sv.ID == id {
			return sv, true
		}
	}
	return Service{}, false
}

func (s *Services) path(sv Service) string { return filepath.Join(s.Prefix, sv.Path) }

// offFile is the script laid over a switched-off service's file.
func (s *Services) offFile() (string, error) {
	p := filepath.Join(s.root, "lib", "service-off")
	if b, err := os.ReadFile(p); err == nil && string(b) == offScript {
		return p, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(offScript), 0o755); err != nil {
		return "", err
	}
	return p, os.Rename(tmp, p)
}

// check says why a service cannot be switched off here ("" if it can).
func (s *Services) check(sv Service) string {
	b, err := os.ReadFile(s.path(sv))
	if errors.Is(err, os.ErrNotExist) {
		return "not_present"
	}
	if err != nil {
		return "unreadable"
	}
	sum := sha256.Sum256(b)
	for _, k := range sv.Known {
		if k == hex.EncodeToString(sum[:]) {
			return ""
		}
	}
	return "unknown_version"
}

// errUnknownService and friends are the API's answers.
var (
	errUnknownService = sdk.NewError(http.StatusNotFound, "unknown_service", "no such service")
	errServiceVersion = sdk.NewError(http.StatusConflict, "unknown_version",
		"this firmware's file for the service is not one rcplug knows; it stays on")
	errServiceMissing = sdk.NewError(http.StatusConflict, "not_present", "this firmware has no such service")
)

// Off switches a service off: no more starts, and what runs stops.
func (s *Services) Off(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sv, ok := s.lookup(id)
	if !ok {
		return errUnknownService
	}
	dst := s.path(sv)
	if !s.mnt.mounted(dst) {
		switch s.check(sv) {
		case "":
		case "not_present":
			return errServiceMissing
		default:
			return errServiceVersion
		}
		if sv.Script {
			if err := s.run(ctx, dst, "stop"); err != nil {
				s.logf("services: %s: %v (stopping it by name)", sv.ID, err)
			}
		}
		off, err := s.offFile()
		if err != nil {
			return err
		}
		if err := s.mnt.bind(off, dst); err != nil {
			return fmt.Errorf("switching %s off: %w", sv.ID, err)
		}
	}
	s.killAll(sv.Procs)
	s.saveOff(id, true)
	s.logf("services: %s switched off", sv.ID)
	return nil
}

// On switches a service on again and starts it.
func (s *Services) On(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sv, ok := s.lookup(id)
	if !ok {
		return errUnknownService
	}
	dst := s.path(sv)
	if s.mnt.mounted(dst) {
		if err := s.mnt.unbind(dst); err != nil {
			return fmt.Errorf("switching %s on: %w", sv.ID, err)
		}
	}
	s.saveOff(id, false)
	if s.running(sv.Procs) == 0 {
		if err := s.start(dst, sv.Script); err != nil {
			return fmt.Errorf("starting %s: %w", sv.ID, err)
		}
	}
	s.logf("services: %s switched on", sv.ID)
	return nil
}

// Apply makes the bind mounts for the services switched off, at boot before
// RØDE's services start (rcplugd --services apply). A service whose file
// rcplug does not know (another firmware) stays on.
func (s *Services) Apply() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []string
	for _, id := range s.loadOff() {
		sv, ok := s.lookup(id)
		if !ok {
			continue
		}
		dst := s.path(sv)
		if s.mnt.mounted(dst) {
			continue
		}
		if why := s.check(sv); why != "" {
			errs = append(errs, sv.ID+": "+why+", left on")
			continue
		}
		off, err := s.offFile()
		if err == nil {
			err = s.mnt.bind(off, dst)
		}
		if err != nil {
			errs = append(errs, sv.ID+": "+err.Error())
			continue
		}
		s.logf("services: %s off", sv.ID)
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Restore undoes every bind mount and starts the services again, when
// rcplug is uninstalled (rcplugd --services restore). The choices stay in
// state.json, which goes with the install directory anyway.
func (s *Services) Restore() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []string
	for _, sv := range s.catalog {
		dst := s.path(sv)
		if !s.mnt.mounted(dst) {
			continue
		}
		if err := s.mnt.unbind(dst); err != nil {
			errs = append(errs, sv.ID+": "+err.Error())
			continue
		}
		if err := s.start(dst, sv.Script); err != nil {
			errs = append(errs, sv.ID+": "+err.Error())
		}
		s.logf("services: %s on again", sv.ID)
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// ---- the choices, in state.json

func (s *Services) stateFile() string { return filepath.Join(s.root, "state.json") }

func (s *Services) loadOff() []string {
	var ps persistState
	_ = sdk.LoadJSON(s.stateFile(), &ps)
	return ps.ServicesOff
}

// saveOff updates one choice; the rest of state.json is kept as it is.
func (s *Services) saveOff(id string, off bool) {
	stateMu.Lock()
	defer stateMu.Unlock()
	var ps persistState
	_ = sdk.LoadJSON(s.stateFile(), &ps)
	if ps.Enabled == nil {
		ps.Enabled = map[string]bool{}
	}
	var keep []string
	for _, x := range ps.ServicesOff {
		if x != id {
			keep = append(keep, x)
		}
	}
	if off {
		keep = append(keep, id)
		sort.Strings(keep)
	}
	ps.ServicesOff = keep
	_ = sdk.SaveJSON(s.stateFile(), ps)
}

// ---- processes

// pids returns the processes named name (their comm).
func (s *Services) pids(names []string) []int {
	want := map[string]bool{}
	for _, n := range names {
		if len(n) > 15 {
			n = n[:15] // the kernel keeps 15 characters of a name
		}
		want[n] = true
	}
	dirs, _ := filepath.Glob(filepath.Join(s.procRoot, "[0-9]*"))
	var out []int
	for _, d := range dirs {
		pid, err := strconv.Atoi(filepath.Base(d))
		if err != nil || pid == os.Getpid() {
			continue
		}
		comm, err := os.ReadFile(filepath.Join(d, "comm"))
		if err == nil && want[strings.TrimSpace(string(comm))] {
			out = append(out, pid)
		}
	}
	return out
}

func (s *Services) running(names []string) int { return len(s.pids(names)) }

// killAll stops the processes of a service: TERM, then KILL after 5 s.
func (s *Services) killAll(names []string) {
	signal(s.pids(names), syscall.SIGTERM)
	pids := s.pids(names)
	for end := time.Now().Add(5 * time.Second); len(pids) > 0 && time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		pids = s.pids(names)
	}
	signal(pids, syscall.SIGKILL)
}

// ---- what the dashboard shows

// ServiceStatus is one service for the dashboard.
type ServiceStatus struct {
	ID string `json:"id"`
	// State is "on", "off" or "unavailable" (Reason says why).
	State  string     `json:"state"`
	Reason string     `json:"reason,omitempty"`
	Procs  []ProcInfo `json:"procs"`
	// Listening are the ports it accepts connections on ("tcp 5060"),
	// Outside how many connections it holds to machines outside the network.
	Listening []string `json:"listening"`
	Outside   int      `json:"outside"`
}

// Status describes every service, with what its processes cost (from snap).
func (s *Services) Status(snap procSnapshot) []ServiceStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	off := map[string]bool{}
	for _, id := range s.loadOff() {
		off[id] = true
	}
	socks := s.sockets()
	out := []ServiceStatus{}
	for _, sv := range s.catalog {
		st := ServiceStatus{ID: sv.ID, State: "on", Procs: []ProcInfo{}, Listening: []string{}}
		switch {
		case s.mnt.mounted(s.path(sv)):
			st.State = "off"
		default:
			if why := s.check(sv); why != "" {
				st.State, st.Reason = "unavailable", why
			} else if off[sv.ID] {
				st.Reason = "not_applied" // off by choice, but the mount is missing (applied at the next start)
			}
		}
		names := map[string]bool{}
		for _, n := range sv.Procs {
			if len(n) > 15 {
				n = n[:15]
			}
			names[n] = true
		}
		pids := map[int]bool{}
		for _, p := range snap.Procs {
			if names[p.Name] {
				st.Procs = append(st.Procs, p)
				pids[p.PID] = true
			}
		}
		seen := map[string]bool{}
		for _, so := range socks {
			if !pids[so.pid] {
				continue
			}
			if so.listen && !seen[so.proto+" "+so.port] {
				seen[so.proto+" "+so.port] = true
				st.Listening = append(st.Listening, so.proto+" "+so.port)
			}
			if so.outside {
				st.Outside++
			}
		}
		sort.Strings(st.Listening)
		out = append(out, st)
	}
	return out
}

type svcSocket struct {
	pid         int
	proto, port string
	listen      bool
	outside     bool // connected to a machine outside the local network
}

// sockets lists the TCP and UDP sockets with the processes holding them.
func (s *Services) sockets() []svcSocket {
	owner := map[uint64]int{}
	dirs, _ := filepath.Glob(filepath.Join(s.procRoot, "[0-9]*"))
	for _, d := range dirs {
		pid, err := strconv.Atoi(filepath.Base(d))
		if err != nil {
			continue
		}
		fds, err := os.ReadDir(filepath.Join(d, "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(d, "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(link, "socket:[") {
				continue
			}
			if ino, err := strconv.ParseUint(strings.TrimSuffix(link[len("socket:["):], "]"), 10, 64); err == nil {
				owner[ino] = pid
			}
		}
	}
	var out []svcSocket
	add := func(proto string, list []procSocket) {
		for _, so := range list {
			pid, ok := owner[so.inode]
			if !ok {
				continue
			}
			_, port, _ := net.SplitHostPort(so.local)
			host, _, _ := net.SplitHostPort(so.remote)
			ip := net.ParseIP(host)
			listen := (proto == "tcp" && so.state == "0A") || (proto == "udp" && (ip == nil || ip.IsUnspecified()))
			outside := ip != nil && !ip.IsUnspecified() && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() &&
				(proto == "udp" || so.state == "01")
			out = append(out, svcSocket{pid: pid, proto: proto, port: port, listen: listen, outside: outside})
		}
	}
	add("tcp", readProcNet(filepath.Join(s.procRoot, "net/tcp"), filepath.Join(s.procRoot, "net/tcp6")))
	add("udp", readProcNet(filepath.Join(s.procRoot, "net/udp"), filepath.Join(s.procRoot, "net/udp6")))
	return out
}

func signal(pids []int, sig os.Signal) {
	for _, pid := range pids {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Signal(sig)
		}
	}
}

// ---- the API

var errNoServices = sdk.NewError(http.StatusNotFound, "not_available", "switching services is not available here")

// handleServices: GET api/services, every service with what it costs, and
// which plugin has the device's screen.
func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	if s.Services == nil {
		sdk.WriteError(w, errNoServices)
		return
	}
	snap := s.procs.sample("/proc", s.procGroups(), os.Getpid())
	screen := ""
	if s.m.Screen != nil {
		screen = s.m.Screen.Holder()
	}
	writeJSON(w, map[string]interface{}{"services": s.Services.Status(snap), "screen": screen, "cores": snap.Cores})
}

// handleService: POST api/services/<id> {"on": false} switches a service
// off, {"on": true} on again.
func (s *Server) handleService(w http.ResponseWriter, r *http.Request) {
	if s.Services == nil {
		sdk.WriteError(w, errNoServices)
		return
	}
	var req struct {
		On *bool `json:"on"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.On == nil {
		sdk.WriteError(w, sdk.NewError(http.StatusBadRequest, "bad_request", `send {"on": true} or {"on": false}`))
		return
	}
	id := r.PathValue("id")
	var err error
	if *req.On {
		err = s.Services.On(id)
	} else {
		err = s.Services.Off(r.Context(), id)
	}
	if err != nil {
		var ae *sdk.APIError
		if !errors.As(err, &ae) {
			err = sdk.NewError(http.StatusInternalServerError, "service_failed", err.Error())
		}
		sdk.WriteError(w, err)
		return
	}
	state := "off"
	if *req.On {
		state = "on"
	}
	s.m.hostEvent("host.service", map[string]string{"id": id, "state": state})
	s.handleServices(w, r)
}
