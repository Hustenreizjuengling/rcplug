//go:build linux

package hw

import (
	"fmt"
	"image"
	"os"
	"syscall"
	"unsafe"
)

// DRM/KMS capture: if the GUI draws through DRM instead of the legacy
// framebuffer, /dev/fb0 stays black. The scanout buffer of the active CRTC
// can then be read directly: GETRESOURCES → GETCRTC (fb id) → GETFB2/GETFB
// (GEM handle; only returned to root/DRM master) → MAP_DUMB or a PRIME
// dma-buf → mmap → copy. Linear 16/32-bit formats only.

const (
	drmIoctlBase = 'd'
	iocWrite     = 1
	iocRead      = 2
)

func iowr(nr, size uintptr) uintptr {
	return (iocRead|iocWrite)<<30 | size<<16 | drmIoctlBase<<8 | nr
}
func iow(nr, size uintptr) uintptr { return iocWrite<<30 | size<<16 | drmIoctlBase<<8 | nr }

type drmCardRes struct {
	FbIDPtr, CrtcIDPtr, ConnectorIDPtr, EncoderIDPtr uint64
	CountFbs, CountCrtcs, CountConnectors, CountEncs uint32
	MinWidth, MaxWidth, MinHeight, MaxHeight         uint32
}

type drmModeInfo struct {
	Clock                                         uint32
	HDisplay, HSyncStart, HSyncEnd, HTotal, HSkew uint16
	VDisplay, VSyncStart, VSyncEnd, VTotal, VScan uint16
	VRefresh, Flags, Type                         uint32
	Name                                          [32]byte
}

type drmCrtc struct {
	SetConnectorsPtr                               uint64
	CountConnectors, CrtcID, FbID, X, Y, GammaSize uint32
	ModeValid                                      uint32
	Mode                                           drmModeInfo
}

type drmFbCmd struct {
	FbID, Width, Height, Pitch, Bpp, Depth, Handle uint32
}

type drmFbCmd2 struct {
	FbID, Width, Height, PixelFormat, Flags uint32
	Handles, Pitches, Offsets               [4]uint32
	_                                       uint32 // alignment for modifier
	Modifier                                [4]uint64
}

type drmMapDumb struct {
	Handle, Pad uint32
	Offset      uint64
}

type drmPrimeHandle struct {
	Handle, Flags uint32
	Fd            int32
}

type drmGemClose struct{ Handle, Pad uint32 }

var (
	ioctlGetResources = iowr(0xA0, unsafe.Sizeof(drmCardRes{}))
	ioctlGetCrtc      = iowr(0xA1, unsafe.Sizeof(drmCrtc{}))
	ioctlGetFb        = iowr(0xAD, unsafe.Sizeof(drmFbCmd{}))
	ioctlGetFb2       = iowr(0xCE, unsafe.Sizeof(drmFbCmd2{}))
	ioctlMapDumb      = iowr(0xB3, unsafe.Sizeof(drmMapDumb{}))
	ioctlPrimeToFd    = iowr(0x2D, unsafe.Sizeof(drmPrimeHandle{}))
	ioctlGemClose     = iow(0x09, unsafe.Sizeof(drmGemClose{}))
	ioctlDropMaster   = uintptr(drmIoctlBase<<8 | 0x1f) // _IO('d', 0x1f)
)

func fourcc(a, b, c, d byte) uint32 {
	return uint32(a) | uint32(b)<<8 | uint32(c)<<16 | uint32(d)<<24
}

// DRMInfo describes the captured scanout buffer.
type DRMInfo struct {
	Device string `json:"device"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Pitch  int    `json:"pitch"`
	Format string `json:"format"`
	Via    string `json:"via"` // dumb | prime
}

// CaptureDRM grabs the image currently scanned out by the first active CRTC.
func CaptureDRM(dev string) (image.Image, *DRMInfo, error) {
	f, err := os.OpenFile(dev, os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	fd := f.Fd()
	// Opening the primary node makes us DRM master if nobody holds it (e.g.
	// while the GUI restarts), which would keep the GUI from taking the
	// display back. Drop it right away; failing (not master) is the normal case.
	_ = ioctl(fd, ioctlDropMaster, nil)

	var res drmCardRes
	if err := ioctl(fd, ioctlGetResources, unsafe.Pointer(&res)); err != nil {
		return nil, nil, fmt.Errorf("GETRESOURCES: %w", err)
	}
	if res.CountCrtcs == 0 || res.CountCrtcs > 16 {
		return nil, nil, fmt.Errorf("no CRTCs")
	}
	crtcs := make([]uint32, res.CountCrtcs)
	res2 := drmCardRes{CrtcIDPtr: uint64(uintptr(unsafe.Pointer(&crtcs[0]))), CountCrtcs: res.CountCrtcs}
	if err := ioctl(fd, ioctlGetResources, unsafe.Pointer(&res2)); err != nil {
		return nil, nil, fmt.Errorf("GETRESOURCES: %w", err)
	}
	var fbID uint32
	for _, id := range crtcs {
		c := drmCrtc{CrtcID: id}
		if ioctl(fd, ioctlGetCrtc, unsafe.Pointer(&c)) == nil && c.FbID != 0 && c.ModeValid != 0 {
			fbID = c.FbID
			break
		}
	}
	if fbID == 0 {
		return nil, nil, fmt.Errorf("no active display (no CRTC with a framebuffer)")
	}

	info := &DRMInfo{Device: dev}
	var handle, format uint32
	var offset uint32
	fb2 := drmFbCmd2{FbID: fbID}
	if err := ioctl(fd, ioctlGetFb2, unsafe.Pointer(&fb2)); err == nil {
		if fb2.Modifier[0] != 0 && fb2.Modifier[0] != 0x00ffffffffffffff { // non-linear (tiled/compressed)
			closeGem(fd, fb2.Handles[:])
			return nil, nil, fmt.Errorf("framebuffer is tiled (modifier %#x), cannot be read", fb2.Modifier[0])
		}
		handle, format, offset = fb2.Handles[0], fb2.PixelFormat, fb2.Offsets[0]
		info.Width, info.Height, info.Pitch = int(fb2.Width), int(fb2.Height), int(fb2.Pitches[0])
		defer closeGem(fd, fb2.Handles[:])
	} else {
		fb := drmFbCmd{FbID: fbID}
		if err := ioctl(fd, ioctlGetFb, unsafe.Pointer(&fb)); err != nil {
			return nil, nil, fmt.Errorf("GETFB: %w", err)
		}
		handle = fb.Handle
		info.Width, info.Height, info.Pitch = int(fb.Width), int(fb.Height), int(fb.Pitch)
		switch fb.Bpp {
		case 16:
			format = fourcc('R', 'G', '1', '6')
		case 32:
			format = fourcc('X', 'R', '2', '4')
		default:
			closeGem(fd, []uint32{fb.Handle})
			return nil, nil, fmt.Errorf("unsupported framebuffer depth %d bpp", fb.Bpp)
		}
		defer closeGem(fd, []uint32{fb.Handle})
	}
	if handle == 0 {
		return nil, info, fmt.Errorf("no access to the display buffer (root required)")
	}
	if info.Width <= 0 || info.Height <= 0 || info.Width > 8192 || info.Height > 8192 || info.Pitch <= 0 {
		return nil, info, fmt.Errorf("implausible geometry %dx%d", info.Width, info.Height)
	}
	size := int(offset) + info.Pitch*info.Height

	var mem []byte
	md := drmMapDumb{Handle: handle}
	if err := ioctl(fd, ioctlMapDumb, unsafe.Pointer(&md)); err == nil {
		mem, err = syscall.Mmap(int(fd), int64(md.Offset), size, syscall.PROT_READ, syscall.MAP_SHARED)
		if err == nil {
			info.Via = "dumb"
		}
	}
	if mem == nil {
		ph := drmPrimeHandle{Handle: handle, Flags: syscall.O_CLOEXEC}
		if err := ioctl(fd, ioctlPrimeToFd, unsafe.Pointer(&ph)); err != nil {
			return nil, info, fmt.Errorf("display buffer cannot be mapped: %w", err)
		}
		defer syscall.Close(int(ph.Fd))
		m, err := syscall.Mmap(int(ph.Fd), 0, size, syscall.PROT_READ, syscall.MAP_SHARED)
		if err != nil {
			return nil, info, fmt.Errorf("dma-buf mmap: %w", err)
		}
		mem, info.Via = m, "prime"
	}
	defer syscall.Munmap(mem)

	fbi := &FBInfo{Width: info.Width, Height: info.Height, Stride: info.Pitch}
	switch format {
	case fourcc('X', 'R', '2', '4'), fourcc('A', 'R', '2', '4'):
		fbi.BitsPerPixel, fbi.R, fbi.G, fbi.B = 32, [2]int{16, 8}, [2]int{8, 8}, [2]int{0, 8}
		info.Format = "XRGB8888"
	case fourcc('X', 'B', '2', '4'), fourcc('A', 'B', '2', '4'):
		fbi.BitsPerPixel, fbi.R, fbi.G, fbi.B = 32, [2]int{0, 8}, [2]int{8, 8}, [2]int{16, 8}
		info.Format = "XBGR8888"
	case fourcc('R', 'G', '1', '6'):
		fbi.BitsPerPixel, fbi.R, fbi.G, fbi.B = 16, [2]int{11, 5}, [2]int{5, 6}, [2]int{0, 5}
		info.Format = "RGB565"
	default:
		return nil, info, fmt.Errorf("pixel format %q not supported", string([]byte{byte(format), byte(format >> 8), byte(format >> 16), byte(format >> 24)}))
	}
	// Copy out of the mapping first: the buffer keeps changing underneath.
	buf := make([]byte, info.Pitch*info.Height)
	copy(buf, mem[offset:])
	img, err := toImage(buf, fbi)
	return img, info, err
}

func closeGem(fd uintptr, handles []uint32) {
	seen := map[uint32]bool{}
	for _, h := range handles {
		if h == 0 || seen[h] {
			continue
		}
		seen[h] = true
		c := drmGemClose{Handle: h}
		_ = ioctl(fd, ioctlGemClose, unsafe.Pointer(&c))
	}
}
