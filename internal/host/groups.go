package host

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Running plugins are recorded in <root>/.groups (process group per plugin,
// with the boot they belong to). If rcplugd dies without stopping its
// plugins, the kernel sends SIGTERM to each plugin's own process, but helpers
// it started may live on. The next rcplugd removes those groups before it
// starts anything (ReapLeftovers).

type groupFile struct {
	Boot   string         `json:"boot"`
	Groups map[string]int `json:"groups"`
}

type groupLog struct {
	mu   sync.Mutex
	path string
}

func (g *groupLog) update(fn func(map[string]int)) {
	if g == nil || g.path == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	f := groupFile{Groups: map[string]int{}}
	if raw, err := os.ReadFile(g.path); err == nil {
		_ = json.Unmarshal(raw, &f)
	}
	if f.Groups == nil || f.Boot != bootID() {
		f = groupFile{Boot: bootID(), Groups: map[string]int{}}
	}
	fn(f.Groups)
	raw, _ := json.Marshal(f)
	tmp := g.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o644) == nil {
		_ = os.Rename(tmp, g.path)
	}
}

func (m *Manager) groupStarted(name string, pgid int) {
	m.groups.update(func(g map[string]int) { g[name] = pgid })
}

func (m *Manager) groupEnded(name string) {
	m.groups.update(func(g map[string]int) { delete(g, name) })
}

// ReapLeftovers kills what is left of plugins of an earlier rcplugd of this
// boot that died without stopping them: process groups recorded in .groups
// that still have a member working in the plugins directory. Call it before
// Load. It returns the plugins whose leftovers were removed.
func (m *Manager) ReapLeftovers() []string {
	raw, err := os.ReadFile(m.groups.path)
	if err != nil {
		return nil
	}
	_ = os.Remove(m.groups.path)
	var f groupFile
	if json.Unmarshal(raw, &f) != nil || f.Boot == "" || f.Boot != bootID() {
		return nil // another boot: those process groups are long gone
	}
	dir, err := filepath.EvalSymlinks(m.pluginsDir)
	if err != nil {
		return nil
	}
	var reaped []string
	for name, pgid := range f.Groups {
		if pgid > 1 && killGroupIfIn(pgid, dir+string(filepath.Separator)) {
			reaped = append(reaped, name)
		}
	}
	return reaped
}

// bootID identifies this boot of the machine ("" where unknown).
func bootID() string {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
