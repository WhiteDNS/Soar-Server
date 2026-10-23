package panel

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"time"

	master "masterdnsvpn-go/server"
	storm "stormdns-go/server"
)

type dnsEngine interface {
	HandleDNS([]byte) []byte
	Close() error
}

func additionalKey(seed []byte, engine string) string {
	mac := hmac.New(sha256.New, seed)
	mac.Write([]byte("WhiteDNS " + engine + " account key v1"))
	return hex.EncodeToString(mac.Sum(nil))
}

func (m *Manager) additionalFor(rt *accountRuntime, name string) (dnsEngine, error) {
	rt.additionalMu.Lock()
	defer rt.additionalMu.Unlock()
	if rt.ctx.Err() != nil || !rt.client.hasEngine(name) {
		return nil, ErrDenied
	}
	if engine := rt.additional[name]; engine != nil {
		return engine, nil
	}
	seed, err := m.store.Seed(rt.client.ID)
	if err != nil {
		return nil, err
	}
	domains := make([]string, len(rt.zones))
	for i, zone := range rt.zones {
		domains[i] = enginePrefix(name) + zone
	}
	allowed := func() bool {
		c, err := m.store.Get(rt.client.ID)
		return rt.ctx.Err() == nil && err == nil && c.Status(time.Now()) == "active" && c.hasEngine(name) && c.UsageEpoch == rt.client.UsageEpoch && c.PublicKey == rt.client.PublicKey
	}
	upstream := upstreamStorm
	if name == "masterdns" {
		upstream = upstreamMaster
	}
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		return m.dialForEngine(rt, ctx, network, address, upstream)
	}
	exchange := func(ctx context.Context, query []byte) ([]byte, error) {
		return m.exchangeEngineDNS(rt, ctx, query, upstream)
	}
	var engine dnsEngine
	switch name {
	case "stormdns":
		engine, err = storm.New(rt.ctx, storm.Config{Domains: domains, Key: additionalKey(seed, name), StreamLimit: rt.client.StreamLimit, Allowed: allowed, DialContext: dial, ExchangeDNS: exchange})
	case "masterdns":
		engine, err = master.New(rt.ctx, master.Config{Domains: domains, Key: additionalKey(seed, name), StreamLimit: rt.client.StreamLimit, Allowed: allowed, DialContext: dial, ExchangeDNS: exchange})
	default:
		return nil, ErrDenied
	}
	if err != nil {
		m.diagnostics.forAdditional(name).startErrors.Add(1)
		m.diagnostics.lastEngineIssueUnixMS.Store(time.Now().UnixMilli())
		return nil, err
	}
	if rt.additional == nil {
		rt.additional = make(map[string]dnsEngine)
	}
	rt.additional[name] = engine
	m.diagnostics.forAdditional(name).starts.Add(1)
	m.diagnostics.forAdditional(name).running.Add(1)
	return engine, nil
}

func (m *Manager) closeAdditional(rt *accountRuntime) {
	rt.additionalMu.Lock()
	engines := rt.additional
	rt.additional = nil
	rt.additionalMu.Unlock()
	for name, engine := range engines {
		_ = engine.Close()
		m.diagnostics.forAdditional(name).running.Add(-1)
	}
}
