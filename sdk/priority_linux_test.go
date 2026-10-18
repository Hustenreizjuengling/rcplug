package sdk

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLowPriorityReachesThreadsAndChildren(t *testing.T) {
	if err := LowPriority(); err != nil {
		t.Skipf("setpriority: %v", err)
	}
	tasks, _ := os.ReadDir("/proc/self/task")
	for _, task := range tasks {
		raw, _ := os.ReadFile(filepath.Join("/proc/self/task", task.Name(), "stat"))
		f := strings.Fields(string(raw[strings.LastIndexByte(string(raw), ')')+2:]))
		if len(f) > 16 && f[16] != "19" {
			t.Errorf("thread %s nice %s", task.Name(), f[16])
		}
	}
	out, err := exec.Command("sh", "-c", "cut -d' ' -f19 /proc/$$/stat").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "19" {
		t.Errorf("child nice %q", out)
	}
}
