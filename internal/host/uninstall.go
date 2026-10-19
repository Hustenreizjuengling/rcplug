package host

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// Uninstalling from the dashboard, the last step that needed a computer: a
// detached script stops rcplug (this process and every plugin), switches
// RØDE's services on again that were switched off, removes its init script
// and then the whole install directory, plugin data included.
// The device itself keeps running; installing again takes the toolkit.

// uninstallScript runs detached: $1 init dir, $2 install dir, $3 "1" to
// remove the boot links with update-rc.d.
const uninstallScript = `sleep 1
"$1/rcplug" stop >/dev/null 2>&1
"$2/rcplugd" --root "$2" --services restore >/dev/null 2>&1
if [ "$3" = 1 ] && command -v update-rc.d >/dev/null 2>&1; then update-rc.d -f rcplug remove >/dev/null 2>&1; fi
rm -f "$1/rcplug" /var/log/rcplug.log /var/log/rcplug.log.1
case "$2" in */rcplug) rm -rf "$2";; esac`

// uninstallable says whether root looks like an install directory this
// host may remove: named rcplug and holding rcplugd (never a development
// tree or another directory).
func uninstallable(root string) error {
	if filepath.Base(root) != "rcplug" {
		return errors.New("this rcplug does not run from an install directory named rcplug (a development setup?)")
	}
	if fi, err := os.Stat(filepath.Join(root, "rcplugd")); err != nil || fi.IsDir() {
		return errors.New("no rcplugd in " + root)
	}
	return nil
}

// spawnUninstall is spawnDetached in production; tests replace it.
var spawnUninstall = func(initDir, root string, links bool) error {
	l := "0"
	if links {
		l = "1"
	}
	return spawnDetached(uninstallScript, initDir, root, l)
}

// handleUninstall: POST api/uninstall {"confirm": "uninstall"}.
func (s *Server) handleUninstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirm string `json:"confirm"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Confirm != "uninstall" {
		sdk.WriteError(w, sdk.NewError(http.StatusBadRequest, "confirm", `send {"confirm": "uninstall"}`))
		return
	}
	root := s.m.Root()
	if err := uninstallable(root); err != nil {
		sdk.WriteError(w, sdk.NewError(http.StatusConflict, "not_installed", err.Error()))
		return
	}
	initDir := os.Getenv("RCPLUG_INITDIR")
	links := initDir == ""
	if initDir == "" {
		initDir = "/etc/init.d"
	}
	if err := spawnUninstall(initDir, root, links); err != nil {
		sdk.WriteError(w, sdk.NewError(http.StatusInternalServerError, "uninstall_failed", err.Error()))
		return
	}
	s.m.hostEvent("host.uninstall", map[string]string{"dir": root})
	writeJSON(w, map[string]bool{"ok": true})
}
