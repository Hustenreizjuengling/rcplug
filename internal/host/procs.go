package host

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The device's resources for the dashboard and the diagnostics bundle:
//
//   - CPU of the whole device since the previous look, split into programs,
//     the kernel, interrupts and waiting for storage (/proc/stat);
//   - memory as the kernel accounts it (/proc/meminfo): what programs take,
//     shared memory, the kernel, the file cache (mostly freeable) and free;
//   - per process its CPU, its own memory (RssAnon) and what it shares with
//     others (program files and shared memory: RssFile + RssShmem, counted
//     in every process that maps them, so these do not add up), its threads
//     and who it belongs to: rcplug (the host and each plugin's process
//     group) or RØDE's own programs.
//
// Only counters are read (/proc/<pid>/stat and status), never smaps: walking
// a process's page tables holds its memory lock, which the mixer's audio
// threads may need. A look takes a few milliseconds on the Duo.

// ProcInfo is one process.
type ProcInfo struct {
	PID      int     `json:"pid"`
	Name     string  `json:"name"`
	Command  string  `json:"command"`
	CPU      float64 `json:"cpu"`      // percent of one core since the previous sample
	CPUTime  float64 `json:"cpuTime"`  // seconds since the process started
	RSSKB    int64   `json:"rssKB"`    // resident: own + shared
	OwnKB    int64   `json:"ownKB"`    // RssAnon: memory only this process holds
	SharedKB int64   `json:"sharedKB"` // RssFile + RssShmem: program files and shared memory
	Threads  int     `json:"threads"`
	Kernel   bool    `json:"kernel,omitempty"` // a kernel thread
	Owner    string  `json:"owner,omitempty"`  // "rcplug" or "rode"
	Plugin   string  `json:"plugin,omitempty"` // the plugin whose process group it is in
}

// CPUUsage is the share of the whole device (all cores) in percent.
type CPUUsage struct {
	Total  float64 `json:"total"`
	User   float64 `json:"user"`   // programs
	System float64 `json:"system"` // the kernel, for programs and in its own threads
	IRQ    float64 `json:"irq"`    // interrupts
	IOWait float64 `json:"iowait"` // idle while waiting for storage
}

// MemUsage is the device's memory as /proc/meminfo accounts it, in KB.
type MemUsage struct {
	TotalKB     int64 `json:"totalKB"`
	AvailableKB int64 `json:"availableKB"` // free plus what the kernel can free at once
	UsedKB      int64 `json:"usedKB"`      // total - available
	ProgramsKB  int64 `json:"programsKB"`  // AnonPages: the processes' own memory
	SharedKB    int64 `json:"sharedKB"`    // Shmem: shared memory and tmpfs (/tmp, /dev/shm)
	KernelKB    int64 `json:"kernelKB"`    // SUnreclaim + KernelStack + PageTables
	CacheKB     int64 `json:"cacheKB"`     // Buffers + Cached - Shmem + SReclaimable: files, mostly freeable
	FreeKB      int64 `json:"freeKB"`      // MemFree
}

// rodePrograms lists the stock firmware's own long-running programs.
var rodePrograms = map[string]bool{"rc_audio_mixer": true, "rc_gui_juce": true, "jackd": true, "callme-t": true,
	"audio-start.sh": true, "display-start.sh": true}

const (
	userHZ     = 100.0      // clock ticks per second in /proc on Linux
	pfKthread  = 0x00200000 // task flag: a kernel thread
	maxCommand = 160
)

// procSnapshot is one look at the device.
type procSnapshot struct {
	At       time.Time
	Cores    int
	CPU      CPUUsage
	CPUValid bool // false on the first look: CPU needs a previous one
	Mem      MemUsage
	Procs    []ProcInfo
}

type procSampler struct {
	mu   sync.Mutex
	at   time.Time
	last map[int]float64 // pid → CPU ticks at the previous sample
	stat []float64       // /proc/stat's cpu line at the previous sample
	cmds map[int]cmdEntry
}

type cmdEntry struct {
	start string // the process's start time: a reused pid is another process
	cmd   string
}

// sample looks at procRoot; groups maps a process group to its plugin.
func (p *procSampler) sample(procRoot string, groups map[int]string, self int) procSnapshot {
	now := time.Now()
	snap := procSnapshot{At: now, Cores: runtime.NumCPU(), Mem: memUsage(readMeminfo(procRoot + "/meminfo"))}
	ents, err := os.ReadDir(procRoot)
	if err != nil {
		return snap
	}
	page := int64(os.Getpagesize())
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmds == nil {
		p.cmds = map[int]cmdEntry{}
	}
	if stat, cores := readCPUStat(procRoot + "/stat"); stat != nil {
		if cores > 0 {
			snap.Cores = cores
		}
		if len(p.stat) == len(stat) {
			snap.CPU, snap.CPUValid = cpuUsage(p.stat, stat)
		}
		p.stat = stat
	}
	elapsed := now.Sub(p.at).Seconds()
	prev := p.last
	cur := make(map[int]float64, len(ents))
	seen := make(map[int]bool, len(ents))
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(fmt.Sprintf("%s/%d/stat", procRoot, pid))
		if err != nil {
			continue
		}
		s := string(stat)
		open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
		if open < 0 || end < open {
			continue
		}
		f := strings.Fields(s[end+1:])
		if len(f) < 22 {
			continue
		}
		flags, _ := strconv.ParseUint(f[6], 10, 64)
		ut, _ := strconv.ParseFloat(f[11], 64)
		st, _ := strconv.ParseFloat(f[12], 64)
		pgrp, _ := strconv.Atoi(f[2])
		threads, _ := strconv.Atoi(f[17])
		rss, _ := strconv.ParseInt(f[21], 10, 64)
		ticks := ut + st
		cur[pid] = ticks
		seen[pid] = true
		info := ProcInfo{PID: pid, Name: s[open+1 : end], CPUTime: ticks / userHZ, RSSKB: rss * page / 1024, Threads: threads,
			Kernel: flags&pfKthread != 0}
		if before, ok := prev[pid]; ok && elapsed > 0 && ticks >= before {
			info.CPU = (ticks - before) / userHZ / elapsed * 100
		}
		if !info.Kernel {
			info.OwnKB, info.SharedKB = statusMemory(fmt.Sprintf("%s/%d/status", procRoot, pid))
			info.Command = p.command(procRoot, pid, f[19])
		}
		switch {
		case pid == self:
			info.Owner, info.Plugin = "rcplug", "rcplugd"
		case groups[pgrp] != "":
			info.Owner, info.Plugin = "rcplug", groups[pgrp]
		case rodePrograms[info.Name]:
			info.Owner = "rode"
		}
		snap.Procs = append(snap.Procs, info)
	}
	for pid := range p.cmds {
		if !seen[pid] {
			delete(p.cmds, pid)
		}
	}
	p.at, p.last = now, cur
	return snap
}

// command reads a process's command line once (the rest of its life it is
// taken from the cache: reading it briefly holds the process's memory lock).
func (p *procSampler) command(procRoot string, pid int, start string) string {
	if c, ok := p.cmds[pid]; ok && c.start == start {
		return c.cmd
	}
	cmd := ""
	if raw, err := os.ReadFile(fmt.Sprintf("%s/%d/cmdline", procRoot, pid)); err == nil && len(raw) > 0 {
		cmd = strings.Join(maskArgs(strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")), " ")
		if len(cmd) > maxCommand {
			cmd = cmd[:maxCommand] + "…"
		}
	}
	p.cmds[pid] = cmdEntry{start: start, cmd: cmd}
	return cmd
}

// statusMemory reads RssAnon and RssFile + RssShmem from /proc/<pid>/status.
func statusMemory(path string) (own, shared int64) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "Rss") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64)
		switch k {
		case "RssAnon":
			own = n
		case "RssFile", "RssShmem":
			shared += n
		}
	}
	return own, shared
}

// readCPUStat returns the fields of /proc/stat's "cpu" line and the number
// of cpuN lines.
func readCPUStat(path string) ([]float64, int) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, 0
	}
	var total []float64
	cores := 0
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) < 8 || !strings.HasPrefix(f[0], "cpu") {
			continue
		}
		if f[0] != "cpu" {
			cores++
			continue
		}
		for _, x := range f[1:9] {
			v, _ := strconv.ParseFloat(x, 64)
			total = append(total, v)
		}
	}
	return total, cores
}

// cpuUsage compares two "cpu" lines: user nice system idle iowait irq
// softirq steal.
func cpuUsage(a, b []float64) (CPUUsage, bool) {
	d := make([]float64, 8)
	var sum float64
	for i := range d {
		d[i] = b[i] - a[i]
		if d[i] < 0 {
			return CPUUsage{}, false
		}
		sum += d[i]
	}
	if sum <= 0 {
		return CPUUsage{}, false
	}
	pct := func(x float64) float64 { return x / sum * 100 }
	return CPUUsage{
		Total:  pct(sum - d[3] - d[4]),
		User:   pct(d[0] + d[1]),
		System: pct(d[2] + d[7]),
		IRQ:    pct(d[5] + d[6]),
		IOWait: pct(d[4]),
	}, true
}

// readMeminfo returns /proc/meminfo in KB.
func readMeminfo(path string) map[string]int64 {
	out := map[string]int64{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Fields(sc.Text())
		if len(parts) >= 2 {
			if v, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
				out[strings.TrimSuffix(parts[0], ":")] = v
			}
		}
	}
	return out
}

func memUsage(mi map[string]int64) MemUsage {
	m := MemUsage{
		TotalKB: mi["MemTotal"], AvailableKB: mi["MemAvailable"], ProgramsKB: mi["AnonPages"], SharedKB: mi["Shmem"],
		KernelKB: mi["SUnreclaim"] + mi["KernelStack"] + mi["PageTables"],
		CacheKB:  mi["Buffers"] + mi["Cached"] - mi["Shmem"] + mi["SReclaimable"], FreeKB: mi["MemFree"],
	}
	if m.CacheKB < 0 {
		m.CacheKB = 0
	}
	if m.TotalKB > 0 {
		m.UsedKB = m.TotalKB - m.AvailableKB
	}
	return m
}

// procGroups maps the plugins' process groups (and the audio taps' bridge)
// to their names.
func (s *Server) procGroups() map[int]string {
	groups := map[int]string{}
	for _, st := range s.m.List() {
		if st.Running && st.PID > 0 {
			groups[st.PID] = st.Name // plugins run in their own process group
		}
	}
	if s.Taps != nil {
		if b, ok := s.Taps.bridge.(interface{ PIDs() []int }); ok {
			for _, pid := range b.PIDs() {
				groups[pid] = "audio taps"
			}
		}
	}
	return groups
}

func (s *Server) handleProcesses(w http.ResponseWriter, r *http.Request) {
	snap := s.procs.sample("/proc", s.procGroups(), os.Getpid())
	all := r.URL.Query().Get("all") == "1"
	var rss, own int64
	var kernelCount int
	var kernelCPU float64
	kept := snap.Procs[:0]
	for _, p := range snap.Procs {
		rss += p.RSSKB
		own += p.OwnKB
		if p.Kernel {
			kernelCount++
			kernelCPU += p.CPU
		}
		if all || !p.Kernel { // kernel threads only on request
			kept = append(kept, p)
		}
	}
	sortProcs(kept, r.URL.Query().Get("sort"))
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 || limit > 500 {
		limit = 20
	}
	count := len(kept)
	if len(kept) > limit {
		kept = kept[:limit]
	}
	doc := map[string]interface{}{"cores": snap.Cores, "count": count, "rssKB": rss, "ownKB": own, "memory": snap.Mem,
		"kernelThreads": map[string]interface{}{"count": kernelCount, "cpu": kernelCPU}, "processes": kept}
	if snap.CPUValid {
		doc["cpu"] = snap.CPU
	}
	writeJSON(w, doc)
}

func sortProcs(ps []ProcInfo, by string) {
	if by == "mem" {
		sort.Slice(ps, func(i, j int) bool {
			if ps[i].OwnKB != ps[j].OwnKB {
				return ps[i].OwnKB > ps[j].OwnKB
			}
			return ps[i].RSSKB > ps[j].RSSKB
		})
		return
	}
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].CPU != ps[j].CPU {
			return ps[i].CPU > ps[j].CPU
		}
		return ps[i].OwnKB > ps[j].OwnKB
	})
}
