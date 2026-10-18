package host

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"github.com/Hustenreizjuengling/rcplug/sdk"
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

	mu       sync.Mutex
	plugins  map[string]*Plugin
	order    []string
	nextPort int
	saveMu   sync.Mutex
	groups   *groupLog // process groups of running plugins (groups.go)
}

type persistState struct {
	Enabled map[string]bool `json:"enabled"`
	// AutoCheck: the user switched on the daily check for updates.
	AutoCheck bool `json:"autoCheck,omitempty"`
	// AudioTaps: the user switched on the shared audio taps.
	AudioTaps bool `json:"audioTaps,omitempty"`
	// Catalogs: plugin catalogs the user added (catalog.go).
	Catalogs []CatalogSource `json:"catalogs,omitempty"`
}

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
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	ps := m.loadState()
	ps.Enabled[name] = enabled
	_ = sdk.SaveJSON(m.stateFile(), ps)
}

// AutoCheck reports whether the user switched on the daily update check.
func (m *Manager) AutoCheck() bool { return m.loadState().AutoCheck }

// SetAutoCheck records the user's choice for the daily update check.
func (m *Manager) SetAutoCheck(on bool) {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
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
	p := m.get(name)
	if p == nil {
		return false
	}
	p.start()
	m.saveState(name, true)
	return true
}

// Disable stops a plugin and persists the choice.
func (m *Manager) Disable(name string) bool {
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
	p := m.get(name)
	if p == nil {
		return false
	}
	p.mu.Lock()
	enabled := p.enabled
	p.mu.Unlock()
	if enabled {
		p.stop()
		p.start()
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
		p.stop()
		p.start()
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
		p.stop()
	}
	target := filepath.Join(m.pluginsDir, name)
	backup := staged + ".previous"
	_ = os.RemoveAll(backup)
	restore := func() {
		if p != nil && wasEnabled {
			p.start()
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
		np.start()
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
