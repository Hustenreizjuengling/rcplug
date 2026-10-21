package host

import (
	"net/http"

	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// The device's screen on request. A plugin with the permission
// "screen:request" runs like any other plugin (its web pages are always
// there) and takes the device's screen when it asks for it, giving it back
// without ending: POST /api/screen/take and /api/screen/release with its
// token. The host remembers who holds it across a restart of the device.
// GET /api/screen tells who has it and who wants it.

// screenCaller names the plugin behind a screen request, or writes the
// error and returns "".
func (s *Server) screenCaller(w http.ResponseWriter, r *http.Request) string {
	name, ok := "", false
	if s.Events != nil {
		name, ok = s.Events.pluginOf(r.Header.Get("X-Rcplug-Token"))
	}
	if !ok {
		sdk.WriteError(w, sdk.NewError(http.StatusForbidden, "plugin_token", "only a plugin takes the device's screen (X-Rcplug-Token, from RCPLUG_TOKEN)"))
		return ""
	}
	return name
}

func (s *Server) handleScreenState(w http.ResponseWriter, r *http.Request) {
	holder, wanted := s.m.ScreenState()
	sdk.WriteJSON(w, http.StatusOK, map[string]interface{}{"holder": holder, "wanted": wanted})
}

func (s *Server) handleScreenTake(w http.ResponseWriter, r *http.Request) {
	name := s.screenCaller(w, r)
	if name == "" {
		return
	}
	if err := s.m.TakeScreenFor(name); err != nil {
		sdk.WriteError(w, err)
		return
	}
	sdk.WriteJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "holder": name})
}

func (s *Server) handleScreenRelease(w http.ResponseWriter, r *http.Request) {
	name := s.screenCaller(w, r)
	if name == "" {
		return
	}
	s.m.ReleaseScreenFor(name)
	holder, _ := s.m.ScreenState()
	sdk.WriteJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "holder": holder})
}
