// Command soar-release-sign creates the keys and signatures that panels require
// before updating. Only the release workflow's own key may reach GitHub; the
// offline key must never reach GitHub or a build runner.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func keygen(path string) (string, error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	// O_EXCL: replacing a key would lock installed panels out of updates.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	_, err = f.WriteString(base64.StdEncoding.EncodeToString(private.Seed()) + "\n")
	if ce := f.Close(); err == nil {
		err = ce
	}
	return base64.StdEncoding.EncodeToString(public), err
}

func sign(keyPath string, files []string) (string, error) {
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return "", err
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return "", errors.New("invalid signing key file")
	}
	key := ed25519.NewKeyFromSeed(seed)
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		if err = os.WriteFile(file+".sig", ed25519.Sign(key, data), 0644); err != nil {
			return "", err
		}
	}
	return base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)), nil
}

func run(args []string) error {
	switch {
	case len(args) == 2 && args[0] == "keygen":
		public, err := keygen(args[1])
		if err == nil {
			fmt.Println(public)
		}
		return err
	case len(args) >= 3 && args[0] == "sign":
		// The caller can compare this public key with the keys panels trust.
		public, err := sign(args[1], args[2:])
		if err == nil {
			fmt.Println(public)
		}
		return err
	}
	return errors.New("usage: soar-release-sign keygen KEY_FILE | sign KEY_FILE FILE.sha256...")
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
