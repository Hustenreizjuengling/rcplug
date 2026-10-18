package hw

import (
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"strings"
	"testing"
	"time"
)

func TestToImageFormats(t *testing.T) {
	// 2x1 XRGB8888 with stride padding: red, then blue.
	info := &FBInfo{Width: 2, Height: 1, BitsPerPixel: 32, Stride: 12,
		R: [2]int{16, 8}, G: [2]int{8, 8}, B: [2]int{0, 8}}
	buf := make([]byte, 12)
	binary.LittleEndian.PutUint32(buf[0:], 0x00ff0000)
	binary.LittleEndian.PutUint32(buf[4:], 0x000000ff)
	img, err := toImage(buf, info)
	if err != nil {
		t.Fatal(err)
	}
	if c := img.RGBAAt(0, 0); c.R != 255 || c.G != 0 || c.B != 0 {
		t.Errorf("px0 %v", c)
	}
	if c := img.RGBAAt(1, 0); c.R != 0 || c.B != 255 {
		t.Errorf("px1 %v", c)
	}
	// RGB565 white and green.
	info = &FBInfo{Width: 2, Height: 1, BitsPerPixel: 16, Stride: 4,
		R: [2]int{11, 5}, G: [2]int{5, 6}, B: [2]int{0, 5}}
	img, err = toImage([]byte{0xff, 0xff, 0xe0, 0x07}, info)
	if err != nil {
		t.Fatal(err)
	}
	if c := img.RGBAAt(0, 0); c.R != 255 || c.G != 255 || c.B != 255 {
		t.Errorf("565 white %v", c)
	}
	if c := img.RGBAAt(1, 0); c.R != 0 || c.G != 255 || c.B != 0 {
		t.Errorf("565 green %v", c)
	}
}

func TestToImageRejectsBadGeometry(t *testing.T) {
	cases := []*FBInfo{
		{Width: 4, Height: 2, BitsPerPixel: 32, Stride: 12}, // stride < width*4
		{Width: 4, Height: 2, BitsPerPixel: 12, Stride: 16}, // odd depth
		{Width: 0, Height: 2, BitsPerPixel: 32, Stride: 16},
	}
	for _, c := range cases {
		if _, err := toImage(make([]byte, 64), c); err == nil {
			t.Errorf("accepted %+v", c)
		}
	}
	// A short buffer is an error, not a panic.
	if _, err := toImage(make([]byte, 10), &FBInfo{Width: 4, Height: 2, BitsPerPixel: 32, Stride: 16}); err == nil {
		t.Error("short buffer accepted")
	}
}

func TestScreenCachesAndLimitsStreams(t *testing.T) {
	calls := 0
	frame := image.NewRGBA(image.Rect(0, 0, 4, 4))
	frame.Set(1, 1, color.White)
	s := &Screen{MaxFPS: 2, MaxStreams: 1, grab: func() (image.Image, string, string, error) {
		calls++
		return frame, "fb", "test", nil
	}}
	for i := 0; i < 5; i++ {
		if _, err := s.JPEG(); err != nil {
			t.Fatal(err)
		}
		_ = s.Status()
		if _, err := s.PNG(); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("%d captures within one frame interval, want 1", calls)
	}
	if st := s.Status(); !st.Available || st.Blank || st.Width != 4 {
		t.Fatalf("status %+v", st)
	}
	release, err := s.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Acquire(); err != ErrBusy {
		t.Fatalf("second stream: %v", err)
	}
	release()
	release() // idempotent
	if r, err := s.Acquire(); err != nil {
		t.Fatal(err)
	} else {
		r()
	}
	time.Sleep(s.Interval() + 10*time.Millisecond)
	_, _ = s.JPEG()
	if calls != 2 {
		t.Fatalf("expected a new capture after the interval, got %d", calls)
	}
}

func TestIsBlank(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 50))
	if !isBlank(img) {
		t.Error("black frame not blank")
	}
	img.Set(99, 49, color.White)
	if isBlank(img) {
		t.Error("frame with a white corner reported blank")
	}
}

func TestPanelControlsUnique(t *testing.T) {
	seen := map[string]bool{}
	codes := map[[2]byte]bool{}
	for _, c := range PanelControls {
		if seen[c.ID] || codes[c.code] {
			t.Fatalf("duplicate %s", c.ID)
		}
		seen[c.ID], codes[c.code] = true, true
	}
	p := &Panel{TTY: "/nonexistent/tty"}
	if err := p.Do("mute1", 0); err == nil {
		t.Error("expected error for missing tty")
	}
	if err := p.Do("nope", 0); err == nil {
		t.Error("expected error for unknown control")
	}
}

// On the Duo the GUI draws through DRM, and /dev/fb0 still holds the boot
// splash: auto mode must show the DRM buffer, not the splash.
func TestPickSourcePrefersDRM(t *testing.T) {
	blank := image.NewRGBA(image.Rect(0, 0, 100, 50))
	content := func() *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, 100, 50))
		img.Set(99, 49, color.White)
		return img
	}
	splash, gui := content(), content()
	ok := func(img image.Image, d string) tryFunc {
		return func() (image.Image, string, error) { return img, d, nil }
	}
	fails := func() (image.Image, string, error) { return nil, "", errors.New("no device") }
	cases := []struct {
		name        string
		fb, drm     tryFunc
		want, wantD string
	}{
		{"Duo: splash in fb0, GUI in DRM", ok(splash, "fb"), ok(gui, "drm"), "drm", "drm"},
		{"no DRM", ok(splash, "fb"), fails, "fb", "fb"},
		{"DRM blank, fb has a picture", ok(splash, "fb"), ok(blank, "drm"), "fb", "fb"},
		{"both blank", ok(blank, "fb"), ok(blank, "drm"), "drm", "drm"},
		{"DRM only", nil, ok(gui, "drm"), "drm", "drm"},
	}
	for _, c := range cases {
		_, src, d, err := pickSource("auto", c.fb, c.drm)
		if err != nil || src != c.want || d != c.wantD {
			t.Errorf("%s: got %s/%s, %v", c.name, src, d, err)
		}
	}
	if _, _, _, err := pickSource("auto", fails, fails); err == nil || !strings.Contains(err.Error(), "DRM: no device; framebuffer: no device") {
		t.Errorf("both failing: %v", err)
	}
	if _, src, _, _ := pickSource("fb", ok(splash, "fb"), ok(gui, "drm")); src != "fb" {
		t.Errorf("mode fb must use the framebuffer, got %s", src)
	}
}
