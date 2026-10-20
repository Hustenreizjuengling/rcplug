package host

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// Backups of rcplug's own settings, for the backup plugin (it keeps them
// and restores them from the browser): which plugins are installed and
// switched on, their options, the daily update check and the extra plugin
// catalogs. Left out: the dashboard's password (it is entered again),
// options that look like secrets, the plugins' own data and the logs, and
// the plugins of rcplug's device package (the console): what they hold goes
// with the device and its shows, not with rcplug.
//
//	GET  /api/backup          the settings now (RcplugBackup)
//	POST /api/backup/diff     with a backup: what restoring it would change (BackupPlan)
//	POST /api/backup/restore  with a backup: restores it, answers the plan it carried out
//
// Restoring makes the settings of every installed plugin in the backup what
// they were (an option the backup does not have goes back to its default);
// plugins the backup does not know stay as they are, and plugins it has but
// the device lacks are listed as missing (the backup plugin installs them
// from the catalog first).

// BackupFormat is the version of the backup document.
const BackupFormat = 1

// RcplugBackup is rcplug's own settings.
type RcplugBackup struct {
	Format    int             `json:"format"`
	Kind      string          `json:"kind"` // "rcplug-settings"
	Created   string          `json:"created"`
	Version   string          `json:"version"` // rcplug's
	Plugins   []BackupPlugin  `json:"plugins"`
	AutoCheck bool            `json:"autoCheck"`
	Catalogs  []CatalogSource `json:"catalogs"`
	// ServicesOff are RØDE's services switched off (services.go); nil in a
	// backup made before rcplug knew them, which then leaves them alone.
	ServicesOff *[]string `json:"servicesOff,omitempty"`
}

const backupKind = "rcplug-settings"

// BackupPlugin is one plugin's settings.
type BackupPlugin struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
	// Enabled: the plugin runs (switched on by the user or by its autostart).
	// A backup of rcplug 0.8.5 has it only where the user chose; absent,
	// restoring leaves the plugin as it is.
	Enabled *bool                      `json:"enabled,omitempty"`
	Options map[string]json.RawMessage `json:"options,omitempty"`
	// LeftOut are options left out as secrets (restoring keeps them).
	LeftOut []string `json:"leftOut,omitempty"`
}

// secretOption reports whether an option's value is kept out of backups.
func secretOption(o Option) bool { return o.Type == "text" && secretKey.MatchString(o.ID) }

// Backup reads rcplug's settings.
func (m *Manager) Backup(version string) RcplugBackup {
	ps := m.loadState()
	off := append([]string{}, ps.ServicesOff...)
	b := RcplugBackup{Format: BackupFormat, Kind: backupKind, Created: time.Now().Format(time.RFC3339), Version: version,
		Plugins: []BackupPlugin{}, AutoCheck: ps.AutoCheck, Catalogs: ps.Catalogs, ServicesOff: &off}
	if b.Catalogs == nil {
		b.Catalogs = []CatalogSource{}
	}
	for _, man := range m.Manifests() {
		if platformPlugins[man.Name] {
			continue
		}
		p := BackupPlugin{Name: man.Name, Title: man.Title, Version: man.Version}
		if pl := m.get(man.Name); pl != nil {
			on := pl.status().Enabled
			p.Enabled = &on
		}
		cfg, _ := LoadUserConfig(m.root, man.Name)
		for _, o := range man.Options {
			raw, ok := cfg.Options[o.ID]
			switch {
			case !ok || string(raw) == "null":
			case secretOption(o):
				p.LeftOut = append(p.LeftOut, o.ID)
			default:
				if p.Options == nil {
					p.Options = map[string]json.RawMessage{}
				}
				p.Options[o.ID] = raw
			}
		}
		b.Plugins = append(b.Plugins, p)
	}
	return b
}

// BackupChange is one setting restoring changes. What: "enabled", "option",
// "autoCheck" or "catalog"; From/To null: not set (an option's default, a
// catalog that is not there).
type BackupChange struct {
	Plugin string          `json:"plugin,omitempty"`
	What   string          `json:"what"`
	ID     string          `json:"id,omitempty"`    // the option, the catalog's repository
	Title  string          `json:"title,omitempty"` // the option's title
	From   json.RawMessage `json:"from"`
	To     json.RawMessage `json:"to"`
}

// BackupSkip is a setting of the backup this rcplug cannot restore.
type BackupSkip struct {
	Plugin string `json:"plugin,omitempty"`
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// BackupPlan is what restoring a backup changes.
type BackupPlan struct {
	Changes []BackupChange `json:"changes"`
	// Missing are plugins of the backup this device does not have.
	Missing []BackupPlugin `json:"missing"`
	Skipped []BackupSkip   `json:"skipped"`
}

func rawJSON(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func compactJSON(raw json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return string(raw)
	}
	return b.String()
}

// plan compares a backup with the settings now.
func (m *Manager) plan(b RcplugBackup) BackupPlan {
	now := m.Backup("")
	plan := BackupPlan{Changes: []BackupChange{}, Missing: []BackupPlugin{}, Skipped: []BackupSkip{}}
	current := map[string]BackupPlugin{}
	for _, p := range now.Plugins {
		current[p.Name] = p
	}
	manifests := map[string]*Manifest{}
	for _, man := range m.Manifests() {
		manifests[man.Name] = man
	}
	for _, want := range b.Plugins {
		if platformPlugins[want.Name] {
			continue // in a backup of rcplug 0.8.5: left alone
		}
		have, ok := current[want.Name]
		man := manifests[want.Name]
		if !ok || man == nil {
			plan.Missing = append(plan.Missing, want)
			continue
		}
		if want.Enabled != nil && (have.Enabled == nil || *have.Enabled != *want.Enabled) {
			from := json.RawMessage("null")
			if have.Enabled != nil {
				from = rawJSON(*have.Enabled)
			}
			plan.Changes = append(plan.Changes, BackupChange{Plugin: want.Name, What: "enabled", From: from, To: rawJSON(*want.Enabled)})
		}
		left := map[string]bool{}
		for _, id := range want.LeftOut {
			left[id] = true
		}
		byID := map[string]Option{}
		for _, o := range man.Options {
			byID[o.ID] = o
		}
		ids := make([]string, 0, len(want.Options))
		for id := range want.Options {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			o, ok := byID[id]
			switch {
			case !ok:
				plan.Skipped = append(plan.Skipped, BackupSkip{Plugin: want.Name, ID: id, Reason: "this version of the plugin has no such option"})
				continue
			case secretOption(o):
				continue // never restored from a file
			}
			if _, err := o.value(want.Options[id]); err != nil {
				plan.Skipped = append(plan.Skipped, BackupSkip{Plugin: want.Name, ID: id, Reason: err.Error()})
				continue
			}
			from, has := have.Options[id]
			if has && compactJSON(from) == compactJSON(want.Options[id]) {
				continue
			}
			if !has {
				from = json.RawMessage("null")
			}
			plan.Changes = append(plan.Changes, BackupChange{Plugin: want.Name, What: "option", ID: id, Title: o.Title, From: from, To: want.Options[id]})
		}
		for _, o := range man.Options { // set now, at the default in the backup
			if _, inBackup := want.Options[o.ID]; inBackup || left[o.ID] || secretOption(o) {
				continue
			}
			if from, has := have.Options[o.ID]; has {
				plan.Changes = append(plan.Changes, BackupChange{Plugin: want.Name, What: "option", ID: o.ID, Title: o.Title, From: from, To: json.RawMessage("null")})
			}
		}
	}
	if b.AutoCheck != now.AutoCheck {
		plan.Changes = append(plan.Changes, BackupChange{What: "autoCheck", From: rawJSON(now.AutoCheck), To: rawJSON(b.AutoCheck)})
	}
	have := map[string]CatalogSource{}
	for _, c := range now.Catalogs {
		have[strings.ToLower(c.GitHub)] = c
	}
	want := map[string]bool{}
	for _, c := range b.Catalogs {
		want[strings.ToLower(c.GitHub)] = true
		if h, ok := have[strings.ToLower(c.GitHub)]; ok && h.PublicKey == c.PublicKey {
			continue
		}
		from := json.RawMessage("null")
		if h, ok := have[strings.ToLower(c.GitHub)]; ok {
			from = rawJSON(h)
		}
		plan.Changes = append(plan.Changes, BackupChange{What: "catalog", ID: c.GitHub, From: from, To: rawJSON(c)})
	}
	for _, c := range now.Catalogs {
		if !want[strings.ToLower(c.GitHub)] {
			plan.Changes = append(plan.Changes, BackupChange{What: "catalog", ID: c.GitHub, From: rawJSON(c), To: json.RawMessage("null")})
		}
	}
	if b.ServicesOff != nil { // RØDE's services: what the backup has switched off, and nothing else
		offNow, offThen := map[string]bool{}, map[string]bool{}
		for _, id := range *now.ServicesOff {
			offNow[id] = true
		}
		for _, id := range *b.ServicesOff {
			offThen[id] = true
		}
		for _, sv := range defaultServices {
			if offNow[sv.ID] != offThen[sv.ID] {
				plan.Changes = append(plan.Changes, BackupChange{What: "service", ID: sv.ID, From: rawJSON(!offNow[sv.ID]), To: rawJSON(!offThen[sv.ID])})
			}
		}
		for _, id := range *b.ServicesOff {
			if _, known := (&Services{catalog: defaultServices}).lookup(id); !known {
				plan.Skipped = append(plan.Skipped, BackupSkip{Plugin: "rcplug", ID: id, Reason: "this rcplug does not know the RØDE service"})
			}
		}
	}
	return plan
}

// restore carries out a plan: plugins switched off first, then the options
// (a running plugin restarts with them), then plugins switched on.
func (m *Manager) restore(plan BackupPlan, services *Services) (catalogs bool, errs []string) {
	options := map[string]map[string]json.RawMessage{}
	var on, off []string
	for _, c := range plan.Changes {
		switch c.What {
		case "enabled":
			if string(c.To) == "true" {
				on = append(on, c.Plugin)
			} else {
				off = append(off, c.Plugin)
			}
		case "option":
			if options[c.Plugin] == nil {
				options[c.Plugin] = map[string]json.RawMessage{}
			}
			options[c.Plugin][c.ID] = c.To
		case "autoCheck":
			m.SetAutoCheck(string(c.To) == "true")
		case "service":
			var err error
			switch {
			case services == nil:
				err = errors.New("switching RØDE's services is not available here")
			case string(c.To) == "true":
				err = services.On(c.ID)
			default:
				err = services.Off(context.Background(), c.ID)
			}
			if err != nil {
				errs = append(errs, fmt.Sprintf("RØDE service %s: %v", c.ID, err))
			}
		case "catalog":
			catalogs = true
			if string(c.To) == "null" {
				m.RemoveCatalog(c.ID)
				continue
			}
			var src CatalogSource
			if err := json.Unmarshal(c.To, &src); err != nil {
				errs = append(errs, fmt.Sprintf("catalog %s: %v", c.ID, err))
			} else if err := m.AddCatalog(src); err != nil {
				errs = append(errs, fmt.Sprintf("catalog %s: %v", c.ID, err))
			}
		}
	}
	for _, name := range off {
		m.Disable(name)
	}
	names := make([]string, 0, len(options))
	for n := range options {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, _, err := m.SetOptions(name, options[name]); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
		}
	}
	for _, name := range on {
		m.Enable(name)
	}
	return catalogs, errs
}

func readBackup(w http.ResponseWriter, r *http.Request) (RcplugBackup, bool) {
	var b RcplugBackup
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&b); err != nil {
		sdk.WriteError(w, sdk.NewError(http.StatusBadRequest, "bad_backup", "invalid backup: "+err.Error()))
		return b, false
	}
	if b.Kind != backupKind || b.Format != BackupFormat {
		sdk.WriteError(w, sdk.NewError(http.StatusBadRequest, "bad_backup", "not a backup of rcplug's settings in a format this version knows"))
		return b, false
	}
	return b, true
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.m.Backup(s.version))
}

func (s *Server) handleBackupDiff(w http.ResponseWriter, r *http.Request) {
	if b, ok := readBackup(w, r); ok {
		writeJSON(w, s.m.plan(b))
	}
}

func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	b, ok := readBackup(w, r)
	if !ok {
		return
	}
	plan := s.m.plan(b)
	catalogs, errs := s.m.restore(plan, s.Services)
	if catalogs && s.Updates != nil {
		s.Updates.forgetCatalog()
	}
	if errs == nil {
		errs = []string{}
	}
	writeJSON(w, map[string]interface{}{"plan": plan, "errors": errs})
}
