package hw

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
)

// FBInfo describes a framebuffer's geometry and pixel format.
type FBInfo struct {
	Device       string `json:"device"`
	ID           string `json:"id"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	VirtualH     int    `json:"virtualHeight"`
	YOffset      int    `json:"yOffset"`
	BitsPerPixel int    `json:"bitsPerPixel"`
	Stride       int    `json:"stride"`
	R, G, B      [2]int `json:"-"` // bitfield offset, length
}

// toImage converts raw pixel rows to RGBA using the bitfield layout. It
// validates the geometry first, so a bogus stride or depth is an error rather
// than an out-of-range panic.
func toImage(buf []byte, info *FBInfo) (*image.RGBA, error) {
	bpp := info.BitsPerPixel / 8
	if bpp < 2 || bpp > 4 || info.BitsPerPixel%8 != 0 {
		return nil, fmt.Errorf("unsupported pixel depth %d", info.BitsPerPixel)
	}
	if info.Width <= 0 || info.Height <= 0 || info.Width > 8192 || info.Height > 8192 {
		return nil, fmt.Errorf("implausible size %dx%d", info.Width, info.Height)
	}
	if info.Stride < info.Width*bpp {
		return nil, fmt.Errorf("stride %d too small for %d pixels of %d bytes", info.Stride, info.Width, bpp)
	}
	if len(buf) < info.Stride*(info.Height-1)+info.Width*bpp {
		return nil, fmt.Errorf("short frame: %d bytes", len(buf))
	}
	img := image.NewRGBA(image.Rect(0, 0, info.Width, info.Height))
	ch := func(px uint32, bf [2]int) uint8 {
		if bf[1] == 0 {
			return 0
		}
		v := (px >> uint(bf[0])) & (1<<uint(bf[1]) - 1)
		return uint8(v * 255 / (1<<uint(bf[1]) - 1))
	}
	for y := 0; y < info.Height; y++ {
		row := buf[y*info.Stride:]
		for x := 0; x < info.Width; x++ {
			var px uint32
			switch bpp {
			case 4:
				px = binary.LittleEndian.Uint32(row[x*4:])
			case 3:
				px = uint32(row[x*3]) | uint32(row[x*3+1])<<8 | uint32(row[x*3+2])<<16
			case 2:
				px = uint32(binary.LittleEndian.Uint16(row[x*2:]))
			}
			img.SetRGBA(x, y, color.RGBA{ch(px, info.R), ch(px, info.G), ch(px, info.B), 255})
		}
	}
	return img, nil
}

// isBlank reports whether a frame is a single colour (sampled on a grid).
func isBlank(img image.Image) bool {
	b := img.Bounds()
	if b.Empty() {
		return true
	}
	r0, g0, b0, _ := img.At(b.Min.X, b.Min.Y).RGBA()
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			r, g, bb, _ := img.At(b.Min.X+(b.Dx()-1)*x/15, b.Min.Y+(b.Dy()-1)*y/15).RGBA()
			if r != r0 || g != g0 || bb != b0 {
				return false
			}
		}
	}
	return true
}
