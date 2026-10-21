package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// The device's clock. RØDE's screen sets it on its date and time page: the
// system clock, then the hardware clock (hwclock --systohc), and the time
// zone as the link /etc/localtime → /usr/share/zoneinfo/<zone>. The plugin
// that stands in for that screen (permission "screen") asks the host to do
// the same with POST /api/system/clock.

// zoneinfo is where the device keeps its time zones.
const zoneinfo = "/usr/share/zoneinfo"

// ClockSetter sets the device's clock: t (nil: unchanged) is the new time,
// zone (empty: unchanged) a zoneinfo name such as Europe/Berlin.
type ClockSetter func(t *time.Time, zone string) error

// clockRequest is the body of POST /api/system/clock. The time is local wall
// time without an offset (as a person reads it off the page), in the zone
// of the same request if one is given, else the device's.
type clockRequest struct {
	Time string `json:"time,omitempty"`
	Zone string `json:"zone,omitempty"`
}

var zoneName = regexp.MustCompile(`^[A-Za-z0-9_+\-]+(/[A-Za-z0-9_+\-]+){0,2}$`)

// parseClock turns a request into the time to set, resolving the wall time
// in the zone (the request's, else the device's).
func parseClock(req clockRequest) (*time.Time, string, error) {
	zone := req.Zone
	if zone != "" {
		if !zoneName.MatchString(zone) || strings.Contains(zone, "..") {
			return nil, "", errors.New("zone must be a zoneinfo name such as Europe/Berlin")
		}
		if _, err := os.Stat(filepath.Join(zoneinfo, zone)); err != nil {
			return nil, "", fmt.Errorf("unknown time zone %q", zone)
		}
	}
	if req.Time == "" {
		if zone == "" {
			return nil, "", errors.New("nothing to set: give time, zone or both")
		}
		return nil, zone, nil
	}
	loc := time.Local
	if zone != "" {
		l, err := time.LoadLocation(zone)
		if err != nil {
			return nil, "", fmt.Errorf("unknown time zone %q", zone)
		}
		loc = l
	}
	t, err := time.ParseInLocation("2006-01-02T15:04:05", req.Time, loc)
	if err != nil {
		return nil, "", errors.New("time must be local wall time as 2006-01-02T15:04:05")
	}
	if t.Year() < 2000 || t.Year() > 2100 {
		return nil, "", errors.New("time is out of the device's range (2000…2100)")
	}
	return &t, zone, nil
}

// setDeviceClock is the real ClockSetter: the system clock, the hardware
// clock, and the time zone link. It needs root, which rcplugd has on the
// device.
func setDeviceClock(t *time.Time, zone string) error {
	if zone != "" {
		target := filepath.Join(zoneinfo, zone)
		tmp := "/etc/.localtime.rcplug"
		os.Remove(tmp)
		if err := os.Symlink(target, tmp); err != nil {
			return fmt.Errorf("time zone: %w", err)
		}
		if err := os.Rename(tmp, "/etc/localtime"); err != nil {
			os.Remove(tmp)
			return fmt.Errorf("time zone: %w", err)
		}
	}
	if t != nil {
		tv := syscall.NsecToTimeval(t.UnixNano())
		if err := syscall.Settimeofday(&tv); err != nil {
			return fmt.Errorf("system clock: %w", err)
		}
		if out, err := exec.Command("hwclock", "--systohc").CombinedOutput(); err != nil {
			// The hardware clock is kept only where there is one; the
			// system clock is set either way.
			return fmt.Errorf("hardware clock: %v (%s)", err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// handleClock is POST /api/system/clock for the plugin with the screen.
func (s *Server) handleClock(w http.ResponseWriter, r *http.Request) {
	name, ok := "", false
	if s.Events != nil {
		name, ok = s.Events.pluginOf(r.Header.Get("X-Rcplug-Token"))
	}
	if !ok {
		sdk.WriteError(w, sdk.NewError(http.StatusForbidden, "plugin_token", "only the plugin with the screen sets the clock (X-Rcplug-Token, from RCPLUG_TOKEN)"))
		return
	}
	perms := s.permissionsFor
	if perms == nil {
		perms = s.pluginPermissions
	}
	if !hasPermission(perms(name), "screen") {
		sdk.WriteError(w, sdk.NewError(http.StatusForbidden, "permission_screen", name+` does not declare the permission "screen" in its plugin.json`))
		return
	}
	var req clockRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		sdk.WriteError(w, sdk.NewError(http.StatusBadRequest, "bad_request", `body must be {"time": "2006-01-02T15:04:05", "zone": "Europe/Berlin"} (either optional)`))
		return
	}
	t, zone, err := parseClock(req)
	if err != nil {
		sdk.WriteError(w, sdk.NewError(http.StatusBadRequest, "bad_clock", err.Error()))
		return
	}
	set := s.Clock
	if set == nil {
		set = setDeviceClock
	}
	if err := set(t, zone); err != nil {
		log.Printf("clock: %s asked for %v %q: %v", name, req.Time, zone, err)
		sdk.WriteError(w, sdk.NewError(http.StatusInternalServerError, "clock_failed", err.Error()))
		return
	}
	log.Printf("clock: set by %s (%s %s)", name, req.Time, zone)
	out := map[string]interface{}{"ok": true, "zone": zone}
	if t != nil {
		out["time"] = t.Format(time.RFC3339)
	}
	sdk.WriteJSON(w, http.StatusOK, out)
}

func hasPermission(perms []string, p string) bool {
	for _, q := range perms {
		if q == p {
			return true
		}
	}
	return false
}
