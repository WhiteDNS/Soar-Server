package panel

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBulkExportReturnsLinksOfSelectedAccounts(t *testing.T) {
	b, _, _ := testBrowser(t)
	b.request("/api/clients/export", "POST", map[string][]string{"ids": {"x"}}, 401)
	b.login()
	var ids []string
	for _, name := range []string{"first", "second"} {
		var c Client
		if e := json.Unmarshal(b.request("/api/clients", "POST", ClientInput{Name: name, Enabled: true, StreamLimit: 16}, 201), &c); e != nil {
			t.Fatal(e)
		}
		ids = append(ids, c.ID)
	}
	var v struct{ Accounts []exportedAccount }
	if e := json.Unmarshal(b.request("/api/clients/export", "POST", map[string][]string{"ids": ids}, 200), &v); e != nil {
		t.Fatal(e)
	}
	if len(v.Accounts) != 2 {
		t.Fatalf("want 2 accounts, got %d", len(v.Accounts))
	}
	for i, a := range v.Accounts {
		var single struct{ Links map[string]string }
		_ = json.Unmarshal(b.request("/api/clients/"+ids[i]+"/link", "GET", nil, 200), &single)
		var sub struct{ URL string }
		_ = json.Unmarshal(b.request("/api/clients/"+ids[i]+"/subscription", "GET", nil, 200), &sub)
		if a.ID != ids[i] || len(a.Links) == 0 || a.Links["soar"] != single.Links["soar"] || a.Subscription != sub.URL || !strings.Contains(a.Subscription, "/sub/") {
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
