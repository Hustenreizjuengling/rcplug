package host

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Resources in the diagnostics bundle: a look at the device when the bundle
// is made (system/resources.txt and system/processes.txt), and what rcplugd
// noted once a minute since it started, for the last three hours
// (system/resources-history.txt): CPU, memory, what rcplug and RØDE's
// programs take, and the busiest processes. A minute's look costs a few
// milliseconds at the host's low priority.

const (
	resourceEvery = time.Minute
	resourceKeep  = 180
)

// ResourceLog keeps the minute history.
type ResourceLog struct {
	mu      sync.Mutex
	sampler procSampler
	entries []resourceEntry
}

type resourceEntry struct {
	at       time.Time
	cpu      CPUUsage
	cpuValid bool
	load     string
	mem      MemUsage
	groups   map[string]groupUse
	topCPU   []ProcInfo
	topMem   []ProcInfo
	cores    int
}

// groupUse is what a group of processes takes: CPU in percent of one core.
type groupUse struct {
	cpu   float64
	ownKB int64
}

// groupOf names whose a process is: "rcplug", "RØDE", "kernel" or "other".
func groupOf(p ProcInfo) string {
	switch {
	case p.Owner == "rcplug":
		return "rcplug"
	case p.Owner == "rode":
		return "RØDE"
	case p.Kernel:
		return "kernel"
	}
	return "other"
}

func groupUses(ps []ProcInfo) map[string]groupUse {
	out := map[string]groupUse{}
	for _, p := range ps {
		g := out[groupOf(p)]
		g.cpu += p.CPU
		g.ownKB += p.OwnKB
		out[groupOf(p)] = g
	}
	return out
}

func top(ps []ProcInfo, n int, by string) []ProcInfo {
	c := append([]ProcInfo(nil), ps...)
	sortProcs(c, by)
	if len(c) > n {
		c = c[:n]
	}
	return c
}

func loadAvg(procRoot string) string {
	b, err := os.ReadFile(procRoot + "/loadavg")
	if err != nil {
		return "?"
	}
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return "?"
	}
	return strings.Join(f[:3], " ")
}

// Run notes the device's resources once a minute until ctx ends.
func (r *ResourceLog) Run(ctx context.Context, groups func() map[int]string) {
	r.sampler.sample("/proc", groups(), os.Getpid()) // the baseline for the first minute's CPU
	t := time.NewTicker(resourceEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.note(r.sampler.sample("/proc", groups(), os.Getpid()), loadAvg("/proc"))
		}
	}
}

func (r *ResourceLog) note(s procSnapshot, load string) {
	e := resourceEntry{at: s.At, cpu: s.CPU, cpuValid: s.CPUValid, load: load, mem: s.Mem, groups: groupUses(s.Procs),
		topCPU: top(s.Procs, 3, "cpu"), topMem: top(s.Procs, 3, "mem"), cores: s.Cores}
	r.mu.Lock()
	r.entries = append(r.entries, e)
	if len(r.entries) > resourceKeep {
		r.entries = append(r.entries[:0], r.entries[len(r.entries)-resourceKeep:]...)
	}
	r.mu.Unlock()
}

// History is the minute history as text, oldest first.
func (r *ResourceLog) History() string {
	r.mu.Lock()
	entries := append([]resourceEntry(nil), r.entries...)
	r.mu.Unlock()
	var b strings.Builder
	b.WriteString(`The device's resources once a minute, oldest first (the last three hours at most).
CPU: share of the whole device over the minute (programs/kernel/interrupts/waiting
for storage). RAM in MB: used of total, then programs (own memory of the
processes), shared memory, kernel, file cache. rcplug, RØDE: their processes'
CPU (share of the device) and own memory. Top: the busiest processes.

`)
	if len(entries) == 0 {
		b.WriteString("(nothing yet: the first line comes a minute after rcplugd started)\n")
		return b.String()
	}
	mb := func(kb int64) int64 { return (kb + 512) / 1024 }
	for _, e := range entries {
		cpu := "    ?"
		if e.cpuValid {
			cpu = fmt.Sprintf("%5.1f%% (%.1f/%.1f/%.1f/%.1f)", e.cpu.Total, e.cpu.User, e.cpu.System, e.cpu.IRQ, e.cpu.IOWait)
		}
		cores := float64(e.cores)
		if cores < 1 {
			cores = 1
		}
		fmt.Fprintf(&b, "%s  CPU %s  load %s  RAM %d/%d (%d %d %d %d)  rcplug %.1f%% %d MB  RØDE %.1f%% %d MB\n",
			e.at.Format("2006-01-02 15:04"), cpu, e.load, mb(e.mem.UsedKB), mb(e.mem.TotalKB),
			mb(e.mem.ProgramsKB), mb(e.mem.SharedKB), mb(e.mem.KernelKB), mb(e.mem.CacheKB),
			e.groups["rcplug"].cpu/cores, mb(e.groups["rcplug"].ownKB), e.groups["RØDE"].cpu/cores, mb(e.groups["RØDE"].ownKB))
		var tc, tm []string
		for _, p := range e.topCPU {
			tc = append(tc, fmt.Sprintf("%s %.1f%%", p.Name, p.CPU/cores))
		}
		for _, p := range e.topMem {
			tm = append(tm, fmt.Sprintf("%s %d MB", p.Name, mb(p.OwnKB)))
		}
		fmt.Fprintf(&b, "                  top CPU: %s · top RAM: %s\n", strings.Join(tc, ", "), strings.Join(tm, ", "))
	}
	return b.String()
}

// ResourceReport looks at the device twice, a second apart, and describes
// it: the summary (CPU, memory, storage, what rcplug and its plugins take)
// and the table of all processes.
func ResourceReport(ctx context.Context, root string, plugins []string, groups map[int]string) (summary, table string) {
	var p procSampler
	p.sample("/proc", groups, os.Getpid())
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
	}
	s := p.sample("/proc", groups, os.Getpid())
	return resourceSummary(s, loadAvg("/proc"), root, plugins), processTable(s)
}

func resourceSummary(s procSnapshot, load, root string, plugins []string) string {
	var b strings.Builder
	mb := func(kb int64) string { return fmt.Sprintf("%d MB", (kb+512)/1024) }
	cores := float64(s.Cores)
	if cores < 1 {
		cores = 1
	}
	fmt.Fprintf(&b, "Resources at %s (CPU over the last second)\n\n", s.At.Format("2006-01-02 15:04:05 MST"))
	if s.CPUValid {
		fmt.Fprintf(&b, "CPU: %.1f %% of the device (%d cores): programs %.1f %%, kernel %.1f %%, interrupts %.1f %%, waiting for storage %.1f %%\n",
			s.CPU.Total, s.Cores, s.CPU.User, s.CPU.System, s.CPU.IRQ, s.CPU.IOWait)
	}
	fmt.Fprintf(&b, "Load (1/5/15 min): %s\n\n", load)
	g := groupUses(s.Procs)
	m := s.Mem
	fmt.Fprintf(&b, "RAM: %s of %s used, %s available\n", mb(m.UsedKB), mb(m.TotalKB), mb(m.AvailableKB))
	fmt.Fprintf(&b, "  programs (own memory of the processes)  %8s   (the processes below together: %s)\n", mb(m.ProgramsKB),
		mb(g["rcplug"].ownKB+g["RØDE"].ownKB+g["other"].ownKB))
	fmt.Fprintf(&b, "  shared memory and tmpfs                 %8s\n", mb(m.SharedKB))
	fmt.Fprintf(&b, "  kernel                                  %8s\n", mb(m.KernelKB))
	fmt.Fprintf(&b, "  file cache (mostly freeable)            %8s\n", mb(m.CacheKB))
	fmt.Fprintf(&b, "  free                                    %8s\n\n", mb(m.FreeKB))
	for _, k := range []string{"rcplug", "RØDE", "other", "kernel"} {
		label := map[string]string{"rcplug": "rcplug and its plugins", "RØDE": "RØDE's programs", "other": "other programs", "kernel": "kernel threads"}[k]
		fmt.Fprintf(&b, "%-24s CPU %5.1f %%  own RAM %s\n", label, g[k].cpu/cores, mb(g[k].ownKB))
	}
	b.WriteString("\nStorage\n")
	seen := map[string]bool{}
	for _, path := range []string{root, "/", "/tmp", "/var/volatile", "/Application/emmc-data", "/Application/sd-card-mount", "/Application/ssd-mount"} {
		free, total, ok := diskStats(path)
		if !ok || seen[fmt.Sprint(free, total)] {
			continue
		}
		seen[fmt.Sprint(free, total)] = true
		fmt.Fprintf(&b, "  %-28s %6d MB free of %6d MB\n", path, free, total)
	}
	if root != "" {
		b.WriteString("\nrcplug's files per plugin (program, data, settings and logs)\n")
		sort.Strings(plugins)
		for _, name := range plugins {
			prog := dirSize(filepath.Join(root, "plugins", name))
			data := dirSize(filepath.Join(root, "data", name))
			var logs int64
			files, _ := filepath.Glob(filepath.Join(root, "logs", name+".log*"))
			for _, f := range files {
				if fi, err := os.Stat(f); err == nil {
					logs += fi.Size()
				}
			}
			if fi, err := os.Stat(filepath.Join(root, "config", name+".json")); err == nil {
				data += fi.Size() // its settings count as its data
			}
			fmt.Fprintf(&b, "  %-20s program %8s  data %8s  logs %8s\n", name, size(prog), size(data), size(logs))
		}
		fmt.Fprintf(&b, "  %-20s %8s\n", "rcplug in all", size(dirSize(root)))
	}
	return b.String()
}

func size(n int64) string {
	switch {
	case n >= 10<<20:
		return fmt.Sprintf("%d MB", n>>20)
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d KB", (n+1023)>>10)
}

// dirSize adds up the files under dir (at most 100 000 of them).
func dirSize(dir string) int64 {
	var total int64
	n := 0
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if n++; n > 100000 {
			return filepath.SkipAll
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return total
}

// processTable lists every process, kernel threads too.
func processTable(s procSnapshot) string {
	ps := append([]ProcInfo(nil), s.Procs...)
	sortProcs(ps, "cpu")
	cores := float64(s.Cores)
	if cores < 1 {
		cores = 1
	}
	var b strings.Builder
	b.WriteString("CPU: share of the whole device over the last second. OWN: memory only this process\n" +
		"holds (RssAnon). SHARED: program files and shared memory it maps (RssFile + RssShmem),\n" +
		"counted in every process that maps them, so it does not add up across processes.\n\n")
	fmt.Fprintf(&b, "%-7s %-16s %-10s %6s %8s %9s %8s %7s %9s  %s\n", "PID", "NAME", "OWNER", "CPU%", "OWN MB", "SHARED MB", "RSS MB", "THREADS", "CPU s", "COMMAND")
	for _, p := range ps {
		owner := groupOf(p)
		if p.Plugin != "" && p.Plugin != "rcplugd" {
			owner = p.Plugin
		}
		cmd := p.Command
		if cmd == "" {
			cmd = "[" + p.Name + "]"
		}
		fmt.Fprintf(&b, "%-7d %-16.16s %-10.10s %6.1f %8.1f %9.1f %8.1f %7d %9.1f  %s\n", p.PID, p.Name, owner, p.CPU/cores,
			float64(p.OwnKB)/1024, float64(p.SharedKB)/1024, float64(p.RSSKB)/1024, p.Threads, p.CPUTime, cmd)
	}
	return b.String()
}
