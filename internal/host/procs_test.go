package host

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestProcessSample(t *testing.T) {
	root := t.TempDir()
	write := func(pid, stat, cmdline string) {
		d := filepath.Join(root, pid)
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "stat"), []byte(stat), 0o644)
		os.WriteFile(filepath.Join(d, "cmdline"), []byte(cmdline), 0o644)
	}
	// pid (comm) state ppid pgrp session tty tpgid flags minflt cminflt majflt cmajflt utime stime cutime cstime prio nice threads itrealvalue starttime vsize rss
	stat := func(pid, comm, pgrp string, ticks, rss int) string {
		return pid + " (" + comm + ") S 1 " + pgrp + " 1 0 -1 0 0 0 0 0 " + strconv.Itoa(ticks) + " 0 0 0 20 0 4 0 100 1000 " + strconv.Itoa(rss) + " 0"
	}
	write("10", stat("10", "rc_audio_mixer", "10", 1000, 2000), "rc_audio_mixer\x00")
	write("20", stat("20", "console", "20", 50, 500), "./console\x00--addr\x00127.0.0.1:9100\x00--pin\x004711\x00")
	write("30", stat("30", "kworker/0:1", "0", 5, 0), "")
	var p procSampler
	first := p.sample(root, map[int]string{20: "console"}, 99)
	if len(first) != 3 {
		t.Fatalf("got %d processes", len(first))
	}
	for _, x := range first {
		if x.CPU != 0 {
			t.Errorf("CPU without a previous sample: %+v", x)
		}
	}
	p.mu.Lock()
	p.at = p.at.Add(-time.Second) // one second later…
	p.mu.Unlock()
	write("10", stat("10", "rc_audio_mixer", "10", 1050, 2000), "rc_audio_mixer\x00") // …50 ticks = half a core
	second := p.sample(root, map[int]string{20: "console"}, 99)
	byPID := map[int]ProcInfo{}
	for _, x := range second {
		byPID[x.PID] = x
	}
	if m := byPID[10]; m.Owner != "rode" || m.CPU < 45 || m.CPU > 55 || m.RSSKB != int64(2000*os.Getpagesize()/1024) || m.Threads != 4 {
		t.Errorf("mixer %+v", m)
	}
	if c := byPID[20]; c.Owner != "rcplug" || c.Plugin != "console" || c.Command != "./console --addr 127.0.0.1:9100 --pin ***" {
		t.Errorf("console %+v", c)
	}
}

func TestProcessesAPI(t *testing.T) {
	m := NewManager(t.TempDir(), 19800)
	srv := NewServer(m, nil, "test")
	h := srv.Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/api/processes?sort=mem&limit=5", nil))
	var doc struct {
		Cores     int
		Count     int
		Processes []ProcInfo
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || doc.Cores < 1 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(doc.Processes) > 5 {
		t.Fatalf("limit ignored: %d", len(doc.Processes))
	}
	self := false
	full := httptest.NewRecorder()
	h.ServeHTTP(full, httptest.NewRequest("GET", "http://127.0.0.1/api/processes?limit=500", nil))
	json.Unmarshal(full.Body.Bytes(), &doc)
	for _, p := range doc.Processes {
		if p.PID == os.Getpid() && p.Owner == "rcplug" {
			self = true
		}
	}
	if !self && len(doc.Processes) > 0 {
		t.Error("the host itself is not marked as rcplug")
	}
}
