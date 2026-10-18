package rcsim

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hustenreizjuengling/rcplug/internal/rcproto"
)

// Every committed fixture must parse, round-trip byte for byte and already be
// in sanitized form, so nothing personal can slip into the repository.
func TestFixturesAreSanitized(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("fixture", "*.frame"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	notice, _ := os.ReadFile(filepath.Join("fixture", "NOTICE"))
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		tree, err := TreeFromFrame(raw)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		enc := rcproto.Frame((&rcproto.Change{Type: rcproto.FullSync, Tree: tree}).Encode())
		if !bytes.Equal(enc, raw) {
			t.Errorf("%s: does not round-trip", f)
		}
		san := rcproto.Frame((&rcproto.Change{Type: rcproto.FullSync, Tree: rcproto.Sanitize(tree)}).Encode())
		if !bytes.Equal(san, raw) {
			t.Errorf("%s: not sanitized (run it through rcproto.Sanitize / rccapture)", f)
		}
		if !strings.Contains(string(notice), filepath.Base(f)) {
			t.Errorf("%s: not described in fixture/NOTICE", f)
		}
	}
}
