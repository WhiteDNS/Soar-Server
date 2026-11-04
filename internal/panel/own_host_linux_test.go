//go:build linux

package panel

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"testing"
	"time"

	cottenprobe "cottendns-go/probe"
	masterprobe "masterdnsvpn-go/probe"
)

const (
	ownTestIP   = "8.8.4.4"
	otherHostIP = "9.9.9.9"
)

// Soar's isLocal only trusts real interface addresses, so these tests give a
// private network namespace real public addresses and use the real dialer.
func inOwnHostNamespace(t *testing.T) bool {
	t.Helper()
	if os.Getenv("PANEL_TEST_OWN_HOST") != "" {
		return true
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root for a network namespace; CI runs this with sudo")
	}
	script := `ip link set lo up && ip addr add ` + ownTestIP + `/32 dev lo && ip addr add ` + otherHostIP + `/32 dev lo && exec "$0" "$@"`
	child := exec.Command("unshare", "-n", "sh", "-c", script, os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
	child.Env = append(os.Environ(), "PANEL_TEST_OWN_HOST=1")
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return false
}

func listenEcho(t *testing.T, address string) {
	t.Helper()
	ln, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); io.Copy(conn, conn) }()
		}
	}()
}

func ownHostManager(t *testing.T, ownPorts string, route func(*EgressConfig)) (*Manager, *accountRuntime, Client) {
	t.Helper()
	s := testStore(t)
	c, err := s.Create(ClientInput{Name: "own host", Engines: "all", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := s.Settings()
	cfg.PublicIP = ownTestIP
	cfg.AllowPorts = []uint16{14756, 2222}
	policy := DefaultAntiFloodPolicy()
	policy.Mode = "off"
	cfg.AntiFlood = &policy
	if err = s.SaveSettings(cfg); err != nil {
		t.Fatal(err)
	}
	egress, _ := s.Egress()
	egress.OwnHostPorts = ownPorts
	if route != nil {
		route(&egress)
	}
	if err = s.SaveEgress(egress); err != nil {
		t.Fatal(err)
	}
	m := testManager(t, s, nil)
	return m, m.accounts[c.ID], c
}

func dialOwn(m *Manager, rt *accountRuntime, target string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return m.dialForEngine(rt, ctx, "tcp", target, upstreamSoar)
}

func wantNotPublic(t *testing.T, conn net.Conn, err error, label string) {
	t.Helper()
	if err == nil {
		conn.Close()
		t.Fatal(label, "was opened")
	}
	if got := diagnosticReason(err); got != "destination_not_public" {
		t.Fatal(label, "wrong reason:", got, err)
	}
}

func TestOwnHostStaysBlockedUnlessThePortIsListed(t *testing.T) {
	if !inOwnHostNamespace(t) {
		return
	}
	listenEcho(t, ownTestIP+":14756")
	listenEcho(t, ownTestIP+":2222")
	listenEcho(t, otherHostIP+":14756")
	listenEcho(t, "127.0.0.1:14756")

	m, rt, _ := ownHostManager(t, "", nil)
	conn, err := dialOwn(m, rt, ownTestIP+":14756")
	wantNotPublic(t, conn, err, "empty list")

	m, rt, _ = ownHostManager(t, "14756", nil)
	conn, err = dialOwn(m, rt, ownTestIP+":14756")
	assertEcho(t, conn, err, "listed port")
	conn, err = dialOwn(m, rt, "[::ffff:"+ownTestIP+"]:14756")
	assertEcho(t, conn, err, "mapped form of the same address")
	conn, err = dialOwn(m, rt, ownTestIP+":2222")
	wantNotPublic(t, conn, err, "unlisted own port")

	// Every other inside address stays closed even on a listed port.
	for _, target := range []string{"127.0.0.1:14756", otherHostIP + ":14756", "[64:ff9b::808:404]:14756", "[2002:808:404::]:14756"} {
		conn, err = dialOwn(m, rt, target)
		wantNotPublic(t, conn, err, target)
	}

	m.lookupOverride = func(_ context.Context, _, host string) ([]netip.Addr, error) {
		if host == "rebind.example.org" {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
		return []netip.Addr{netip.MustParseAddr(ownTestIP)}, nil
	}
	conn, err = dialOwn(m, rt, "own.example.org:14756")
	assertEcho(t, conn, err, "hostname of the public IP")
	conn, err = dialOwn(m, rt, "rebind.example.org:14756")
	wantNotPublic(t, conn, err, "hostname rebinding to loopback")

	// The list only narrows the route: a port needs both to open.
	m, rt, _ = ownHostManager(t, "14756,3333", nil)
	if conn, err = dialOwn(m, rt, ownTestIP+":3333"); !errors.Is(err, errDestinationPort) || errors.Is(err, errDestinationNotPublic) {
		t.Fatal("own list opened a port outside the route list", conn, err)
	}

	// A panel whose address became unusable fails closed.
	// Saved through the store: the manager reloads its settings every second.
	settings, _ := m.store.Settings()
	settings.PublicIP = "10.0.0.5"
	if err = m.store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err = m.Reconcile(); err != nil {
		t.Fatal(err)
	}
	conn, err = dialOwn(m, rt, ownTestIP+":14756")
	wantNotPublic(t, conn, err, "unusable public IP")
}

func TestOwnHostFollowsTheListOnProxyRoutes(t *testing.T) {
	if !inOwnHostNamespace(t) {
		return
	}
	for _, mode := range []string{"socks5", "http"} {
		address, proxy := startMatrixProxy(t, mode, "", "")
		route := func(c *EgressConfig) { c.Soar = EngineEgress{Mode: mode, Address: address, Unrestricted: true} }
		m, rt, _ := ownHostManager(t, "", route)
		conn, err := dialOwn(m, rt, ownTestIP+":14756")
		wantNotPublic(t, conn, err, mode+" unrestricted without own list")
		if proxy.contacted() != 0 {
			t.Fatal(mode, "blocked target reached the proxy")
		}
		m, rt, _ = ownHostManager(t, "14756", route)
		conn, err = dialOwn(m, rt, ownTestIP+":14756")
		assertEcho(t, conn, err, mode+" listed")
		if !proxy.saw(ownTestIP + ":14756") {
			t.Fatal(mode, "own-host traffic skipped the proxy")
		}
	}
}

func TestNativeClientsReachListedOwnHostPort(t *testing.T) {
	if !inOwnHostNamespace(t) {
		return
	}
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "own host verified") }))
	origin.Listener.Close()
	ln, err := net.Listen("tcp", ownTestIP+":14756")
	if err != nil {
		t.Fatal(err)
	}
	origin.Listener = ln
	origin.StartTLS()
	defer origin.Close()
	insecure := &tls.Config{InsecureSkipVerify: true}
	url := "https://" + ownTestIP + ":14756/"

	m, _, c := ownHostManager(t, "14756", nil)
	seed, _ := m.store.Seed(c.ID)
	resolver := []string{m.pc.LocalAddr().String()}
	client := nativeClient(t, m, c)
	transport := &http.Transport{DialContext: client.DialContext, TLSClientConfig: insecure, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport, Timeout: 25 * time.Second}).Get(url)
	if err != nil {
		t.Fatal("Soar:", err)
	}
	response.Body.Close()
	for _, mode := range []string{"legacy", "cottenmax"} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		status, err := cottenprobe.CheckHTTP(ctx, cottenprobe.Config{TunnelMode: mode, Domain: c.cottenZone(), Key: cottenKey(seed), Resolvers: resolver, Transport: "udp", URL: url, TLSConfig: insecure})
		cancel()
		if err != nil || status != 200 {
			t.Fatal("Cotten", mode, status, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := masterprobe.CheckHTTPDetailed(ctx, masterprobe.Config{Domain: c.masterDNSZone(), Key: masterDNSKey(seed), Resolvers: resolver, Transport: "udp", URL: url, TLSConfig: insecure})
	if err != nil || !result.OK {
		t.Fatal("MasterDNS:", result, err)
	}
}
