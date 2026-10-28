package panel

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	cottenprobe "cottendns-go/probe"
	"golang.org/x/net/dns/dnsmessage"
	masterprobe "masterdnsvpn-go/probe"
)

func mockEgressProxy(t *testing.T, mode string, reject bool) (string, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan string, 32)
	done := make(chan struct{})
	t.Cleanup(func() { listener.Close(); <-done })
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				if mode == "http" {
					req, err := http.ReadRequest(bufio.NewReader(conn))
					if err != nil {
						return
					}
					requests <- req.Host
					if req.Method != "CONNECT" {
						return
					}
					if reject {
						io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
						return
					}
					io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\nhello")
				} else {
					var greeting [3]byte
					if _, err := io.ReadFull(conn, greeting[:]); err != nil {
						return
					}
					conn.Write([]byte{5, greeting[2]})
					if greeting[2] == 2 {
						var h [2]byte
						io.ReadFull(conn, h[:])
						username := make([]byte, int(h[1]))
						io.ReadFull(conn, username)
						io.ReadFull(conn, h[:1])
						password := make([]byte, int(h[0]))
						io.ReadFull(conn, password)
						if string(username) != "user" || string(password) != "secret" {
							conn.Write([]byte{1, 1})
							return
						}
						conn.Write([]byte{1, 0})
					}
					var header [4]byte
					if _, err := io.ReadFull(conn, header[:]); err != nil {
						return
					}
					target, err := readSocksAddress(conn, header[3])
					if err != nil {
						return
					}
					requests <- target
					if reject {
						conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
						return
					}
					conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
					io.WriteString(conn, "hello")
				}
				io.Copy(conn, conn)
			}()
		}
	}()
	return listener.Addr().String(), requests
}

func TestEgressProxyTCPHandshakeAndBufferedPayload(t *testing.T) {
	for _, mode := range []string{"http", "socks5"} {
		t.Run(mode, func(t *testing.T) {
			address, requests := mockEgressProxy(t, mode, false)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			route := EngineEgress{Mode: mode, Address: address, Username: "user", Password: "secret", Ports: "1-2000"}
			conn, err := dialProxy(ctx, route, "tcp", "8.8.8.8:443")
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if target := <-requests; target != "8.8.8.8:443" {
				t.Fatal(target)
			}
			b := make([]byte, 5)
			if _, err := io.ReadFull(conn, b); err != nil || string(b) != "hello" {
				t.Fatal(string(b), err)
			}
			if _, err := conn.Write([]byte("payload")); err != nil {
				t.Fatal(err)
			}
			b = make([]byte, 7)
			if _, err := io.ReadFull(conn, b); err != nil || string(b) != "payload" {
				t.Fatal(string(b), err)
			}
		})
	}
}

func TestEgressSettingsRangesSecretsAndCompatibility(t *testing.T) {
	s := testStore(t)
	c := account(t, s, 0)
	cfg, _ := s.Egress()
	cfg.Cotten = EngineEgress{Mode: "socks5", Address: "127.0.0.1:1080", Username: "user", Password: "secret", Ports: "1-2000,8443"}
	if err := s.SaveEgress(cfg); err != nil {
		t.Fatal(err)
	}
	saved, _ := s.Egress()
	public := saved.publicView()
	raw, _ := json.Marshal(public)
	if bytes.Contains(raw, []byte("secret")) || !public.Cotten.PasswordConfigured {
		t.Fatal(string(raw))
	}
	if err := s.SaveEgress(public); err != nil {
		t.Fatal(err)
	}
	saved, _ = s.Egress()
	if saved.Cotten.Password != "secret" {
		t.Fatal("blank password erased stored credential")
	}
	if err := s.SaveEgress(public); !errors.Is(err, ErrConflict) {
		t.Fatal("stale edit accepted", err)
	}
	settings, _ := s.Settings()
	settings.AllowPorts = []uint16{443}
	if err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	saved, _ = s.Egress()
	if saved.Cotten.Password != "secret" {
		t.Fatal("ordinary settings erased proxy")
	}
	if !saved.Cotten.allows(1999, []uint16{443}) || saved.Cotten.allows(2001, []uint16{443}) || !saved.Cotten.allows(8443, nil) {
		t.Fatal("range policy incorrect")
	}
	if saved.Soar.allows(1999, []uint16{443}) {
		t.Fatal("proxy ports leaked into direct policy")
	}
	public = saved.publicView()
	public.Cotten.Address = "127.0.0.1:1081"
	if err := s.SaveEgress(public); err == nil {
		t.Fatal("secret silently forwarded to a changed endpoint")
	}
	after, _ := s.Get(c.ID)
	if after != c {
		t.Fatal("configuration changed account identity or usage")
	}
	for _, ports := range []string{"0", "2-1", "65536", "1-2-3", "", "80;443"} {
		if _, err := parsePortIntervals(ports); err == nil {
			t.Fatal("invalid ports accepted", ports)
		}
	}
}

func TestEgressNoDirectFallbackAndPrivateTargetsBlocked(t *testing.T) {
	s := testStore(t)
	c := account(t, s, 0)
	var direct atomic.Int32
	m := testManager(t, s, func(context.Context, string, string) (net.Conn, error) {
		direct.Add(1)
		return nil, errors.New("direct path must not run")
	})
	address, requests := mockEgressProxy(t, "http", true)
	cfg, _ := s.Egress()
	cfg.Soar = EngineEgress{Mode: "http", Address: address, Ports: "1-2000"}
	if err := s.SaveEgress(cfg); err != nil {
		t.Fatal(err)
	}
	if err := m.RestartEngines(); err != nil {
		t.Fatal(err)
	}
	m.mu.RLock()
	rt := m.accounts[c.ID]
	m.mu.RUnlock()
	for _, target := range []string{"127.0.0.1:443", "169.254.169.254:80", "10.0.0.1:443"} {
		if conn, err := m.dialForEngine(rt, context.Background(), "tcp", target, upstreamSoar); err == nil {
			conn.Close()
			t.Fatal("private target allowed", target)
		}
	}
	select {
	case target := <-requests:
		t.Fatal("private target reached proxy", target)
	default:
	}
	if conn, err := m.dialForEngine(rt, context.Background(), "tcp", "8.8.8.8:443", upstreamSoar); err == nil {
		conn.Close()
		t.Fatal("rejected CONNECT accepted")
	}
	if direct.Load() != 0 {
		t.Fatal("upstream failure fell back to direct")
	}
	if conn, err := m.dialForEngine(rt, context.Background(), "udp", "8.8.8.8:443", upstreamSoar); err == nil {
		conn.Close()
		t.Fatal("HTTP UDP silently escaped")
	}
	if direct.Load() != 0 {
		t.Fatal("UDP fell back to direct")
	}
	for len(requests) > 0 {
		<-requests
	}
	if conn, err := m.dialForEngine(rt, context.Background(), "udp", "unsupported-request.example:443", upstreamSoar); !errors.Is(err, errProxyUDP) {
		if conn != nil {
			conn.Close()
		}
		t.Fatal("unsupported UDP resolved a name before rejection", err)
	}
	select {
	case target := <-requests:
		t.Fatal("unsupported UDP generated proxy DNS traffic", target)
	default:
	}
}

func TestEgressEngineIsolationAndOutboundPayloadCounts(t *testing.T) {
	s := testStore(t)
	c := account(t, s, 0)
	address, _ := mockEgressProxy(t, "socks5", false)
	cfg, _ := s.Egress()
	cfg.Cotten = EngineEgress{Mode: "socks5", Address: address, Unrestricted: true}
	cfg.Logs.Enabled = true
	if err := s.SaveEgress(cfg); err != nil {
		t.Fatal(err)
	}
	m := testManager(t, s, nil)
	m.mu.RLock()
	rt := m.accounts[c.ID]
	m.mu.RUnlock()
	if conn, err := m.dialForEngine(rt, context.Background(), "tcp", "8.8.8.8:2525", upstreamSoar); err == nil {
		conn.Close()
		t.Fatal("direct inherited proxy exception")
	}
	conn, err := m.dialForEngine(rt, context.Background(), "tcp", "8.8.8.8:2525", upstreamCotten)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 5)
	io.ReadFull(conn, b)
	conn.Write([]byte("seven77"))
	b = make([]byte, 7)
	io.ReadFull(conn, b)
	conn.Close()
	rows, err := m.outbound.query(context.Background(), outboundQuery{From: time.Now().Add(-time.Hour).UnixMilli(), Until: time.Now().UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.Engine == "cotten" {
			found = true
			if row.UploadBytes != 7 || row.DownloadBytes != 12 || row.DestinationIP != "8.8.8.8" || row.Route != "socks5" || row.State != "completed" {
				t.Fatalf("%+v", row)
			}
		}
	}
	if !found {
		t.Fatal("missing per-engine traffic record")
	}
}

func TestEgressAPIAuthenticationAndSecretRedaction(t *testing.T) {
	b, _, _ := testBrowser(t)
	b.request("/api/egress", "GET", nil, 401)
	b.request("/api/outbound", "GET", nil, 401)
	b.login()
	var cfg EgressConfig
	json.Unmarshal(b.request("/api/egress", "GET", nil, 200), &cfg)
	cfg.Soar = EngineEgress{Mode: "socks5", Address: "127.0.0.1:1080", Username: "user", Password: "do-not-return-this", Ports: "80,443"}
	token := b.csrf
	b.csrf = ""
	b.request("/api/egress", "PUT", cfg, 403)
	b.csrf = token
	for _, raw := range [][]byte{b.request("/api/egress", "PUT", cfg, 200), b.request("/api/egress", "GET", nil, 200), b.request("/api/settings", "GET", nil, 200), b.request("/api/diagnostics", "GET", nil, 200)} {
		if bytes.Contains(raw, []byte("do-not-return-this")) {
			t.Fatal("proxy credential leaked")
		}
	}
	b.request("/api/outbound?from=500&until=100", "GET", nil, 400)
	b.request("/api/outbound?engine=unknown", "GET", nil, 400)
	b.request("/api/outbound?format=csv", "GET", nil, 200)
}

func TestEgressRestorePreservesCurrentRoute(t *testing.T) {
	s := testStore(t)
	backup := filepath.Join(t.TempDir(), "restore.db")
	file, err := os.Create(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Backup(file); err != nil {
		t.Fatal(err)
	}
	file.Close()
	cfg, _ := s.Egress()
	cfg.Soar = EngineEgress{Mode: "socks5", Address: "127.0.0.1:1080", Ports: "443"}
	if err := s.SaveEgress(cfg); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(t.TempDir(), "current.db")
	file, err = os.Create(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Backup(file); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if err := PreserveRestoreAdmin(backup, current); err != nil {
		t.Fatal(err)
	}
	candidate, err := OpenStore(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer candidate.Close()
	restored, err := candidate.Egress()
	if err != nil || restored.Soar.Mode != "socks5" {
		t.Fatal(restored, err)
	}
}

// Keep the wire-format fixtures independent from the implementation's parser.
func TestEgressSOCKSUDPDatagramAndControlLifetime(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	control := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		var b [10]byte
		io.ReadFull(conn, b[:3])
		conn.Write([]byte{5, 0})
		io.ReadFull(conn, b[:])
		port := udp.LocalAddr().(*net.UDPAddr).Port
		reply := []byte{5, 0, 0, 1, 127, 0, 0, 1}
		reply = binary.BigEndian.AppendUint16(reply, uint16(port))
		conn.Write(reply)
		control <- conn
	}()
	go func() {
		b := make([]byte, 2048)
		n, peer, err := udp.ReadFrom(b)
		if err == nil {
			udp.WriteTo(b[:n], peer)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dialProxy(ctx, EngineEgress{Mode: "socks5", Address: listener.Addr().String()}, "udp", "8.8.8.8:443")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if n, err := conn.Write([]byte("datagram")); err != nil || n != 8 {
		t.Fatal(n, err)
	}
	b := make([]byte, 100)
	if n, err := conn.Read(b); err != nil || string(b[:n]) != "datagram" {
		t.Fatal(string(b[:n]), err)
	}
	(<-control).Close()
	_, err = conn.Read(b)
	if err == nil {
		t.Fatal("UDP survived closed SOCKS control connection")
	}
}

func TestEgressProxyCancellationAndHTTPSVerification(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	closed := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.Copy(io.Discard, conn)
		close(closed)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if conn, err := dialProxy(ctx, EngineEgress{Mode: "socks5", Address: listener.Addr().String()}, "tcp", "8.8.8.8:443"); err == nil {
		conn.Close()
		t.Fatal("stalled handshake accepted")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("canceled handshake left proxy socket open")
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if conn, err := dialProxy(ctx2, EngineEgress{Mode: "https", Address: server.Listener.Addr().String()}, "tcp", "8.8.8.8:443"); err == nil {
		conn.Close()
		t.Fatal("untrusted HTTPS proxy certificate accepted")
	}
}

func TestEgressAllNativeEnginesProxyDNSAndHTTPS(t *testing.T) {
	s := testStore(t)
	c, err := s.Create(ClientInput{Name: "Native upstream fixture", Enabled: true, Engines: "all", StreamLimit: 16, QuotaBytes: 2 << 20})
	if err != nil {
		t.Fatal(err)
	}
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "verified through upstream") }))
	defer origin.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var dnsQueries, proxyStreams, direct atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(20 * time.Second))
				reader := bufio.NewReader(conn)
				req, err := http.ReadRequest(reader)
				if err != nil || req.Method != "CONNECT" {
					return
				}
				_, port, _ := net.SplitHostPort(req.Host)
				io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n")
				if port == "53" {
					dnsQueries.Add(1)
					var size [2]byte
					if _, err := io.ReadFull(reader, size[:]); err != nil {
						return
					}
					raw := make([]byte, binary.BigEndian.Uint16(size[:]))
					if _, err := io.ReadFull(reader, raw); err != nil {
						return
					}
					var query dnsmessage.Message
					if query.Unpack(raw) != nil {
						return
					}
					answer := dnsmessage.Message{Header: dnsmessage.Header{ID: query.ID, Response: true, RecursionDesired: true, RecursionAvailable: true}, Questions: query.Questions}
					for _, q := range query.Questions {
						if q.Type == dnsmessage.TypeA {
							answer.Answers = append(answer.Answers, dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 30}, Body: &dnsmessage.AResource{A: [4]byte{8, 8, 8, 8}}})
						}
					}
					body, _ := answer.Pack()
					conn.Write(binary.BigEndian.AppendUint16(nil, uint16(len(body))))
					conn.Write(body)
					return
				}
				if req.Host != "8.8.8.8:443" {
					return
				}
				proxyStreams.Add(1)
				upstream, err := net.Dial("tcp", origin.Listener.Addr().String())
				if err != nil {
					return
				}
				defer upstream.Close()
				finished := make(chan struct{})
				go func() { io.Copy(upstream, reader); upstream.(*net.TCPConn).CloseWrite(); close(finished) }()
				io.Copy(conn, upstream)
				conn.Close()
				<-finished
			}()
		}
	}()
	cfg, _ := s.Egress()
	route := EngineEgress{Mode: "http", Address: listener.Addr().String(), Ports: "443"}
	cfg.Soar = route
	cfg.Cotten = route
	cfg.MasterDNS = route
	cfg.Logs.Enabled = true
	if err := s.SaveEgress(cfg); err != nil {
		t.Fatal(err)
	}
	m := testManager(t, s, func(context.Context, string, string) (net.Conn, error) {
		direct.Add(1)
		return nil, errors.New("direct is forbidden")
	})
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	tlsConfig := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	seed, _ := s.Seed(c.ID)
	client := nativeClient(t, m, c)
	transport := &http.Transport{DialContext: client.DialContext, TLSClientConfig: tlsConfig, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport, Timeout: 20 * time.Second}).Get("https://example.com/")
	if err != nil {
		t.Fatal("Soar upstream HTTPS", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "verified through upstream" {
		t.Fatal(string(body))
	}
	for _, mode := range []string{"legacy", "cottenmax"} {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		status, err := cottenprobe.CheckHTTP(ctx, cottenprobe.Config{TunnelMode: mode, Domain: c.cottenZone(), Key: cottenKey(seed), Resolvers: []string{m.pc.LocalAddr().String()}, Transport: "udp", URL: "https://example.com/", TLSConfig: tlsConfig})
		cancel()
		if err != nil || status != 200 {
			t.Fatal(mode, status, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	result, err := masterprobe.CheckHTTPDetailed(ctx, masterprobe.Config{Domain: c.masterDNSZone(), Key: masterDNSKey(seed), Resolvers: []string{m.pc.LocalAddr().String()}, Transport: "udp", URL: "https://example.com/", TLSConfig: tlsConfig})
	cancel()
	if err != nil || !result.OK {
		t.Fatal("MasterDNS upstream", result, err)
	}
	if direct.Load() != 0 || dnsQueries.Load() < 1 || proxyStreams.Load() < 4 {
		t.Fatal("proxy/DNS coverage", direct.Load(), dnsQueries.Load(), proxyStreams.Load())
	}
	rows, err := m.outbound.query(context.Background(), outboundQuery{From: time.Now().Add(-time.Hour).UnixMilli(), Until: time.Now().UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	for _, engine := range []string{"soar", "cotten", "masterdns"} {
		found := false
		for _, row := range rows {
			if row.Engine == engine && row.Purpose == "traffic" && row.DestinationIP == "8.8.8.8" && row.UploadBytes > 0 && row.DownloadBytes > 0 {
				found = true
				if row.Route != "http" {
					t.Fatal("wrong route", row)
				}
			}
		}
		if !found {
			t.Fatal("missing native engine outbound evidence", engine)
		}
	}
}

func TestEgressRevisionReconcileClosesOldRouteAndRejectsInvalidSave(t *testing.T) {
	s := testStore(t)
	c := account(t, s, 0)
	a, b := net.Pipe()
	defer b.Close()
	m := testManager(t, s, func(context.Context, string, string) (net.Conn, error) { return a, nil })
	m.mu.RLock()
	old := m.accounts[c.ID]
	m.mu.RUnlock()
	conn, err := m.dialForEngine(old, context.Background(), "tcp", "8.8.8.8:443", upstreamSoar)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cfg, _ := s.Egress()
	cfg.Soar = EngineEgress{Mode: "http", Address: "127.0.0.1:8080", Ports: "443"}
	if err := s.SaveEgress(cfg); err != nil {
		t.Fatal(err)
	}
	if err := m.Reconcile(); err != nil {
		t.Fatal(err)
	}
	if old.ctx.Err() == nil {
		t.Fatal("old direct runtime survived route revision")
	}
	m.mu.RLock()
	current := m.accounts[c.ID]
	m.mu.RUnlock()
	if current == old {
		t.Fatal("egress revision failed to replace old runtime")
	}
	cfg, _ = s.Egress()
	cfg.MasterDNS.Unrestricted = true
	if err := s.SaveEgress(cfg); err == nil {
		t.Fatal("unrestricted direct ports accepted")
	}
	if err := m.Reconcile(); err != nil {
		t.Fatal(err)
	}
	m.mu.RLock()
	same := m.accounts[c.ID] == current
	m.mu.RUnlock()
	if !same || current.ctx.Err() != nil {
		t.Fatal("rejected save interrupted active runtime")
	}
	got, _ := s.Get(c.ID)
	if got != c {
		t.Fatal("route change modified account")
	}
}
