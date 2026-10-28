package panel

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/getlantern/soar"
)

func (l *floodLease) releaseTarget() {
	if l == nil {
		return
	}
	g := l.guard
	g.mu.Lock()
	defer g.mu.Unlock()
	if l.target != nil {
		l.target.active--
		l.target = nil
	}
	if l.network != nil {
		l.network.active--
		l.network = nil
	}
}

// Pin resolution to the exact address inspected by the guard. The dialer keeps
// PublicOnly as defense in depth. All candidate addresses share the same account
// admission; each actual socket attempt consumes target/network admission.
func (m *Manager) guardedDial(ctx context.Context, network, address string, resolver bool, l *floodLease) (net.Conn, error) {
	return m.guardedDialRoute(ctx, network, address, resolver, l, EngineEgress{Mode: "direct"}, nil, nil)
}

func (m *Manager) guardedDialRoute(ctx context.Context, network, address string, resolver bool, l *floodLease, route EngineEgress, flow *outboundFlow, resolved []netip.Addr) (net.Conn, error) {
	dial := m.dialOverride
	if !route.proxy() && dial != nil && m.lookupOverride == nil {
		// Existing in-process interop tests supply their own local dialer. No
		// shipped command can set either of these hooks.
		return dial(ctx, network, address)
	}
	if dial == nil {
		dial = (&net.Dialer{Control: soar.PublicOnly}).DialContext
	}
	if route.proxy() {
		dial = func(ctx context.Context, network, target string) (net.Conn, error) {
			return dialProxy(ctx, route, network, target)
		}
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	var ips []netip.Addr
	if ip, e := netip.ParseAddr(host); e == nil {
		ips = []netip.Addr{ip}
	} else if route.proxy() {
		if len(resolved) == 0 {
			return nil, errUpstreamProxy
		}
		ips = resolved
	} else {
		lookup := m.lookupOverride
		if lookup == nil {
			lookup = net.DefaultResolver.LookupNetIP
		}
		ips, err = lookup(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
	}
	last := errors.New("no eligible public destination")
	seen := map[netip.Addr]bool{}
	attempts := 0
	for _, ip := range ips {
		ip = ip.Unmap()
		if !ip.IsValid() || seen[ip] || strings.HasSuffix(network, "4") && !ip.Is4() || strings.HasSuffix(network, "6") && !ip.Is6() {
			continue
		}
		seen[ip] = true
		pinned := net.JoinHostPort(ip.String(), port)
		if route.proxy() || m.dialOverride == nil {
			if e := soar.PublicOnly(network, pinned, nil); e != nil {
				last = errDestinationPort
				continue
			}
		}
		if !resolver {
			if e, q := m.flood.target(l, ip, time.Now()); e != nil {
				if q {
					m.Disconnect(l.id)
				}
				return nil, e
			}
		}
		attempts++
		// Leave time for a second family/address without multiplying the total
		// 15-second dial deadline. A canceled operation never creates new traffic.
		if err := ctx.Err(); err != nil {
			l.releaseTarget()
			return nil, err
		}
		budget := 5 * time.Second
		if deadline, ok := ctx.Deadline(); ok {
			budget = min(budget, time.Until(deadline))
		}
		attempt, stop := context.WithTimeout(ctx, budget)
		conn, e := dial(attempt, network, pinned)
		result := "connected"
		if e != nil {
			result = diagnosticReason(e)
		}
		flow.attempt(ip.String(), result)
		stop()
		if !resolver && !errors.Is(e, context.Canceled) {
			if q := m.flood.outcome(l, e != nil, time.Now()); q {
				if conn != nil {
					conn.Close()
				}
				m.Disconnect(l.id)
				l.releaseTarget()
				return nil, errAntiFlood
			}
		}
		if e == nil {
			return conn, nil
		}
		last = e
		l.releaseTarget()
		if attempts == 4 || ctx.Err() != nil {
			break
		}
	}
	return nil, last
}

func (c *meterConn) guardWrite(bytes int) error {
	err, quarantined := c.manager.flood.write(c.flood, bytes, time.Now())
	if quarantined {
		c.manager.Disconnect(c.rt.client.ID)
	}
	if err != nil {
		c.Close()
	}
	return err
}
