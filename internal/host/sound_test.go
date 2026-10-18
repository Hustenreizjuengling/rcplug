package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSoundState(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"proc/asound/cards":                      " 0 [UAC2Gadget     ]: UAC2_Gadget - UAC2_Gadget\n",
		"proc/asound/card0/pcm0p/sub0/status":    "state: XRUN\n",
		"proc/asound/card0/pcm0p/sub0/hw_params": "closed",
		"sys/class/udc/ff400000.usb/state":       "configured\n",
	}
	for p, c := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755)
		os.WriteFile(filepath.Join(root, p), []byte(c), 0o644)
	}
	s := soundState(root)
	for _, want := range []string{"== /proc/asound/cards\n 0 [UAC2Gadget", "== /proc/asound/card0/pcm0p/sub0/status\nstate: XRUN\n", "hw_params\nclosed\n", "== /sys/class/udc/ff400000.usb/state\nconfigured\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if soundState(t.TempDir()) != "" {
		t.Error("state without any sound device")
	}
}
