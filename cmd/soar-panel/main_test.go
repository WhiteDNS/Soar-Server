package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"soar-server-setup/internal/panel"
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

func captureCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	defer func() { os.Stdout = original; reader.Close() }()
	err = execute(args)
	writer.Close()
	var out bytes.Buffer
	_, _ = io.Copy(&out, reader)
	return strings.TrimSpace(out.String()), err
}

func TestOfflineInstallerProbeAndVerifiedResolvers(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "panel.db")
	pw := filepath.Join(dir, "password")
	if err := os.WriteFile(pw, []byte("test-only-password-2026"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := execute([]string{"init", "--db", db, "--zone", "soar.example.org", "--password-file", pw}); err != nil {
		t.Fatal(err)
	}
	out, err := captureCLI(t, "create-install-probe", "--db", db)
	fields := strings.Fields(out)
	if err != nil || len(fields) != 4 || !strings.HasSuffix(fields[1], ".soar.example.org") {
		t.Fatal("invalid probe identity", out, err)
	}
	if err = execute([]string{"remove-install-probe", "--db", db, "--probe-id", fields[0], "--update-resolvers", "--resolvers", "9.9.9.9"}); err != nil {
		t.Fatal(err)
	}
	s, err := panel.OpenStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	list, err := s.List()
	if err != nil || len(list) != 0 {
		t.Fatal("temporary account retained", err)
	}
	cfg, err := s.Settings()
	if err != nil || len(cfg.Resolvers) != 1 || cfg.Resolvers[0] != "9.9.9.9" || cfg.BaseZone != "soar.example.org" {
		t.Fatal("verified resolvers not saved", err)
	}
}

func TestAccessURLRetainsPublicHostPathAndHTTPSDomain(t *testing.T) {
	path := "/abcdefghijklmnopqrstuvwxyz012345/"
	cfgFile := filepath.Join(t.TempDir(), "access.json")
	if err := panel.WritePrivateJSON(cfgFile, panel.AccessConfig{BasePath: path}); err != nil {
		t.Fatal(err)
	}
	out, err := captureCLI(t, "access-url", "--access-file", cfgFile, "--listen", "8.8.4.4:2053")
	if err != nil || out != "http://8.8.4.4:2053"+path {
		t.Fatal(out, err)
	}
	if err := panel.WritePrivateJSON(cfgFile, panel.AccessConfig{BasePath: path, Domain: "panel.example.org", HTTPS: true}); err != nil {
		t.Fatal(err)
	}
	out, err = captureCLI(t, "access-url", "--access-file", cfgFile, "--listen", "8.8.4.4:2053")
	if err != nil || out != "https://panel.example.org"+path {
		t.Fatal(out, err)
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
