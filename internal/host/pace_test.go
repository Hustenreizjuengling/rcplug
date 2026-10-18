package host

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPacedReadAndWrite(t *testing.T) {
	data := bytes.Repeat([]byte("rcplug"), 50000) // 300 KB
	start := time.Now()
	r := &pacedReader{r: bytes.NewReader(data), p: newPacer(1 << 20)}
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	w := &pacedFile{f: f, p: newPacer(0), syncEvery: 64 << 10}
	n, err := io.Copy(w, r)
	f.Close()
	if err != nil || n != int64(len(data)) {
		t.Fatalf("copied %d of %d: %v", n, len(data), err)
	}
	// 300 KB at 1 MB/s: about 0.29 s.
	if d := time.Since(start); d < 250*time.Millisecond || d > 3*time.Second {
		t.Errorf("took %v", d)
	}
	got, _ := os.ReadFile(f.Name())
	if !bytes.Equal(got, data) {
		t.Error("written data differs")
	}
	buf := make([]byte, 1<<20)
	if n, _ := (&pacedReader{r: bytes.NewReader(data), p: newPacer(1 << 30)}).Read(buf); n > paceChunk {
		t.Errorf("read %d bytes at once, want at most %d", n, paceChunk)
	}
	start = time.Now()
	if _, err := io.Copy(io.Discard, &pacedReader{r: bytes.NewReader(data), p: newPacer(0)}); err != nil || time.Since(start) > 200*time.Millisecond {
		t.Errorf("unlimited copy: %v in %v", err, time.Since(start))
	}
}
