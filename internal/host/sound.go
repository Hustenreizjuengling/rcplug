package host

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// soundState collects the sound cards' and PCM streams' state and the USB
// device controller's state: whether the stream the computer plays through
// still runs (or sits in XRUN) and whether the USB gadget is configured, to
// see what silences USB audio. Empty where there is none.
func soundState(root string) string {
	var paths []string
	for _, p := range []string{"proc/asound/cards", "proc/asound/pcm"} {
		paths = append(paths, filepath.Join(root, p))
	}
	for _, g := range []string{
		"proc/asound/card*/id", "proc/asound/card*/pcm*/info", "proc/asound/card*/pcm*/sub*/status",
		"proc/asound/card*/pcm*/sub*/hw_params", "proc/asound/card*/pcm*/sub*/sw_params",
		"sys/class/udc/*/state", "sys/class/udc/*/current_speed", "sys/class/udc/*/function",
	} {
		m, _ := filepath.Glob(filepath.Join(root, g))
		sort.Strings(m)
		paths = append(paths, m...)
	}
	var b strings.Builder
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		b.WriteString("== /" + strings.TrimPrefix(filepath.ToSlash(strings.TrimPrefix(p, root)), "/") + "\n")
		b.Write(limit(raw, 8<<10))
		if len(raw) > 0 && raw[len(raw)-1] != '\n' {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
