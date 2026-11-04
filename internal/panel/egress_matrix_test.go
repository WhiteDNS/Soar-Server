package panel

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// matrixProxy is a real forwarding proxy: every byte must cross it. Port 53
// answers DNS over TCP and every other destination echoes, so a test passes only
// when the panel really reached the requested target through this proxy.
type matrixProxy struct {
	mu      sync.Mutex
	targets []string
}

func (p *matrixProxy) note(target string) {
	p.mu.Lock()
	p.targets = append(p.targets, target)
	p.mu.Unlock()
}

func (p *matrixProxy) saw(target string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, t := range p.targets {
		if t == target {
			return true
		}
	}
	return false
}

func (p *matrixProxy) contacted() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.targets)
}

func serveMatrixTarget(conn io.ReadWriter, target string) {
	if !strings.HasSuffix(target, ":53") {
		io.Copy(conn, conn)
		return
	}
	for {
		var size [2]byte
		if _, err := io.ReadFull(conn, size[:]); err != nil {
			return
		}
		query := make([]byte, binary.BigEndian.Uint16(size[:]))
		if _, err := io.ReadFull(conn, query); err != nil {
			return
		}
		var m dnsmessage.Message
		if m.Unpack(query) != nil {
			return
		}
		m.Response = true
		if q := m.Questions[0]; q.Type == dnsmessage.TypeA {
			m.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: q.Type, Class: q.Class, TTL: 60}, Body: &dnsmessage.AResource{A: [4]byte{8, 8, 4, 4}}}}
		}
		answer, err := m.Pack()
		if err != nil {
			return
		}
		conn.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(answer))), answer...))
	}
}

func startMatrixProxy(t *testing.T, mode, user, pass string) (string, *matrixProxy) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	p := &matrixProxy{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(20 * time.Second))
				if mode == "http" {
					p.serveHTTP(conn, user, pass)
				} else {
					p.serveSocks(conn, user, pass)
				}
			}()
		}
	}()
	return ln.Addr().String(), p
}

func (p *matrixProxy) serveHTTP(conn net.Conn, user, pass string) {
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil || req.Method != http.MethodConnect {
		return
	}
	if user != "" && req.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)) {
		io.WriteString(conn, "HTTP/1.1 407 Proxy Authentication Required\r\n\r\n")
		return
	}
	p.note(req.Host)
	io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n")
	serveMatrixTarget(struct {
		io.Reader
		io.Writer
	}{reader, conn}, req.Host)
}

func (p *matrixProxy) serveSocks(conn net.Conn, user, pass string) {
	var head [2]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return
	}
	if _, err := io.ReadFull(conn, make([]byte, head[1])); err != nil {
		return
	}
	if user == "" {
		conn.Write([]byte{5, 0})
	} else {
		conn.Write([]byte{5, 2})
		var h [2]byte
		io.ReadFull(conn, h[:])
		gotUser := make([]byte, h[1])
		io.ReadFull(conn, gotUser)
		io.ReadFull(conn, h[:1])
		gotPass := make([]byte, h[0])
		io.ReadFull(conn, gotPass)
		if string(gotUser) != user || string(gotPass) != pass {
			conn.Write([]byte{1, 1})
			return
		}
		conn.Write([]byte{1, 0})
	}
	var req [4]byte
	if _, err := io.ReadFull(conn, req[:]); err != nil {
		return
	}
	target, err := readSocksAddress(conn, req[3])
	if err != nil {
		return
	}
	if req[1] == 3 {
		p.serveSocksUDP(conn)
		return
	}
	p.note(target)
	conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	serveMatrixTarget(conn, target)
}

// The relay echoes each datagram with its SOCKS header, as if the target replied.
func (p *matrixProxy) serveSocksUDP(control net.Conn) {
	relay, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return
	}
	defer relay.Close()
	port := relay.LocalAddr().(*net.UDPAddr).Port
	control.Write(append([]byte{5, 0, 0, 1, 127, 0, 0, 1}, byte(port>>8), byte(port)))
	go func() {
		buf := make([]byte, 65535)
		for {
			n, from, err := relay.ReadFrom(buf)
			if err != nil {
				return
			}
			reader := strings.NewReader(string(buf[4:n]))
			if target, e := readSocksAddress(reader, buf[3]); e == nil {
				p.note("udp/" + target)
			}
			relay.WriteTo(buf[:n], from)
		}
	}()
	io.Copy(io.Discard, control)
}

func matrixManager(t *testing.T, ports []uint16, set func(*EgressConfig)) (*Manager, *accountRuntime) {
	t.Helper()
	s := testStore(t)
	c, err := s.Create(ClientInput{Name: "matrix", Engines: "all", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := s.Settings()
	cfg.AllowPorts = ports
	policy := DefaultAntiFloodPolicy()
	policy.Mode = "off"
	cfg.AntiFlood = &policy
	if err = s.SaveSettings(cfg); err != nil {
		t.Fatal(err)
	}
	egress, _ := s.Egress()
	set(&egress)
	if err = s.SaveEgress(egress); err != nil {
		t.Fatal(err)
	}
	m := testManager(t, s, func(ctx context.Context, network, address string) (net.Conn, error) {
		local, peer := net.Pipe()
		go func() { defer peer.Close(); io.Copy(peer, peer) }()
		return local, nil
	})
	m.lookupOverride = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.4.4")}, nil
	}
	return m, m.accounts[c.ID]
}

func assertEcho(t *testing.T, conn net.Conn, err error, label string) {
	t.Helper()
	if err != nil {
		t.Fatal(label, "dial failed:", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(8 * time.Second))
	want := "payload for " + label
	if _, err = conn.Write([]byte(want)); err != nil {
		t.Fatal(label, "write failed:", err)
	}
	got := make([]byte, len(want))
	if _, err = io.ReadFull(conn, got); err != nil || string(got) != want {
		t.Fatal(label, "bytes did not cross the route:", err, string(got))
	}
}

var matrixEngines = []struct {
	name   string
	engine upstreamEngine
	set    func(*EgressConfig, EngineEgress)
}{
	{"soar", upstreamSoar, func(c *EgressConfig, r EngineEgress) { c.Soar = r }},
	{"cotten", upstreamCotten, func(c *EgressConfig, r EngineEgress) { c.Cotten = r }},
	{"masterdns", upstreamMasterDNS, func(c *EgressConfig, r EngineEgress) { c.MasterDNS = r }},
}

var matrixPorts = []uint16{22, 1052, 8443, 14756, 65535}

func TestDirectRouteOpensEveryListedPortForEveryEngine(t *testing.T) {
	for _, e := range matrixEngines {
		t.Run(e.name, func(t *testing.T) {
			m, rt := matrixManager(t, matrixPorts, func(*EgressConfig) {})
			for _, port := range matrixPorts {
				for _, host := range []string{"8.8.8.8", "[2001:4860:4860::8888]", "app.example.org"} {
					target := host + ":" + strconv.Itoa(int(port))
					conn, err := m.dialForEngine(rt, context.Background(), "tcp", target, e.engine)
					assertEcho(t, conn, err, "direct "+target)
				}
				conn, err := m.dialForEngine(rt, context.Background(), "udp", "8.8.8.8:"+strconv.Itoa(int(port)), e.engine)
				assertEcho(t, conn, err, "direct udp")
			}
			if _, err := m.dialForEngine(rt, context.Background(), "tcp", "8.8.8.8:80", e.engine); !errors.Is(err, errDestinationPort) {
				t.Fatal("port outside the list was opened", err)
			}
		})
	}
}

func TestProxyRoutesOpenEveryAllowedPortForEveryEngine(t *testing.T) {
	type policy struct {
		name, ports  string
		unrestricted bool
	}
	policies := []policy{
		{"list", "22,1052,8443,14756,65535", false},
		{"persian-commas", "22،1052 8443\n14756,65535", false},
		{"range", "1-65535", false},
		{"unrestricted", "", true},
	}
	for _, e := range matrixEngines {
		for _, mode := range []string{"socks5", "http"} {
			for _, auth := range []bool{false, true} {
				for _, pol := range policies {
					name := fmt.Sprintf("%s/%s/auth=%v/%s", e.name, mode, auth, pol.name)
					t.Run(name, func(t *testing.T) {
						user, pass := "", ""
						if auth {
							user, pass = "proxy-user", "proxy-secret"
						}
						address, proxy := startMatrixProxy(t, mode, user, pass)
						m, rt := matrixManager(t, []uint16{443}, func(c *EgressConfig) {
							e.set(c, EngineEgress{Mode: mode, Address: address, Username: user, Password: pass, Ports: pol.ports, Unrestricted: pol.unrestricted})
						})
						for _, port := range matrixPorts {
							p := strconv.Itoa(int(port))
							conn, err := m.dialForEngine(rt, context.Background(), "tcp", "8.8.8.8:"+p, e.engine)
							assertEcho(t, conn, err, "ip "+p)
							if !proxy.saw("8.8.8.8:" + p) {
								t.Fatal("traffic did not use the proxy", p)
							}
							conn, err = m.dialForEngine(rt, context.Background(), "tcp", "[2001:4860:4860::8888]:"+p, e.engine)
							assertEcho(t, conn, err, "ipv6 "+p)
							conn, err = m.dialForEngine(rt, context.Background(), "tcp", "app.example.org:"+p, e.engine)
							assertEcho(t, conn, err, "hostname "+p)
							if !proxy.saw("8.8.4.4:" + p) {
								t.Fatal("hostname was not resolved through the proxy", p)
							}
							conn, err = m.dialForEngine(rt, context.Background(), "udp", "8.8.8.8:"+p, e.engine)
							if mode == "http" {
								if !errors.Is(err, errProxyUDP) {
									t.Fatal("HTTP proxy must refuse UDP clearly", err)
								}
							} else {
								assertEcho(t, conn, err, "udp "+p)
							}
						}
					})
				}
			}
		}
	}
}

func TestProxyPortListIsEnforcedAndKeepsEnginesSeparate(t *testing.T) {
	address, proxy := startMatrixProxy(t, "socks5", "", "")
	m, rt := matrixManager(t, []uint16{443}, func(c *EgressConfig) {
		c.Soar = EngineEgress{Mode: "socks5", Address: address, Ports: "22,1000-2000"}
	})
	for _, port := range []string{"22", "1000", "2000"} {
		conn, err := m.dialForEngine(rt, context.Background(), "tcp", "8.8.8.8:"+port, upstreamSoar)
		assertEcho(t, conn, err, "allowed "+port)
	}
	before := proxy.contacted()
	for _, port := range []string{"21", "23", "999", "2001", "443"} {
		if _, err := m.dialForEngine(rt, context.Background(), "tcp", "8.8.8.8:"+port, upstreamSoar); !errors.Is(err, errDestinationPort) {
			t.Fatal("port outside the proxy list was opened", port, err)
		}
	}
	if proxy.contacted() != before {
		t.Fatal("denied port still reached the proxy")
	}
	// Cotten stays direct: it uses the server list and never touches Soar's proxy.
	conn, err := m.dialForEngine(rt, context.Background(), "tcp", "8.8.8.8:443", upstreamCotten)
	assertEcho(t, conn, err, "cotten direct")
	if _, err = m.dialForEngine(rt, context.Background(), "tcp", "8.8.8.8:22", upstreamCotten); !errors.Is(err, errDestinationPort) {
		t.Fatal("Soar's proxy list leaked into Cotten", err)
	}
	if proxy.contacted() != before {
		t.Fatal("Cotten traffic reached Soar's proxy")
	}
}

func TestPortChangesApplyAfterSaveThroughTheAPI(t *testing.T) {
	b, s, h := testBrowser(t)
	c, err := s.Create(ClientInput{Name: "ports", Engines: "all", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.manager.Reconcile(); err != nil {
		t.Fatal(err)
	}
	h.manager.dialOverride = func(context.Context, string, string) (net.Conn, error) {
		local, peer := net.Pipe()
		go func() { defer peer.Close(); io.Copy(peer, peer) }()
		return local, nil
	}
	dial := func(port string) error {
		h.manager.mu.RLock()
		rt := h.manager.accounts[c.ID]
		h.manager.mu.RUnlock()
		conn, err := h.manager.dialForEngine(rt, context.Background(), "tcp", "8.8.8.8:"+port, upstreamSoar)
		if conn != nil {
			conn.Close()
		}
		return err
	}
	if err = dial("14756"); !errors.Is(err, errDestinationPort) {
		t.Fatal("port was open before being added", err)
	}
	b.login()
	cfg, _ := s.Settings()
	cfg.AllowPorts = append(cfg.AllowPorts, 14756)
	b.request("/api/settings", "PUT", cfg, 200)
	if err = dial("14756"); err != nil {
		t.Fatal("saved port did not open", err)
	}
	cfg.AllowPorts = []uint16{80, 443}
	b.request("/api/settings", "PUT", cfg, 200)
	if err = dial("14756"); !errors.Is(err, errDestinationPort) {
		t.Fatal("removed port stayed open", err)
	}
}
