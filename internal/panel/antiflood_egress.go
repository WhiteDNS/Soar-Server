package panel

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"syscall"
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
	l.releaseTargetLocked()
}

// A dead proxy or a down uplink fails every destination at once. It says nothing
// about the account, so it must not feed the dial_failures scan detector.
func dialFailureCountable(route EngineEgress, err error) bool {
	if route.proxy() || errors.Is(err, context.Canceled) {
		return false
	}
	return !errors.Is(err, syscall.ENETUNREACH) && !errors.Is(err, syscall.ENETDOWN)
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
	for i, ip := range ips {
		ip = ip.Unmap()
		if !ip.IsValid() || seen[ip] || strings.HasSuffix(network, "4") && !ip.Is4() || strings.HasSuffix(network, "6") && !ip.Is6() {
			continue
		}
		seen[ip] = true
		pinned := net.JoinHostPort(ip.String(), port)
		var isOwnHost, ownHost bool
		if !resolver {
			isOwnHost, ownHost = m.ownHostPolicy(ip, port)
		}
		// Behind NAT the public IP is on no interface, so PublicOnly cannot see it.
		if isOwnHost && !ownHost {
			last = errDestinationNotPublic
			continue
		}
		if (route.proxy() || m.dialOverride == nil) && !ownHost {
			if e := soar.PublicOnly(network, pinned, nil); e != nil {
				last = errDestinationNotPublic
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
		// Share the 15-second dial deadline across the addresses still to try, so a
		// slow proxy chain with one address gets all of it. A canceled operation
		// never creates new traffic.
		if err := ctx.Err(); err != nil {
			l.releaseTarget()
			return nil, err
		}
		budget := 5 * time.Second
		if deadline, ok := ctx.Deadline(); ok {
			budget = time.Until(deadline) / time.Duration(min(len(ips)-i, 5-attempts))
		}
		attempt, stop := context.WithTimeout(ctx, budget)
		attemptDial := dial
		if ownHost && !route.proxy() && m.dialOverride == nil {
			// The exact address and port were approved above, and the pinned IP cannot rebind.
			attemptDial = (&net.Dialer{}).DialContext
		}
		conn, e := attemptDial(attempt, network, pinned)
		result := "connected"
		if e != nil {
			result = diagnosticReason(e)
		}
		flow.attempt(ip.String(), result)
		stop()
		if !resolver && (e == nil || dialFailureCountable(route, e)) {
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

// ownHostPolicy reports whether ip is the panel's exact public IP and whether the
// administrator listed this port, the only exception to the non-public block.
func (m *Manager) ownHostPolicy(ip netip.Addr, rawPort string) (own, allowed bool) {
	port, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil {
		return false, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	public, ok := usableOwnIP(m.cfg.PublicIP)
	if !ok || ip != public {
		return false, false
	}
	return true, m.egress.ownHostAllows(uint16(port))
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
