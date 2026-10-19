package panel

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	cottenprobe "cottendns-go/probe"
	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/proxy"
)

func TestCottenInstallerHTTPSProbe(t *testing.T) {
	s := testStore(t)
	c, err := s.CreateInstallProbe()
	if err != nil {
		t.Fatal(err)
	}
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "Cotten installer HTTPS verified") }))
	defer origin.Close()
	m := testManager(t, s, func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, origin.Listener.Addr().String())
	})
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	seed, _ := s.Seed(c.ID)
	for _, transport := range []string{"udp", "tcp"} {
		t.Run(transport, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			code, err := cottenprobe.CheckHTTP(ctx, cottenprobe.Config{Domain: c.cottenZone(), Key: cottenKey(seed), Resolvers: []string{m.pc.LocalAddr().String()}, Transport: transport, URL: "https://example.com/", TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}})
			if err != nil || code != 200 {
				t.Fatal("installer Cotten HTTPS failed", code, err)
			}
		})
	}
}

func TestCottenUDPAccountingPreservesLargeDatagrams(t *testing.T) {
	s := testStore(t)
	c, err := s.Create(ClientInput{Name: "UDP", Engines: "cotten", Enabled: true, StreamLimit: 8, QuotaBytes: 200000})
	if err != nil {
		t.Fatal(err)
	}
	echo, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		buf := make([]byte, 65535)
		for {
			n, a, e := echo.ReadFrom(buf)
			if e != nil {
				return
			}
			echo.WriteTo(buf[:n], a)
		}
	}()
	m := testManager(t, s, func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, echo.LocalAddr().String())
	})
	m.mu.RLock()
	rt := m.accounts[c.ID]
	m.mu.RUnlock()
	conn, err := m.dial(rt, context.Background(), "udp", "8.8.8.8:443")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	payload := bytes.Repeat([]byte{42}, 50000)
	if n, err := conn.Write(payload); err != nil || n != len(payload) {
		t.Fatal("UDP write was split", n, err)
	}
	buf := make([]byte, 65535)
	n, err := conn.Read(buf)
	if err != nil || !bytes.Equal(buf[:n], payload) {
		t.Fatal("UDP packet truncated", n, err)
	}
	actual, _ := s.Get(c.ID)
	if actual.UploadBytes != 50000 || actual.DownloadBytes != 50000 {
		t.Fatal("wrong datagram billing", actual.Used())
	}
	if _, _, err = s.reserveDatagramEpoch(c.ID, 65535, true, c.UsageEpoch); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Get(c.ID)
	if _, err = conn.Write(payload); err == nil {
		t.Fatal("oversized quota reservation allowed")
	}
	after, _ := s.Get(c.ID)
	if after.Used() != before.Used() {
		t.Fatal("rejected UDP changed quota")
	}
}

func TestCottenProfilesAndLegacyAccounts(t *testing.T) {
	s := testStore(t)
	legacy := account(t, s, 0)
	before, _ := s.Profile(legacy.ID)
	links, err := s.Profiles(legacy.ID)
	if err != nil || len(links) != 1 || links["soar"] != before {
		t.Fatal("legacy profile changed", err)
	}
	c, err := s.Create(ClientInput{Name: "both", Engines: "both", Enabled: true, StreamLimit: 8})
	if err != nil {
		t.Fatal(err)
	}
	links, err = s.Profiles(c.ID)
	if err != nil || len(links) != 2 {
		t.Fatal("missing engine profiles", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(links["cotten"], "cottendns://"))
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Profile struct {
			ID     string
			Server struct {
				Domain string
				Key    string `json:"encryption_key"`
				Method int    `json:"encryption_method"`
			}
		}
	}
	if err = json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body.Profile.ID == c.ID || body.Profile.Server.Domain != c.cottenZone() || body.Profile.Server.Method != 5 || len(body.Profile.Server.Key) != 64 {
		t.Fatal("invalid Cotten profile")
	}
	oldKey := body.Profile.Server.Key
	if _, err = s.Rotate(c.ID); err != nil {
		t.Fatal(err)
	}
	links, _ = s.Profiles(c.ID)
	if strings.Contains(links["cotten"], oldKey) {
		t.Fatal("rotation retained key")
	}
	seed, _ := s.Seed(c.ID)
	if cottenKey(seed) == oldKey {
		t.Fatal("rotation did not revoke Cotten key")
	}
	// An older API caller omits engines while editing another field.
	c, _ = s.Get(c.ID)
	c, err = s.Edit(c.ID, ClientInput{Name: c.Name, Enabled: true, StreamLimit: 8, Revision: c.Revision})
	if err != nil || c.Engines != "both" {
		t.Fatal("old API reset engine selection", err)
	}
}

type cottenLog struct {
	sync.Mutex
	text bytes.Buffer
}

func (l *cottenLog) Write(p []byte) (int, error) { l.Lock(); defer l.Unlock(); return l.text.Write(p) }
func (l *cottenLog) String() string              { l.Lock(); defer l.Unlock(); return l.text.String() }

// CI builds the pinned native client once before go test. No fake decoder or
// substituted protocol implementation is involved in this regression test.
func TestCottenNativeClientAndSharedBudget(t *testing.T) {
	binary := os.Getenv("PANEL_COTTEN_CLIENT")
	if binary == "" {
		t.Skip("PANEL_COTTEN_CLIENT not set; CI supplies the pinned native engine")
	}
	for _, scenario := range []string{"udp", "tcp", "udp-alias", "tcp-alias"} {
		t.Run(scenario, func(t *testing.T) {
			transport := strings.TrimSuffix(scenario, "-alias")
			s := testStore(t)
			c, err := s.Create(ClientInput{Name: "native cotten", Engines: "both", Enabled: true, StreamLimit: 16, QuotaBytes: 2 << 20})
			if err != nil {
				t.Fatal(err)
			}
			settings, _ := s.Settings()
			settings.TunnelZones = []string{settings.BaseZone, "t.example.net"}
			if err = s.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			domain := c.cottenZone()
			if strings.HasSuffix(scenario, "-alias") {
				domain = "cot." + settings.accountZone(c, "t.example.net")
			}
			echo, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer echo.Close()
			udpEcho, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer udpEcho.Close()
			dnsEcho, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer dnsEcho.Close()
			go func() {
				buf := make([]byte, 4096)
				for {
					n, a, err := dnsEcho.ReadFrom(buf)
					if err != nil {
						return
					}
					if n >= 12 {
						reply := append([]byte(nil), buf[:n]...)
						reply[2] |= 0x80
						dnsEcho.WriteTo(reply, a)
					}
				}
			}()
			go func() {
				buf := make([]byte, 65535)
				for {
					n, a, err := udpEcho.ReadFrom(buf)
					if err != nil {
						return
					}
					udpEcho.WriteTo(buf[:n], a)
				}
			}()
			go func() {
				for {
					conn, err := echo.Accept()
					if err != nil {
						return
					}
					go func() { defer conn.Close(); io.Copy(conn, conn) }()
				}
			}()
			m := testManager(t, s, func(ctx context.Context, network, destination string) (net.Conn, error) {
				if strings.HasSuffix(destination, ":53") && strings.HasPrefix(network, "udp") {
					return (&net.Dialer{}).DialContext(ctx, network, dnsEcho.LocalAddr().String())
				}
				if strings.HasPrefix(network, "udp") {
					return (&net.Dialer{}).DialContext(ctx, network, udpEcho.LocalAddr().String())
				}
				return (&net.Dialer{}).DialContext(ctx, network, echo.Addr().String())
			})
			local, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			localPort := local.Addr().(*net.TCPAddr).Port
			local.Close()
			localDNS, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			dnsPort := localDNS.LocalAddr().(*net.UDPAddr).Port
			localDNS.Close()
			work := t.TempDir()
			seed, _ := s.Seed(c.ID)
			cfg := fmt.Sprintf(`PROTOCOL_TYPE = "SOCKS5"
LISTEN_IP = "127.0.0.1"
LISTEN_PORT = %d
LOCAL_DNS_ENABLED = true
LOCAL_DNS_IP = "127.0.0.1"
LOCAL_DNS_PORT = %d
SOCKS5_AUTH = false
DOMAINS = [%q]
ENCRYPTION_KEY = %q
DATA_ENCRYPTION_METHOD = 5
LEGACY_SESSION_ID = false
TERMINAL_UI = "plain"
STARTUP_MODE = "resolvers"
RESOLVER_TRANSPORT = %q
QUERY_TYPES = ["TXT", "CNAME", "A", "AAAA"]
MIN_UPLOAD_MTU = 80
MAX_UPLOAD_MTU = 142
MIN_DOWNLOAD_MTU = 16
MAX_DOWNLOAD_MTU = 1500
MTU_TEST_RETRIES_RESOLVERS = 0
MTU_TEST_TIMEOUT_RESOLVERS = 0.5
MTU_TEST_PARALLELISM_RESOLVERS = 1
FAST_CONNECT = true
UPLOAD_PACKET_DUPLICATION_COUNT = 1
DOWNLOAD_PACKET_DUPLICATION_COUNT = 1
UPLOAD_SETUP_PACKET_DUPLICATION_COUNT = 1
DOWNLOAD_SETUP_PACKET_DUPLICATION_COUNT = 1
RECHECK_INACTIVE_SERVERS_ENABLED = false
AUTO_DISABLE_TIMEOUT_SERVERS = false
LOG_LEVEL = "INFO"
`, localPort, dnsPort, domain, cottenKey(seed), transport)
			configPath := filepath.Join(work, "client.toml")
			resolvers := filepath.Join(work, "resolvers.txt")
			os.WriteFile(configPath, []byte(cfg), 0600)
			os.WriteFile(resolvers, []byte(m.pc.LocalAddr().String()+"\n"), 0600)
			cmd := exec.Command(binary, "--config", configPath, "--resolvers", resolvers)
			cmd.Dir = work
			log := &cottenLog{}
			cmd.Stdout, cmd.Stderr = log, log
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { cmd.Process.Kill(); cmd.Wait() }()
			deadline := time.Now().Add(20 * time.Second)
			for !strings.Contains(log.String(), "is listening") && time.Now().Before(deadline) {
				time.Sleep(50 * time.Millisecond)
			}
			if !strings.Contains(log.String(), "is listening") {
				t.Fatal("native client not ready:", log.String())
			}
			dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), nil, &net.Dialer{Timeout: 8 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			conn, err := dialer.Dial("tcp", "8.8.8.8:443")
			if err != nil {
				t.Fatal("native SOCKS dial failed", err, log.String())
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(15 * time.Second))
			payload := bytes.Repeat([]byte("Cotten real payload\n"), 2048)
			go func() { conn.Write(payload) }()
			got := make([]byte, len(payload))
			if _, err = io.ReadFull(conn, got); err != nil || !bytes.Equal(got, payload) {
				t.Fatal("native payload corrupted", err, log.String())
			}
			cottenNativeUDP(t, localPort, bytes.Repeat([]byte("U"), 50000))
			cottenNativeDNS(t, dnsPort)
			actual, _ := s.Get(c.ID)
			if actual.UploadBytes < int64(len(payload)+50000) || actual.DownloadBytes < int64(len(payload)+50000) {
				t.Fatal("Cotten payload not billed", actual.UploadBytes, actual.DownloadBytes)
			}
			// The unmodified Soar engine spends the same account budget.
			soarClient := nativeClient(t, m, c)
			soarConn, err := soarClient.DialContext(context.Background(), "tcp", "8.8.8.8:443")
			if err != nil {
				t.Fatal(err)
			}
			soarConn.SetDeadline(time.Now().Add(8 * time.Second))
			if _, err = soarConn.Write([]byte("Soar")); err != nil {
				t.Fatal(err)
			}
			if _, err = io.ReadFull(soarConn, make([]byte, 4)); err != nil {
				t.Fatal(err)
			}
			soarConn.Close()
			updated, _ := s.Get(c.ID)
			if updated.Used() < actual.Used()+8 {
				t.Fatal("engines did not share budget")
			}
			// Exhaust the remainder and synchronously revoke both runtimes.
			for {
				current, _ := s.Get(c.ID)
				if current.Used() >= c.QuotaBytes {
					break
				}
				if _, _, err := s.reserve(c.ID, int(c.QuotaBytes-current.Used()), true); err != nil {
					t.Fatal(err)
				}
			}
			if err = m.Reconcile(); err != nil {
				t.Fatal(err)
			}
			updated, _ = s.Get(c.ID)
			if updated.Used() != c.QuotaBytes || updated.Status(time.Now()) != "exhausted" {
				t.Fatal("shared quota exceeded or not enforced")
			}
			if m.ActiveCounts()[c.ID] != 0 {
				t.Fatal("exhausted account retained connections")
			}
		})
	}
}

func cottenNativeUDP(t *testing.T, port int, payload []byte) {
	t.Helper()
	control, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	control.SetDeadline(time.Now().Add(8 * time.Second))
	if _, err = control.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	var method [2]byte
	if _, err = io.ReadFull(control, method[:]); err != nil || method != [2]byte{5, 0} {
		t.Fatal("UDP SOCKS method", err)
	}
	if _, err = control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	var bound [10]byte
	if _, err = io.ReadFull(control, bound[:]); err != nil || bound[1] != 0 || bound[3] != 1 {
		t.Fatal("UDP SOCKS associate", bound, err)
	}
	addr := &net.UDPAddr{IP: net.IP(bound[4:8]), Port: int(binary.BigEndian.Uint16(bound[8:]))}
	udp, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	udp.SetDeadline(time.Now().Add(15 * time.Second))
	datagram := append([]byte{0, 0, 0, 1, 8, 8, 8, 8, 1, 187}, payload...)
	if _, err = udp.Write(datagram); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 65535)
	n, err := udp.Read(buffer)
	if err != nil || n < 10 || !bytes.Equal(buffer[10:n], payload) {
		t.Fatal("native large UDP datagram failed", n, err)
	}
}

func cottenNativeDNS(t *testing.T, port int) {
	t.Helper()
	name, _ := dnsmessage.NewName("example.com.")
	message := dnsmessage.Message{Header: dnsmessage.Header{ID: 1234, RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}
	query, _ := message.Pack()
	conn, err := net.DialTimeout("udp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(8 * time.Second))
	if _, err = conn.Write(query); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal("native tunneled DNS did not respond", err)
	}
	var answer dnsmessage.Message
	if err = answer.Unpack(buf[:n]); err != nil || !answer.Response || answer.ID != 1234 || len(answer.Questions) != 1 {
		t.Fatal("native tunneled DNS changed the response", err)
	}
}
