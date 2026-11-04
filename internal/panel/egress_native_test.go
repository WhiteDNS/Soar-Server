package panel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	cottenprobe "cottendns-go/probe"
	masterprobe "masterdnsvpn-go/probe"
)

const nativeCustomPort = "14756"

// Real client protocols, real engines and a real forwarding proxy on a port that
// is neither 80 nor 443. Every engine's bytes must reach the HTTPS origin.
func TestNativeClientsReachCustomPortOnEveryRoute(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "custom port verified") }))
	defer origin.Close()
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	tlsConfig := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	url := "https://example.com:" + nativeCustomPort + "/"
	for _, tc := range []struct {
		name, mode, ports string
		unrestricted      bool
	}{
		{name: "direct", mode: "direct", ports: "80,443"},
		{name: "socks5-list", mode: "socks5", ports: nativeCustomPort},
		{name: "http-list", mode: "http", ports: nativeCustomPort},
		{name: "socks5-range", mode: "socks5", ports: "10000-20000"},
		{name: "http-unrestricted", mode: "http", unrestricted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			c, err := s.Create(ClientInput{Name: "native custom port", Engines: "all", Enabled: true, StreamLimit: 16})
			if err != nil {
				t.Fatal(err)
			}
			cfg, _ := s.Settings()
			cfg.AllowPorts = []uint16{14756}
			if err = s.SaveSettings(cfg); err != nil {
				t.Fatal(err)
			}
			var proxy *matrixProxy
			if tc.mode != "direct" {
				var address string
				address, proxy = runMatrixProxy(t, &matrixProxy{forward: origin.Listener.Addr().String()}, tc.mode, "", "")
				egress, _ := s.Egress()
				route := EngineEgress{Mode: tc.mode, Address: address, Ports: tc.ports, Unrestricted: tc.unrestricted}
				egress.Soar, egress.Cotten, egress.MasterDNS = route, route, route
				if err = s.SaveEgress(egress); err != nil {
					t.Fatal(err)
				}
			}
			m := testManager(t, s, func(ctx context.Context, network, _ string) (net.Conn, error) {
				if tc.mode != "direct" {
					return nil, errors.New("direct is forbidden on a proxy route")
				}
				return (&net.Dialer{}).DialContext(ctx, network, origin.Listener.Addr().String())
			})
			m.lookupOverride = func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("8.8.4.4")}, nil
			}
			seed, _ := s.Seed(c.ID)
			resolver := []string{m.pc.LocalAddr().String()}

			client := nativeClient(t, m, c)
			transport := &http.Transport{DialContext: client.DialContext, TLSClientConfig: tlsConfig, DisableKeepAlives: true}
			defer transport.CloseIdleConnections()
			response, err := (&http.Client{Transport: transport, Timeout: 25 * time.Second}).Get(url)
			if err != nil {
				t.Fatal("Soar:", err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if string(body) != "custom port verified" {
				t.Fatal("Soar body:", string(body))
			}
			for _, mode := range []string{"legacy", "cottenmax"} {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				status, err := cottenprobe.CheckHTTP(ctx, cottenprobe.Config{TunnelMode: mode, Domain: c.cottenZone(), Key: cottenKey(seed), Resolvers: resolver, Transport: "udp", URL: url, TLSConfig: tlsConfig})
				cancel()
				if err != nil || status != 200 {
					t.Fatal("Cotten", mode, status, err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			result, err := masterprobe.CheckHTTPDetailed(ctx, masterprobe.Config{Domain: c.masterDNSZone(), Key: masterDNSKey(seed), Resolvers: resolver, Transport: "udp", URL: url, TLSConfig: tlsConfig})
			cancel()
			if err != nil || !result.OK {
				t.Fatal("MasterDNS:", result, err)
			}
			if proxy != nil && !proxy.saw("8.8.4.4:"+nativeCustomPort) {
				t.Fatal("custom port did not cross the proxy")
			}
		})
	}
}

func TestSlowProxyWithOneAddressGetsTheFullDialTime(t *testing.T) {
	p := &matrixProxy{connectDelay: 7 * time.Second}
	address, _ := runMatrixProxy(t, p, "socks5", "", "")
	m, rt := matrixManager(t, []uint16{443}, func(c *EgressConfig) {
		c.Soar = EngineEgress{Mode: "socks5", Address: address, Unrestricted: true}
	})
	conn, err := m.dialForEngine(rt, context.Background(), "tcp", "8.8.8.8:14756", upstreamSoar)
	assertEcho(t, conn, err, "slow proxy")
}

func TestSocksUDPAcceptsIPv4MappedReplySource(t *testing.T) {
	local, peer := net.Pipe()
	defer peer.Close()
	control, controlPeer := net.Pipe()
	defer controlPeer.Close()
	u := &socksUDPConn{Conn: local, control: control, target: netip.MustParseAddrPort("8.8.8.8:53")}
	defer u.Close()
	mapped := netip.MustParseAddr("::ffff:8.8.8.8").As16()
	reply := append([]byte{0, 0, 0, 4}, mapped[:]...)
	reply = append(reply, 0, 53)
	reply = append(reply, "answer"...)
	go peer.Write(reply)
	u.Conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	b := make([]byte, 64)
	n, err := u.Read(b)
	if err != nil || string(b[:n]) != "answer" {
		t.Fatal("mapped reply source was dropped:", string(b[:n]), err)
	}
}

func TestProxyThatRefusesDNSHasAClearReasonAndKeepsIPTargets(t *testing.T) {
	for _, mode := range []string{"http", "socks5"} {
		t.Run(mode, func(t *testing.T) {
			address, _ := runMatrixProxy(t, &matrixProxy{refuseDNS: true}, mode, "", "")
			m, rt := matrixManager(t, []uint16{443}, func(c *EgressConfig) {
				c.Soar = EngineEgress{Mode: mode, Address: address, Unrestricted: true}
			})
			conn, err := m.dialForEngine(rt, context.Background(), "tcp", "8.8.8.8:14756", upstreamSoar)
			assertEcho(t, conn, err, "ip target")
			_, err = m.dialForEngine(rt, context.Background(), "tcp", "app.example.org:14756", upstreamSoar)
			if got := diagnosticReason(err); got != "proxy_dns_failed" {
				t.Fatal("wrong reason for refused proxy DNS:", got, err)
			}
		})
	}
}
