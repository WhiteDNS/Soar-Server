package panel

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManualTLSHealthUsesActualPortAndPrivatePath(t *testing.T) {
	const prefix = "/abcdefghijklmnopqrstuvwx/"
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != prefix+"api/health" {
			t.Errorf("wrong health path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(204)
	}))
	defer s.Close()
	db := filepath.Join(t.TempDir(), "panel.db")
	if err := WritePrivateJSON(filepath.Join(filepath.Dir(db), "access.json"), AccessConfig{BasePath: prefix}); err != nil {
		t.Fatal(err)
	}
	certFile := filepath.Join(filepath.Dir(db), "public.pem")
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw})
	if err := os.WriteFile(certFile, certificate, 0600); err != nil {
		t.Fatal(err)
	}
	// Reproduce #21: plaintext HTTP to the manually configured TLS port fails.
	r, err := http.Get("http://" + s.Listener.Addr().String() + prefix + "api/health")
	if err == nil {
		r.Body.Close()
		if r.StatusCode == 204 {
			t.Fatal("fixture did not reproduce manual TLS mismatch")
		}
	}
	if err := WriteListenerConfig(db, s.Listener.Addr().String(), "127.0.0.1:5353", certFile, "example.com"); err != nil {
		t.Fatal(err)
	}
	if err := CheckManagementHealth(context.Background(), db, "127.0.0.1:2053"); err != nil {
		t.Fatal("manual TLS custom-port health failed", err)
	}
	live, _, _ := ReadListenerConfig(db)
	live.ServerName = "wrong.example.org"
	_ = WritePrivateJSON(filepath.Join(filepath.Dir(db), "listener.json"), live)
	if err := CheckManagementHealth(context.Background(), db, s.Listener.Addr().String()); err == nil {
		t.Fatal("TLS hostname verification was bypassed")
	}
}

func TestHealthPlainHTTPAndBuiltInHTTPSRemainDistinct(t *testing.T) {
	db := filepath.Join(t.TempDir(), "panel.db")
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer s.Close()
	address := strings.TrimPrefix(s.URL, "http://")
	if err := CheckManagementHealth(context.Background(), db, address); err != nil {
		t.Fatal(err)
	}
	_ = WritePrivateJSON(filepath.Join(filepath.Dir(db), "access.json"), AccessConfig{HTTPS: true, Domain: "panel.example.org"})
	req, transport, err := managementHealthRequest(context.Background(), db, address)
	if err != nil || req.URL.String() != "https://panel.example.org/api/health" || transport.DialContext == nil {
		t.Fatal("built-in ACME no longer uses its HTTPS listener", req, err)
	}
	if _, err := localListenerAddress("8.8.8.8:443"); err == nil {
		t.Fatal("runtime health allowed a nonlocal listener")
	}
}
