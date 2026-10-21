package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"github.com/Hustenreizjuengling/rcplug/sdk"
	"time"
)

// Manager loads plugins from <root>/plugins, assigns each a loopback port, and
// tracks enable/disable state persisted to <root>/state.json.
//
// Directory layout under root:
//
//	plugins/<name>/   code (plugin.json + executable), replaced on reinstall
//	data/<name>/      the plugin's persistent data (RCPLUG_DATA_DIR)
//	config/<name>.json  optional operator overlay (extra args/env)
//	state.json        which plugins are enabled
type Manager struct {
	root       string
	pluginsDir string
	basePort   int

	// HostURL and Version are passed to plugins (RCPLUG_HOST_URL, RCPLUG_VERSION).
	HostURL string
	Version string
	// Events is the event bus; plugins get a token for it (RCPLUG_TOKEN).
	Events *Bus
	// Screen hands the device's screen to plugins with the permission
	// "screen" (nil: no such plugin starts).
	Screen *Screen
	// screenReturn names the plugin to start again when the one the screen
	// was handed over to ends (see Handover); under screenMu, which the
	// ending plugin's loop takes (never opMu: a stop waits for that loop).
	screenMu   sync.Mutex
	screenHand struct{ from, to string } // the last handover, until `to` ends
	screenKeep string                    // a screen plugin being restarted or updated: it keeps the screen

	mu       sync.Mutex
	plugins  map[string]*Plugin
	order    []string
	nextPort int
	opMu     sync.Mutex // one change to a plugin's state or options at a time
	groups   *groupLog  // process groups of running plugins (groups.go)
}

type persistState struct {
	Enabled map[string]bool `json:"enabled"`
	// Panel names the plugin that holds the device's screen on request
	// (permission screen:request) and gets it again after a restart.
	Panel string `json:"panel,omitempty"`
	// AutoCheck: the user switched on the daily check for updates.
	AutoCheck bool `json:"autoCheck,omitempty"`
	// Catalogs: plugin catalogs the user added (catalog.go).
	Catalogs []CatalogSource `json:"catalogs,omitempty"`
	// ServicesOff: RØDE's services the user switched off (services.go).
	ServicesOff []string `json:"servicesOff,omitempty"`
}

// stateMu serializes changes to state.json (plugins, catalogs, services).
var stateMu sync.Mutex

// NewManager creates a manager rooted at root. basePort is the first loopback
// port handed to plugins; each plugin with a web UI gets the next free one.
func NewManager(root string, basePort int) *Manager {
	return &Manager{
		root:       root,
		pluginsDir: filepath.Join(root, "plugins"),
		basePort:   basePort,
		nextPort:   basePort,
		plugins:    map[string]*Plugin{},
		groups:     &groupLog{path: filepath.Join(root, ".groups")},
		HostURL:    "http://127.0.0.1:8080",
		Version:    "dev",
	}
}

// Root returns the rcplug root directory.
func (m *Manager) Root() string { return m.root }

func (m *Manager) stateFile() string { return filepath.Join(m.root, "state.json") }

func (m *Manager) dataDir(name string) string { return filepath.Join(m.root, "data", name) }

// LogDir holds the persistent logs: <plugin>.log per plugin and rcplugd.log.
func (m *Manager) LogDir() string { return filepath.Join(m.root, "logs") }

func (m *Manager) loadState() persistState {
	ps := persistState{Enabled: map[string]bool{}}
	_ = sdk.LoadJSON(m.stateFile(), &ps)
	if ps.Enabled == nil {
		ps.Enabled = map[string]bool{}
	}
	return ps
}

// saveState records the user's choice for one plugin. Plugins the user
// never started or stopped stay unlisted, so their manifest's autostart
// remains the default (a new version may change it).
func (m *Manager) saveState(name string, enabled bool) {
	stateMu.Lock()
	defer stateMu.Unlock()
	ps := m.loadState()
	ps.Enabled[name] = enabled
	_ = sdk.SaveJSON(m.stateFile(), ps)
}

// savePanel remembers which plugin wants the device's screen ("" none).
func (m *Manager) savePanel(name string) {
	stateMu.Lock()
	defer stateMu.Unlock()
	ps := m.loadState()
	ps.Panel = name
	_ = sdk.SaveJSON(m.stateFile(), ps)
}

// AutoCheck reports whether the user switched on the daily update check.
func (m *Manager) AutoCheck() bool { return m.loadState().AutoCheck }

// SetAutoCheck records the user's choice for the daily update check.
func (m *Manager) SetAutoCheck(on bool) {
	stateMu.Lock()
	defer stateMu.Unlock()
	ps := m.loadState()
	ps.AutoCheck = on
	_ = sdk.SaveJSON(m.stateFile(), ps)
}

// Load scans the plugins directory and starts plugins that should run per
// manifest autostart + persisted state. It is safe to call again later: new
// plugin directories are picked up, removed ones are stopped and forgotten,
// known ones keep their port and process (with a refreshed manifest). It
// returns the names of plugins skipped because their manifest was invalid.
func (m *Manager) Load() ([]string, error) {
	entries, err := os.ReadDir(m.pluginsDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	ps := m.loadState()
	invalid, added := []string{}, []string(nil)
	found := map[string]*Manifest{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		man, err := LoadManifest(filepath.Join(m.pluginsDir, e.Name()))
		if err != nil {
			invalid = append(invalid, e.Name()+": "+err.Error())
			continue
		}
		found[man.Name] = man
	}
	names := make([]string, 0, len(found))
	for n := range found {
		names = append(names, n)
	}
	sort.Strings(names) // stable port assignment across restarts

	m.mu.Lock()
	var removed []*Plugin
	for name, p := range m.plugins {
		if _, ok := found[name]; !ok {
			removed = append(removed, p)
			delete(m.plugins, name)
		}
	}
	for _, name := range names {
		man := found[name]
		p, ok := m.plugins[name]
		if !ok {
			p = &Plugin{dir: filepath.Join(m.pluginsDir, name), host: m,
				log: newRingLog(500, NewRotatingFile(filepath.Join(m.LogDir(), name+".log"), LogFileMax))}
			m.plugins[name] = p
			added = append(added, name)
		}
		p.mu.Lock()
		p.manifest = man
		if man.HTTPProxy != "" && p.port == 0 {
			p.port = m.freePortLocked()
		}
		p.mu.Unlock()
	}
	m.order = m.order[:0]
	for name := range m.plugins {
		m.order = append(m.order, name)
	}
	m.sortLocked()
	m.mu.Unlock()

	for _, p := range removed {
		p.stop()
		if _, err := os.Stat(filepath.Join(p.dir, "plugin.json")); err == nil {
			p.log.add("stopped: its plugin.json is invalid now (see the Plugins page)")
		} else {
			p.log.add("removed: plugin folder no longer exists")
		}
	}
	for _, name := range added {
		p := m.get(name)
		if p.Manifest().has("screen:request") && ps.Panel == name {
			// It runs like any plugin and asks for the screen itself;
			// the screen it had when the device last ran comes back to it
			// once RØDE's screen program is up.
			go m.restorePanelAtBoot(p)
		}
		if p.Manifest().has("screen") {
			// Only ever started from the dashboard; once started, it comes
			// back at every start of rcplug until it is stopped.
			if ps.Enabled[name] {
				go m.startScreenAtBoot(p)
			}
			continue
		}
		want := p.Manifest().Autostart
		if v, ok := ps.Enabled[name]; ok {
			want = v
		}
		if want {
			p.start()
		}
	}
	return invalid, nil
}

// freePortLocked hands out the next loopback port nobody listens on, so a
// port taken by another program (or a second rcplug) does not trap a plugin
// in a restart loop.
func (m *Manager) freePortLocked() int {
	for tries := 0; tries < 200; tries++ {
		port := m.nextPort
		m.nextPort++
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err == nil {
			ln.Close()
			return port
		}
	}
	port := m.nextPort // give up looking; the plugin's log will say why it fails
	m.nextPort++
	return port
}

// sortLocked orders plugins by manifest order, then name.
func (m *Manager) sortLocked() {
	sort.SliceStable(m.order, func(i, j int) bool {
		a, b := m.plugins[m.order[i]].Manifest(), m.plugins[m.order[j]].Manifest()
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return a.Name < b.Name
	})
}

func (m *Manager) get(name string) *Plugin {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.plugins[name]
}

// List returns the status of all plugins in navigation order.
func (m *Manager) List() []Status {
	m.mu.Lock()
	names := append([]string(nil), m.order...)
	m.mu.Unlock()
	out := make([]Status, 0, len(names))
	for _, n := range names {
		if p := m.get(n); p != nil {
			out = append(out, p.status())
		}
	}
	return out
}

// Enable starts a plugin and persists the choice.
func (m *Manager) Enable(name string) bool {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	p := m.get(name)
	if p == nil {
		return false
	}
	if !m.startPlugin(p) {
		return true
	}
	m.saveState(name, true)
	return true
}

// startPlugin starts a plugin, taking the device's screen first for one
// that needs it; false when the screen could not be taken.
func (m *Manager) startPlugin(p *Plugin) bool {
	if p.Manifest().has("screen") {
		if err := m.takeScreen(p.Manifest().Name); err != nil {
			p.mu.Lock()
			p.lastErr = err.Error()
			p.mu.Unlock()
			p.log.add("not started: " + err.Error())
			return false
		}
	}
	p.start()
	return true
}

// Handover passes the device's screen from the plugin that has it to
// another screen plugin: the first stops (its remembered state stays, so it
// comes back after a reboot), the second starts without RØDE's screen in
// between, and when the second ends — however it ends — the first is
// started again. A demo like PlugRain is started from the screen plugin's
// own menu this way.
func (m *Manager) Handover(from, to string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if m.Screen == nil {
		return errors.New("this rcplug cannot hand over the device's screen")
	}
	src, dst := m.get(from), m.get(to)
	if src == nil || dst == nil {
		return sdk.NewError(http.StatusNotFound, "not_found", "no such plugin")
	}
	if !dst.Manifest().has("screen") {
		return sdk.NewError(http.StatusBadRequest, "no_screen", to+" does not take over the screen (permission screen)")
	}
	if from == to {
		return sdk.NewError(http.StatusBadRequest, "same_plugin", "a plugin cannot hand the screen to itself")
	}
	if m.Screen.Holder() != from {
		return sdk.NewError(http.StatusConflict, "not_holder", from+" does not have the screen")
	}
	if err := m.Screen.Transfer(from, to); err != nil {
		return err
	}
	m.screenMu.Lock()
	m.screenHand.from, m.screenHand.to = from, to
	m.screenMu.Unlock()
	src.log.add("handed the screen over to " + to + "; comes back when it ends")
	// The stop and the start happen after this returns: the caller is
	// usually the plugin that hands over, waiting for the answer in its own
	// loop, and stopping it before it has the answer would leave it hanging
	// until it is killed (3 s of black screen).
	go func() {
		m.opMu.Lock()
		defer m.opMu.Unlock()
		if !src.Manifest().has("screen:request") {
			src.stop() // its loop's Release is a no-op: the screen is with the other now
		}
		dst.log.add("started with the screen handed over from " + from)
		dst.start()
	}()
	return nil
}

// screenEnded is called when a screen plugin's run is over: the screen goes
// back to the plugin it was handed over from, if any, else to RØDE — unless
// the plugin is being restarted or updated (stopKeepingScreen), when it
// keeps the screen for its next run.
func (m *Manager) screenEnded(name string) {
	// Only the plugin the screen was handed to ends the handover; the one
	// that handed it over ends too, right after, and must not consume it.
	m.screenMu.Lock()
	if m.screenKeep == name {
		m.screenMu.Unlock()
		return
	}
	ret := ""
	if m.screenHand.to == name {
		ret = m.screenHand.from
		m.screenHand.from, m.screenHand.to = "", ""
	}
	m.screenMu.Unlock()
	if ret != "" && ret != name && m.Screen != nil && m.Screen.Holder() == name {
		if p := m.get(ret); p != nil && m.loadState().Enabled[ret] {
			p.mu.Lock()
			running := p.cancel != nil
			p.mu.Unlock()
			if err := m.Screen.Transfer(name, ret); err == nil {
				if running {
					// It kept running without the screen (screen:request)
					// and finds the screen back with it (the marker).
					p.log.add(name + " gave the screen back")
					return
				}
				p.log.add("started again: " + name + " gave the screen back")
				p.start()
				return
			}
		}
	}
	m.Screen.Release(name)
}

// startScreenAtBoot starts a remembered screen plugin once RØDE's screen
// program has come up (rcplugd starts before the display service at boot,
// and taking the screen earlier would leave two programs on it), or after
// Screen.BootWait at the latest. A stop from the dashboard meanwhile wins.
func (m *Manager) startScreenAtBoot(p *Plugin) {
	name := p.Manifest().Name
	if m.Screen != nil && m.Screen.BootWait > 0 {
		end := time.Now().Add(m.Screen.BootWait)
		for !m.Screen.AppRunning() && time.Now().Before(end) {
			time.Sleep(time.Second)
		}
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if cur := m.get(name); cur != p {
		return // reloaded or removed meanwhile
	}
	if !m.loadState().Enabled[name] {
		return // stopped from the dashboard while waiting
	}
	p.mu.Lock()
	running := p.cancel != nil
	p.mu.Unlock()
	if running {
		return
	}
	p.log.add("started at rcplug's start (it was running when the device last ran)")
	m.startPlugin(p)
}

func (m *Manager) takeScreen(name string) error {
	if m.Screen == nil {
		return errors.New("this rcplug cannot hand over the device's screen")
	}
	return m.Screen.Take(name)
}

// TakeScreenFor gives the device's screen to a running plugin that asks for
// it (permission screen:request) and remembers the wish for the next start
// of the device.
func (m *Manager) TakeScreenFor(name string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	p := m.get(name)
	if p == nil {
		return sdk.NewError(http.StatusNotFound, "not_found", "no such plugin")
	}
	if !p.Manifest().has("screen:request") {
		return sdk.NewError(http.StatusForbidden, "permission_screen", name+` does not declare the permission "screen:request"`)
	}
	p.mu.Lock()
	running := p.cancel != nil
	p.mu.Unlock()
	if !running {
		return sdk.NewError(http.StatusConflict, "not_running", name+" is not running")
	}
	if err := m.takeScreen(name); err != nil {
		return err
	}
	m.savePanel(name)
	p.log.add("has the device's screen")
	return nil
}

// ReleaseScreenFor gives the screen back to RØDE if plugin name has it, and
// forgets the wish.
func (m *Manager) ReleaseScreenFor(name string) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if m.Screen != nil && m.Screen.Holder() == name {
		m.Screen.Release(name)
		if p := m.get(name); p != nil {
			p.log.add("gave the device's screen back")
		}
	}
	if m.loadState().Panel == name {
		m.savePanel("")
	}
}

// ScreenState tells who has the device's screen and who wants it.
func (m *Manager) ScreenState() (holder, wanted string) {
	if m.Screen != nil {
		holder = m.Screen.Holder()
	}
	return holder, m.loadState().Panel
}

// restorePanelAtBoot gives the screen back to the plugin that had it when
// the device last ran (permission screen:request), once RØDE's screen
// program has come up, or after Screen.BootWait at the latest.
func (m *Manager) restorePanelAtBoot(p *Plugin) {
	name := p.Manifest().Name
	if m.Screen != nil && m.Screen.BootWait > 0 {
		end := time.Now().Add(m.Screen.BootWait)
		for !m.Screen.AppRunning() && time.Now().Before(end) {
			time.Sleep(time.Second)
		}
	}
	// The plugin itself may take a moment to start; give it that.
	for i := 0; i < 30; i++ {
		p.mu.Lock()
		running := p.cancel != nil
		p.mu.Unlock()
		if running {
			break
		}
		time.Sleep(time.Second)
	}
	if m.loadState().Panel != name || m.get(name) != p {
		return // released, reloaded or removed meanwhile
	}
	if err := m.TakeScreenFor(name); err != nil {
		p.log.add("the device's screen did not come back: " + err.Error())
	} else {
		p.log.add("has the device's screen again (it had it when the device last ran)")
	}
}

// stopKeepingScreen stops p for a restart or an update: a screen plugin
// keeps the screen meanwhile, so RØDE's screen program is not started for
// the moment in between (it would only be stopped again at once, and while
// it still comes up it takes the display memory the plugin's next process
// needs). The caller starts p again, or releases the screen if it cannot.
func (m *Manager) stopKeepingScreen(p *Plugin) {
	name := p.Manifest().Name
	keep := m.Screen != nil && m.Screen.Holder() == name
	if keep {
		m.screenMu.Lock()
		m.screenKeep = name
		m.screenMu.Unlock()
	}
	p.stop()
	if keep {
		m.screenMu.Lock()
		m.screenKeep = ""
		m.screenMu.Unlock()
		p.log.add("keeps the screen meanwhile")
	}
}

// releaseScreenUnlessRunning gives the screen back to RØDE if plugin name
// has it but is not running (an update or restart that could not start it).
func (m *Manager) releaseScreenUnlessRunning(name string) {
	if m.Screen == nil || m.Screen.Holder() != name {
		return
	}
	if p := m.get(name); p != nil {
		p.mu.Lock()
		running := p.cancel != nil
		p.mu.Unlock()
		if running {
			return
		}
	}
	m.Screen.Release(name)
}

// Disable stops a plugin and persists the choice.
func (m *Manager) Disable(name string) bool {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	p := m.get(name)
	if p == nil {
		return false
	}
	p.stop()
	m.saveState(name, false)
	return true
}

// Restart stops and starts an enabled plugin without changing its persisted
// state. A disabled plugin is left alone (use Enable).
func (m *Manager) Restart(name string) bool {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	p := m.get(name)
	if p == nil {
		return false
	}
	p.mu.Lock()
	enabled := p.enabled
	p.mu.Unlock()
	if enabled {
		m.stopKeepingScreen(p)
		m.startPlugin(p)
		m.releaseScreenUnlessRunning(name)
	}
	return true
}

// Options returns the option values set for a plugin (nil, false if there
// is no such plugin).
func (m *Manager) Options(name string) (map[string]json.RawMessage, bool, error) {
	p := m.get(name)
	if p == nil {
		return nil, false, nil
	}
	cfg, err := LoadUserConfig(m.root, name)
	if cfg.Options == nil {
		cfg.Options = map[string]json.RawMessage{}
	}
	return cfg.Options, true, err
}

// SetOptions stores option values for a plugin and restarts it if it runs,
// so they take effect.
func (m *Manager) SetOptions(name string, values map[string]json.RawMessage) (found, restarted bool, err error) {
	m.opMu.Lock() // the file is read and written whole: no two changes at once
	defer m.opMu.Unlock()
	p := m.get(name)
	if p == nil {
		return false, false, nil
	}
	if err := SaveOptions(m.root, p.Manifest(), values); err != nil {
		return true, false, err
	}
	p.mu.Lock()
	enabled := p.enabled
	p.mu.Unlock()
	if enabled {
		p.log.add("options changed in the dashboard; restarting")
		m.stopKeepingScreen(p)
		m.startPlugin(p)
		m.releaseScreenUnlessRunning(name)
	}
	return true, enabled, nil
}

// Manifests returns the manifests of the loaded plugins in navigation order.
func (m *Manager) Manifests() []*Manifest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Manifest, 0, len(m.order))
	for _, n := range m.order {
		out = append(out, m.plugins[n].Manifest())
	}
	return out
}

// ReplacePlugin swaps the code of plugin name for the directory staged (an
// unpacked, checked package) and starts it again if it was running. The
// plugin's data and its operator config stay untouched. If the swap fails,
// the previous code is put back.
func (m *Manager) ReplacePlugin(name, staged string) (started bool, err error) {
	if !validName.MatchString(name) {
		return false, fmt.Errorf("invalid plugin name %q", name)
	}
	p := m.get(name)
	wasEnabled := false
	if p != nil {
		p.mu.Lock()
		wasEnabled = p.enabled
		p.mu.Unlock()
		m.stopKeepingScreen(p)
		defer m.releaseScreenUnlessRunning(name)
	}
	target := filepath.Join(m.pluginsDir, name)
	backup := staged + ".previous"
	_ = os.RemoveAll(backup)
	restore := func() {
		if p != nil && wasEnabled {
			m.startPlugin(p)
		}
	}
	hadOld := false
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, backup); err != nil {
			restore()
			return false, err
		}
		hadOld = true
	}
	if err := os.MkdirAll(m.pluginsDir, 0o755); err != nil {
		return false, err
	}
	if err := os.Rename(staged, target); err != nil {
		if hadOld {
			_ = os.Rename(backup, target)
		}
		restore()
		return false, err
	}
	if _, err := m.Load(); err != nil {
		return false, err
	}
	if np := m.get(name); np != nil && wasEnabled {
		m.startPlugin(np)
	}
	if np := m.get(name); np != nil {
		np.log.add("updated to version " + np.Manifest().Version)
	}
	_ = os.RemoveAll(backup)
	return wasEnabled, nil
}

// Logs returns the recent log lines of a plugin.
func (m *Manager) Logs(name string) []string {
	p := m.get(name)
	if p == nil {
		return nil
	}
	return p.log.snapshot()
}

// StopAll stops every plugin in parallel (used on host shutdown). Persisted
// state is left untouched so the same plugins come back on the next boot.
func (m *Manager) StopAll() {
	m.mu.Lock()
	ps := make([]*Plugin, 0, len(m.plugins))
	for _, p := range m.plugins {
		ps = append(ps, p)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, p := range ps {
		wg.Add(1)
		go func(p *Plugin) { defer wg.Done(); p.stop() }(p)
	}
	wg.Wait()
}
