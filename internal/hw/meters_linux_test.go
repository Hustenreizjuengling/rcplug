//go:build linux && (amd64 || arm64)

package hw

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// testSegment creates a System V segment under a random key, fills it with
// data and removes it when the test ends.
func testSegment(t *testing.T, key, size int, data []byte) int {
	t.Helper()
	id, _, e := syscall.Syscall(syscall.SYS_SHMGET, uintptr(key), uintptr(size), 0o1000|0o2000|0o600) // IPC_CREAT|IPC_EXCL
	if e != 0 {
		t.Skipf("System V shared memory not available: %v", e)
	}
	t.Cleanup(func() { syscall.Syscall(syscall.SYS_SHMCTL, id, 0, 0) }) // IPC_RMID
	writeSegment(t, int(id), data)
	return int(id)
}

func writeSegment(t *testing.T, id int, data []byte) {
	t.Helper()
	addr, _, e := syscall.Syscall(syscall.SYS_SHMAT, uintptr(id), 0, 0)
	if e != 0 {
		t.Fatal(e)
	}
	p := *(*unsafe.Pointer)(unsafe.Pointer(&addr))
	copy(unsafe.Slice((*byte)(p), len(data)), data)
	syscall.Syscall(syscall.SYS_SHMDT, addr, 0, 0)
}

func TestMetersFromASharedSegment(t *testing.T) {
	key := 0x52430000 | rand.IntN(0xffff)
	ms := make([]Meter, MeterRecords)
	ms[9] = Meter{Stereo: true, PeakL: 0.75, PeakR: 0.5, LevelL: 0.25, LevelR: 0.125}
	testSegment(t, key, MeterSegmentSize, segment([]int32{64}, ms))

	m := &Meters{Key: key}
	defer m.Close()
	var r MeterReading
	if err := m.Read(&r); err != nil {
		t.Fatal(err)
	}
	if r.Faders[0] != 64 || r.Meters[9] != ms[9] {
		t.Fatalf("read %v %+v", r.Faders[:2], r.Meters[9])
	}

	// The mixer writes, the reader sees it on the next read.
	ms[9].LevelL = 0.5
	writeSegment(t, m.id, segment([]int32{65}, ms))
	if err := m.Read(&r); err != nil || r.Faders[0] != 65 || r.Meters[9].LevelL != 0.5 {
		t.Fatalf("second read %v %v %+v", err, r.Faders[0], r.Meters[9])
	}

	// A restarted mixer makes a new segment under the same key.
	old := m.id
	syscall.Syscall(syscall.SYS_SHMCTL, uintptr(old), 0, 0)
	testSegment(t, key, MeterSegmentSize, segment([]int32{3}, ms))
	m.mu.Lock()
	m.checked = time.Time{}
	m.mu.Unlock()
	if err := m.Read(&r); err != nil || r.Faders[0] != 3 || m.id == old {
		t.Fatalf("after a restart: %v fader %d id %d (was %d)", err, r.Faders[0], m.id, old)
	}
}

func TestMetersRefuseUnknownSegments(t *testing.T) {
	key := 0x52440000 | rand.IntN(0xffff)
	m := &Meters{Key: key}
	var r MeterReading
	if err := m.Read(&r); err != ErrNoMeters {
		t.Errorf("missing segment: %v", err)
	}
	testSegment(t, key, 1024, make([]byte, 1024))
	m.checked = time.Time{}
	if err := m.Read(&r); err != ErrNoMeters || m.seg != nil {
		t.Errorf("a segment of another size must not be read: %v", err)
	}
}

func TestFindSegment(t *testing.T) {
	table := filepath.Join(t.TempDir(), "shm")
	os.WriteFile(table, []byte(`       key      shmid perms                  size  cpid  lpid nattch   uid   gid  cuid  cgid      atime      dtime      ctime                   rss                  swap
         0      65562  1600               1048576  3005  2992      2  1000  1000  1000  1000 1792285229          0 1792285229                     0                131072
1293366353          7   644                   712   312   401      2     0     0     0     0 1792285229          0 1792285229                  4096                     0
`), 0o600)
	if id, size, ok := findSegment(table, MeterKey); !ok || id != 7 || size != 712 {
		t.Errorf("found %d %d %v", id, size, ok)
	}
	if _, _, ok := findSegment(table, 42); ok {
		t.Error("found a key that is not there")
	}
}
