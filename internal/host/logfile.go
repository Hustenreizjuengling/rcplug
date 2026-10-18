package host

import (
	"os"
	"path/filepath"
	"sync"
)

// LogFileMax is the size at which a log file is rotated. One older file
// (<name>.1) is kept, so a log takes at most twice this on the device's flash.
const LogFileMax = 512 << 10

// RotatingFile is an append-only log file that keeps its size bounded: when a
// write would exceed max bytes, the file is renamed to <path>.1 (replacing an
// older one) and a new file is started. Errors are swallowed after being
// reported once through Err, because logging must never stop a plugin.
type RotatingFile struct {
	path string
	max  int64

	mu   sync.Mutex
	f    *os.File
	size int64
	err  error
}

// NewRotatingFile returns a log file at path ("" = discard everything).
func NewRotatingFile(path string, max int64) *RotatingFile {
	return &RotatingFile{path: path, max: max}
}

// Path is the current log file.
func (r *RotatingFile) Path() string { return r.path }

func (r *RotatingFile) Write(p []byte) (int, error) {
	if r == nil || r.path == "" {
		return len(p), nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f != nil && r.size+int64(len(p)) > r.max && r.size > 0 {
		_ = r.f.Close()
		r.f = nil
		_ = os.Rename(r.path, r.path+".1")
	}
	if r.f == nil {
		if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
			r.fail(err)
			return len(p), nil
		}
		f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			r.fail(err)
			return len(p), nil
		}
		st, _ := f.Stat()
		r.f, r.size = f, 0
		if st != nil {
			r.size = st.Size()
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	if err != nil {
		r.fail(err)
	}
	return len(p), nil
}

func (r *RotatingFile) fail(err error) {
	if r.err == nil {
		r.err = err
	}
}

// Err returns the first error, if any (for diagnostics).
func (r *RotatingFile) Err() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// Close closes the current file.
func (r *RotatingFile) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}
