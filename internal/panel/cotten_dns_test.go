package panel

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestCottenQnameMinimisationOverUDPAndTCP(t *testing.T) {
	s := testStore(t)
	c, err := s.Create(ClientInput{Name: "cotten only", Engines: "cotten", Enabled: true, StreamLimit: 4})
	if err != nil {
		t.Fatal(err)
	}
	m := testManager(t, s, nil)
	cfg, _ := s.Settings()
	for _, zone := range []string{c.Zone, c.cottenZone()} {
		for _, network := range []string{"udp", "tcp"} {
			name, _ := dnsmessage.NewName(zone + ".")
			message := dnsmessage.Message{Header: dnsmessage.Header{ID: 321}, Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypeNS, Class: dnsmessage.ClassINET}}}
			query, _ := message.Pack()
			conn, err := net.DialTimeout(network, m.pc.LocalAddr().String(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			conn.SetDeadline(time.Now().Add(2 * time.Second))
			var response []byte
			if network == "tcp" {
				conn.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(query))), query...))
				var size [2]byte
				if _, err = io.ReadFull(conn, size[:]); err != nil {
					t.Fatal(err)
				}
				response = make([]byte, int(binary.BigEndian.Uint16(size[:])))
				if _, err = io.ReadFull(conn, response); err != nil {
					t.Fatal(err)
				}
			} else {
				conn.Write(query)
				buf := make([]byte, 4096)
				n, err := conn.Read(buf)
				if err != nil {
					t.Fatal(err)
				}
				response = buf[:n]
			}
			conn.Close()
			var answer dnsmessage.Message
			if err = answer.Unpack(response); err != nil {
				t.Fatal(err)
			}
			if answer.RCode != dnsmessage.RCodeSuccess || !answer.Authoritative || len(answer.Authorities) != 1 || answer.Authorities[0].Header.Name.String() != cfg.BaseZone+"." {
				t.Fatalf("minimised %s %s response: %+v", network, zone, answer)
			}
		}
	}
}
