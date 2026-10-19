package panel

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getlantern/soar"
)

func testManager(t *testing.T, s *Store, override func(context.Context, string, string) (net.Conn, error)) *Manager {
	t.Helper()
	tcp, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	pc, e := net.ListenPacket("udp", tcp.Addr().String())
	if e != nil {
		tcp.Close()
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := NewManager(ctx, s, pc)
	m.dialOverride = override
	if e = m.Reconcile(); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- m.Run() }()
	tcpDone := make(chan error, 1)
	go func() { tcpDone <- m.ServeTCP(tcp) }()
	t.Cleanup(func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(5 * time.Second):
			t.Error("manager did not stop")
		}
		select {
		case err := <-tcpDone:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("TCP DNS did not stop")
		}
	})
	return m
}
func nativeClient(t *testing.T, m *Manager, c Client) *soar.Client {
	t.Helper()
	key, _ := base64.StdEncoding.DecodeString(c.PublicKey)
	client, e := soar.NewClient(soar.Config{Zone: c.Zone, ServerKey: ed25519.PublicKey(key), Resolvers: []string{m.pc.LocalAddr().String()}, TCP: "off"})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { client.Close() })
	return client
}
func TestUnmodifiedClientHTTPSAndIsolation(t *testing.T) {
	s := testStore(t)
	a := account(t, s, 0)
	b := account(t, s, 0)
	probe, err := s.CreateInstallProbe()
	if err != nil {
		t.Fatal(err)
	}
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "authenticated tunnel payload") }))
	defer origin.Close()
	m := testManager(t, s, func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, origin.Listener.Addr().String())
	})
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	for _, c := range []Client{a, b, probe} {
		client := nativeClient(t, m, c)
		tr := &http.Transport{DialContext: client.DialContext, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DisableKeepAlives: true}
		hc := &http.Client{Transport: tr, Timeout: 10 * time.Second}
		r, e := hc.Get("https://" + origin.Certificate().DNSNames[0] + "/")
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(r.Body)
		r.Body.Close()
		tr.CloseIdleConnections()
		if e != nil || string(body) != "authenticated tunnel payload" {
			t.Fatal("bad tunnel payload", e)
		}
	}
	for _, c := range []Client{a, b, probe} {
		actual, _ := s.Get(c.ID)
		if actual.UploadBytes == 0 || actual.DownloadBytes == 0 {
			t.Fatal("traffic not billed", c.ID)
		}
	}
	if m.dnsIn.Load() == 0 || m.dnsOut.Load() == 0 {
		t.Fatal("DNS metrics missing")
	}
}

// Exercise flow control, half-close and accounting across many 32-KiB boundaries,
// rather than considering a tiny successful startup request sufficient.
func TestConcurrentLargeTLSBodiesRemainIntact(t *testing.T) {
	s := testStore(t)
	a := account(t, s, 16<<20)
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i, e := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		if e != nil {
			w.WriteHeader(400)
			return
		}
		body, e := io.ReadAll(io.LimitReader(r.Body, (128<<10)+1))
		if e != nil || !bytes.Equal(body, bytes.Repeat([]byte{byte(i)}, 128<<10)) {
			w.WriteHeader(400)
			return
		}
		_, _ = w.Write(bytes.Repeat([]byte{byte(i + 1)}, 256<<10))
	}))
	defer origin.Close()
	m := testManager(t, s, func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, origin.Listener.Addr().String())
	})
	client := nativeClient(t, m, a)
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	tr := &http.Transport{DialContext: client.DialContext, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DisableKeepAlives: true, DisableCompression: true}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr, Timeout: 45 * time.Second}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			url := "https://" + origin.Certificate().DNSNames[0] + "/" + strconv.Itoa(i)
			r, e := hc.Post(url, "application/octet-stream", bytes.NewReader(bytes.Repeat([]byte{byte(i)}, 128<<10)))
			if e != nil {
				t.Error(e)
				return
			}
			defer r.Body.Close()
			body, e := io.ReadAll(r.Body)
			if e != nil || r.StatusCode != 200 || !bytes.Equal(body, bytes.Repeat([]byte{byte(i + 1)}, 256<<10)) {
				t.Errorf("response %d corrupted: status=%d bytes=%d error=%v", i, r.StatusCode, len(body), e)
			}
		}()
	}
	wg.Wait()
	actual, e := s.Get(a.ID)
	if e != nil {
		t.Fatal(e)
	}
	if actual.UploadBytes < 8*(128<<10) || actual.DownloadBytes < 8*(256<<10) || actual.Used() >= actual.QuotaBytes {
		t.Fatalf("unexpected payload accounting: up=%d down=%d", actual.UploadBytes, actual.DownloadBytes)
	}
}
func TestDisableRotateAndAnonymousRoot(t *testing.T) {
	s := testStore(t)
	a := account(t, s, 0)
	m := testManager(t, s, func(context.Context, string, string) (net.Conn, error) {
		left, right := net.Pipe()
		go func() { io.Copy(io.Discard, right); right.Close() }()
		return left, nil
	})
	client := nativeClient(t, m, a)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, e := client.DialContext(ctx, "tcp", "example.org:443")
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	a.Enabled = false
	if _, e = s.Edit(a.ID, input(a)); e != nil {
		t.Fatal(e)
	}
	if e = m.Reconcile(); e != nil {
		t.Fatal(e)
	}
	if len(m.ActiveCounts()) != 0 || m.active.Load() != 0 {
		t.Fatal("disable left connections open")
	}
	a, _ = s.Get(a.ID)
	a.Enabled = true
	a, _ = s.Edit(a.ID, input(a))
	if e = m.Reconcile(); e != nil {
		t.Fatal(e)
	}
	old := a
	a, e = s.Rotate(a.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Reconcile(); e != nil {
		t.Fatal(e)
	}
	m.mu.RLock()
	_, exists := m.zones[old.Zone]
	m.mu.RUnlock()
	if exists {
		t.Fatal("old zone still routed")
	}
	old.Zone = "soar.example.org"
	root := nativeClient(t, m, old)
	short, stop := context.WithTimeout(ctx, 300*time.Millisecond)
	defer stop()
	if c, e := root.DialContext(short, "tcp", "example.org:443"); e == nil {
		c.Close()
		t.Fatal("anonymous root account bypass")
	}
}
func TestMeterExactQuotaStreamLimitAndRefund(t *testing.T) {
	s := testStore(t)
	a := account(t, s, 100)
	a.StreamLimit = 1
	a, _ = s.Edit(a.ID, input(a))
	m := testManager(t, s, func(context.Context, string, string) (net.Conn, error) {
		left, right := net.Pipe()
		go func() { io.Copy(io.Discard, right); right.Close() }()
		return left, nil
	})
	m.mu.RLock()
	rt := m.accounts[a.ID]
	m.mu.RUnlock()
	conn, e := m.dial(rt, context.Background(), "tcp", "example.org:443")
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	if extra, e := m.dial(rt, context.Background(), "tcp", "example.org:443"); e == nil {
		extra.Close()
		t.Fatal("stream limit bypass")
	}
	n, e := conn.Write(bytes.Repeat([]byte{1}, 200))
	if n != 100 || !errors.Is(e, ErrDenied) {
		t.Fatalf("quota write n=%d e=%v", n, e)
	}
	actual, _ := s.Get(a.ID)
	if actual.Used() != 100 {
		t.Fatal("quota over/undercharged")
	}
	m.Disconnect(a.ID)
	if m.active.Load() != 0 {
		t.Fatal("stream capacity leaked")
	}
}
func TestPublicDestinationAndPortProtection(t *testing.T) {
	s := testStore(t)
	a := account(t, s, 0)
	m := testManager(t, s, nil)
	m.mu.RLock()
	rt := m.accounts[a.ID]
	m.mu.RUnlock()
	for _, addr := range []string{"127.0.0.1:443", "10.0.0.1:443", "169.254.169.254:80", "[::1]:443", "example.org:22"} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		c, e := m.dial(rt, ctx, "tcp", addr)
		cancel()
		if e == nil {
			c.Close()
			t.Error("unsafe destination accepted", addr)
		}
	}
	if m.active.Load() != 0 {
		t.Fatal("failed dials leaked capacity")
	}
}
func TestSharedRateAndOldEpochRejected(t *testing.T) {
	s := testStore(t)
	a := account(t, s, 0)
	a.RateBytes = 100
	a, _ = s.Edit(a.ID, input(a))
	m := testManager(t, s, func(context.Context, string, string) (net.Conn, error) {
		left, right := net.Pipe()
		go func() { io.Copy(io.Discard, right); right.Close() }()
		return left, nil
	})
	m.mu.RLock()
	rt := m.accounts[a.ID]
	m.mu.RUnlock()
	c, e := m.dial(rt, context.Background(), "tcp", "example.org:443")
	if e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	if _, e = c.Write([]byte(strings.Repeat("a", 50))); e != nil {
		t.Fatal(e)
	}
	if time.Since(start) < 450*time.Millisecond {
		t.Fatal("rate limit not applied")
	}
	if e = s.ResetUsage(a.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Write([]byte("old")); !errors.Is(e, ErrDenied) {
		t.Fatal("old connection crossed reset epoch")
	}
	c.Close()
	actual, _ := s.Get(a.ID)
	if actual.Used() != 0 {
		t.Fatal("new period billed old payload")
	}
}
func TestDNSParserBoundsAndControl(t *testing.T) {
	for _, b := range [][]byte{nil, make([]byte, 12), append([]byte{0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0}, 0xc0, 12, 0, 1, 0, 1)} {
		if _, _, _, ok := dnsQuestion(b); ok {
			t.Fatal("malformed accepted")
		}
	}
	q := make([]byte, 12)
	q[5] = 1
	q = append(q, dnsName("SOAR.EXAMPLE.ORG")...)
	q = append(q, 0, 6, 0, 1)
	name, end, typ, ok := dnsQuestion(q)
	if !ok || name != "soar.example.org" {
		t.Fatal("case folding failed")
	}
	r := dnsControl(q, end, name, typ, name)
	if r[3]&15 != 0 || r[7] != 1 {
		t.Fatal("base SOA missing")
	}
	r = dnsControl(q, end, "unknown.example.org", typ, name)
	if r[3]&15 != 5 {
		t.Fatal("unknown zone not refused")
	}
}
