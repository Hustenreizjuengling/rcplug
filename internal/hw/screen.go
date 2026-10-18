package hw

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"runtime/debug"
	"strconv"
	"sync"
	"time"
)

// Screen serves throttled screenshots of the desk's display. Viewers share
// captures, so the device never does more than MaxFPS captures per second no
// matter how many browsers watch, and at most MaxStreams streams run at once.
type Screen struct {
	FB         string // framebuffer device, e.g. /dev/fb0 ("" = skip)
	DRM        string // DRM card, e.g. /dev/dri/card0 ("" = skip)
	Mode       string // auto | fb | drm
	MaxFPS     float64
	MaxStreams int // concurrent MJPEG streams (default 2)

	grab func() (image.Image, string, string, error) // test hook replacing capture

	mu              sync.Mutex
	fbBufs, drmBufs frameBufs   // reused capture buffers, one set per source
	rotBuf          *image.RGBA // reused for the turned picture
	idle            *time.Timer // frees the buffers when nobody watches
	fixed           bool        // rotate is set; otherwise a portrait buffer is turned to landscape
	rotate          int         // degrees clockwise when fixed
	turned          int         // the turn applied to the cached frame
	at              time.Time
	img             image.Image
	jpg             []byte
	err             error
	source          string // "fb" | "drm" of the last capture
	detail          string
	streams         int
}

// ScreenStatus describes the mirror for the UI.
type ScreenStatus struct {
	Available bool    `json:"available"`
	Source    string  `json:"source,omitempty"`
	Blank     bool    `json:"blank"`
	Width     int     `json:"width,omitempty"`
	Height    int     `json:"height,omitempty"`
	Detail    string  `json:"detail,omitempty"`
	Error     string  `json:"error,omitempty"`
	FPS       float64 `json:"fps"`
	// Rotation is how far the picture is turned (degrees clockwise), Rotate
	// the setting ("auto" or the same number).
	Rotation int    `json:"rotation"`
	Rotate   string `json:"rotate"`
}

// RotateAuto turns a portrait buffer by 270° (90° counter-clockwise) and
// leaves a landscape one alone: the Duo's GUI draws its landscape screen
// into a 720×1280 buffer whose top is on the right.
const RotateAuto = -1

// SetRotation sets how frames are turned: RotateAuto, 0, 90, 180 or 270
// degrees clockwise. Other values count as RotateAuto.
func (s *Screen) SetRotation(deg int) {
	fixed := deg == 0 || deg == 90 || deg == 180 || deg == 270
	if !fixed {
		deg = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fixed != fixed || s.rotate != deg {
		s.fixed, s.rotate, s.at = fixed, deg, time.Time{} // the next request captures anew
	}
}

// rotation returns the clockwise turn for a w×h buffer.
func (s *Screen) rotation(w, h int) int {
	switch {
	case s.fixed:
		return s.rotate
	case h > w:
		return 270
	}
	return 0
}

// ErrBusy is returned when the stream limit is reached.
var ErrBusy = errors.New("too many screen streams")

func (s *Screen) interval() time.Duration {
	fps := s.MaxFPS
	if fps <= 0 {
		fps = 2
	}
	return time.Duration(float64(time.Second) / fps)
}

// frameLocked returns the current frame, capturing only if the cached one is
// older than one frame interval.
func (s *Screen) frameLocked() (image.Image, error) {
	if !s.at.IsZero() && time.Since(s.at) < s.interval() {
		return s.img, s.err
	}
	grab := s.capture
	if s.grab != nil {
		grab = s.grab
	}
	img, src, detail, err := grab()
	if err == nil {
		b := img.Bounds()
		s.turned = s.rotation(b.Dx(), b.Dy())
		img = rotateInto(img, s.turned, &s.rotBuf)
	}
	s.at, s.img, s.err, s.jpg = time.Now(), img, err, nil
	s.armIdleLocked()
	if err == nil {
		s.source, s.detail = src, detail
	}
	return img, err
}

// screenIdle: without a request for this long the mirror frees its frames
// and buffers and returns the memory to the system.
var screenIdle = 30 * time.Second

func (s *Screen) armIdleLocked() {
	if s.idle != nil {
		s.idle.Reset(screenIdle)
		return
	}
	s.idle = time.AfterFunc(screenIdle, func() {
		s.mu.Lock()
		s.img, s.jpg, s.at = nil, nil, time.Time{}
		s.fbBufs, s.drmBufs, s.rotBuf = frameBufs{}, frameBufs{}, nil
		s.mu.Unlock()
		debug.FreeOSMemory()
	})
}

// capture grabs one frame from the configured source(s).
func (s *Screen) capture() (image.Image, string, string, error) {
	tryFB := func() (image.Image, string, error) {
		img, info, err := captureFB(s.FB, &s.fbBufs)
		if err != nil {
			return nil, "", err
		}
		return img, fmt.Sprintf("%s · %d bit", s.FB, info.BitsPerPixel), nil
	}
	tryDRM := func() (image.Image, string, error) {
		img, info, err := captureDRM(s.DRM, &s.drmBufs)
		if err != nil {
			return nil, "", err
		}
		return img, fmt.Sprintf("%s · %s · %s", s.DRM, info.Format, info.Via), nil
	}
	if s.FB == "" {
		tryFB = nil
	}
	if s.DRM == "" {
		tryDRM = nil
	}
	return pickSource(s.Mode, tryFB, tryDRM)
}

type tryFunc func() (image.Image, string, error)

// pickSource captures from the source the mode names, or in auto mode from
// DRM first: the Duo's GUI draws into a DRM/KMS buffer (rc_gui_juce, firmware
// 1.7.x), while /dev/fb0 is the kernel's fbdev emulation, which keeps the
// boot splash and so is not black. The framebuffer is the fallback when DRM
// cannot be read or shows nothing. A nil try function means not configured.
func pickSource(mode string, tryFB, tryDRM tryFunc) (image.Image, string, string, error) {
	missing := func() (image.Image, string, error) { return nil, "", errors.New("not configured") }
	if tryFB == nil {
		tryFB = missing
	}
	if tryDRM == nil {
		tryDRM = missing
	}
	switch mode {
	case "fb":
		img, d, err := tryFB()
		return img, "fb", d, err
	case "drm":
		img, d, err := tryDRM()
		return img, "drm", d, err
	}
	drmImg, drmDetail, drmErr := tryDRM()
	if drmErr == nil && !isBlank(drmImg) {
		return drmImg, "drm", drmDetail, nil
	}
	fbImg, fbDetail, fbErr := tryFB()
	if fbErr == nil && (!isBlank(fbImg) || drmErr != nil) {
		return fbImg, "fb", fbDetail, nil
	}
	if drmErr == nil {
		return drmImg, "drm", drmDetail, nil // both blank: the GUI's buffer is the live one
	}
	return nil, "", "", fmt.Errorf("DRM: %v; framebuffer: %v", drmErr, fbErr)
}

// JPEG returns a recent frame as JPEG.
func (s *Screen) JPEG() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	img, err := s.frameLocked()
	if err != nil {
		return nil, err
	}
	if s.jpg == nil {
		var b bytes.Buffer
		if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 80}); err != nil {
			return nil, err
		}
		s.jpg = b.Bytes()
	}
	return s.jpg, nil
}

// PNG returns a recent frame losslessly (for the "save screenshot" button).
func (s *Screen) PNG() ([]byte, error) {
	s.mu.Lock() // held while encoding: the next capture reuses the frame's buffers
	defer s.mu.Unlock()
	img, err := s.frameLocked()
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Status reports what the mirror sees (rate-limited like the frames).
func (s *Screen) Status() ScreenStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := ScreenStatus{FPS: s.MaxFPS}
	img, err := s.frameLocked()
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Available, st.Source, st.Detail, st.Blank = true, s.source, s.detail, isBlank(img)
	st.Width, st.Height = img.Bounds().Dx(), img.Bounds().Dy()
	st.Rotation, st.Rotate = s.turned, "auto"
	if s.fixed {
		st.Rotate = strconv.Itoa(s.rotate)
	}
	return st
}

// StillScreen is a Screen that always shows img (tests, demos).
func StillScreen(img image.Image) *Screen {
	return &Screen{MaxFPS: 2, grab: func() (image.Image, string, string, error) { return img, "still", "a still picture", nil }}
}

// Orientation is how the mirrored picture lies: the turn applied to the
// buffer (degrees clockwise) and whether the buffer itself is portrait. It
// captures a frame if there is none yet.
func (s *Screen) Orientation() (turn int, portrait bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	img, err := s.frameLocked()
	if err != nil {
		return 0, false, err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if s.turned == 90 || s.turned == 270 {
		w, h = h, w
	}
	return s.turned, h > w, nil
}

// Acquire reserves one stream slot; call the returned release when done.
func (s *Screen) Acquire() (release func(), err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	max := s.MaxStreams
	if max <= 0 {
		max = 2
	}
	if s.streams >= max {
		return nil, ErrBusy
	}
	s.streams++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.streams--
			s.mu.Unlock()
		})
	}, nil
}

// Interval is the time between frames of a stream.
func (s *Screen) Interval() time.Duration { return s.interval() }

// rotate turns img clockwise by deg (0, 90, 180 or 270).
func rotate(img image.Image, deg int) image.Image { return rotateInto(img, deg, nil) }

// rotateInto turns img into *buf (reused when its size fits; a new image with
// buf nil).
func rotateInto(img image.Image, deg int, buf **image.RGBA) image.Image {
	if deg != 90 && deg != 180 && deg != 270 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := h, w
	if deg == 180 {
		dw, dh = w, h
	}
	dst := reuseRGBA(buf, dw, dh)
	src, fast := img.(*image.RGBA)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch deg {
			case 90:
				dx, dy = h-1-y, x
			case 180:
				dx, dy = w-1-x, h-1-y
			default: // 270: the right edge becomes the top
				dx, dy = y, w-1-x
			}
			if fast {
				si, di := src.PixOffset(b.Min.X+x, b.Min.Y+y), dst.PixOffset(dx, dy)
				copy(dst.Pix[di:di+4], src.Pix[si:si+4])
			} else {
				dst.Set(dx, dy, img.At(b.Min.X+x, b.Min.Y+y))
			}
		}
	}
	return dst
}
