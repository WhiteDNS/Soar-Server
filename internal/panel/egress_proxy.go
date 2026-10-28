package panel

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

var errUpstreamProxy = errors.New("upstream proxy unavailable or handshake rejected")
var errProxyUDP = errors.New("this upstream proxy does not support UDP; direct fallback is disabled")

// Resolve final target names through the same engine's configured upstream.
// Returning a stream Conn makes Go's resolver use DNS-over-TCP framing even
// when its first requested transport is UDP. Proxy endpoint bootstrap DNS is
// separate; an IP-literal proxy address avoids that bootstrap lookup entirely.
func (m *Manager) lookupViaProxy(ctx context.Context, rt *accountRuntime, engine upstreamEngine, host string) ([]netip.Addr, error) {
	m.mu.RLock()
	resolvers := m.cfg.cottenResolvers()
	m.mu.RUnlock()
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		for _, address := range resolvers {
			conn, err := m.dialEgressFor(rt, ctx, "tcp", address, true, engine)
			if err == nil {
				return conn, nil
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		return nil, errUpstreamProxy
	}}
	ips, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	return ips, nil
}

func proxyEndpointAllowed(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && !ip.IsUnspecified() && !ip.IsMulticast() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && (ip.IsLoopback() || ip.IsGlobalUnicast())
}

// This exception belongs only to the administrator-selected proxy endpoint.
// Final destinations are checked separately by guardedDialRoute before here.
func connectProxy(ctx context.Context, r EngineEgress) (net.Conn, error) {
	host, port, err := net.SplitHostPort(r.Address)
	if err != nil {
		return nil, errUpstreamProxy
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, errUpstreamProxy
	}
	for _, ip := range ips {
		if !proxyEndpointAllowed(ip) {
			continue
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
		if err != nil {
			continue
		}
		return conn, nil
	}
	return nil, errUpstreamProxy
}

func dialProxy(ctx context.Context, r EngineEgress, network, target string) (result net.Conn, err error) {
	if strings.HasPrefix(network, "udp") && r.Mode != "socks5" {
		return nil, errProxyUDP
	}
	if !strings.HasPrefix(network, "tcp") && !strings.HasPrefix(network, "udp") {
		return nil, errUpstreamProxy
	}
	conn, err := connectProxy(ctx, r)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			conn.Close()
		}
	}()
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { rawConn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	if r.Mode == "https" {
		host, _, _ := net.SplitHostPort(r.Address)
		secure := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err := secure.HandshakeContext(ctx); err != nil {
			return nil, errUpstreamProxy
		}
		conn = secure
	}
	if r.Mode == "http" || r.Mode == "https" {
		req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
		if r.Username != "" {
			req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(r.Username+":"+r.Password)))
		}
		if err := req.Write(conn); err != nil {
			return nil, errUpstreamProxy
		}
		reader := bufio.NewReader(conn)
		var header strings.Builder
		for {
			line, err := reader.ReadSlice('\n')
			if err != nil || header.Len()+len(line) > 16384 {
				return nil, errUpstreamProxy
			}
			header.Write(line)
			if string(line) == "\r\n" || string(line) == "\n" {
				break
			}
		}
		response, err := http.ReadResponse(bufio.NewReader(strings.NewReader(header.String())), req)
		if err != nil || response.StatusCode < 200 || response.StatusCode > 299 {
			return nil, errUpstreamProxy
		}
		conn.SetDeadline(time.Time{})
		if !stop() && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		success = true
		return &bufferedProxyConn{Conn: conn, reader: reader}, nil
	}
	if err := socksAuthenticate(conn, r); err != nil {
		return nil, err
	}
	command := byte(1)
	address := target
	if strings.HasPrefix(network, "udp") {
		command = 3
		address = "0.0.0.0:0"
	}
	encoded, err := socksAddress(address)
	if err != nil {
		return nil, errUpstreamProxy
	}
	if _, err := conn.Write(append([]byte{5, command, 0}, encoded...)); err != nil {
		return nil, errUpstreamProxy
	}
	var reply [4]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil || reply[0] != 5 || reply[1] != 0 || reply[2] != 0 {
		return nil, errUpstreamProxy
	}
	bound, err := readSocksAddress(conn, reply[3])
	if err != nil {
		return nil, errUpstreamProxy
	}
	conn.SetDeadline(time.Time{})
	if !stop() && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if command == 1 {
		success = true
		return conn, nil
	}
	// Relays are pinned to the explicitly configured proxy's actual peer IP.
	peerHost, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
	relayHost, relayPort, _ := net.SplitHostPort(bound)
	peer, ep := netip.ParseAddr(peerHost)
	relay, er := netip.ParseAddr(relayHost)
	if ep != nil || er != nil || relayPort == "0" || !relay.IsUnspecified() && relay.Unmap() != peer.Unmap() {
		return nil, errUpstreamProxy
	}
	udp, err := (&net.Dialer{}).DialContext(ctx, "udp", net.JoinHostPort(peerHost, relayPort))
	if err != nil {
		return nil, errUpstreamProxy
	}
	endpoint, err := netip.ParseAddrPort(target)
	if err != nil {
		udp.Close()
		return nil, errUpstreamProxy
	}
	u := &socksUDPConn{Conn: udp, control: conn, target: endpoint, header: append([]byte{0, 0, 0}, mustSocksAddress(target)...)}
	go func() { var b [1]byte; conn.Read(b[:]); u.Close() }()
	success = true
	return u, nil
}

type bufferedProxyConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedProxyConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
func (c *bufferedProxyConn) CloseWrite() error {
	if v, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return v.CloseWrite()
	}
	return nil
}

func socksAuthenticate(conn net.Conn, r EngineEgress) error {
	method := byte(0)
	if r.Username != "" {
		method = 2
	}
	if _, err := conn.Write([]byte{5, 1, method}); err != nil {
		return errUpstreamProxy
	}
	var response [2]byte
	if _, err := io.ReadFull(conn, response[:]); err != nil || response[0] != 5 || response[1] != method {
		return errUpstreamProxy
	}
	if method == 0 {
		return nil
	}
	message := append([]byte{1, byte(len(r.Username))}, []byte(r.Username)...)
	message = append(message, byte(len(r.Password)))
	message = append(message, []byte(r.Password)...)
	if _, err := conn.Write(message); err != nil {
		return errUpstreamProxy
	}
	if _, err := io.ReadFull(conn, response[:]); err != nil || response[0] != 1 || response[1] != 0 {
		return errUpstreamProxy
	}
	return nil
}

func socksAddress(address string) ([]byte, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return nil, err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return nil, err
	}
	ip = ip.Unmap()
	b := []byte{4}
	if ip.Is4() {
		b[0] = 1
	}
	b = append(b, ip.AsSlice()...)
	return binary.BigEndian.AppendUint16(b, uint16(n)), nil
}
func mustSocksAddress(address string) []byte { b, _ := socksAddress(address); return b }
func readSocksAddress(reader io.Reader, kind byte) (string, error) {
	length := 0
	switch kind {
	case 1:
		length = 4
	case 4:
		length = 16
	case 3:
		var n [1]byte
		if _, err := io.ReadFull(reader, n[:]); err != nil {
			return "", err
		}
		length = int(n[0])
		if length == 0 {
			return "", errUpstreamProxy
		}
	default:
		return "", errUpstreamProxy
	}
	b := make([]byte, length+2)
	if _, err := io.ReadFull(reader, b); err != nil {
		return "", err
	}
	host := string(b[:length])
	if kind != 3 {
		ip, ok := netip.AddrFromSlice(b[:length])
		if !ok {
			return "", errUpstreamProxy
		}
		host = ip.String()
	}
	return net.JoinHostPort(host, fmt.Sprint(binary.BigEndian.Uint16(b[length:]))), nil
}

type socksUDPConn struct {
	net.Conn
	control net.Conn
	target  netip.AddrPort
	header  []byte
	once    sync.Once
}

var socksUDPBuffers = sync.Pool{New: func() any { return make([]byte, 65535) }}

func (c *socksUDPConn) Close() error {
	var err error
	c.once.Do(func() { err = c.Conn.Close(); c.control.Close() })
	return err
}
func (c *socksUDPConn) Write(p []byte) (int, error) {
	if len(p) > 65507-len(c.header) {
		return 0, errors.New("upstream UDP datagram too large")
	}
	b := append(append([]byte(nil), c.header...), p...)
	n, err := c.Conn.Write(b)
	return max(0, n-len(c.header)), err
}
func (c *socksUDPConn) Read(p []byte) (int, error) {
	b := socksUDPBuffers.Get().([]byte)
	defer socksUDPBuffers.Put(b)
	for i := 0; i < 64; i++ {
		n, err := c.Conn.Read(b)
		if err != nil {
			return 0, err
		}
		if n < 4 || b[0] != 0 || b[1] != 0 || b[2] != 0 {
			continue
		}
		r := bytes.NewReader(b[4:n])
		address, e := readSocksAddress(r, b[3])
		if e != nil {
			continue
		}
		endpoint, e := netip.ParseAddrPort(address)
		if e != nil || endpoint != c.target {
			continue
		}
		offset := n - r.Len()
		return copy(p, b[offset:n]), nil
	}
	return 0, errUpstreamProxy
}
