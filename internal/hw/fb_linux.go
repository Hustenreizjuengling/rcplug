//go:build linux

package hw

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// Linux framebuffer ioctls.
const (
	fbioGetVScreenInfo = 0x4600
	fbioGetFScreenInfo = 0x4602
)

// bitfield is struct fb_bitfield.
type bitfield struct{ Offset, Length, MSBRight uint32 }

// varInfo is struct fb_var_screeninfo (160 bytes).
type varInfo struct {
	XRes, YRes, XResVirtual, YResVirtual, XOffset, YOffset, BitsPerPixel, Grayscale uint32
	Red, Green, Blue, Transp                                                        bitfield
	Rest                                                                            [20]uint32
}

// ProbeFB reads the geometry of a framebuffer device.
func ProbeFB(dev string) (*FBInfo, error) {
	f, err := os.Open(dev)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var v varInfo
	if err := ioctl(f.Fd(), fbioGetVScreenInfo, unsafe.Pointer(&v)); err != nil {
		return probeSysfs(dev)
	}
	var fix [80]byte // struct fb_fix_screeninfo on 64-bit
	stride := 0
	id := ""
	if err := ioctl(f.Fd(), fbioGetFScreenInfo, unsafe.Pointer(&fix[0])); err == nil {
		id = strings.TrimRight(string(fix[:16]), "\x00")
		if unsafe.Sizeof(uintptr(0)) == 8 {
			stride = int(binary.LittleEndian.Uint32(fix[48:52]))
		} else {
			stride = int(binary.LittleEndian.Uint32(fix[40:44]))
		}
	}
	if stride == 0 {
		stride = int(v.XResVirtual * v.BitsPerPixel / 8)
	}
	return &FBInfo{
		Device: dev, ID: id, Width: int(v.XRes), Height: int(v.YRes), VirtualH: int(v.YResVirtual),
		YOffset: int(v.YOffset), BitsPerPixel: int(v.BitsPerPixel), Stride: stride,
		R: [2]int{int(v.Red.Offset), int(v.Red.Length)},
		G: [2]int{int(v.Green.Offset), int(v.Green.Length)},
		B: [2]int{int(v.Blue.Offset), int(v.Blue.Length)},
	}, nil
}

// probeSysfs is the fallback when the ioctl is unavailable.
func probeSysfs(dev string) (*FBInfo, error) {
	name := dev[strings.LastIndex(dev, "/")+1:]
	base := "/sys/class/graphics/" + name + "/"
	read := func(f string) string {
		b, _ := os.ReadFile(base + f)
		return strings.TrimSpace(string(b))
	}
	size := strings.Split(read("virtual_size"), ",")
	if len(size) != 2 {
		return nil, fmt.Errorf("%s: geometry unavailable", dev)
	}
	w, _ := strconv.Atoi(size[0])
	h, _ := strconv.Atoi(size[1])
	bpp, _ := strconv.Atoi(read("bits_per_pixel"))
	stride, _ := strconv.Atoi(read("stride"))
	if stride == 0 {
		stride = w * bpp / 8
	}
	info := &FBInfo{Device: dev, Width: w, Height: h, VirtualH: h, BitsPerPixel: bpp, Stride: stride}
	switch bpp {
	case 32, 24:
		info.R, info.G, info.B = [2]int{16, 8}, [2]int{8, 8}, [2]int{0, 8}
	case 16:
		info.R, info.G, info.B = [2]int{11, 5}, [2]int{5, 6}, [2]int{0, 5}
	}
	return info, nil
}

func ioctl(fd uintptr, req uintptr, arg unsafe.Pointer) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg))
	if e != 0 {
		return e
	}
	return nil
}

// Capture reads the visible framebuffer page into an RGBA image.
func Capture(dev string) (image.Image, *FBInfo, error) {
	info, err := ProbeFB(dev)
	if err != nil {
		return nil, nil, err
	}
	if info.Width <= 0 || info.Height <= 0 || info.Width > 8192 || info.Height > 8192 {
		return nil, info, fmt.Errorf("implausible framebuffer size %dx%d", info.Width, info.Height)
	}
	bpp := info.BitsPerPixel / 8
	if bpp < 2 || bpp > 4 {
		return nil, info, fmt.Errorf("unsupported pixel depth %d", info.BitsPerPixel)
	}
	f, err := os.Open(dev)
	if err != nil {
		return nil, info, err
	}
	defer f.Close()
	// Read only the visible page (double-buffered displays pan via yoffset).
	off := int64(info.YOffset) * int64(info.Stride)
	buf := make([]byte, info.Stride*info.Height)
	if _, err := f.ReadAt(buf, off); err != nil && !errors.Is(err, io.EOF) {
		return nil, info, err
	}
	img, err := toImage(buf, info)
	return img, info, err
}
