package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestKeygenAndSignRoundTrip(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "private", "release.key")
	encoded, err := keygen(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	public, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(public) != ed25519.PublicKeySize {
		t.Fatal("invalid public key", err)
	}
	if _, err = keygen(keyPath); err == nil {
		t.Fatal("existing signing key was replaced")
	}
	file := filepath.Join(dir, "panel.tar.gz.sha256")
	if err = os.WriteFile(file, []byte("digest  panel.tar.gz\n"), 0644); err != nil {
		t.Fatal(err)
	}
	signer, err := sign(keyPath, []string{file})
	if err != nil || signer != encoded {
		t.Fatal("sign did not report its public key", signer, err)
	}
	signature, err := os.ReadFile(file + ".sig")
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(public, []byte("digest  panel.tar.gz\n"), signature) {
		t.Fatal("signature does not verify")
	}
	if ed25519.Verify(public, []byte("other  panel.tar.gz\n"), signature) {
		t.Fatal("signature accepted different content")
	}
}
