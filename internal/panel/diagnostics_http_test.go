package panel

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestDiagnosticsRequiresAuthenticationAndReportsAggregateMetrics(t *testing.T) {
	b, s, h := testBrowser(t)
	b.request("/api/diagnostics", "GET", nil, 401)
	b.login()
	c := account(t, s, 0)
	if _, _, err := s.reserveEpoch(c.ID, 7, true, c.UsageEpoch); err != nil {
		t.Fatal(err)
	}
	h.manager.diagnostics.udpWriteErrors.Add(2)
	raw := b.request("/api/diagnostics", "GET", nil, 200)
	var d struct {
		Runtime    RuntimeDiagnostics `json:"runtime"`
		Accounting map[string]uint64  `json:"accounting"`
		Host       map[string]any     `json:"host"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if d.Runtime.UDPWriteErrors != 2 || d.Accounting["requests"] != 1 || d.Accounting["commits"] != 1 {
		t.Fatalf("metrics: %s", raw)
	}
	if len(d.Host) == 0 {
		t.Fatal("host metrics missing")
	}
	for _, secret := range []string{c.ID, c.Name, c.PublicKey, c.Zone, "private_key", "encryption_key"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("diagnostics leaked account data: %q", secret)
		}
	}
	dashboard := b.request("/api/dashboard", "GET", nil, 200)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(dashboard, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"active_streams", "diagnostics", "accounting", "host"} {
		if len(fields[key]) == 0 {
			t.Errorf("missing dashboard field %s", key)
		}
	}
}
