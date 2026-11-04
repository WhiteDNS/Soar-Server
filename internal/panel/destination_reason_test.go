package panel

import (
	"context"
	"errors"
	"testing"
)

func TestPrivateDestinationHasItsOwnReasonOnEveryRoute(t *testing.T) {
	for _, mode := range []string{"direct", "socks5"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			c, err := s.Create(ClientInput{Name: "a", Engines: "soar", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			if mode != "direct" {
				cfg, _ := s.Egress()
				cfg.Soar = EngineEgress{Mode: mode, Address: "127.0.0.1:1", Unrestricted: true}
				if err = s.SaveEgress(cfg); err != nil {
					t.Fatal(err)
				}
			}
			m := testManager(t, s, nil)
			m.dialOverride = nil
			rt := m.accounts[c.ID]
			// 443 is allowed on both routes, so only the destination can refuse this.
			_, err = m.dialForEngine(rt, context.Background(), "tcp", "127.0.0.1:443", upstreamSoar)
			if !errors.Is(err, errDestinationPort) {
				t.Fatal("private destination was not a policy denial", err)
			}
			if got := diagnosticReason(err); got != "destination_not_public" {
				t.Fatal("wrong reason", got)
			}
			_, err = m.dialForEngine(rt, context.Background(), "tcp", "8.8.8.8:25", upstreamSoar)
			if mode == "direct" && diagnosticReason(err) != "destination_policy" {
				t.Fatal("port denial lost its reason", err)
			}
		})
	}
}
