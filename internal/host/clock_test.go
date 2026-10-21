package host

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// parseClock reads local wall time in the requested zone and refuses
// anything that is not a zoneinfo name.
func TestParseClock(t *testing.T) {
	tm, zone, err := parseClock(clockRequest{Time: "2026-10-21T20:05:00", Zone: "Europe/Berlin"})
	if err != nil {
		t.Skipf("zoneinfo not available here: %v", err)
	}
	if zone != "Europe/Berlin" || tm == nil || tm.UTC().Hour() != 18 {
		t.Fatalf("got %v %q, want 18:05 UTC in Europe/Berlin", tm, zone)
	}
	for _, bad := range []clockRequest{
		{Zone: "../../etc/passwd"}, {Zone: "Europe/Berlin/../../x"}, {Zone: "No/Such/Zone/Here"},
		{Time: "yesterday"}, {Time: "1999-01-01T00:00:00"}, {},
	} {
		if _, _, err := parseClock(bad); err == nil {
			t.Errorf("%+v must be refused", bad)
		}
	}
	if _, zone, err := parseClock(clockRequest{Zone: "UTC"}); err != nil || zone != "UTC" {
		t.Errorf("zone alone: %v %q", err, zone)
	}
}

// The endpoint takes only a plugin token, and only from a plugin with the
// screen permission; the setter gets the parsed time and zone.
func TestHandleClock(t *testing.T) {
	s := &Server{m: &Manager{}, Events: NewBus()}
	var gotZone string
	var gotTime *time.Time
	s.Clock = func(tm *time.Time, zone string) error { gotTime, gotZone = tm, zone; return nil }
	post := func(token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/system/clock", strings.NewReader(body))
		r.Header.Set("X-Rcplug", "1")
		if token != "" {
			r.Header.Set("X-Rcplug-Token", token)
		}
		w := httptest.NewRecorder()
		s.handleClock(w, r)
		return w
	}
	if w := post("", `{"zone":"UTC"}`); w.Code != http.StatusForbidden {
		t.Fatalf("without a token: %d %s", w.Code, w.Body)
	}
	if w := post(s.Events.Token("sonar"), `{"zone":"UTC"}`); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "permission_screen") {
		t.Fatalf("without the screen permission: %d %s", w.Code, w.Body)
	}
	s.permissionsFor = func(name string) []string { return []string{"desk:write", "screen"} }
	if w := post(s.Events.Token("screen"), `{"zone":"UTC"}`); w.Code != http.StatusOK {
		t.Fatalf("screen plugin: %d %s", w.Code, w.Body)
	}
	if gotZone != "UTC" || gotTime != nil {
		t.Fatalf("setter got %v %q", gotTime, gotZone)
	}
	if w := post(s.Events.Token("screen"), `{"time":"2026-10-21T20:05:00","zone":"UTC"}`); w.Code != http.StatusOK {
		t.Fatalf("time and zone: %d %s", w.Code, w.Body)
	}
	if gotTime == nil || gotTime.UTC().Hour() != 20 {
		t.Fatalf("setter got time %v", gotTime)
	}
	if w := post(s.Events.Token("screen"), `{"zone":"../x"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad zone: %d %s", w.Code, w.Body)
	}
}
