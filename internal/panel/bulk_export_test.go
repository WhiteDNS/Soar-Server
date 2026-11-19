package panel

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func exportAccounts(t *testing.T, b *browser, body any) []exportedAccount {
	t.Helper()
	var v struct{ Accounts []exportedAccount }
	if e := json.Unmarshal(b.request("/api/clients/export", "POST", body, 200), &v); e != nil {
		t.Fatal(e)
	}
	return v.Accounts
}

func TestBulkExportReturnsLinksOfSelectedAccounts(t *testing.T) {
	b, _, _ := testBrowser(t)
	b.request("/api/clients/export", "POST", map[string][]string{"ids": {"x"}}, 401)
	b.login()
	var ids []string
	for _, name := range []string{"first", "second"} {
		var c Client
		if e := json.Unmarshal(b.request("/api/clients", "POST", ClientInput{Name: name, Engines: "all", Enabled: true, StreamLimit: 16}, 201), &c); e != nil {
			t.Fatal(e)
		}
		ids = append(ids, c.ID)
	}
	accounts := exportAccounts(t, b, map[string][]string{"ids": ids})
	if len(accounts) != 2 {
		t.Fatalf("want 2 accounts, got %d", len(accounts))
	}
	for i, a := range accounts {
		var single struct{ Links map[string]string }
		_ = json.Unmarshal(b.request("/api/clients/"+ids[i]+"/link", "GET", nil, 200), &single)
		var sub struct{ URL string }
		_ = json.Unmarshal(b.request("/api/clients/"+ids[i]+"/subscription", "GET", nil, 200), &sub)
		if a.ID != ids[i] || len(a.Domains) != 1 || !a.Domains[0].Primary || len(a.Domains[0].Links) != 3 || !reflect.DeepEqual(a.Domains[0].Links, single.Links) || a.Subscription != sub.URL || !strings.Contains(a.Subscription, "/sub/") {
			t.Fatalf("export differs from the single-account endpoints: %+v", a)
		}
	}
	b.request("/api/clients/export", "POST", map[string][]string{"ids": {}}, 400)
	b.request("/api/clients/export", "POST", map[string][]string{"ids": {ids[0], strings.Repeat("0", 32)}}, 404)
	b.request("/api/clients/export", "GET", nil, 405)
	token := b.csrf
	b.csrf = ""
	b.request("/api/clients/export", "POST", map[string][]string{"ids": ids}, 403)
	b.csrf = token
	if raw := b.request("/api/events", "GET", nil, 200); !strings.Contains(string(raw), "clients.exported") {
		t.Fatal("export was not audited")
	}
}

// Every domain's links must be the exact profiles the subscription serves, so
// importing either gives an app the same profile IDs.
func TestBulkExportCoversEveryTunnelDomain(t *testing.T) {
	b, s, _ := testBrowser(t)
	b.login()
	c, err := s.Create(ClientInput{Name: "multi", Engines: "all", Enabled: true, StreamLimit: 8})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.Profiles(c.ID)
	b.request("/api/tunnel-domains", "PUT", map[string]any{"zones": []string{"soar.example.org", "t.example.net"}, "primary": "t.example.net"}, 200)
	a := exportAccounts(t, b, map[string][]string{"ids": {c.ID}})[0]
	if len(a.Domains) != 2 || a.Domains[0].Zone != "t.example.net" || !a.Domains[0].Primary || a.Domains[1].Zone != "soar.example.org" || a.Domains[1].Primary {
		t.Fatalf("domains are not listed preferred first: %+v", a.Domains)
	}
	if !reflect.DeepEqual(a.Domains[1].Links, before) {
		t.Fatal("original domain links changed")
	}
	token, _ := s.SubscriptionToken(c.ID, false)
	served := strings.Fields(string(b.request("/sub/"+token+"?format=plain", "GET", nil, 200)))
	var exported []string
	for _, d := range a.Domains {
		for _, engine := range profileEngines {
			exported = append(exported, d.Links[engine])
		}
	}
	if !reflect.DeepEqual(served, exported) {
		t.Fatal("export differs from the subscription body")
	}
	only := exportAccounts(t, b, map[string]any{"ids": []string{c.ID}, "zones": []string{"soar.example.org"}})[0]
	if len(only.Domains) != 1 || !reflect.DeepEqual(only.Domains[0], a.Domains[1]) {
		t.Fatalf("domain filter ignored: %+v", only.Domains)
	}
	for _, zones := range [][]string{{"x.example.com"}, {""}, {"t.example.net", "t.example.net"}} {
		b.request("/api/clients/export", "POST", map[string]any{"ids": []string{c.ID}, "zones": zones}, 400)
	}
	var link struct {
		Links   map[string]string
		Domains []DomainProfiles
	}
	_ = json.Unmarshal(b.request("/api/clients/"+c.ID+"/link", "GET", nil, 200), &link)
	if !reflect.DeepEqual(link.Domains, a.Domains) || !reflect.DeepEqual(link.Links, a.Domains[0].Links) {
		t.Fatal("link endpoint domains differ from the export")
	}
	_ = json.Unmarshal(b.request("/api/clients/"+c.ID+"/link?zone=soar.example.org&engine=cotten", "GET", nil, 200), &link)
	if !reflect.DeepEqual(link.Links, before) {
		t.Fatal("zone parameter did not select the domain")
	}
	png := b.request("/api/clients/"+c.ID+"/qr.png?engine=masterdns&zone=soar.example.org", "GET", nil, 200)
	if !bytes.HasPrefix(png, []byte{137, 80, 78, 71}) {
		t.Fatal("invalid QR image")
	}
	b.request("/api/clients/"+c.ID+"/qr.png?zone=x.example.com", "GET", nil, 400)
}
