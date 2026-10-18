// Package release is the format of signed rcplug releases. A release on
// GitHub carries a manifest, release.json, that lists its files with their
// size and SHA-256, and release.json.sig, an Ed25519 signature over the exact
// bytes of the manifest. rcplugd installs an update only if the signature is
// made by a key it trusts and every file matches the manifest.
//
// The release workflows write both files with cmd/rcplug-release; plugin
// authors can publish signed releases of their own plugins the same way.
package release

import (
	"archive/tar"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// File names of the manifest and its signature in a release.
const (
	ManifestName  = "release.json"
	SignatureName = "release.json.sig"
)

// Kinds of release files.
const (
	KindPlatform = "platform" // rcplug's device package (has install.sh)
	KindPlugin   = "plugin"   // one plugin (has plugin.json at its root)
)

// MaxManifestSize bounds what a client reads for release.json and its
// signature.
const MaxManifestSize = 256 << 10

// Manifest describes one release.
type Manifest struct {
	Format    int     `json:"format"`              // 1
	Project   string  `json:"project"`             // repository name, e.g. "rcplug" or "rcplug-plugins"
	Version   string  `json:"version"`             // without a leading "v"
	Published string  `json:"published,omitempty"` // RFC 3339
	Assets    []Asset `json:"assets"`
}

// Asset is one file of a release.
type Asset struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	Kind    string `json:"kind,omitempty"`    // KindPlatform, KindPlugin or "" for other files
	Plugin  string `json:"plugin,omitempty"`  // for KindPlugin: the plugin's name
	Version string `json:"version,omitempty"` // for KindPlugin: the plugin's version
	// Info is what a catalog shows about a plugin before it is installed,
	// taken from its plugin.json (rcplug 0.7.1 and later).
	Info *PluginInfo `json:"info,omitempty"`
}

// PluginInfo describes a plugin package for the dashboard's catalog.
type PluginInfo struct {
	Title        string                       `json:"title,omitempty"`
	Description  string                       `json:"description,omitempty"`
	Icon         string                       `json:"icon,omitempty"`
	Permissions  []string                     `json:"permissions,omitempty"`
	Translations map[string]map[string]string `json:"translations,omitempty"` // language → "title"/"description"
}

// Limits for PluginInfo: a catalog shows a card, not a page.
const (
	maxInfoTitle       = 80
	maxInfoDescription = 600
	maxInfoIcon        = 8
)

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return string(r)
}

// infoFrom builds a package's PluginInfo from its plugin.json.
func infoFrom(raw []byte) *PluginInfo {
	var pm struct {
		Title        string                       `json:"title"`
		Description  string                       `json:"description"`
		Icon         string                       `json:"icon"`
		NeedsAudio   bool                         `json:"needsAudio"`
		Permissions  []string                     `json:"permissions"`
		Translations map[string]map[string]string `json:"translations"`
	}
	if json.Unmarshal(raw, &pm) != nil {
		return nil
	}
	info := &PluginInfo{Title: clip(pm.Title, maxInfoTitle), Description: clip(pm.Description, maxInfoDescription)}
	if icon := []rune(strings.TrimSpace(pm.Icon)); len(icon) <= maxInfoIcon {
		info.Icon = string(icon)
	}
	seen := map[string]bool{}
	for _, p := range pm.Permissions {
		if !seen[p] {
			seen[p] = true
			info.Permissions = append(info.Permissions, p)
		}
	}
	if pm.NeedsAudio && !seen["audio"] {
		info.Permissions = append(info.Permissions, "audio")
	}
	for lang, tr := range pm.Translations {
		if !langCode.MatchString(lang) {
			continue
		}
		t := map[string]string{}
		if v := clip(tr["title"], maxInfoTitle); v != "" {
			t["title"] = v
		}
		if v := clip(tr["description"], maxInfoDescription); v != "" {
			t["description"] = v
		}
		if len(t) > 0 {
			if info.Translations == nil {
				info.Translations = map[string]map[string]string{}
			}
			info.Translations[lang] = t
		}
	}
	return info
}

var (
	assetName  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
	pluginName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	hexSHA256  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	langCode   = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})?$`)
)

// Parse reads and checks a manifest.
func Parse(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("release manifest: %w", err)
	}
	if m.Format != 1 {
		return nil, fmt.Errorf("release manifest: unsupported format %d", m.Format)
	}
	if m.Project == "" || !assetName.MatchString(m.Project) {
		return nil, fmt.Errorf("release manifest: invalid project %q", m.Project)
	}
	if _, err := ParseVersion(m.Version); err != nil {
		return nil, fmt.Errorf("release manifest: %w", err)
	}
	seen := map[string]bool{}
	for _, a := range m.Assets {
		switch {
		case !assetName.MatchString(a.Name) || seen[a.Name]:
			return nil, fmt.Errorf("release manifest: invalid or repeated file name %q", a.Name)
		case !hexSHA256.MatchString(a.SHA256):
			return nil, fmt.Errorf("release manifest: %s: invalid sha256", a.Name)
		case a.Size <= 0:
			return nil, fmt.Errorf("release manifest: %s: invalid size", a.Name)
		case a.Kind == KindPlugin && !pluginName.MatchString(a.Plugin):
			return nil, fmt.Errorf("release manifest: %s: invalid plugin name %q", a.Name, a.Plugin)
		}
		if a.Kind == KindPlugin {
			if _, err := ParseVersion(a.Version); err != nil {
				return nil, fmt.Errorf("release manifest: %s: %w", a.Name, err)
			}
		}
		seen[a.Name] = true
	}
	return &m, nil
}

// Platform returns the release's device package, if it has one.
func (m *Manifest) Platform() (Asset, bool) {
	for _, a := range m.Assets {
		if a.Kind == KindPlatform {
			return a, true
		}
	}
	return Asset{}, false
}

// Plugin returns the package of the named plugin, if the release has one.
func (m *Manifest) Plugin(name string) (Asset, bool) {
	for _, a := range m.Assets {
		if a.Kind == KindPlugin && a.Plugin == name {
			return a, true
		}
	}
	return Asset{}, false
}

// ---- keys and signatures

// GenerateKey makes a new signing key. Both halves are returned as base64:
// the public key (32 bytes) and the private key's seed (32 bytes).
func GenerateKey() (public, private string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(priv.Seed()), nil
}

// ParsePublicKey reads a base64 public key.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("not a base64 Ed25519 public key")
	}
	return ed25519.PublicKey(b), nil
}

// ParsePrivateKey reads a base64 private key seed.
func ParsePrivateKey(s string) (ed25519.PrivateKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.SeedSize {
		return nil, errors.New("not a base64 Ed25519 private key seed")
	}
	return ed25519.NewKeyFromSeed(b), nil
}

// KeyID is a short, stable name for a public key, for logs and the UI.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:4])
}

// Sign signs a manifest's bytes and returns the content of
// release.json.sig: the base64 signature and a newline.
func Sign(manifest []byte, priv ed25519.PrivateKey) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, manifest)) + "\n")
}

// Verify checks a release.json.sig against the manifest's bytes and returns
// the key that made it. Any of the trusted keys may have signed.
func Verify(manifest, sig []byte, trusted []ed25519.PublicKey) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return nil, errors.New("release signature: not a base64 Ed25519 signature")
	}
	for _, k := range trusted {
		if ed25519.Verify(k, manifest, raw) {
			return k, nil
		}
	}
	return nil, errors.New("release signature: not made by a trusted key")
}

// ---- versions

// Version is a parsed major.minor.patch version with an optional
// pre-release suffix ("0.6.0-rc1").
type Version struct {
	Major, Minor, Patch int
	Pre                 string
}

var versionRE = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$`)

// ParseVersion reads a version such as "0.6.0" or "v0.6.0-rc1".
func ParseVersion(s string) (Version, error) {
	m := versionRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, fmt.Errorf("invalid version %q", s)
	}
	var v Version
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	v.Patch, _ = strconv.Atoi(m[3])
	v.Pre = m[4]
	return v, nil
}

// Compare returns -1, 0 or 1. A pre-release sorts before its release;
// pre-release suffixes compare as plain strings.
func (v Version) Compare(o Version) int {
	for _, d := range [][2]int{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}} {
		if d[0] != d[1] {
			if d[0] < d[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case v.Pre == o.Pre:
		return 0
	case v.Pre == "":
		return 1
	case o.Pre == "":
		return -1
	case v.Pre < o.Pre:
		return -1
	}
	return 1
}

// Newer reports whether version latest is newer than current. A current
// version that does not parse (a development build, "dev") counts as older
// than any release.
func Newer(latest, current string) bool {
	l, err := ParseVersion(latest)
	if err != nil {
		return false
	}
	c, err := ParseVersion(current)
	if err != nil {
		return true
	}
	return l.Compare(c) > 0
}

// ---- building a manifest

// Describe makes the manifest entry for a release file: its size and
// SHA-256, and for a .tar.gz what it installs (a device package has
// install.sh at its root, a plugin has plugin.json there).
func Describe(file string) (Asset, error) {
	f, err := os.Open(file)
	if err != nil {
		return Asset{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return Asset{}, err
	}
	a := Asset{Name: filepath.Base(file), Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}
	if !assetName.MatchString(a.Name) {
		return Asset{}, fmt.Errorf("%s: file name not allowed in a release", a.Name)
	}
	if strings.HasSuffix(a.Name, ".tar.gz") {
		if err := classify(file, &a); err != nil {
			return Asset{}, fmt.Errorf("%s: %w", a.Name, err)
		}
	}
	return a, nil
}

func classify(file string, a *Asset) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch path.Clean(strings.TrimPrefix(h.Name, "./")) {
		case "install.sh":
			a.Kind = KindPlatform
		case "plugin.json":
			raw, err := io.ReadAll(io.LimitReader(tr, 1<<20))
			if err != nil {
				return fmt.Errorf("plugin.json: %w", err)
			}
			var pm struct{ Name, Version string }
			if err := json.Unmarshal(raw, &pm); err != nil {
				return fmt.Errorf("plugin.json: %w", err)
			}
			if !pluginName.MatchString(pm.Name) {
				return fmt.Errorf("plugin.json: invalid name %q", pm.Name)
			}
			if _, err := ParseVersion(pm.Version); err != nil {
				return fmt.Errorf("plugin.json: %w", err)
			}
			a.Kind, a.Plugin, a.Version = KindPlugin, pm.Name, pm.Version
			a.Info = infoFrom(raw)
			return nil
		}
	}
}
