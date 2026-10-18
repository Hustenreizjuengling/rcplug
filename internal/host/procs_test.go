package host

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestProcessSample(t *testing.T) {
	root := t.TempDir()
	write := func(pid, stat, cmdline, status string) {
		d := filepath.Join(root, pid)
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "stat"), []byte(stat), 0o644)
		os.WriteFile(filepath.Join(d, "cmdline"), []byte(cmdline), 0o644)
		os.WriteFile(filepath.Join(d, "status"), []byte(status), 0o644)
	}
	// pid (comm) state ppid pgrp session tty tpgid flags minflt cminflt majflt cmajflt utime stime cutime cstime prio nice threads itrealvalue starttime vsize rss
	stat := func(pid, comm, pgrp string, flags, ticks, rss int) string {
		return pid + " (" + comm + ") S 1 " + pgrp + " 1 0 -1 " + strconv.Itoa(flags) + " 0 0 0 0 " + strconv.Itoa(ticks) + " 0 0 0 20 0 4 0 100 1000 " + strconv.Itoa(rss) + " 0"
	}
	mem := func(anon, file, shmem int) string {
		return "Name:\tx\nVmRSS:\t1 kB\nRssAnon:\t" + strconv.Itoa(anon) + " kB\nRssFile:\t" + strconv.Itoa(file) + " kB\nRssShmem:\t" + strconv.Itoa(shmem) + " kB\nThreads:\t4\n"
	}
	cpuStat := func(user, idle, irq int) string {
		return "cpu  " + strconv.Itoa(user) + " 0 100 " + strconv.Itoa(idle) + " 10 " + strconv.Itoa(irq) + " 0 0 0 0\ncpu0 1 2 3 4 5 6 7 8 0 0\ncpu1 1 2 3 4 5 6 7 8 0 0\nintr 1\n"
	}
	os.WriteFile(filepath.Join(root, "meminfo"), []byte("MemTotal: 480000 kB\nMemFree: 100000 kB\nMemAvailable: 300000 kB\nBuffers: 5000 kB\nCached: 200000 kB\n"+
		"Shmem: 30000 kB\nAnonPages: 120000 kB\nSReclaimable: 8000 kB\nSUnreclaim: 12000 kB\nKernelStack: 3000 kB\nPageTables: 2000 kB\n"), 0o644)
	os.WriteFile(filepath.Join(root, "stat"), []byte(cpuStat(1000, 5000, 50)), 0o644)
	write("10", stat("10", "rc_audio_mixer", "10", 0, 1000, 2000), "rc_audio_mixer\x00", mem(6000, 2000, 1000))
	write("20", stat("20", "console", "20", 0, 50, 500), "./console\x00--addr\x00127.0.0.1:9100\x00--pin\x004711\x00", mem(1500, 500, 0))
	write("30", stat("30", "irq/51-dwc2", "0", pfKthread, 5, 0), "", "Name:\tirq/51-dwc2\n")
	var p procSampler
	first := p.sample(root, map[int]string{20: "console"}, 99)
	if len(first.Procs) != 3 || first.CPUValid || first.Cores != 2 {
		t.Fatalf("first look: %d processes, cpu valid %v, %d cores", len(first.Procs), first.CPUValid, first.Cores)
	}
	if m := first.Mem; m.UsedKB != 180000 || m.ProgramsKB != 120000 || m.SharedKB != 30000 || m.KernelKB != 17000 || m.CacheKB != 183000 || m.FreeKB != 100000 {
		t.Errorf("memory %+v", m)
	}
	for _, x := range first.Procs {
		if x.CPU != 0 {
			t.Errorf("CPU without a previous sample: %+v", x)
		}
	}
	p.mu.Lock()
	p.at = p.at.Add(-time.Second) // one second later…
	p.mu.Unlock()
	write("10", stat("10", "rc_audio_mixer", "10", 0, 1050, 2000), "rc_audio_mixer\x00", mem(6000, 2000, 1000)) // …50 ticks = half a core
	write("30", stat("30", "irq/51-dwc2", "0", pfKthread, 15, 0), "", "Name:\tirq/51-dwc2\n")
	os.WriteFile(filepath.Join(root, "stat"), []byte(cpuStat(1050, 5130, 60)), 0o644) // +50 user, +130 idle, +10 irq of 190
	os.WriteFile(filepath.Join(root, "20", "cmdline"), []byte("changed\x00"), 0o644)  // a command line is read once
	second := p.sample(root, map[int]string{20: "console"}, 99)
	byPID := map[int]ProcInfo{}
	for _, x := range second.Procs {
		byPID[x.PID] = x
	}
	if m := byPID[10]; m.Owner != "rode" || m.CPU < 45 || m.CPU > 55 || m.RSSKB != int64(2000*os.Getpagesize()/1024) || m.Threads != 4 ||
		m.OwnKB != 6000 || m.SharedKB != 3000 || m.Kernel {
		t.Errorf("mixer %+v", m)
	}
	if c := byPID[20]; c.Owner != "rcplug" || c.Plugin != "console" || c.Command != "./console --addr 127.0.0.1:9100 --pin ***" || c.OwnKB != 1500 {
		t.Errorf("console %+v", c)
	}
	if k := byPID[30]; !k.Kernel || k.Command != "" || k.CPU < 5 {
		t.Errorf("kernel thread %+v", k)
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 0.01 }
	if c := second.CPU; !second.CPUValid || !near(c.Total, 60.0/190*100) || !near(c.User, 50.0/190*100) || !near(c.IRQ, 10.0/190*100) {
		t.Errorf("cpu %+v", c)
	}
}

func TestProcessesAPI(t *testing.T) {
	m := NewManager(t.TempDir(), 19800)
	srv := NewServer(m, nil, "test")
	h := srv.Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/api/processes?sort=mem&limit=5", nil))
	var doc struct {
		Cores         int
		Count         int
		OwnKB         int64
		Memory        MemUsage
		KernelThreads struct{ Count int }
		Processes     []ProcInfo
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || doc.Cores < 1 || doc.Memory.TotalKB == 0 || doc.OwnKB == 0 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(doc.Processes) > 5 {
		t.Fatalf("limit ignored: %d", len(doc.Processes))
	}
	self := false
	time.Sleep(50 * time.Millisecond) // so that /proc/stat moves between the two looks
	full := httptest.NewRecorder()
	h.ServeHTTP(full, httptest.NewRequest("GET", "http://127.0.0.1/api/processes?limit=500", nil))
	json.Unmarshal(full.Body.Bytes(), &doc)
	for _, p := range doc.Processes {
		if p.PID == os.Getpid() && p.Owner == "rcplug" {
			self = true
		}
		if p.Kernel {
			t.Errorf("kernel thread listed without all=1: %+v", p)
		}
	}
	if !self && len(doc.Processes) > 0 {
		t.Error("the host itself is not marked as rcplug")
	}
	if doc.KernelThreads.Count == 0 {
		t.Error("no kernel threads counted")
	}
	if !json.Valid(full.Body.Bytes()) || !containsKey(full.Body.Bytes(), "cpu") {
		t.Error("the second look has no device CPU")
	}
}

func containsKey(raw []byte, key string) bool {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	_, ok := m[key]
	return ok
}

func TestResourceHistoryAndReport(t *testing.T) {
	snap := procSnapshot{At: time.Date(2026, 10, 18, 20, 0, 0, 0, time.UTC), Cores: 4, CPUValid: true,
		CPU: CPUUsage{Total: 30, User: 20, System: 6, IRQ: 3, IOWait: 1},
		Mem: MemUsage{TotalKB: 480 << 10, AvailableKB: 280 << 10, UsedKB: 200 << 10, ProgramsKB: 120 << 10, SharedKB: 30 << 10, KernelKB: 17 << 10, CacheKB: 180 << 10, FreeKB: 100 << 10},
		Procs: []ProcInfo{
			{PID: 10, Name: "rc_audio_mixer", Owner: "rode", CPU: 80, OwnKB: 60 << 10, SharedKB: 10 << 10, RSSKB: 70 << 10, Threads: 12},
			{PID: 20, Name: "console", Owner: "rcplug", Plugin: "console", CPU: 4, OwnKB: 20 << 10, Command: "./console"},
			{PID: 30, Name: "irq/51-dwc2", Kernel: true, CPU: 20},
		}}
	var r ResourceLog
	if h := r.History(); !strings.Contains(h, "nothing yet") {
		t.Fatalf("empty history: %s", h)
	}
	for i := 0; i < resourceKeep+5; i++ {
		r.note(snap, "0.50 0.40 0.30")
	}
	h := r.History()
	if n := strings.Count(h, "top CPU:"); n != resourceKeep {
		t.Errorf("%d entries kept", n)
	}
	for _, want := range []string{"CPU  30.0% (20.0/6.0/3.0/1.0)", "RAM 200/480 (120 30 17 180)", "rcplug 1.0% 20 MB", "RØDE 20.0% 60 MB",
		"top CPU: rc_audio_mixer 20.0%, irq/51-dwc2 5.0%, console 1.0%", "top RAM: rc_audio_mixer 60 MB"} {
		if !strings.Contains(h, want) {
			t.Errorf("history lacks %q:\n%s", want, h[:600])
		}
	}
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "plugins", "console"), 0o755)
	os.WriteFile(filepath.Join(root, "plugins", "console", "console"), make([]byte, 3<<20), 0o644)
	os.MkdirAll(filepath.Join(root, "logs"), 0o755)
	os.WriteFile(filepath.Join(root, "logs", "console.log"), make([]byte, 2048), 0o644)
	sum := resourceSummary(snap, "0.50 0.40 0.30", root, []string{"console"})
	for _, want := range []string{"CPU: 30.0 % of the device (4 cores)", "RAM: 200 MB of 480 MB used", "programs (own memory of the processes)    120 MB   (the processes below together: 80 MB)",
		"rcplug and its plugins   CPU   1.0 %  own RAM 20 MB", "kernel threads           CPU   5.0 %", "console              program   3.0 MB  data     0 KB  logs     2 KB"} {
		if !strings.Contains(sum, want) {
			t.Errorf("summary lacks %q:\n%s", want, sum)
		}
	}
	table := processTable(snap)
	if !strings.Contains(table, "[irq/51-dwc2]") || !strings.Contains(table, "console   ") || strings.Index(table, "rc_audio_mixer") > strings.Index(table, "console ") {
		t.Errorf("table:\n%s", table)
	}
}
