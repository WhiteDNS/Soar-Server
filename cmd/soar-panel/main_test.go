package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCLIInitializesAndProtectsManagementBind(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "panel.db")
	pw := filepath.Join(dir, "password")
	if e := os.WriteFile(pw, []byte("test-only-password-2026\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := execute([]string{"init", "--db", db, "--zone", "soar.example.org", "--password-file", pw}); e != nil {
		t.Fatal(e)
	}
	if e := execute([]string{"init", "--db", db, "--zone", "soar.example.org", "--password-file", pw}); e == nil {
		t.Fatal("existing database overwritten")
	}
	if e := execute([]string{"run", "--db", db, "--listen", "0.0.0.0:2053"}); e == nil {
		t.Fatal("public plaintext admin accepted")
	}
	if e := execute([]string{"backup", "--db", db, "--output", filepath.Join(dir, "backup.db")}); e != nil {
		t.Fatal(e)
	}
	if e := execute([]string{"admin-reset", "--db", db, "--password-file", pw}); e != nil {
		t.Fatal(e)
	}
}

func TestDirectManagementAccessRequiresExplicitTransportChoice(t *testing.T) {
	for _, tc := range []struct {
		name, bind, cert, key string
		allowHTTP, valid      bool
	}{
		{name: "private SSH", bind: "127.0.0.1:2053", valid: true},
		{name: "private IPv6", bind: "[::1]:2053", valid: true},
		{name: "public HTTP without opt-in", bind: "0.0.0.0:2053"},
		{name: "direct test HTTP", bind: "0.0.0.0:2053", allowHTTP: true, valid: true},
		{name: "direct specific IPv4", bind: "203.0.113.10:2053", allowHTTP: true, valid: true},
		{name: "public HTTPS", bind: ":2053", cert: "cert.pem", key: "key.pem", valid: true},
		{name: "partial TLS must not fall back to HTTP", bind: ":2053", cert: "cert.pem", allowHTTP: true},
		{name: "invalid bind", bind: "invalid", allowHTTP: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateManagementListen(tc.bind, tc.cert, tc.key, tc.allowHTTP)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}
