package console

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Hustenreizjuengling/rcplug/internal/rcsim"
)

// The bundle goes into bug reports: names and browsers' addresses stay out.
func TestDiagnosticsLeaveNamesOut(t *testing.T) {
	r := newRig(t, rcsim.Options{})
	var sum struct{ Name string }
	json.Unmarshal(r.do(t, http.MethodGet, "/api/summary", nil).raw, &sum)
	if sum.Name == "" {
		t.Fatal("the simulator has no name")
	}
	r.console.mu.Lock()
	r.console.audit = append(r.console.audit,
		AuditEntry{Node: "SYSTEM", Name: "systemName", Value: `"My Studio"`, Remote: "10.0.0.7"},
		AuditEntry{Node: "FADER", Name: "faderLevel", Value: "512", Remote: "plugin sonar"},
		AuditEntry{Node: "console", Name: "control", Value: "expert", Remote: "10.0.0.7"})
	r.console.mu.Unlock()
	res := r.do(t, http.MethodGet, "/api/diagnostics", nil)
	z, err := zip.NewReader(bytes.NewReader(res.raw), int64(len(res.raw)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range z.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = string(b)
	}
	summary, audit := files["summary.json"], files["audit.json"]
	if !strings.Contains(summary, `"model"`) || strings.Contains(summary, sum.Name) {
		t.Fatalf("summary: %s", summary)
	}
	for _, leak := range []string{"My Studio", "10.0.0.7"} {
		if strings.Contains(audit, leak) {
			t.Fatalf("%q in the audit: %s", leak, audit)
		}
	}
	for _, kept := range []string{`"512"`, "plugin sonar", `"expert"`} {
		if !strings.Contains(audit, kept) {
			t.Fatalf("%s missing in the audit: %s", kept, audit)
		}
	}
}
