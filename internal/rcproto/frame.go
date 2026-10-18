package rcproto

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Magic is the 4-byte header (little-endian on the wire) that precedes every
// message on rc_audio_mixer's TCP control port.
const Magic uint32 = 0xF2B49E2C

// MaxFrame caps a single message body. A full sync of a Duo is ~156 KB; this
// leaves ample room while stopping a corrupt length from allocating gigabytes.
const MaxFrame = 8 << 20

// DefaultPort is the TCP port rc_audio_mixer listens on.
const DefaultPort = 2345

// SessionOpen is the body a USB HID client sends to start a session (and get
// a full sync). TCP clients normally receive the full sync without it.
var SessionOpen = []byte{0xAD, 0x10, 0xA7, 0xB0}

// AppendFrame wraps payload in the TCP transport frame: magic, length, body.
func AppendFrame(out, payload []byte) []byte {
	out = binary.LittleEndian.AppendUint32(out, Magic)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(payload)))
	return append(out, payload...)
}

// Frame returns payload wrapped in a fresh transport frame.
func Frame(payload []byte) []byte { return AppendFrame(make([]byte, 0, 8+len(payload)), payload) }

// FrameReader splits a TCP byte stream into message bodies. On a bad magic it
// slides forward one byte at a time until it finds the next header, the same
// recovery the reference implementation uses; Resyncs counts how often.
type FrameReader struct {
	r       *bufio.Reader
	Resyncs int
}

// NewFrameReader wraps r with a buffered frame splitter.
func NewFrameReader(r io.Reader) *FrameReader {
	return &FrameReader{r: bufio.NewReaderSize(r, 64<<10)}
}

// ErrFrameTooLarge is returned for a length prefix above MaxFrame.
var ErrFrameTooLarge = errors.New("rcproto: frame too large")

// Next returns the next message body. The returned slice is owned by the caller.
func (f *FrameReader) Next() ([]byte, error) {
	for {
		hdr, err := f.r.Peek(8)
		if err != nil {
			return nil, err
		}
		if binary.LittleEndian.Uint32(hdr) != Magic {
			f.Resyncs++
			if _, err := f.r.Discard(1); err != nil {
				return nil, err
			}
			continue
		}
		n := binary.LittleEndian.Uint32(hdr[4:])
		if n > MaxFrame {
			// Treat as desync: skip this "magic" and hunt for the next one.
			f.Resyncs++
			if _, err := f.r.Discard(1); err != nil {
				return nil, err
			}
			continue
		}
		if _, err := f.r.Discard(8); err != nil {
			return nil, err
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(f.r, body); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		return body, nil
	}
}

// ParseFrame splits one complete frame from the start of b, returning the body
// and the number of bytes consumed.
func ParseFrame(b []byte) (body []byte, n int, err error) {
	if len(b) < 8 {
		return nil, 0, ErrShort
	}
	if binary.LittleEndian.Uint32(b) != Magic {
		return nil, 0, fmt.Errorf("rcproto: bad magic %08x", binary.LittleEndian.Uint32(b))
	}
	ln := int(binary.LittleEndian.Uint32(b[4:]))
	if ln > MaxFrame {
		return nil, 0, ErrFrameTooLarge
	}
	if len(b) < 8+ln {
		return nil, 0, ErrShort
	}
	return b[8 : 8+ln], 8 + ln, nil
}
