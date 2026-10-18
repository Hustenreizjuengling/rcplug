package host

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Hustenreizjuengling/rcplug/sdk/release"
)

// Manifest is the on-disk description of a plugin, read from plugin.json in
// the plugin's directory. The directory name is the canonical plugin id. The
// full contract is documented in docs/plugin-api.md and plugin.schema.json.
type Manifest struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Version     string `json:"version"`
	// Exec is the command line to run, relative to the plugin directory. The
	// token {{port}} is replaced with the loopback port the host assigned and
	// {{dir}} with the plugin directory.
	Exec []string `json:"exec"`
	// HTTPProxy, when set, is the loopback address the host reverse-proxies
	// to under /plugins/<name>/ ({{port}} is substituted). Leave it empty for
	// a plugin without a web UI.
	HTTPProxy string `json:"httpProxy"`
	// Autostart controls whether the plugin runs by default. The persisted
	// state file and the user config can override this.
	Autostart bool `json:"autostart"`
	// NeedsAudio is advisory metadata for the dashboard.
	NeedsAudio bool `json:"needsAudio"`
	// Primary plugins open as the dashboard's start page.
	Primary bool `json:"primary"`
	// Order sorts plugins in the dashboard navigation (lower first).
	Order int `json:"order"`
	// Icon is a short glyph shown in the navigation.
	Icon string `json:"icon"`
	// Translations holds localised titles and descriptions keyed by
	// language code ("de", …). Title and Description are the English
	// defaults.
	Translations map[string]Translation `json:"translations,omitempty"`
	// Actions are entry points other UIs offer for this plugin, e.g. a
	// button on every channel strip of the console that opens the plugin
	// for that channel. See docs/plugin-api.md#actions.
	Actions []Action `json:"actions,omitempty"`
	// Update says where newer versions of the plugin are published, so the
	// dashboard can update it (see docs/updates.md). Nil: no in-app updates.
	Update *UpdateSource `json:"update,omitempty"`

	// Unknown lists fields of plugin.json this host does not understand
	// (typos, or a manifest written for a newer host).
	Unknown []string `json:"-"`
}

// UpdateSource names the GitHub repository ("owner/name") whose signed
// releases (sdk/release) carry new versions of a plugin. PublicKey, if set,
// is trusted for this plugin in addition to rcplug's own release keys, so a
// third-party plugin can be signed with its author's key.
type UpdateSource struct {
	GitHub    string `json:"github"`
	PublicKey string `json:"publicKey,omitempty"`
}

var githubRepo = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$`)

func validateUpdate(u *UpdateSource) error {
	if u == nil {
		return nil
	}
	if !githubRepo.MatchString(u.GitHub) {
		return fmt.Errorf("update: github %q must be owner/repository", u.GitHub)
	}
	if u.PublicKey != "" {
		if _, err := release.ParsePublicKey(u.PublicKey); err != nil {
			return fmt.Errorf("update: publicKey: %w", err)
		}
	}
	return nil
}

// Translation is a localised title and description of a plugin.
type Translation struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// Action is an entry point into a plugin's web UI that other UIs show in a
// context. Path is relative to the plugin's UI and may contain placeholders
// that the showing UI fills in (for "channel": {strip}, {index}, {source},
// {label}, all URL-encoded).
type Action struct {
	ID           string                       `json:"id"`
	Context      string                       `json:"context"`
	Title        string                       `json:"title"`
	Icon         string                       `json:"icon,omitempty"`
	Path         string                       `json:"path"`
	Translations map[string]ActionTranslation `json:"translations,omitempty"`
}

// ActionTranslation is a localised action title.
type ActionTranslation struct {
	Title string `json:"title,omitempty"`
}

var (
	actionID      = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	actionContext = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	schemeLike    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`) // https:, javascript:, …
)

// validateActions checks the actions of a manifest. Contexts are free-form
// so a plugin written for a newer console still loads; the console shows the
// contexts it knows.
func validateActions(m *Manifest) error {
	seen := map[string]bool{}
	for i, a := range m.Actions {
		switch {
		case m.HTTPProxy == "":
			return fmt.Errorf("actions need a web UI (httpProxy)")
		case !actionID.MatchString(a.ID) || seen[a.ID]:
			return fmt.Errorf("action %d: id %q must be unique and use a-z, 0-9, - and _", i, a.ID)
		case !actionContext.MatchString(a.Context):
			return fmt.Errorf("action %q: invalid context %q", a.ID, a.Context)
		case strings.TrimSpace(a.Title) == "":
			return fmt.Errorf("action %q: title is empty", a.ID)
		case schemeLike.MatchString(a.Path) || strings.HasPrefix(a.Path, "/") || strings.HasPrefix(a.Path, "\\") || strings.ContainsAny(a.Path, "\r\n") || leavesDir(a.Path):
			return fmt.Errorf("action %q: path %q must be relative to the plugin's UI", a.ID, a.Path)
		}
		seen[a.ID] = true
	}
	return nil
}

// leavesDir reports whether a relative URL path has a "." or ".." segment,
// also percent-encoded (browsers resolve %2e%2e like ..), or a backslash.
func leavesDir(p string) bool {
	p, _, _ = strings.Cut(p, "?")
	p, _, _ = strings.Cut(p, "#")
	for _, seg := range strings.Split(p, "/") {
		if d, err := url.PathUnescape(seg); err != nil || d == "." || d == ".." || strings.ContainsAny(d, "/\\") {
			return true
		}
	}
	return false
}

// LoadManifest reads and validates plugin.json from dir.
func LoadManifest(dir string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("plugin.json: %w", err)
	}
	m.Unknown = unknownFields(raw)
	// The directory name is authoritative so a renamed folder can't collide
	// with another plugin's declared name. It is also the URL path segment.
	m.Name = filepath.Base(dir)
	if !validName.MatchString(m.Name) {
		return nil, fmt.Errorf("plugin directory %q: use lower-case letters, digits, '-' and '_'", m.Name)
	}
	if m.Title == "" {
		m.Title = m.Name
	}
	if len(m.Exec) == 0 {
		return nil, fmt.Errorf("plugin %q: exec is empty", m.Name)
	}
	if m.HTTPProxy != "" && !loopbackTarget(m.HTTPProxy) {
		return nil, fmt.Errorf("plugin %q: httpProxy %q must be a loopback host:port such as 127.0.0.1:{{port}}", m.Name, m.HTTPProxy)
	}
	if err := validateActions(&m); err != nil {
		return nil, fmt.Errorf("plugin %q: %w", m.Name, err)
	}
	if err := validateUpdate(m.Update); err != nil {
		return nil, fmt.Errorf("plugin %q: %w", m.Name, err)
	}
	return &m, nil
}

// loopbackTarget reports whether a httpProxy value points at this machine.
// Plugins only ever listen on loopback; the host is their sole entry point.
func loopbackTarget(s string) bool {
	s = strings.TrimPrefix(expand(s, 1, ""), "http://")
	host, port, err := net.SplitHostPort(s)
	if err != nil || port == "" || strings.ContainsAny(port, "/?#") {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

var knownFields = map[string]bool{"$schema": true, "name": true, "title": true, "description": true, "version": true,
	"exec": true, "httpProxy": true, "autostart": true, "needsAudio": true, "primary": true, "order": true, "icon": true, "translations": true, "actions": true, "update": true}

func unknownFields(raw []byte) []string {
	var all map[string]json.RawMessage
	if json.Unmarshal(raw, &all) != nil {
		return nil
	}
	var out []string
	for k := range all {
		if !knownFields[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// UserConfig is the operator's per-plugin overlay at <root>/config/<name>.json.
// It is never touched by installs, so options like a read-only flag survive
// reinstalls (a firmware update removes the whole install directory).
type UserConfig struct {
	// Args are appended to the manifest's exec line.
	Args []string `json:"args"`
	// Env adds environment variables.
	Env map[string]string `json:"env"`
}

// LoadUserConfig reads <root>/config/<name>.json (missing = empty).
func LoadUserConfig(root, name string) (UserConfig, error) {
	var c UserConfig
	raw, err := os.ReadFile(filepath.Join(root, "config", name+".json"))
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("config/%s.json: %w", name, err)
	}
	return c, nil
}

// expand substitutes {{port}} and {{dir}} tokens in a string.
func expand(s string, port int, dir string) string {
	s = strings.ReplaceAll(s, "{{port}}", fmt.Sprintf("%d", port))
	return strings.ReplaceAll(s, "{{dir}}", dir)
}

// maskArgs hides values that follow secret-looking flags (--pin, --password,
// --token, --secret, --key…) and the paths and queries of URLs, which often
// carry tokens, so command lines can be logged safely.
func maskArgs(args []string) []string {
	out := make([]string, len(args))
	copy(out, args)
	secret := func(a string) bool {
		a = strings.ToLower(strings.TrimLeft(a, "-"))
		for _, s := range []string{"pin", "pass", "token", "secret", "key"} {
			if strings.HasPrefix(a, s) {
				return true
			}
		}
		return false
	}
	for i := 0; i < len(out); i++ {
		a := out[i]
		if !strings.HasPrefix(a, "-") || !secret(a) {
			out[i] = maskURLs(a)
			continue
		}
		if j := strings.IndexByte(a, '='); j >= 0 {
			out[i] = a[:j+1] + "***"
		} else if i+1 < len(out) {
			out[i+1] = "***"
			i++
		}
	}
	return out
}

var urlLike = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s,;"']+`)

// maskURLs keeps only the scheme and host of every URL in s:
// https://user:pw@hooks.example/abc?t=1 becomes https://hooks.example/***.
func maskURLs(s string) string {
	return urlLike.ReplaceAllStringFunc(s, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return raw[:strings.Index(raw, "://")+3] + "***"
		}
		out := u.Scheme + "://" + u.Host
		if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			out += "/***"
		}
		return out
	})
}
