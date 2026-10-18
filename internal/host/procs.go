package host

import (
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

// The processes on the device for the dashboard: CPU (share of one core
// since the previous look), memory and threads, and who they belong to:
// rcplug (the host and each plugin's process group) or RØDE's own programs.
// It reads /proc only when asked, a few milliseconds for the Duo's
// processes.

// ProcInfo is one process.
type ProcInfo struct {
	PID     int     `json:"pid"`
	Name    string  `json:"name"`
	Command string  `json:"command"`
	CPU     float64 `json:"cpu"`     // percent of one core since the previous sample
	CPUTime float64 `json:"cpuTime"` // seconds since the process started
	RSSKB   int64   `json:"rssKB"`
	Threads int     `json:"threads"`
	Owner   string  `json:"owner,omitempty"`  // "rcplug" or "rode"
	Plugin  string  `json:"plugin,omitempty"` // the plugin whose process group it is in
}

// rodePrograms lists the stock firmware's own long-running programs.
var rodePrograms = map[string]bool{"rc_audio_mixer": true, "rc_gui_juce": true, "jackd": true, "callme-t": true,
	"audio-start.sh": true, "display-start.sh": true}

type procSampler struct {
	mu   sync.Mutex
	at   time.Time
	last map[int]float64 // pid → CPU ticks at the previous sample
}

const userHZ = 100.0 // clock ticks per second in /proc on Linux

// sample reads /proc; groups maps a process group to its plugin.
func (p *procSampler) sample(procRoot string, groups map[int]string, self int) []ProcInfo {
	ents, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	page := int64(os.Getpagesize())
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	elapsed := now.Sub(p.at).Seconds()
	prev := p.last
	cur := make(map[int]float64, len(ents))
	var out []ProcInfo
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
		ut, _ := strconv.ParseFloat(f[11], 64)
		st, _ := strconv.ParseFloat(f[12], 64)
		pgrp, _ := strconv.Atoi(f[2])
		threads, _ := strconv.Atoi(f[17])
		rss, _ := strconv.ParseInt(f[21], 10, 64)
		ticks := ut + st
		cur[pid] = ticks
		info := ProcInfo{PID: pid, Name: s[open+1 : end], CPUTime: ticks / userHZ, RSSKB: rss * page / 1024, Threads: threads}
		if before, ok := prev[pid]; ok && elapsed > 0 && ticks >= before {
			info.CPU = (ticks - before) / userHZ / elapsed * 100
		}
		if raw, err := os.ReadFile(fmt.Sprintf("%s/%d/cmdline", procRoot, pid)); err == nil && len(raw) > 0 {
			info.Command = strings.Join(maskArgs(strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")), " ")
			if len(info.Command) > 160 {
				info.Command = info.Command[:160] + "…"
			}
		}
		switch {
		case pid == self:
			info.Owner, info.Plugin = "rcplug", "rcplugd"
		case groups[pgrp] != "":
			info.Owner, info.Plugin = "rcplug", groups[pgrp]
		case rodePrograms[info.Name]:
			info.Owner = "rode"
		}
		out = append(out, info)
	}
	p.at, p.last = now, cur
	return out
}

func (s *Server) handleProcesses(w http.ResponseWriter, r *http.Request) {
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
	list := s.procs.sample("/proc", groups, os.Getpid())
	all := r.URL.Query().Get("all") == "1"
	var total int64
	kept := list[:0]
	for _, p := range list {
		total += p.RSSKB
		if all || p.Command != "" { // kernel threads only on request
			kept = append(kept, p)
		}
	}
	if r.URL.Query().Get("sort") == "mem" {
		sort.Slice(kept, func(i, j int) bool { return kept[i].RSSKB > kept[j].RSSKB })
	} else {
		sort.Slice(kept, func(i, j int) bool {
			if kept[i].CPU != kept[j].CPU {
				return kept[i].CPU > kept[j].CPU
			}
			return kept[i].RSSKB > kept[j].RSSKB
		})
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 || limit > 500 {
		limit = 20
	}
	count := len(kept)
	if len(kept) > limit {
		kept = kept[:limit]
	}
	writeJSON(w, map[string]interface{}{"cores": runtime.NumCPU(), "count": count, "rssKB": total, "processes": kept})
}
