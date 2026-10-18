// Command rcplug-release makes and checks signed releases (see sdk/release):
//
//	rcplug-release keygen --out DIR
//	    writes a new key pair: DIR/rcplug-release.key (private, keep it secret)
//	    and DIR/rcplug-release.pub (public, compiled into rcplugd)
//	rcplug-release sign --project rcplug --version 0.6.0 --out dist/ FILE...
//	    writes dist/release.json and dist/release.json.sig for FILE...; the
//	    private key comes from the environment variable RCPLUG_SIGNING_KEY
//	rcplug-release verify --pub KEY DIR
//	    checks DIR/release.json against its signature and the files next to it
//
// The release workflows run "sign" with the key from a repository secret.
package main

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hustenreizjuengling/rcplug/sdk/release"
)

var version = "dev"

const keyEnv = "RCPLUG_SIGNING_KEY"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "sign":
		err = sign(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	case "version", "--version", "-version":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "rcplug-release: "+err.Error())
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  rcplug-release keygen --out DIR
  rcplug-release sign --project NAME --version X.Y.Z --out DIR FILE...   (key in $RCPLUG_SIGNING_KEY)
  rcplug-release verify [--pub PUBLIC-KEY-OR-FILE] DIR       (default: the keys built into rcplugd)
`)
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "", "directory for rcplug-release.key and rcplug-release.pub")
	fs.Parse(args)
	if *out == "" {
		return errors.New("keygen needs --out DIR")
	}
	keyFile, pubFile := filepath.Join(*out, "rcplug-release.key"), filepath.Join(*out, "rcplug-release.pub")
	for _, f := range []string{keyFile, pubFile} {
		if _, err := os.Stat(f); err == nil {
			return fmt.Errorf("%s exists; not overwriting a key", f)
		}
	}
	pub, priv, err := release.GenerateKey()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(keyFile, []byte(priv+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(pubFile, []byte(pub+"\n"), 0o644); err != nil {
		return err
	}
	k, _ := release.ParsePublicKey(pub)
	fmt.Printf("wrote %s (private, keep it secret) and %s\npublic key %s (id %s)\n", keyFile, pubFile, pub, release.KeyID(k))
	return nil
}

func sign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	project := fs.String("project", "", "repository name, e.g. rcplug or rcplug-plugins")
	ver := fs.String("version", "", "release version (as in VERSION)")
	out := fs.String("out", ".", "directory for release.json and release.json.sig")
	fs.Parse(args)
	files := fs.Args()
	if *project == "" || *ver == "" || len(files) == 0 {
		return errors.New("sign needs --project, --version and at least one file")
	}
	keyText := os.Getenv(keyEnv)
	if keyText == "" {
		return fmt.Errorf("no signing key: set %s to the content of rcplug-release.key", keyEnv)
	}
	priv, err := release.ParsePrivateKey(keyText)
	if err != nil {
		return fmt.Errorf("%s: %w", keyEnv, err)
	}
	m := release.Manifest{Format: 1, Project: *project, Version: strings.TrimPrefix(*ver, "v"),
		Published: time.Now().UTC().Format(time.RFC3339)}
	for _, f := range files {
		a, err := release.Describe(f)
		if err != nil {
			return err
		}
		m.Assets = append(m.Assets, a)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := release.Parse(data); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, release.ManifestName), data, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, release.SignatureName), release.Sign(data, priv), 0o644); err != nil {
		return err
	}
	pub := priv.Public().(ed25519.PublicKey)
	fmt.Printf("signed %s %s: %d file(s), key %s\n", m.Project, m.Version, len(m.Assets), release.KeyID(pub))
	for _, a := range m.Assets {
		what := a.Kind
		if a.Kind == release.KindPlugin {
			what += " " + a.Plugin + " " + a.Version
		}
		fmt.Printf("  %-44s %10d  %s\n", a.Name, a.Size, what)
	}
	return nil
}

func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	pubArg := fs.String("pub", "", "public key (base64) or a file containing it; default: the keys built into rcplugd")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("verify needs the release directory")
	}
	var keys []ed25519.PublicKey
	if *pubArg != "" {
		pubText := *pubArg
		if b, err := os.ReadFile(pubText); err == nil {
			pubText = string(b)
		}
		pub, err := release.ParsePublicKey(pubText)
		if err != nil {
			return err
		}
		keys = append(keys, pub)
	} else {
		for _, k := range release.BuiltinKeys {
			pub, err := release.ParsePublicKey(k)
			if err != nil {
				return fmt.Errorf("built-in key: %w", err)
			}
			keys = append(keys, pub)
		}
	}
	dir := fs.Arg(0)
	data, err := os.ReadFile(filepath.Join(dir, release.ManifestName))
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(filepath.Join(dir, release.SignatureName))
	if err != nil {
		return err
	}
	pub, err := release.Verify(data, sig, keys)
	if err != nil {
		return err
	}
	m, err := release.Parse(data)
	if err != nil {
		return err
	}
	for _, a := range m.Assets {
		got, err := release.Describe(filepath.Join(dir, a.Name))
		if err != nil {
			return err
		}
		if got.SHA256 != a.SHA256 || got.Size != a.Size {
			return fmt.Errorf("%s does not match the manifest", a.Name)
		}
	}
	fmt.Printf("ok: %s %s, %d file(s), signed by key %s\n", m.Project, m.Version, len(m.Assets), release.KeyID(pub))
	return nil
}
