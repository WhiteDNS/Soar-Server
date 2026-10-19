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
