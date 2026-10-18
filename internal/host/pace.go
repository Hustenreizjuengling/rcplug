package host

import (
	"io"
	"os"
	"time"
)

// Updates go easy on the device. On a RØDECaster the network, the storage
// and the USB audio interface share the CPU's interrupt handling, all at the
// same real-time priority; a download at full speed, or many megabytes
// written at once, delayed the USB audio long enough for the computer to lose
// the Duo as a sound device. So an update reads and writes at a steady,
// modest rate and flushes what it writes in small pieces.

// pacer spreads n bytes over time at rate bytes per second (0 = no limit).
type pacer struct {
	rate  int64
	start time.Time
	done  int64
}

func newPacer(rate int64) *pacer { return &pacer{rate: rate, start: time.Now()} }

// wait sleeps until n more bytes are within the rate.
func (p *pacer) wait(n int) {
	if p.rate <= 0 || n <= 0 {
		return
	}
	p.done += int64(n)
	due := p.start.Add(time.Duration(float64(p.done) / float64(p.rate) * float64(time.Second)))
	if d := time.Until(due); d > 0 {
		time.Sleep(d)
	}
}

// pacedReader reads in small pieces at the pacer's rate.
type pacedReader struct {
	r io.Reader
	p *pacer
}

const paceChunk = 16 << 10

func (r *pacedReader) Read(b []byte) (int, error) {
	if r.p.rate > 0 && len(b) > paceChunk {
		b = b[:paceChunk]
	}
	n, err := r.r.Read(b)
	r.p.wait(n)
	return n, err
}

// pacedFile writes to f at the pacer's rate and flushes it to the storage
// every syncEvery bytes, so the kernel never has much to write at once.
type pacedFile struct {
	f         *os.File
	p         *pacer
	syncEvery int64
	unsynced  int64
}

func (w *pacedFile) Write(b []byte) (int, error) {
	written := 0
	for len(b) > 0 {
		chunk := b
		if w.p.rate > 0 && len(chunk) > paceChunk {
			chunk = chunk[:paceChunk]
		}
		n, err := w.f.Write(chunk)
		written += n
		w.p.wait(n)
		w.unsynced += int64(n)
		if w.syncEvery > 0 && w.unsynced >= w.syncEvery {
			_ = w.f.Sync()
			w.unsynced = 0
		}
		if err != nil {
			return written, err
		}
		b = b[n:]
	}
	return written, nil
}
