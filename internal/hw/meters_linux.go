//go:build linux && (amd64 || arm64)

package hw

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Meters reads the mixer's meter segment. It attaches on the first Read and
// looks the segment up again every few seconds, so it follows a restarted
// mixer (a new segment) and lets go of a removed one.
type Meters struct {
	Key int // System V key; 0 = MeterKey

	mu      sync.Mutex
	id      int // attached segment id, -1 = none
	addr    uintptr
	seg     []byte // the read-only attachment; never handed out
	checked time.Time
	buf     [MeterSegmentSize]byte
}

// meterRecheck is how often Read looks the segment up again.
var meterRecheck = 2 * time.Second

const (
	shmRdonly = 0o10000 // SHM_RDONLY
	sysvShm   = "/proc/sysvipc/shm"
)

// Read copies the segment and decodes it into r.
func (m *Meters) Read(r *MeterReading) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seg == nil || time.Since(m.checked) >= meterRecheck {
		if err := m.attachLocked(); err != nil {
			return err
		}
	}
	copy(m.buf[:], m.seg)
	return parseMeters(m.buf[:], r)
}

// Close detaches the segment.
func (m *Meters) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.detachLocked()
	return nil
}

func (m *Meters) attachLocked() error {
	key := m.Key
	if key == 0 {
		key = MeterKey
	}
	m.checked = time.Now()
	id, size, ok := findSegment(sysvShm, key)
	if !ok || size != MeterSegmentSize {
		m.detachLocked()
		return ErrNoMeters
	}
	if m.seg != nil && id == m.id {
		return nil
	}
	m.detachLocked()
	addr, _, e := syscall.Syscall(syscall.SYS_SHMAT, uintptr(id), 0, shmRdonly)
	if e != 0 {
		return e
	}
	// The attachment lies outside the Go heap and stays mapped until
	// detachLocked, which runs under the same lock as every access.
	p := *(*unsafe.Pointer)(unsafe.Pointer(&addr))
	m.id, m.addr, m.seg = id, addr, unsafe.Slice((*byte)(p), MeterSegmentSize)
	return nil
}

func (m *Meters) detachLocked() {
	if m.seg != nil {
		_, _, _ = syscall.Syscall(syscall.SYS_SHMDT, m.addr, 0, 0)
	}
	m.id, m.addr, m.seg = -1, 0, nil
}

// findSegment looks a key up in /proc/sysvipc/shm ("key shmid perms size
// ..."), which needs no permission on the segment and no
// architecture-specific structures.
func findSegment(table string, key int) (id, size int, ok bool) {
	f, err := os.Open(table)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 {
			continue
		}
		k, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || int32(k) != int32(key) {
			continue
		}
		id, err1 := strconv.Atoi(fields[1])
		size, err2 := strconv.Atoi(fields[3])
		if err1 != nil || err2 != nil {
			continue
		}
		return id, size, true
	}
	return 0, 0, false
}
