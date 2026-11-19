package main

import (
	"fmt"
	"net"
)

func listenDNS(address string) (net.PacketConn, net.Listener, error) {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, nil, err
	}
	// Ephemeral UDP and TCP ranges can differ (notably on Windows). Reserve
	// TCP first so test/development :0 binds select a usable shared port.
	if port == "0" {
		for range 16 {
			tcp, err := net.Listen("tcp", address)
			if err != nil {
				return nil, nil, err
			}
			udp, err := net.ListenPacket("udp", tcp.Addr().String())
			if err == nil {
				return udp, tcp, nil
			}
			tcp.Close()
		}
		return nil, nil, fmt.Errorf("cannot reserve a shared UDP/TCP DNS port")
	}
	udp, err := net.ListenPacket("udp", address)
	if err != nil {
		return nil, nil, err
	}
	tcp, err := net.Listen("tcp", udp.LocalAddr().String())
	if err != nil {
		udp.Close()
		return nil, nil, fmt.Errorf("TCP/53 is required by Cotten: %w", err)
	}
	return udp, tcp, nil
}

// resolveDNSListen turns "auto:PORT" into the default-route IPv4 address, as
// the native installer does, so a host-network container does not collide
// with systemd-resolved on 127.0.0.53.
func resolveDNSListen(address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host != "auto" {
		return address, err
	}
	conn, err := net.Dial("udp4", "1.1.1.1:53") // UDP connect sends no packet
	if err != nil {
		return "", fmt.Errorf("--dns-listen auto: cannot find the default-route IPv4 address: %w", err)
	}
	defer conn.Close()
	return net.JoinHostPort(conn.LocalAddr().(*net.UDPAddr).IP.String(), port), nil
}
