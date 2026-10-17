package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getlantern/soar"
)

func testTunnel(t *testing.T, status int) (soar.Config, *x509.CertPool, string) {
	t.Helper()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte("health check"))
	}))
	t.Cleanup(origin.Close)
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	host := origin.Certificate().DNSNames[0]
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		_ = soar.Serve(ctx, pc, soar.ServerConfig{
			Zone: "t.example.com", PrivateKey: private,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				// Lab-only override. Production uses the unmodified public-only server.
				return (&net.Dialer{}).DialContext(ctx, network, origin.Listener.Addr().String())
			},
		})
	}()
	return soar.Config{Zone: "t.example.com", ServerKey: pub, Resolvers: []string{pc.LocalAddr().String()}, TCP: "off"}, roots, "https://" + host + "/"
}

func TestHTTPSActuallyUsesAuthenticatedTunnel(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	cfg, roots, target := testTunnel(t, http.StatusOK)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	r := check(ctx, cfg, target, roots)
	if !r.OK || !r.Handshake || r.HTTP != 200 {
		t.Fatalf("tunnel check: %+v", r)
	}
}

func TestWrongServerKeyFails(t *testing.T) {
	cfg, roots, target := testTunnel(t, http.StatusOK)
	wrong, _, _ := ed25519.GenerateKey(rand.Reader)
	cfg.ServerKey = wrong
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r := check(ctx, cfg, target, roots)
	if r.OK || r.Handshake || r.Stage != "tunnel" {
		t.Fatalf("wrong key accepted: %+v", r)
	}
}

func TestUntrustedTLSFailsEvenWhenTunnelAuthenticates(t *testing.T) {
	cfg, _, target := testTunnel(t, http.StatusOK)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	r := check(ctx, cfg, target, x509.NewCertPool())
	if r.OK || !r.Handshake || r.Stage != "https" {
		t.Fatalf("untrusted certificate accepted: %+v", r)
	}
}

func TestHTTPFailureIsNotReportedAsSuccess(t *testing.T) {
	cfg, roots, target := testTunnel(t, http.StatusServiceUnavailable)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	r := check(ctx, cfg, target, roots)
	if r.OK || r.HTTP != 503 || !r.Handshake {
		t.Fatalf("HTTP failure accepted: %+v", r)
	}
}

func TestResolverAndURLValidation(t *testing.T) {
	for _, input := range []string{"1.1.1.1,8.8.8.8:53", "::1", "[::1]:5353"} {
		if _, err := resolverList(input); err != nil {
			t.Errorf("resolver rejected: %q", input)
		}
	}
	for _, input := range []string{"", "example.com", "1.1.1.1:0", "1.1.1.1,,8.8.8.8"} {
		if _, err := resolverList(input); err == nil {
			t.Errorf("bad resolver accepted: %q", input)
		}
	}
	for _, input := range []string{"http://example.com", "https://user:pass@example.com", "https:///", "https://example.com/#secret"} {
		if _, err := targetURL(input); err == nil {
			t.Errorf("bad URL accepted: %q", input)
		}
	}
}

func TestCLIRejectsBadArgumentsWithoutNetwork(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	for _, args := range [][]string{
		{"-zone", "t.example.com"},
		{"-zone", "t.example.com", "-pubkey", key, "-resolver", "1.1.1.1", "-tcp", "bad"},
		{"-zone", "t.example.com", "-pubkey", key, "-resolver", "1.1.1.1", "-timeout", "0s"},
	} {
		var output bytes.Buffer
		if code := run(args, &output); code != 2 || !strings.Contains(output.String(), "[ERROR]") {
			t.Errorf("unexpected result: %d %q", code, output.String())
		}
	}
}
