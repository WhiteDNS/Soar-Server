package panel

import (
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"runtime"
	"strings"
	"sync"
	"time"

	cotten "cottendns-go/server"
	"github.com/getlantern/soar"
)

type cottenJob struct {
	rt    *accountRuntime
	query []byte
	reply func([]byte)
}

func (m *Manager) runAccount(rt *accountRuntime, seed []byte) {
	defer m.workers.Done()
	defer rt.stop()
	soarDone := make(chan struct{})
	if rt.client.hasEngine("soar") {
		var servers sync.WaitGroup
		for zone, pc := range rt.soarPCs {
			servers.Add(1)
			go func() {
				defer servers.Done()
				defer rt.cancel()
				_ = soar.Serve(rt.ctx, pc, soar.ServerConfig{Zone: zone, PrivateKey: ed25519.NewKeyFromSeed(seed), Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
					return m.dial(rt, ctx, network, address)
				}, MaxSessions: 8, MaxTotalStreams: 128, MaxStreams: 128, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			}()
		}
		go func() { servers.Wait(); close(soarDone) }()
	} else {
		close(soarDone)
	}
	<-rt.ctx.Done()
	rt.stop()
	rt.cottenMu.Lock()
	engine := rt.cotten
	rt.cottenMu.Unlock()
	if engine != nil {
		_ = engine.Close()
	}
	<-soarDone
}

func (m *Manager) cottenFor(rt *accountRuntime) (*cotten.Server, error) {
	rt.cottenMu.Lock()
	defer rt.cottenMu.Unlock()
	if rt.ctx.Err() != nil {
		return nil, ErrDenied
	}
	if rt.cotten != nil {
		return rt.cotten, nil
	}
	seed, err := m.store.Seed(rt.client.ID)
	if err != nil {
		return nil, err
	}
	domains := make([]string, len(rt.zones))
	for i, zone := range rt.zones {
		domains[i] = "cot." + zone
	}
	engine, err := cotten.New(rt.ctx, cotten.Config{Domain: domains[0], Domains: domains, Key: cottenKey(seed),
		Allowed: func() bool {
			c, err := m.store.Get(rt.client.ID)
			return err == nil && c.Status(time.Now()) == "active" && c.hasEngine("cotten") && c.UsageEpoch == rt.client.UsageEpoch && c.PublicKey == rt.client.PublicKey
		},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return m.dial(rt, ctx, network, address)
		},
		ExchangeDNS: func(ctx context.Context, query []byte) ([]byte, error) { return m.cottenDNS(rt, ctx, query) },
	})
	if err != nil {
		return nil, err
	}
	rt.cotten = engine
	return engine, nil
}

func (m *Manager) startCottenWorkers() {
	for range min(max(runtime.GOMAXPROCS(0), 2), 8) {
		m.workers.Add(1)
		go func() {
			defer m.workers.Done()
			for {
				select {
				case <-m.ctx.Done():
					return
				case job := <-m.cottenJobs:
					var response []byte
					if engine, err := m.cottenFor(job.rt); err == nil {
						response = engine.HandleDNS(job.query)
					}
					job.reply(response)
				}
			}
		}()
	}
}

// Both engines live beneath the existing delegation. "cot" is an internal
// engine label; the operator never needs another DNS record or another key.
func (m *Manager) routeDNS(query []byte, remote net.Addr, tcp bool, reply func([]byte)) {
	name, end, typ, ok := dnsQuestion(query)
	if !ok {
		reply(nil)
		return
	}
	m.mu.RLock()
	base := m.cfg.matchTunnelZone(name)
	nameserver := ""
	if base != "" {
		nameserver = m.cfg.nameserverFor(base)
	}
	if response := m.domainProbeReply(query, name, typ, base); response != nil {
		m.mu.RUnlock()
		reply(response)
		return
	}
	// Recursive resolvers may minimise QNAMEs at either account or engine
	// labels. Answer authoritative NS/SOA control queries before tunnel routing.
	if typ == 2 || typ == 6 {
		m.mu.RUnlock()
		reply(dnsControl(query, end, name, typ, base, nameserver))
		return
	}
	var rt *accountRuntime
	zone := ""
	if strings.HasSuffix(name, "."+base) {
		parts := strings.Split(strings.TrimSuffix(name, "."+base), ".")
		zone = parts[len(parts)-1] + "." + base
		rt = m.zones[zone]
	}
	m.mu.RUnlock()
	if rt != nil {
		cottenZone := "cot." + zone
		cottenQuery := name == cottenZone || strings.HasSuffix(name, "."+cottenZone)
		if cottenQuery && rt.client.hasEngine("cotten") {
			job := cottenJob{rt: rt, query: append([]byte(nil), query...), reply: reply}
			select {
			case m.cottenJobs <- job:
				return
			default:
			}
		} else if !cottenQuery && !tcp && rt.client.hasEngine("soar") {
			q := packet{append([]byte(nil), query...), remote}
			select {
			case rt.soarPCs[zone].input <- q:
			case <-rt.soarPCs[zone].done:
			default:
			}
			return
		}
	}
	reply(dnsControl(query, end, name, typ, base, nameserver))
}

// ServeTCP owns bounded DNS framing, not a second engine listener. A connection
// may contain questions for different accounts, so routing is per message.
func (m *Manager) ServeTCP(listener net.Listener) error {
	var mu sync.Mutex
	open := make(map[net.Conn]struct{})
	var wg sync.WaitGroup
	done := make(chan struct{})
	go func() {
		select {
		case <-m.ctx.Done():
		case <-done:
		}
		listener.Close()
		mu.Lock()
		for c := range open {
			c.Close()
		}
		mu.Unlock()
	}()
	defer func() { close(done); wg.Wait() }()
	for {
		c, err := listener.Accept()
		if err != nil {
			if m.ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		mu.Lock()
		if m.ctx.Err() != nil || len(open) >= 128 {
			mu.Unlock()
			c.Close()
			continue
		}
		open[c] = struct{}{}
		mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { c.Close(); mu.Lock(); delete(open, c); mu.Unlock() }()
			for {
				c.SetReadDeadline(time.Now().Add(30 * time.Second))
				var size [2]byte
				if _, err := io.ReadFull(c, size[:]); err != nil {
					return
				}
				n := int(binary.BigEndian.Uint16(size[:]))
				if n < 17 {
					return
				}
				query := make([]byte, n)
				if _, err := io.ReadFull(c, query); err != nil {
					return
				}
				m.dnsIn.Add(uint64(n))
				responses := make(chan []byte, 1)
				m.routeDNS(query, c.RemoteAddr(), true, func(p []byte) { responses <- p })
				var response []byte
				select {
				case response = <-responses:
				case <-m.ctx.Done():
					return
				}
				if len(response) == 0 {
					return
				}
				frame := binary.BigEndian.AppendUint16(nil, uint16(len(response)))
				frame = append(frame, response...)
				c.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if _, err := io.Copy(c, strings.NewReader(string(frame))); err != nil {
					return
				}
				m.dnsOut.Add(uint64(len(response)))
			}
		}()
	}
}

func (m *Manager) cottenDNS(rt *accountRuntime, parent context.Context, query []byte) ([]byte, error) {
	if _, _, _, ok := dnsQuestion(query); !ok || len(query) > 4096 {
		return nil, errors.New("invalid tunneled DNS query")
	}
	m.mu.RLock()
	resolvers := append([]string(nil), m.cfg.Resolvers...)
	m.mu.RUnlock()
	ctx, cancel := context.WithTimeout(parent, 6*time.Second)
	defer cancel()
	for _, resolver := range resolvers {
		endpoint := net.JoinHostPort(resolver, "53")
		response, err := m.exchangeCottenDNS(rt, ctx, "udp", endpoint, query)
		if err != nil {
			continue
		}
		if len(response) >= 4 && response[2]&2 != 0 {
			response, err = m.exchangeCottenDNS(rt, ctx, "tcp", endpoint, query)
		}
		if err == nil && len(response) >= 12 && response[0] == query[0] && response[1] == query[1] && response[2]&0x80 != 0 {
			return response, nil
		}
	}
	return nil, errors.New("DNS resolver unavailable")
}

func (m *Manager) exchangeCottenDNS(rt *accountRuntime, ctx context.Context, network, address string, query []byte) ([]byte, error) {
	c, err := m.dialEgress(rt, ctx, network, address, true)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		c.SetDeadline(deadline)
	}
	if network == "udp" {
		if _, err = c.Write(query); err != nil {
			return nil, err
		}
		buf := make([]byte, 65535)
		n, err := c.Read(buf)
		return buf[:n], err
	}
	frame := binary.BigEndian.AppendUint16(nil, uint16(len(query)))
	frame = append(frame, query...)
	if _, err = c.Write(frame); err != nil {
		return nil, err
	}
	var size [2]byte
	if _, err = io.ReadFull(c, size[:]); err != nil {
		return nil, err
	}
	buf := make([]byte, int(binary.BigEndian.Uint16(size[:])))
	_, err = io.ReadFull(c, buf)
	return buf, err
}
