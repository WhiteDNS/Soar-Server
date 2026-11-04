//go:build linux

package panel

import (
	"context"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Go loads system roots once per process, so a trusted test CA needs a child
// process. Production has no option to inject one.
func TestHTTPSProxyCarriesCustomPort(t *testing.T) {
	if address := os.Getenv("PANEL_TEST_HTTPS_PROXY"); address != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		conn, err := dialProxy(ctx, EngineEgress{Mode: "https", Address: address, Username: "user", Password: "secret"}, "tcp", "8.8.8.8:14756")
		assertEcho(t, conn, err, "https proxy")
		return
	}
	targets := make(chan string, 1)
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Header.Get("Proxy-Authorization") == "" {
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		targets <- r.Host
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n")
		io.Copy(conn, rw)
	}))
	defer proxy.Close()
	roots := filepath.Join(t.TempDir(), "roots.pem")
	block := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw})
	if err := os.WriteFile(roots, block, 0600); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestHTTPSProxyCarriesCustomPort$", "-test.count=1")
	child.Env = append(os.Environ(), "SSL_CERT_FILE="+roots, "SSL_CERT_DIR=/nonexistent", "PANEL_TEST_HTTPS_PROXY="+proxy.Listener.Addr().String())
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if target := <-targets; target != "8.8.8.8:14756" {
		t.Fatal("wrong CONNECT target", target)
	}
}
