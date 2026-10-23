package panel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

func TestEngineSelection(t *testing.T) {
	for selection, want := range map[string][]string{
		"": {"soar"}, "both": {"soar", "cotten"}, "all": {"soar", "cotten", "stormdns", "masterdns"},
		"stormdns": {"stormdns"}, "masterdns": {"masterdns"}, "masterdns,stormdns": {"stormdns", "masterdns"},
	} {
		got, err := enabledEngines(selection)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%q: %v, %v", selection, got, err)
		}
	}
	for _, invalid := range []string{"unknown", "both,stormdns", "soar,soar", ",stormdns", "stormdns,", "Soar"} {
		if _, err := enabledEngines(invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
}

func TestAdditionalProfilesDomainsAndKeyRotation(t *testing.T) {
	s := testStore(t)
	c, err := s.Create(ClientInput{Name: "Four engines", Engines: "all", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := s.Settings()
	cfg.TunnelZones = []string{cfg.BaseZone, "t.example.net"}
	if err := s.SaveSettings(cfg); err != nil {
		t.Fatal(err)
	}
	seed, _ := s.Seed(c.ID)
	keys := map[string]string{}
	for _, zone := range cfg.orderedZones() {
		links, err := s.profilesAt(c, cfg, zone)
		if err != nil || len(links) != 4 {
			t.Fatalf("profiles: %v %v", links, err)
		}
		for _, name := range []string{"stormdns", "masterdns"} {
			raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(links[name], name+"://"))
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				Schema  string `json:"schema"`
				Version int    `json:"version"`
				Profile struct {
					Server struct {
						Domain string `json:"domain"`
						Key    string `json:"encryption_key"`
						Method int    `json:"encryption_method"`
					} `json:"server"`
				} `json:"profile"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			server := body.Profile.Server
			if body.Schema != "whitedns.profile" || body.Version != 1 || server.Method != 5 || server.Domain != enginePrefix(name)+cfg.accountZone(c, zone) || server.Key != additionalKey(seed, name) {
				t.Fatalf("invalid %s profile: %s", name, raw)
			}
			keys[name] = server.Key
		}
	}
	if keys["stormdns"] == keys["masterdns"] || keys["stormdns"] == cottenKey(seed) {
		t.Fatal("engine keys were reused")
	}
	if _, err := s.Rotate(c.ID); err != nil {
		t.Fatal(err)
	}
	newSeed, _ := s.Seed(c.ID)
	for name, old := range keys {
		if additionalKey(newSeed, name) == old {
			t.Fatal("rotation preserved engine key")
		}
	}
	for _, name := range []string{"stormdns", "masterdns"} {
		c.Engines = name
		links, err := s.profiles(c, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if link, err := selectedProfile(links, ""); err != nil || !strings.HasPrefix(link, name+"://") {
			t.Fatalf("default link: %q %v", link, err)
		}
	}
}

type testDNSEngine struct{ response []byte }

func (e *testDNSEngine) HandleDNS([]byte) []byte { return e.response }
func (e *testDNSEngine) Close() error            { return nil }

func TestAdditionalRoutingNSAndDisabledEngine(t *testing.T) {
	s := testStore(t)
	c, err := s.Create(ClientInput{Name: "routing", Engines: "all", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	m := testManager(t, s, nil)
	m.mu.RLock()
	rt := m.accounts[c.ID]
	m.mu.RUnlock()
	rt.additionalMu.Lock()
	rt.additional = map[string]dnsEngine{"stormdns": &testDNSEngine{[]byte("storm")}, "masterdns": &testDNSEngine{[]byte("master")}}
	rt.additionalMu.Unlock()
	for _, name := range []string{"stormdns", "masterdns"} {
		for _, typ := range []uint16{16, 2, 5, 33} {
			q := make([]byte, 12)
			binary.BigEndian.PutUint16(q[4:6], 1)
			q = append(q, dnsName("payload."+enginePrefix(name)+c.Zone)...)
			q = binary.BigEndian.AppendUint16(q, typ)
			q = binary.BigEndian.AppendUint16(q, 1)
			replies := make(chan []byte, 1)
			m.routeDNS(q, nil, false, func(b []byte) { replies <- b })
			select {
			case b := <-replies:
				if string(b) != strings.TrimSuffix(enginePrefix(name), ".") {
					t.Fatalf("%s type %d intercepted: %q", name, typ, b)
				}
			case <-time.After(time.Second):
				t.Fatal("query was not routed")
			}
		}
	}
	in := input(c)
	in.Engines = "soar"
	if _, err := s.Edit(c.ID, in); err != nil {
		t.Fatal(err)
	}
	if err := m.Reconcile(); err != nil {
		t.Fatal(err)
	}
	q := make([]byte, 12)
	binary.BigEndian.PutUint16(q[4:6], 1)
	q = append(q, dnsName("payload.storm."+c.Zone)...)
	q = binary.BigEndian.AppendUint16(q, 16)
	q = binary.BigEndian.AppendUint16(q, 1)
	var got []byte
	m.routeDNS(q, nil, false, func(b []byte) { got = b })
	if len(got) < 4 || got[3]&15 != 5 {
		t.Fatal("disabled engine was not refused")
	}
}

// Real upstream clients test the wire protocol, not a substitute packet decoder.
func TestAdditionalNativeClients(t *testing.T) {
	for _, scenario := range []struct{ engine, client, query string }{
		{"stormdns", "STORM", "TXT"}, {"stormdns", "STORM", "NS"},
		{"stormdns", "STORM", "CNAME"}, {"stormdns", "STORM", "SRV"},
		{"masterdns", "MASTER", "TXT"}, {"masterdns", "STORM", "TXT"},
	} {
		t.Run(scenario.engine+"-"+scenario.client+"-"+scenario.query, func(t *testing.T) {
			binaryPath := os.Getenv("PANEL_" + scenario.client + "_CLIENT")
			if binaryPath == "" {
				t.Skip("CI supplies pinned upstream native clients")
			}
			s := testStore(t)
			c, err := s.Create(ClientInput{Name: "native", Engines: "all", Enabled: true, StreamLimit: 2, QuotaBytes: 128 << 10})
			if err != nil {
				t.Fatal(err)
			}
			echo, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer echo.Close()
			go func() {
				for {
					conn, err := echo.Accept()
					if err != nil {
						return
					}
					go func() { defer conn.Close(); io.Copy(conn, conn) }()
				}
			}()
			m := testManager(t, s, func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, echo.Addr().String())
			})
			local, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := local.Addr().(*net.TCPAddr).Port
			local.Close()
			work := t.TempDir()
			seed, _ := s.Seed(c.ID)
			configText := fmt.Sprintf(`PROTOCOL_TYPE = "SOCKS5"
LISTEN_IP = "127.0.0.1"
LISTEN_PORT = %d
LOCAL_DNS_ENABLED = false
SOCKS5_AUTH = false
DOMAINS = [%q]
ENCRYPTION_KEY = %q
DATA_ENCRYPTION_METHOD = 5
STARTUP_MODE = "resolvers"
MIN_UPLOAD_MTU = 80
MAX_UPLOAD_MTU = 100
MIN_DOWNLOAD_MTU = 80
MAX_DOWNLOAD_MTU = 500
MTU_TEST_RETRIES_RESOLVERS = 0
MTU_TEST_TIMEOUT_RESOLVERS = 0.5
MTU_TEST_PARALLELISM_RESOLVERS = 1
AUTO_DISABLE_TIMEOUT_SERVERS = false
LOG_LEVEL = "INFO"
DNS_QUERY_TYPE = %q
`, port, enginePrefix(scenario.engine)+c.Zone, additionalKey(seed, scenario.engine), scenario.query)
			cfg := filepath.Join(work, "client.toml")
			resolvers := filepath.Join(work, "resolvers.txt")
			if err := os.WriteFile(cfg, []byte(configText), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(resolvers, []byte(m.pc.LocalAddr().String()+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(binaryPath, "--config", cfg, "--resolvers", resolvers)
			cmd.Dir = work
			log := &cottenLog{}
			cmd.Stdout, cmd.Stderr = log, log
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { cmd.Process.Kill(); cmd.Wait() }()
			deadline := time.Now().Add(20 * time.Second)
			for !strings.Contains(log.String(), "is listening") && time.Now().Before(deadline) {
				time.Sleep(30 * time.Millisecond)
			}
			if !strings.Contains(log.String(), "is listening") {
				t.Fatal("native client not ready: ", log.String())
			}
			dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", port), nil, &net.Dialer{Timeout: 5 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			conn, err := dialer.Dial("tcp", "8.8.8.8:443")
			if err != nil {
				t.Fatal(err, log.String())
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(10 * time.Second))
			payload := bytes.Repeat([]byte("real upstream payload\n"), 256)
			go func() { conn.Write(payload) }()
			got := make([]byte, len(payload))
			if _, err := io.ReadFull(conn, got); err != nil {
				t.Fatal(err, log.String())
			}
			if !bytes.Equal(got, payload) {
				t.Fatal("tunnel corrupted data")
			}
			actual, _ := s.Get(c.ID)
			if actual.UploadBytes < int64(len(payload)) || actual.DownloadBytes < int64(len(payload)) {
				t.Fatalf("unmetered traffic: %+v", actual)
			}
			soarClient := nativeClient(t, m, c)
			other, err := soarClient.DialContext(context.Background(), "tcp", "8.8.8.8:443")
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			other.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err := other.Write([]byte("shared")); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 6)
			if _, err := io.ReadFull(other, buf); err != nil {
				t.Fatal(err)
			}
			m.mu.RLock()
			rt := m.accounts[c.ID]
			m.mu.RUnlock()
			if _, err := m.dialForEngine(rt, context.Background(), "tcp", "8.8.8.8:443", upstreamMaster); err != errAccountStreamLimit {
				t.Fatalf("shared stream cap: %v", err)
			}
			engine, err := m.additionalFor(rt, scenario.engine)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Rotate(c.ID); err != nil {
				t.Fatal(err)
			}
			if reply := engine.HandleDNS([]byte{0}); reply != nil {
				t.Fatal("retired key accepted traffic")
			}
			if err := m.Reconcile(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
