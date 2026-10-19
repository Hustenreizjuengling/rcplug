package host

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Hustenreizjuengling/rcplug/sdk"
	"github.com/Hustenreizjuengling/rcplug/sdk/release"
)

// The plugin catalog: plugins users install from the dashboard. A catalog
// is a GitHub repository whose latest release is signed like an update
// (release.json with release.json.sig); each plugin package in it is an
// entry, described by the info the release tool took from its plugin.json.
// The official catalog is rcplug-plugins, signed with rcplug's own keys.
// Users add other catalogs with the public key their author signs with;
// that key is trusted for that repository only.
//
// Installing downloads, verifies and unpacks the package like an update,
// checks that it is the plugin and version the catalog listed and asks for
// no permission the catalog did not show, then moves it into plugins/.

// DefaultCatalog is the official catalog.
const DefaultCatalog = "Hustenreizjuengling/rcplug-plugins"

// CatalogSource is a repository that offers plugins.
type CatalogSource struct {
	GitHub    string `json:"github"`
	PublicKey string `json:"publicKey,omitempty"` // trusted for this repository besides rcplug's keys
}

const maxCatalogSources = 10

var repoName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}$`)

// CatalogEntry is one plugin a catalog offers.
type CatalogEntry struct {
	Name         string                       `json:"name"`
	Title        string                       `json:"title,omitempty"`
	Description  string                       `json:"description,omitempty"`
	Icon         string                       `json:"icon,omitempty"`
	Translations map[string]map[string]string `json:"translations,omitempty"`
	Permissions  []string                     `json:"permissions"`
	Version      string                       `json:"version"`
	Size         int64                        `json:"size"`
	Repo         string                       `json:"repo"`
	Official     bool                         `json:"official"`
	Page         string                       `json:"page,omitempty"`
	// Installed is the installed version ("" = not installed).
	Installed string `json:"installed,omitempty"`
}

// CatalogSourceStatus is what the last look at a catalog found.
type CatalogSourceStatus struct {
	GitHub   string `json:"github"`
	Official bool   `json:"official"`
	KeyID    string `json:"keyId,omitempty"`
	Release  string `json:"release,omitempty"`
	Plugins  int    `json:"plugins"`
	Error    string `json:"error,omitempty"`
}

// CatalogStatus is the dashboard's catalog.
type CatalogStatus struct {
	Enabled   bool                  `json:"enabled"`
	CheckedAt *time.Time            `json:"checkedAt,omitempty"`
	Sources   []CatalogSourceStatus `json:"sources"`
	Plugins   []CatalogEntry        `json:"plugins"`
	Job       *JobStatus            `json:"job,omitempty"`
}

// platformPlugins come with rcplug's device package (and come back with
// every update of it): they cannot be removed on their own.
var platformPlugins = map[string]bool{"console": true}

// catalogRepo is the official catalog's repository.
func (u *Updater) catalogRepo() string {
	if u.cfg.CatalogRepo != "" {
		return u.cfg.CatalogRepo
	}
	return DefaultCatalog
}

// sources lists the official catalog and the ones the user added.
func (u *Updater) sources() []CatalogSource {
	out := []CatalogSource{{GitHub: u.catalogRepo()}}
	for _, s := range u.m.Catalogs() {
		if s.GitHub != u.catalogRepo() {
			out = append(out, s)
		}
	}
	return out
}

// Catalog returns what the last look found, with the installed versions as
// they are now.
func (u *Updater) Catalog() CatalogStatus {
	u.mu.Lock()
	st := u.catalog
	st.Sources = append([]CatalogSourceStatus(nil), st.Sources...)
	st.Plugins = append([]CatalogEntry(nil), st.Plugins...)
	if u.job != nil {
		st.Job = u.job.snapshot()
	}
	u.mu.Unlock()
	st.Enabled = !u.cfg.Disabled
	if st.Sources == nil {
		for _, s := range u.sources() {
			st.Sources = append(st.Sources, CatalogSourceStatus{GitHub: s.GitHub, Official: s.GitHub == u.catalogRepo(), KeyID: keyID(s.PublicKey)})
		}
	}
	installed := map[string]string{}
	for _, man := range u.m.Manifests() {
		installed[man.Name] = man.Version
		if installed[man.Name] == "" {
			installed[man.Name] = "?"
		}
	}
	for i := range st.Plugins {
		st.Plugins[i].Installed = installed[st.Plugins[i].Name]
	}
	if st.Plugins == nil {
		st.Plugins = []CatalogEntry{}
	}
	return st
}

func keyID(pub string) string {
	if pub == "" {
		return ""
	}
	pk, err := release.ParsePublicKey(pub)
	if err != nil {
		return ""
	}
	return release.KeyID(pk)
}

// RefreshCatalog reads every catalog's latest release (at most every 20 s).
func (u *Updater) RefreshCatalog(ctx context.Context) CatalogStatus {
	if u.cfg.Disabled {
		return u.Catalog()
	}
	u.mu.Lock()
	if u.catChecking || (!u.catChecked.IsZero() && time.Since(u.catChecked) < checkEvery) {
		u.mu.Unlock()
		return u.Catalog()
	}
	u.catChecking = true
	u.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var sources []CatalogSourceStatus
	var entries []CatalogEntry
	found := map[string]*foundUpdate{}
	for _, src := range u.sources() {
		official := src.GitHub == u.catalogRepo()
		ss := CatalogSourceStatus{GitHub: src.GitHub, Official: official, KeyID: keyID(src.PublicKey)}
		keys := u.keys
		if src.PublicKey != "" {
			pk, err := release.ParsePublicKey(src.PublicKey) // checked when it was added
			if err == nil {
				keys = append(append([]ed25519.PublicKey(nil), u.keys...), pk)
			}
		}
		rel, err := u.fetch(ctx, src.GitHub, keys)
		if err != nil {
			ss.Error = err.Error()
			sources = append(sources, ss)
			continue
		}
		ss.Release = rel.manifest.Version
		for _, a := range rel.manifest.Assets {
			if a.Kind != release.KindPlugin || rel.urls[a.Name] == "" {
				continue
			}
			e := CatalogEntry{Name: a.Plugin, Version: a.Version, Size: a.Size, Repo: src.GitHub, Official: official, Page: rel.page, Permissions: []string{}}
			if in := a.Info; in != nil {
				e.Title, e.Description, e.Icon, e.Translations = in.Title, in.Description, in.Icon, in.Translations
				for _, p := range in.Permissions {
					if knownPermissions[p] {
						e.Permissions = append(e.Permissions, p)
					}
				}
			}
			entries = append(entries, e)
			found[src.GitHub+"#"+a.Plugin] = &foundUpdate{
				target: UpdateTarget{Target: "install:" + a.Plugin, Name: a.Plugin, Repo: src.GitHub, Latest: a.Version, Available: true, URL: rel.page},
				asset:  a, url: rel.urls[a.Name], perms: e.Permissions,
			}
			ss.Plugins++
		}
		sources = append(sources, ss)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Official != entries[j].Official {
			return entries[i].Official
		}
		return entries[i].Name < entries[j].Name
	})
	now := time.Now()
	u.mu.Lock()
	u.catChecking, u.catChecked = false, now
	u.catalog = CatalogStatus{CheckedAt: &now, Sources: sources, Plugins: entries}
	u.catFound = found
	u.mu.Unlock()
	return u.Catalog()
}

// InstallFromCatalog installs a plugin the last look at the catalogs found.
func (u *Updater) InstallFromCatalog(repo, name string) error {
	if u.cfg.Disabled {
		return errors.New("updates and the catalog are switched off")
	}
	if u.m.get(name) != nil {
		return fmt.Errorf("%s is installed already; updates come through the System page", name)
	}
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid plugin name %q", name)
	}
	if _, err := os.Lstat(filepath.Join(u.m.pluginsDir, name)); err == nil {
		return fmt.Errorf("plugins/%s exists already (but is not a valid plugin); remove it first", name)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.busyLocked() {
		return ErrUpdateBusy
	}
	fu := u.catFound[repo+"#"+name]
	if fu == nil {
		return fmt.Errorf("%s is not in the catalog %s; look again", name, repo)
	}
	j := &updateJob{st: JobStatus{Target: fu.target.Target, Version: fu.target.Latest, State: "running", Started: time.Now()}}
	u.job = j
	go u.run(j, fu)
	return nil
}

// installNew puts a verified package from a catalog into plugins/.
func (u *Updater) installNew(j *updateJob, fu *foundUpdate, dir, pkg string) error {
	name := fu.target.Name
	staged, man, err := u.stagePlugin(j, fu, dir, pkg)
	if err != nil {
		return err
	}
	shown := map[string]bool{}
	for _, p := range fu.perms {
		shown[p] = true
	}
	for _, p := range man.Permissions {
		if !shown[p] {
			return fmt.Errorf("the package asks for the permission %q, which the catalog did not show; not installed", p)
		}
	}
	j.progress("install", 0, 0)
	started, err := u.m.AddPlugin(name, staged)
	if err != nil {
		return err
	}
	_ = os.RemoveAll(dir)
	if started {
		j.logf("%s %s installed from %s and started", name, man.Version, fu.target.Repo)
	} else {
		j.logf("%s %s installed from %s; start it on the Plugins page", name, man.Version, fu.target.Repo)
	}
	u.m.hostEvent("host.plugin.installed", map[string]string{"name": name, "version": man.Version, "repo": fu.target.Repo})
	return nil
}

// AddPlugin moves a staged plugin directory into plugins/ and loads it; it
// starts if its manifest says autostart. It refuses to replace anything.
func (m *Manager) AddPlugin(name, staged string) (started bool, err error) {
	if !validName.MatchString(name) {
		return false, fmt.Errorf("invalid plugin name %q", name)
	}
	target := filepath.Join(m.pluginsDir, name)
	if err := os.MkdirAll(m.pluginsDir, 0o755); err != nil {
		return false, err
	}
	if _, err := os.Lstat(target); err == nil {
		return false, fmt.Errorf("plugins/%s exists already", name)
	}
	if err := os.Rename(staged, target); err != nil {
		return false, err
	}
	if _, err := m.Load(); err != nil {
		return false, err
	}
	p := m.get(name)
	if p == nil {
		return false, fmt.Errorf("%s did not load", name)
	}
	p.log.add("installed version " + p.Manifest().Version)
	p.mu.Lock()
	started = p.enabled
	p.mu.Unlock()
	return started, nil
}

// RemovePlugin stops a plugin and deletes its directory; with purge also
// its data, settings and logs. Plugins of rcplug's device package stay.
func (m *Manager) RemovePlugin(name string, purge bool) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid plugin name %q", name)
	}
	if platformPlugins[name] {
		return fmt.Errorf("%s is part of rcplug and cannot be removed on its own", name)
	}
	dir := filepath.Join(m.pluginsDir, name)
	if _, err := os.Lstat(dir); err != nil {
		return fmt.Errorf("%s is not installed", name)
	}
	if p := m.get(name); p != nil {
		p.stop()
	}
	// Out of plugins/ first, so that a rescan never sees half of it.
	trash := filepath.Join(m.root, fmt.Sprintf(".removed-%s-%d", name, time.Now().UnixNano()))
	if err := os.Rename(dir, trash); err != nil {
		return err
	}
	if _, err := m.Load(); err != nil {
		return err
	}
	_ = os.RemoveAll(trash)
	m.saveMu.Lock()
	ps := m.loadState()
	delete(ps.Enabled, name)
	_ = sdk.SaveJSON(m.stateFile(), ps)
	m.saveMu.Unlock()
	if purge {
		_ = os.RemoveAll(m.dataDir(name))
		_ = os.Remove(filepath.Join(m.root, "config", name+".json"))
		logs, _ := filepath.Glob(filepath.Join(m.LogDir(), name+".log*"))
		for _, f := range logs {
			_ = os.Remove(f)
		}
	}
	m.hostEvent("host.plugin.removed", map[string]interface{}{"name": name, "purged": purge})
	return nil
}

// Catalogs are the catalogs the user added.
func (m *Manager) Catalogs() []CatalogSource { return m.loadState().Catalogs }

// AddCatalog adds a catalog, or changes the key of one already added.
func (m *Manager) AddCatalog(src CatalogSource) error {
	src.GitHub = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(src.GitHub), "https://github.com/"), "github.com/"))
	src.GitHub = strings.TrimSuffix(src.GitHub, "/")
	src.PublicKey = strings.TrimSpace(src.PublicKey)
	if !repoName.MatchString(src.GitHub) {
		return errors.New("the repository is owner/name on GitHub")
	}
	if src.PublicKey == "" {
		return errors.New("a catalog needs the public key its releases are signed with")
	}
	if _, err := release.ParsePublicKey(src.PublicKey); err != nil {
		return fmt.Errorf("public key: %w", err)
	}
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	ps := m.loadState()
	for i, s := range ps.Catalogs {
		if strings.EqualFold(s.GitHub, src.GitHub) {
			ps.Catalogs[i] = src
			return sdk.SaveJSON(m.stateFile(), ps)
		}
	}
	if len(ps.Catalogs) >= maxCatalogSources {
		return fmt.Errorf("at most %d catalogs", maxCatalogSources)
	}
	ps.Catalogs = append(ps.Catalogs, src)
	return sdk.SaveJSON(m.stateFile(), ps)
}

// RemoveCatalog forgets a catalog the user added.
func (m *Manager) RemoveCatalog(repo string) bool {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	ps := m.loadState()
	for i, s := range ps.Catalogs {
		if strings.EqualFold(s.GitHub, repo) {
			ps.Catalogs = append(ps.Catalogs[:i], ps.Catalogs[i+1:]...)
			_ = sdk.SaveJSON(m.stateFile(), ps)
			return true
		}
	}
	return false
}

// CleanLeftovers removes what an update or a removal leaves when the device
// loses power halfway: <root>/.update (the downloaded package, up to a few
// hundred MB) and <root>/.removed-* (a plugin on its way out). Only what is
// older than half an hour goes, so an installer that is still finishing
// keeps its files. It returns what it removed.
func (m *Manager) CleanLeftovers() []string {
	var gone []string
	found, _ := filepath.Glob(filepath.Join(m.root, ".removed-*"))
	found = append(found, filepath.Join(m.root, ".update"))
	for _, p := range found {
		fi, err := os.Lstat(p)
		if err != nil || time.Since(fi.ModTime()) < 30*time.Minute {
			continue
		}
		if os.RemoveAll(p) == nil {
			gone = append(gone, filepath.Base(p))
		}
	}
	return gone
}

// ---- HTTP

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	if s.Updates == nil {
		writeJSON(w, CatalogStatus{Sources: []CatalogSourceStatus{}, Plugins: []CatalogEntry{}})
		return
	}
	if r.URL.Query().Get("refresh") == "1" {
		writeJSON(w, s.Updates.RefreshCatalog(r.Context()))
		return
	}
	writeJSON(w, s.Updates.Catalog())
}

func (s *Server) handleCatalogInstall(w http.ResponseWriter, r *http.Request) {
	if s.Updates == nil {
		sdk.WriteError(w, sdk.NewError(http.StatusNotFound, "updates_off", "updates and the catalog are switched off"))
		return
	}
	var req struct {
		Repo   string `json:"repo"`
		Plugin string `json:"plugin"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Updates.InstallFromCatalog(req.Repo, req.Plugin); err != nil {
		code := "install_failed"
		if err == ErrUpdateBusy {
			code = "update_busy"
		}
		sdk.WriteError(w, sdk.NewError(http.StatusConflict, code, err.Error()))
		return
	}
	writeJSON(w, s.Updates.Catalog())
}

func (s *Server) handleCatalogSource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GitHub    string `json:"github"`
		PublicKey string `json:"publicKey"`
		Remove    bool   `json:"remove"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Remove {
		if !s.m.RemoveCatalog(req.GitHub) {
			sdk.WriteError(w, sdk.NewError(http.StatusNotFound, "not_found", "no such catalog"))
			return
		}
	} else if err := s.m.AddCatalog(CatalogSource{GitHub: req.GitHub, PublicKey: req.PublicKey}); err != nil {
		sdk.WriteError(w, sdk.NewError(http.StatusBadRequest, "bad_catalog", err.Error()))
		return
	}
	if s.Updates == nil {
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	s.Updates.forgetCatalog()
	writeJSON(w, s.Updates.RefreshCatalog(r.Context()))
}

// forgetCatalog makes the next RefreshCatalog look again at once.
func (u *Updater) forgetCatalog() {
	u.mu.Lock()
	u.catChecked = time.Time{}
	u.mu.Unlock()
}

func (s *Server) handleRemovePlugin(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		Confirm string `json:"confirm"`
		Purge   bool   `json:"purge"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Confirm != name {
		sdk.WriteError(w, sdk.NewError(http.StatusBadRequest, "confirm", `send {"confirm": "<the plugin's name>"}`))
		return
	}
	if err := s.m.RemovePlugin(name, req.Purge); err != nil {
		code := http.StatusConflict
		if strings.HasSuffix(err.Error(), "is not installed") {
			code = http.StatusNotFound
		}
		sdk.WriteError(w, sdk.NewError(code, "remove_failed", err.Error()))
		return
	}
	writeJSON(w, json.RawMessage(`{"ok":true}`))
}
