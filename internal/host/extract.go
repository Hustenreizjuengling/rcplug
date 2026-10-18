package host

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// extractTarGz unpacks a release package into dst, which must not exist yet.
// Only plain directories and regular files are accepted, every path must
// stay inside dst, and the unpacked size is bounded: a package is checked
// against its signed SHA-256 before, but this keeps a mistake in a release
// from writing anywhere else.
func extractTarGz(file, dst string, maxTotal, rate int64, report func(n int64)) error {
	p := newPacer(rate)
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	var src io.Reader = f
	if report != nil {
		src = &counter{r: f, report: report} // progress in package bytes
	}
	gz, err := gzip.NewReader(src)
	if err != nil {
		return err
	}
	if err := os.Mkdir(dst, 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		if name == "." {
			continue
		}
		if !filepath.IsLocal(name) || strings.Contains(name, "\\") {
			return fmt.Errorf("package entry %q leaves the package", h.Name)
		}
		target := filepath.Join(dst, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			total += h.Size
			if total > maxTotal {
				return fmt.Errorf("package unpacks to more than %d MB", maxTotal>>20)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(h.Mode) & 0o755
			if mode&0o600 != 0o600 {
				mode |= 0o600
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, err = io.Copy(&pacedFile{f: out, p: p, syncEvery: 256 << 10}, io.LimitReader(tr, h.Size))
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
			_ = os.Chmod(target, mode) // the umask must not drop the execute bits
		default:
			return fmt.Errorf("package entry %q is not a plain file or directory", h.Name)
		}
	}
}
