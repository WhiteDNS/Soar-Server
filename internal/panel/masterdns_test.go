package panel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cottenprobe "cottendns-go/probe"
	masterprobe "masterdnsvpn-go/probe"
)

func TestMasterDNSNativeHTTPSAndAccounting(t *testing.T) {
	s := testStore(t)
	c, err := s.Create(ClientInput{Name: "MasterDNS native", Engines: "all", Enabled: true, QuotaBytes: 1 << 20, StreamLimit: 8})
	if err != nil {
		t.Fatal(err)
	}
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "MasterDNS native HTTPS verified") }))
	defer origin.Close()
	m := testManager(t, s, func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, origin.Listener.Addr().String())
	})
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	seed, _ := s.Seed(c.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	result, err := masterprobe.CheckHTTPDetailed(ctx, masterprobe.Config{Domain: c.masterDNSZone(), Key: masterDNSKey(seed), Resolvers: []string{m.pc.LocalAddr().String()}, Transport: "udp", URL: "https://example.com/", TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}})
	if err != nil || !result.OK || !result.Handshake || result.HTTPStatus != 200 {
		t.Fatalf("native MasterDNS HTTPS failed: %+v %v", result, err)
	}
	actual, err := s.Get(c.ID)
	if err != nil || actual.UploadBytes == 0 || actual.DownloadBytes == 0 {
		t.Fatalf("MasterDNS usage not billed: %+v %v", actual, err)
	}
	d := m.Diagnostics()
	if d.MasterDNSStarts != 1 || d.MasterDNSOpenedStreams == 0 {
		t.Fatalf("missing MasterDNS diagnostics: %+v", d)
	}
	// All three authenticated engines spend this same account's budget.
	soarClient := nativeClient(t, m, c)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DialContext: soarClient.DialContext, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport, Timeout: 15 * time.Second}).Get("https://example.com/")
	if err != nil {
		t.Fatal("Soar alongside MasterDNS failed", err)
	}
	_, err = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 {
		t.Fatal("Soar HTTPS failed", err)
	}
	afterSoar, _ := s.Get(c.ID)
	if afterSoar.Used() <= actual.Used() {
		t.Fatal("Soar did not share the MasterDNS budget")
	}
	code, err := cottenprobe.CheckHTTP(ctx, cottenprobe.Config{Domain: c.cottenZone(), Key: cottenKey(seed), Resolvers: []string{m.pc.LocalAddr().String()}, Transport: "udp", URL: "https://example.com/", TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}})
	if err != nil || code != 200 {
		t.Fatal("Cotten alongside MasterDNS failed", err)
	}
	afterCotten, _ := s.Get(c.ID)
	if afterCotten.Used() <= afterSoar.Used() {
		t.Fatal("Cotten did not share the MasterDNS budget")
	}
	in := accountInput(afterCotten)
	in.QuotaBytes = afterCotten.Used()
	if _, err = s.Edit(c.ID, in); err != nil {
		t.Fatal(err)
	}
	if err = m.Reconcile(); err != nil {
		t.Fatal(err)
	}
	m.mu.RLock()
	present := m.accounts[c.ID] != nil
	m.mu.RUnlock()
	if present {
		t.Fatal("exhausted shared quota left an engine active")
	}
}

func TestMasterDNSSubscriptionAndBackup(t *testing.T) {
	_, s, h := testBrowser(t)
	limit := 9
	c, err := s.Create(ClientInput{Name: "three-engine backup", Engines: "all", Enabled: true, QuotaBytes: 123456, MasterDNSSessionLimit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.SubscriptionToken(c.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	original := publicSubscription(t, h, "/sub/"+token+"?format=plain", 200).Body.String()
	for _, scheme := range []string{"soar://", "cottendns://", "masterdns://"} {
		if !strings.Contains(original, scheme) {
			t.Fatal("missing subscription engine", scheme)
		}
	}
	path := filepath.Join(t.TempDir(), "restore.db")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Backup(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = InspectBackupFile(path); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	before, _ := s.Profiles(c.ID)
	after, _ := restored.Profiles(c.ID)
	for _, engine := range profileEngines {
		if before[engine] != after[engine] {
			t.Fatal("backup changed engine credential", engine)
		}
	}
	saved, err := restored.Get(c.ID)
	if err != nil || saved.MasterDNSSessionLimit != 9 || saved.Engines != "all" {
		t.Fatal("backup changed MasterDNS policy", saved, err)
	}
	got, _, err := restored.Subscription(token)
	if err != nil || got.ID != c.ID {
		t.Fatal("backup changed subscription identity", err)
	}
}

func TestMasterDNSTelegramAccountControls(t *testing.T) {
	bot, _, _ := newTelegramTest(t)
	mustHandle(t, bot, tgCallback(1, "new"))
	message := tgMessage(2, "MasterDNS Telegram")
	message.Message.Reply = &struct {
		ID int64 `json:"message_id"`
	}{bot.conversation.Prompt}
	mustHandle(t, bot, message)
	key := bot.conversation.Key
	mustHandle(t, bot, tgCallback(3, "set:"+key+":engines:all"))
	if err := bot.setField(bot.conversation, "masterdns", "9"); err != nil {
		t.Fatal(err)
	}
	mustHandle(t, bot, tgCallback(4, "save:"+key))
	mustHandle(t, bot, tgCallback(5, "ok:"+bot.confirmation.Key))
	list, err := bot.h.store.List()
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	c := list[0]
	if c.Engines != "all" || c.MasterDNSSessionLimit != 9 {
		t.Fatal("Telegram lost MasterDNS policy", c)
	}
	if err = bot.callback(context.Background(), "edit:"+c.ID); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"0", "256", "1.5"} {
		if bot.setField(bot.conversation, "masterdns", value) == nil {
			t.Fatal("Telegram accepted invalid MasterDNS session limit", value)
		}
	}
}

func TestMasterDNSSelectionsProfilesAndLegacy(t *testing.T) {
	s := testStore(t)
	for selection, engines := range engineSelections {
		t.Run("engines="+selection, func(t *testing.T) {
			c, err := s.Create(ClientInput{Name: "selection", Engines: selection, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			if c.MasterDNSSessionLimit != 255 {
				t.Fatal("missing default MasterDNS session limit")
			}
			links, err := s.Profiles(c.ID)
			if err != nil || len(links) != len(engines) {
				t.Fatal("wrong profile count", links, err)
			}
			for _, engine := range engines {
				if links[engine] == "" {
					t.Fatal("missing profile", engine)
				}
			}
			if link := links["masterdns"]; link != "" {
				raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(link, "masterdns://"))
				var p struct {
					Schema  string `json:"schema"`
					Profile struct {
						ID     string `json:"id"`
						Server struct {
							Domain string `json:"domain"`
							Key    string `json:"encryption_key"`
							Method int    `json:"encryption_method"`
						} `json:"server"`
					} `json:"profile"`
				}
				if err != nil || json.Unmarshal(raw, &p) != nil || p.Schema != "whitedns.profile" || p.Profile.Server.Domain != c.masterDNSZone() || p.Profile.Server.Method != 5 {
					t.Fatal("invalid MasterDNS profile", string(raw))
				}
				seed, _ := s.Seed(c.ID)
				if p.Profile.Server.Key != masterDNSKey(seed) || p.Profile.Server.Key == cottenKey(seed) {
					t.Fatal("MasterDNS account key is not isolated")
				}
			}
			if _, err := selectedProfile(links, ""); err != nil {
				t.Fatal("default profile unavailable", err)
			}
		})
	}
	if (Client{Engines: "both"}).hasEngine("masterdns") || (Client{}).hasEngine("masterdns") {
		t.Fatal("legacy account opted into MasterDNS")
	}
	for _, limit := range []int{0, 256, -1} {
		if _, err := s.Create(ClientInput{Name: "bad", Engines: "masterdns", Enabled: true, MasterDNSSessionLimit: &limit}); err == nil {
			t.Fatal("invalid MasterDNS session limit accepted", limit)
		}
	}
}

func TestMasterDNSLifecycleAndPolicy(t *testing.T) {
	s := testStore(t)
	c, err := s.Create(ClientInput{Name: "policy", Engines: "masterdns", Enabled: true, StreamLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	m := testManager(t, s, nil)
	m.mu.RLock()
	rt := m.accounts[c.ID]
	m.mu.RUnlock()
	engine, err := m.masterDNSFor(rt)
	if err != nil {
		t.Fatal(err)
	}
	// Egress uses the same server policy as the other engines.
	if _, err = m.dialForEngine(rt, context.Background(), "tcp", "8.8.8.8:25", upstreamMasterDNS); err == nil {
		t.Fatal("MasterDNS bypassed destination policy")
	}
	before, _ := s.Profiles(c.ID)
	rotated, err := s.Rotate(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := s.Profiles(rotated.ID)
	if before["masterdns"] == after["masterdns"] {
		t.Fatal("key rotation retained the old MasterDNS credential")
	}
	if err = m.Reconcile(); err != nil {
		t.Fatal(err)
	}
	if _, err = m.masterDNSFor(rt); err == nil {
		t.Fatal("retired MasterDNS runtime was reused")
	}
	if reply := engine.HandleDNS([]byte{1, 2}); reply != nil {
		t.Fatal("retired engine answered")
	}
	in := accountInput(rotated)
	in.Enabled = false
	if _, err = s.Edit(c.ID, in); err != nil {
		t.Fatal(err)
	}
	if err = m.Reconcile(); err != nil {
		t.Fatal(err)
	}
	m.mu.RLock()
	present := m.accounts[c.ID] != nil
	m.mu.RUnlock()
	if present {
		t.Fatal("disabled MasterDNS account still running")
	}
}
