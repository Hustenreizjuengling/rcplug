package host

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Hustenreizjuengling/rcplug/sdk/release"
)

// In-app updates: rcplug and plugins with an "update" source in plugin.json
// update from signed GitHub releases (sdk/release, docs/updates.md). The
// dashboard asks for a check and shows what is available; nothing is
// downloaded or installed unless the operator asks for it.
//
// Every release must carry release.json, signed (release.json.sig) by one of
// the trusted keys, and every installed file must match its size and SHA-256
// there. rcplug itself is installed by its own install.sh, started in a
// separate session because it stops and replaces this process; a plugin is
// replaced in place and restarted, its data kept.

// DefaultUpdateRepo is where rcplug's own releases are published.
const DefaultUpdateRepo = "Hustenreizjuengling/rcplug"

const (
	maxPackage  = 200 << 20 // download limit for one package
	maxUnpacked = 400 << 20 // and what it may unpack to
	checkEvery  = 20 * time.Second
)

// UpdateConfig configures the updater.
type UpdateConfig struct {
	Disabled     bool
	API          string              // GitHub API base URL (default https://api.github.com)
	PlatformRepo string              // owner/name of rcplug's repository (default DefaultUpdateRepo)
	ExtraKeys    []ed25519.PublicKey // trusted in addition to the built-in release keys
	Client       *http.Client        // nil: a client with timeouts
}

// Updater checks for and installs updates.
type Updater struct {
	cfg     UpdateConfig
	m       *Manager
	version string
	keys    []ed25519.PublicKey
	spawn   func(payload, logFile, resultFile string) error

	mu       sync.Mutex
	status   UpdateStatus
	checked  time.Time
	checking bool
	found    map[string]*foundUpdate // target → what a check found
	job      *updateJob
}

// UpdateStatus is what the dashboard shows.
type UpdateStatus struct {
	Enabled   bool           `json:"enabled"`
	Current   string         `json:"current"`
	CheckedAt *time.Time     `json:"checkedAt,omitempty"`
	Error     string         `json:"error,omitempty"`
	Targets   []UpdateTarget `json:"targets"`
	Job       *JobStatus     `json:"job,omitempty"`
	Last      *LastUpdate    `json:"last,omitempty"`
	KeyIDs    []string       `json:"keyIds"`
}

// UpdateTarget is rcplug itself ("rcplug") or one plugin ("plugin:<name>").
type UpdateTarget struct {
	Target    string `json:"target"`
	Name      string `json:"name"`
	Repo      string `json:"repo"`
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	Notes     string `json:"notes,omitempty"`
	URL       string `json:"url,omitempty"`
	Error     string `json:"error,omitempty"`
}

// JobStatus is the state of an install.
type JobStatus struct {
	Target  string    `json:"target"`
	Version string    `json:"version"`
	State   string    `json:"state"` // running, restarting, done, error
	Log     []string  `json:"log"`
	Error   string    `json:"error,omitempty"`
	Started time.Time `json:"started"`
}

// LastUpdate is the outcome of the last update of rcplug itself, as its
// installer recorded it.
type LastUpdate struct {
	OK   bool     `json:"ok"`
	At   string   `json:"at"`
	From string   `json:"from,omitempty"`
	To   string   `json:"to,omitempty"`
	Log  []string `json:"log,omitempty"`
}

// updateStamp is the line installPlatform writes to update.log first.
var updateStamp = regexp.MustCompile(`(?m)^==== \S+: updating rcplug (\S+) to (\S+)$`)

type foundUpdate struct {
	target UpdateTarget
	asset  release.Asset
	url    string // download URL of the asset
}

type updateJob struct {
	mu sync.Mutex
	st JobStatus
}

func (j *updateJob) logf(format string, args ...interface{}) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.st.Log = append(j.st.Log, fmt.Sprintf(format, args...))
}

func (j *updateJob) snapshot() *JobStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	st := j.st
	st.Log = append([]string(nil), j.st.Log...)
	return &st
}

// NewUpdater makes an updater for the host running version.
func NewUpdater(m *Manager, version string, cfg UpdateConfig) *Updater {
	if cfg.API == "" {
		cfg.API = "https://api.github.com"
	}
	cfg.API = strings.TrimRight(cfg.API, "/")
	if cfg.PlatformRepo == "" {
		cfg.PlatformRepo = DefaultUpdateRepo
	}
	u := &Updater{cfg: cfg, m: m, version: version, spawn: spawnInstaller, found: map[string]*foundUpdate{}}
	for _, k := range release.BuiltinKeys {
		if pk, err := release.ParsePublicKey(k); err == nil {
			u.keys = append(u.keys, pk)
		}
	}
	u.keys = append(u.keys, cfg.ExtraKeys...)
	if cfg.Client == nil {
		u.cfg.Client = &http.Client{Timeout: 10 * time.Minute}
	}
	c := *u.cfg.Client
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return allowedURL(req.URL)
	}
	u.cfg.Client = &c
	return u
}

// allowedURL accepts https, and plain http to the loopback interface (a test
// server or a local mirror).
func allowedURL(u *url.URL) error {
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		if host == "localhost" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("refusing to download from %s (https only)", u.Redacted())
}

// Status returns the last check and the current install, if any.
func (u *Updater) Status() UpdateStatus {
	u.mu.Lock()
	st := u.status
	st.Targets = append([]UpdateTarget(nil), u.status.Targets...)
	if u.job != nil {
		st.Job = u.job.snapshot()
	}
	u.mu.Unlock()
	st.Enabled, st.Current = !u.cfg.Disabled, u.version
	st.Last = u.lastUpdate()
	st.KeyIDs = nil
	for _, k := range u.keys {
		st.KeyIDs = append(st.KeyIDs, release.KeyID(k))
	}
	return st
}

// lastUpdate reads what the installer recorded about the last update of
// rcplug itself.
func (u *Updater) lastUpdate() *LastUpdate {
	raw, err := os.ReadFile(filepath.Join(u.m.LogDir(), "update.result"))
	if err != nil {
		return nil
	}
	f := strings.Fields(string(raw))
	if len(f) < 2 {
		return nil
	}
	lu := &LastUpdate{OK: f[0] == "0", At: f[1]}
	raw, err = os.ReadFile(filepath.Join(u.m.LogDir(), "update.log"))
	if err != nil {
		return lu
	}
	if all := updateStamp.FindAllSubmatch(raw, -1); len(all) > 0 {
		last := all[len(all)-1]
		lu.From, lu.To = string(last[1]), string(last[2])
	}
	if !lu.OK {
		lu.Log = strings.Split(strings.TrimRight(string(tailLines(raw, 30)), "\n"), "\n")
	}
	return lu
}

// Check asks GitHub for the latest releases of rcplug and of every plugin
// with an update source. A second check within 20 s returns the first one.
func (u *Updater) Check(ctx context.Context) UpdateStatus {
	if u.cfg.Disabled {
		return u.Status()
	}
	u.mu.Lock()
	if u.checking || (!u.checked.IsZero() && time.Since(u.checked) < checkEvery) {
		u.mu.Unlock()
		return u.Status()
	}
	u.checking = true
	u.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	found := map[string]*foundUpdate{}
	var targets []UpdateTarget

	// rcplug itself
	pt := UpdateTarget{Target: "rcplug", Name: "rcplug", Repo: u.cfg.PlatformRepo, Current: u.version}
	if rel, err := u.fetch(ctx, u.cfg.PlatformRepo, u.keys); err != nil {
		pt.Error = err.Error()
	} else if a, ok := rel.manifest.Platform(); !ok {
		pt.Error = "the latest release has no device package"
	} else {
		pt.Latest, pt.Notes, pt.URL = rel.manifest.Version, rel.notes, rel.page
		pt.Available = release.Newer(pt.Latest, u.version)
		found[pt.Target] = &foundUpdate{target: pt, asset: a, url: rel.urls[a.Name]}
	}
	targets = append(targets, pt)

	// plugins, one request per repository and key
	type source struct{ repo, key string }
	releases := map[source]*fetched{}
	errs := map[source]error{}
	for _, man := range u.m.Manifests() {
		if man.Update == nil {
			continue
		}
		src := source{man.Update.GitHub, man.Update.PublicKey}
		t := UpdateTarget{Target: "plugin:" + man.Name, Name: man.Name, Repo: src.repo, Current: man.Version}
		rel, done := releases[src]
		if !done && errs[src] == nil {
			keys := u.keys
			if src.key != "" {
				pk, _ := release.ParsePublicKey(src.key) // checked when the manifest was loaded
				keys = append(append([]ed25519.PublicKey(nil), u.keys...), pk)
			}
			var err error
			if rel, err = u.fetch(ctx, src.repo, keys); err != nil {
				errs[src] = err
			} else {
				releases[src] = rel
			}
		}
		switch {
		case errs[src] != nil:
			t.Error = errs[src].Error()
		default:
			a, ok := rel.manifest.Plugin(man.Name)
			if !ok {
				t.Error = fmt.Sprintf("the latest release of %s has no package for %s", src.repo, man.Name)
				break
			}
			t.Latest, t.Notes, t.URL = a.Version, rel.notes, rel.page
			t.Available = release.Newer(a.Version, man.Version)
			found[t.Target] = &foundUpdate{target: t, asset: a, url: rel.urls[a.Name]}
		}
		targets = append(targets, t)
	}
	sort.SliceStable(targets[1:], func(i, j int) bool { return targets[1+i].Name < targets[1+j].Name })

	now := time.Now()
	u.mu.Lock()
	u.checking, u.checked = false, now
	u.status = UpdateStatus{CheckedAt: &now, Targets: targets}
	u.found = found
	u.mu.Unlock()
	return u.Status()
}

// fetched is a verified release.
type fetched struct {
	manifest *release.Manifest
	urls     map[string]string // asset name → download URL
	notes    string
	page     string
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// fetch gets the latest release of repo and checks its signed manifest.
func (u *Updater) fetch(ctx context.Context, repo string, keys []ed25519.PublicKey) (*fetched, error) {
	var gr ghRelease
	err := u.getJSON(ctx, u.cfg.API+"/repos/"+repo+"/releases/latest", &gr)
	var he *httpError
	if errors.As(err, &he) && he.code == http.StatusNotFound {
		return nil, fmt.Errorf("%s has no release yet (or is not public)", repo)
	}
	if err != nil {
		return nil, err
	}
	f := &fetched{urls: map[string]string{}, page: gr.HTMLURL}
	for _, a := range gr.Assets {
		f.urls[a.Name] = a.URL
	}
	if f.urls[release.ManifestName] == "" || f.urls[release.SignatureName] == "" {
		return nil, fmt.Errorf("the latest release of %s (%s) is not signed: it has no %s", repo, gr.TagName, release.ManifestName)
	}
	data, err := u.getSmall(ctx, f.urls[release.ManifestName])
	if err != nil {
		return nil, err
	}
	sig, err := u.getSmall(ctx, f.urls[release.SignatureName])
	if err != nil {
		return nil, err
	}
	if _, err := release.Verify(data, sig, keys); err != nil {
		return nil, fmt.Errorf("%s %s: %w", repo, gr.TagName, err)
	}
	m, err := release.Parse(data)
	if err != nil {
		return nil, err
	}
	if name := repo[strings.IndexByte(repo, '/')+1:]; m.Project != name {
		return nil, fmt.Errorf("%s %s: the manifest is for %q, not for %s", repo, gr.TagName, m.Project, name)
	}
	if m.Version != strings.TrimPrefix(gr.TagName, "v") {
		return nil, fmt.Errorf("%s: the manifest says version %s, the release is %s", repo, m.Version, gr.TagName)
	}
	f.manifest = m
	f.notes = gr.Body
	if len(f.notes) > 4000 {
		f.notes = f.notes[:4000] + "…"
	}
	return f, nil
}

type httpError struct {
	code int
	url  string
}

func (e *httpError) Error() string { return fmt.Sprintf("%s: HTTP %d", e.url, e.code) }

func (u *Updater) get(ctx context.Context, raw string) (*http.Response, error) {
	pu, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if err := allowedURL(pu); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "rcplug/"+u.version)
	req.Header.Set("Accept", "application/vnd.github+json, application/octet-stream;q=0.9, */*;q=0.8")
	resp, err := u.cfg.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, &httpError{resp.StatusCode, pu.Redacted()}
	}
	return resp, nil
}

func (u *Updater) getJSON(ctx context.Context, raw string, v interface{}) error {
	resp, err := u.get(ctx, raw)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
}

func (u *Updater) getSmall(ctx context.Context, raw string) ([]byte, error) {
	resp, err := u.get(ctx, raw)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, release.MaxManifestSize+1))
	if err != nil {
		return nil, err
	}
	if len(b) > release.MaxManifestSize {
		return nil, fmt.Errorf("%s is too large", raw)
	}
	return b, nil
}

// download saves an asset to dst and checks its size and SHA-256 against
// the signed manifest.
func (u *Updater) download(ctx context.Context, raw, dst string, a release.Asset) error {
	if a.Size > maxPackage {
		return fmt.Errorf("%s is larger than %d MB", a.Name, maxPackage>>20)
	}
	resp, err := u.get(ctx, raw)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(resp.Body, a.Size+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return fmt.Errorf("%s does not match the signed manifest (size or SHA-256)", a.Name)
	}
	return nil
}

// ErrUpdateBusy is returned while another install runs.
var ErrUpdateBusy = errors.New("an update is being installed")

// Install starts installing an update that the last check found.
func (u *Updater) Install(target string) error {
	if u.cfg.Disabled {
		return errors.New("updates are switched off")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.job != nil {
		if st := u.job.snapshot().State; st == "running" || st == "restarting" {
			return ErrUpdateBusy
		}
	}
	fu := u.found[target]
	if fu == nil || !fu.target.Available || fu.url == "" {
		return fmt.Errorf("no update for %s is available; check for updates first", target)
	}
	j := &updateJob{st: JobStatus{Target: target, Version: fu.target.Latest, State: "running", Started: time.Now()}}
	u.job = j
	go u.run(j, fu)
	return nil
}

func (u *Updater) run(j *updateJob, fu *foundUpdate) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	state, err := u.install(ctx, j, fu)
	if err != nil {
		// Nothing of a failed attempt stays (the package may be hundreds of MB).
		_ = os.RemoveAll(filepath.Join(u.m.Root(), ".update"))
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err != nil {
		j.st.State, j.st.Error = "error", err.Error()
		j.st.Log = append(j.st.Log, "failed: "+err.Error())
		return
	}
	j.st.State = state
}

func (u *Updater) install(ctx context.Context, j *updateJob, fu *foundUpdate) (string, error) {
	dir := filepath.Join(u.m.Root(), ".update")
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if free, _, ok := diskStats(u.m.Root()); ok && free*(1<<20) < 3*fu.asset.Size+(20<<20) {
		return "", fmt.Errorf("not enough free space: %d MB free", free)
	}
	pkg := filepath.Join(dir, fu.asset.Name)
	j.logf("downloading %s (%d KB)", fu.asset.Name, fu.asset.Size>>10)
	if err := u.download(ctx, fu.url, pkg, fu.asset); err != nil {
		return "", err
	}
	j.logf("signature and SHA-256 checked")
	if fu.target.Target == "rcplug" {
		return u.installPlatform(j, fu, dir, pkg)
	}
	return "done", u.installPlugin(j, fu, dir, pkg)
}

func (u *Updater) installPlatform(j *updateJob, fu *foundUpdate, dir, pkg string) (string, error) {
	payload := filepath.Join(dir, "payload")
	if err := extractTarGz(pkg, payload, maxUnpacked); err != nil {
		return "", err
	}
	_ = os.Remove(pkg)
	if _, err := os.Stat(filepath.Join(payload, "install.sh")); err != nil {
		return "", errors.New("the device package has no install.sh")
	}
	if v, _ := os.ReadFile(filepath.Join(payload, "VERSION")); strings.TrimSpace(string(v)) != fu.target.Latest {
		return "", fmt.Errorf("the device package says version %q, the release is %s", strings.TrimSpace(string(v)), fu.target.Latest)
	}
	logFile := filepath.Join(u.m.LogDir(), "update.log")
	resultFile := filepath.Join(u.m.LogDir(), "update.result")
	_ = os.MkdirAll(u.m.LogDir(), 0o755)
	_ = os.Remove(resultFile)
	stamp := fmt.Sprintf("==== %s: updating rcplug %s to %s\n", time.Now().UTC().Format(time.RFC3339), u.version, fu.target.Latest)
	if f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		f.WriteString(stamp)
		f.Close()
	}
	if err := u.spawn(payload, logFile, resultFile); err != nil {
		return "", err
	}
	j.logf("installer started; rcplug restarts in a few seconds (log: logs/update.log)")
	return "restarting", nil
}

func (u *Updater) installPlugin(j *updateJob, fu *foundUpdate, dir, pkg string) error {
	name := fu.target.Name
	staged := filepath.Join(dir, "plugins", name)
	if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
		return err
	}
	if err := extractTarGz(pkg, staged, maxUnpacked); err != nil {
		return err
	}
	man, err := LoadManifest(staged)
	if err != nil {
		return err
	}
	// LoadManifest takes the name from the directory; the package must
	// declare the same one.
	var declared struct {
		Name string `json:"name"`
	}
	if raw, err := os.ReadFile(filepath.Join(staged, "plugin.json")); err == nil {
		_ = json.Unmarshal(raw, &declared)
	}
	if declared.Name != name {
		return fmt.Errorf("the package is for the plugin %q, not %s", declared.Name, name)
	}
	if man.Version != fu.asset.Version {
		return fmt.Errorf("the package's plugin.json says version %s, the manifest %s", man.Version, fu.asset.Version)
	}
	j.logf("replacing %s %s with %s", name, fu.target.Current, man.Version)
	started, err := u.m.ReplacePlugin(name, staged)
	if err != nil {
		return err
	}
	_ = os.RemoveAll(dir)
	if started {
		j.logf("%s %s installed and restarted; its data and settings are kept", name, man.Version)
	} else {
		j.logf("%s %s installed; it stays stopped, as it was. Its data and settings are kept", name, man.Version)
	}
	u.mu.Lock()
	delete(u.found, fu.target.Target)
	for i := range u.status.Targets {
		if t := &u.status.Targets[i]; t.Target == fu.target.Target {
			t.Current, t.Available = man.Version, false
		}
	}
	u.mu.Unlock()
	return nil
}
