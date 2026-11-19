package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"soar-server-setup/internal/panel"
)

// Docker setup pipes the administrator password, because a host file keeps an
// owner and mode the container user cannot read.
func TestInitReadsPasswordFromStandardInput(t *testing.T) {
	db := filepath.Join(t.TempDir(), "panel.db")
	stdin := func(text string) {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		old := os.Stdin
		os.Stdin = r
		t.Cleanup(func() { os.Stdin = old; r.Close() })
		go func() { w.WriteString(text); w.Close() }()
	}
	stdin(strings.Repeat("x", 257))
	if err := execute([]string{"init", "--db", db, "--zone", "soar.example.org", "--password-file", "-"}); err == nil {
		t.Fatal("oversized piped password accepted")
	}
	stdin("piped-test-password-2026\n")
	if err := execute([]string{"init", "--db", db, "--zone", "soar.example.org", "--password-file", "-"}); err != nil {
		t.Fatal(err)
	}
	s, err := panel.OpenStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !s.CheckAdmin("admin", "piped-test-password-2026") {
		t.Fatal("piped password was not stored without its newline")
	}
}

func TestDNSListenAutoUsesDefaultRouteAddress(t *testing.T) {
	if got, err := resolveDNSListen("127.0.0.1:53"); err != nil || got != "127.0.0.1:53" {
		t.Fatalf("explicit address changed: %q %v", got, err)
	}
	if _, err := resolveDNSListen("auto"); err == nil {
		t.Fatal("address without port accepted")
	}
	got, err := resolveDNSListen("auto:53")
	if err != nil {
		t.Skip("no default IPv4 route in this environment:", err)
	}
	host, port, _ := net.SplitHostPort(got)
	if ip := net.ParseIP(host); ip == nil || ip.To4() == nil || ip.IsUnspecified() || port != "53" {
		t.Fatalf("auto resolved to %q", got)
	}
}
