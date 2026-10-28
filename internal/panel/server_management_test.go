package panel

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestServerConfigurationPreservesAccountsAndRejectsInvalidChanges(t *testing.T) {
	s := testStore(t)
	_, err := s.Create(ClientInput{Name: "Keep all account data", Enabled: true, Engines: "all", StreamLimit: 16})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.List()
	original, _ := s.Settings()
	newZone := "alt.example.org"
	err = s.ConfigureServer(ServerConfiguration{TunnelDomains: original.BaseZone + ", ALT.example.org.", Primary: newZone, Nameservers: newZone + "=ns.example.org"}, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.ConfigureServer(ServerConfiguration{Resolvers: "9.9.9.9,1.1.1.1", Ports: "80,443,8443", Country: "GLOBAL", PublicIP: "8.8.4.4"}, false)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := s.List()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("server configuration changed account identities, keys or usage")
	}
	cfg, _ := s.Settings()
	if cfg.BaseZone != original.BaseZone || cfg.PrimaryZone != newZone || cfg.Country != "" || cfg.PublicIP != "8.8.4.4" || cfg.TunnelNameservers[newZone] != "ns.example.org" || !reflect.DeepEqual(cfg.AntiFlood, original.AntiFlood) {
		t.Fatal("configuration was not saved or changed unrelated settings", cfg)
	}
	for _, test := range []struct {
		in      ServerConfiguration
		tunnels bool
	}{
		{ServerConfiguration{TunnelDomains: ""}, true},
		{ServerConfiguration{TunnelDomains: "alt.example.org,alt.example.org"}, true},
		{ServerConfiguration{TunnelDomains: newZone, Primary: "missing.example.org"}, true},
		{ServerConfiguration{TunnelDomains: newZone, Nameservers: "bad-pair"}, true},
		{ServerConfiguration{Ports: "0,443"}, false},
		{ServerConfiguration{Ports: "443,65536"}, false},
		{ServerConfiguration{Resolvers: "not-an-ip"}, false},
		{ServerConfiguration{Country: "invalid"}, false},
	} {
		if err := s.ConfigureServer(test.in, test.tunnels); err == nil {
			t.Fatal("invalid server configuration accepted", test)
		}
		unchanged, _ := s.Settings()
		if !reflect.DeepEqual(cfg, unchanged) {
			t.Fatal("rejected configuration changed saved settings")
		}
	}
	info, err := s.ServerManagementInfo()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(info)
	if strings.Contains(string(raw), "hash") || strings.Contains(string(raw), "private_key") || !strings.Contains(string(raw), `"accounts":1`) {
		t.Fatal("unsafe or incomplete management summary", string(raw))
	}
}
