package release

import (
	"archive/tar"
	"compress/gzip"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignVerify(t *testing.T) {
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pk, err := ParsePublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	sk, err := ParsePrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if !pk.Equal(sk.Public()) {
		t.Fatal("public key does not belong to the private key")
	}
	data := []byte(`{"format":1}`)
	sig := Sign(data, sk)
	if k, err := Verify(data, sig, []ed25519.PublicKey{pk}); err != nil || !k.Equal(pk) {
		t.Fatalf("Verify = %v, %v", k, err)
	}
	if _, err := Verify([]byte(`{"format":2}`), sig, []ed25519.PublicKey{pk}); err == nil {
		t.Fatal("a changed manifest verified")
	}
	otherPub, _, _ := GenerateKey()
	ok, _ := ParsePublicKey(otherPub)
	if _, err := Verify(data, sig, []ed25519.PublicKey{ok}); err == nil || !strings.Contains(err.Error(), "trusted") {
		t.Fatalf("a signature by an untrusted key verified: %v", err)
	}
	if _, err := Verify(data, []byte("bm90IGEgc2lnbmF0dXJl"), []ed25519.PublicKey{pk}); err == nil {
		t.Fatal("garbage verified")
	}
	if len(KeyID(pk)) != 8 {
		t.Fatalf("KeyID = %q", KeyID(pk))
	}
	for _, bad := range []string{"", "abc", priv + "xx"} {
		if _, err := ParsePublicKey(bad); err == nil {
			t.Errorf("ParsePublicKey(%q) accepted", bad)
		}
	}
}

func TestVersions(t *testing.T) {
	order := []string{"0.4.9", "0.5.0-rc1", "0.5.0-rc2", "0.5.0", "v0.5.1", "0.10.0", "1.0.0"}
	for i := 0; i+1 < len(order); i++ {
		a, _ := ParseVersion(order[i])
		b, _ := ParseVersion(order[i+1])
		if a.Compare(b) != -1 || b.Compare(a) != 1 {
			t.Errorf("%s should sort before %s", order[i], order[i+1])
		}
	}
	if !Newer("0.5.1", "0.5.0") || Newer("0.5.0", "0.5.0") || Newer("0.4.0", "0.5.0") {
		t.Error("Newer compares wrongly")
	}
	if !Newer("0.5.0", "dev") || Newer("nonsense", "0.1.0") {
		t.Error("Newer with unparsable versions")
	}
	for _, bad := range []string{"", "1", "1.2", "1.2.3.4", "x1.2.3", "1.2.3-"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("ParseVersion(%q) accepted", bad)
		}
	}
}

func TestParseManifest(t *testing.T) {
	good := Manifest{Format: 1, Project: "rcplug", Version: "0.6.0", Assets: []Asset{
		{Name: "rcplug-0.6.0-aarch64.tar.gz", Size: 10, SHA256: strings.Repeat("a", 64), Kind: KindPlatform},
		{Name: "sonar-0.6.0-aarch64.tar.gz", Size: 5, SHA256: strings.Repeat("b", 64), Kind: KindPlugin, Plugin: "sonar", Version: "0.6.0"},
	}}
	data, _ := json.Marshal(good)
	m, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := m.Platform(); !ok || a.Name != "rcplug-0.6.0-aarch64.tar.gz" {
		t.Fatalf("Platform = %+v", a)
	}
	if a, ok := m.Plugin("sonar"); !ok || a.Version != "0.6.0" {
		t.Fatalf("Plugin = %+v", a)
	}
	if _, ok := m.Plugin("other"); ok {
		t.Fatal("found a plugin that is not there")
	}
	for name, change := range map[string]func(*Manifest){
		"format":      func(m *Manifest) { m.Format = 2 },
		"version":     func(m *Manifest) { m.Version = "latest" },
		"path name":   func(m *Manifest) { m.Assets[0].Name = "../x" },
		"duplicate":   func(m *Manifest) { m.Assets[1].Name = m.Assets[0].Name },
		"sha256":      func(m *Manifest) { m.Assets[0].SHA256 = "abc" },
		"size":        func(m *Manifest) { m.Assets[0].Size = 0 },
		"plugin name": func(m *Manifest) { m.Assets[1].Plugin = "../evil" },
	} {
		bad := good
		bad.Assets = append([]Asset(nil), good.Assets...)
		change(&bad)
		data, _ := json.Marshal(bad)
		if _, err := Parse(data); err == nil {
			t.Errorf("%s: invalid manifest accepted", name)
		}
	}
}

func writeTarGz(t *testing.T, file string, files map[string]string) {
	t.Helper()
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: "./" + name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	f.Close()
}

func TestDescribe(t *testing.T) {
	dir := t.TempDir()
	platform := filepath.Join(dir, "rcplug-0.6.0-aarch64.tar.gz")
	writeTarGz(t, platform, map[string]string{"install.sh": "#!/bin/sh\n", "VERSION": "0.6.0\n", "plugins/console/plugin.json": `{"name":"console","version":"0.6.0"}`})
	plugin := filepath.Join(dir, "sonar-0.6.0-aarch64.tar.gz")
	writeTarGz(t, plugin, map[string]string{"plugin.json": `{"name":"sonar","version":"0.6.0","exec":["./sonar"]}`, "sonar": "binary"})
	examples := filepath.Join(dir, "rcplug-examples-0.6.0-aarch64.tar.gz")
	writeTarGz(t, examples, map[string]string{"reclog/plugin.json": `{"name":"reclog","version":"0.1.0"}`})
	other := filepath.Join(dir, "SHA256SUMS")
	os.WriteFile(other, []byte("x"), 0o644)

	for file, want := range map[string]Asset{
		platform: {Kind: KindPlatform},
		plugin:   {Kind: KindPlugin, Plugin: "sonar", Version: "0.6.0"},
		examples: {},
		other:    {},
	} {
		a, err := Describe(file)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if a.Kind != want.Kind || a.Plugin != want.Plugin || a.Version != want.Version || len(a.SHA256) != 64 || a.Size == 0 {
			t.Errorf("%s: %+v", filepath.Base(file), a)
		}
	}
}

func TestBuiltinKeys(t *testing.T) {
	if len(BuiltinKeys) == 0 {
		t.Fatal("no built-in keys")
	}
	for _, k := range BuiltinKeys {
		if _, err := ParsePublicKey(k); err != nil {
			t.Errorf("%q: %v", k, err)
		}
	}
}
