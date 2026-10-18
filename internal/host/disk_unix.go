//go:build !windows

package host

import "syscall"

// diskStats returns free and total MB of the filesystem holding path.
func diskStats(path string) (free, total int64, ok bool) {
	var st syscall.Statfs_t
	if path == "" || syscall.Statfs(path, &st) != nil {
		return 0, 0, false
	}
	bs := int64(st.Bsize)
	return int64(st.Bavail) * bs / (1 << 20), int64(st.Blocks) * bs / (1 << 20), true
}
