package panel

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestCottenDNSCustomPortAndResolverFailover(t *testing.T) {
	for _, scenario := range []string{"timeout", "servfail", "refused", "truncated", "carrier-bootstrap"} {
		t.Run(scenario, func(t *testing.T) {
			s := testStore(t)
			c, err := s.Create(ClientInput{Name: "DNS fallback", Enabled: true, Engines: "cotten", StreamLimit: 8})
			if err != nil {
				t.Fatal(err)
			}
			cfg, _ := s.Settings()
			cfg.Resolvers = []string{"1.1.1.1:5353", "8.8.8.8:5353"}
			first, backup := "1.1.1.1:5353", "8.8.8.8:5353"
			if scenario == "carrier-bootstrap" {
				cfg.Resolvers = []string{"10.0.0.1"}
				first = "10.0.0.1:53"
				backup = "1.1.1.1:53"
			}
			if err = s.SaveSettings(cfg); err != nil {
				t.Fatal(err)
			}
			var second, tcp atomic.Int32
			m := testManager(t, s, func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != first && address != backup {
					t.Errorf("invalid resolver endpoint %s", address)
				}
				if scenario == "carrier-bootstrap" && address == first {
					return nil, errors.New("carrier resolver unavailable from VPS")
				}
				a, b := net.Pipe()
				go func() {
					defer b.Close()
					var query []byte
					if network == "tcp" {
						tcp.Add(1)
						var header [2]byte
						if _, err := io.ReadFull(b, header[:]); err != nil {
							return
						}
						query = make([]byte, binary.BigEndian.Uint16(header[:]))
						if _, err := io.ReadFull(b, query); err != nil {
							return
						}
					} else {
						buf := make([]byte, 4096)
						n, err := b.Read(buf)
						if err != nil {
							return
						}
						query = append([]byte(nil), buf[:n]...)
					}
					if address == first && scenario == "timeout" {
						io.Copy(io.Discard, b)
						return
					}
					query[2] |= 0x80
					if address == first {
						switch scenario {
						case "servfail":
							query[3] = 2
						case "refused":
							query[3] = 5
						case "truncated":
							if network == "udp" {
								query[2] |= 2
							}
						}
					} else {
						second.Add(1)
					}
					if network == "tcp" {
						frame := binary.BigEndian.AppendUint16(nil, uint16(len(query)))
						b.Write(append(frame, query...))
					} else {
						b.Write(query)
					}
				}()
				return a, nil
			})
			name, _ := dnsmessage.NewName("example.com.")
			message := dnsmessage.Message{Header: dnsmessage.Header{ID: 1234, RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}
			query, _ := message.Pack()
			m.mu.RLock()
			rt := m.accounts[c.ID]
			m.mu.RUnlock()
			started := time.Now()
			answer, err := m.cottenDNS(rt, context.Background(), query)
			if err != nil || len(answer) < 12 || answer[3]&15 != 0 {
				t.Fatal("DNS fallback failed", err)
			}
			if scenario != "truncated" && second.Load() != 1 {
				t.Fatal("second resolver was never queried")
			}
			if scenario == "truncated" && tcp.Load() != 1 {
				t.Fatal("truncated UDP did not retry TCP on custom port")
			}
			if time.Since(started) > 4*time.Second {
				t.Fatal("first resolver consumed the fallback budget")
			}
			actual, _ := s.Get(c.ID)
			if actual.UploadBytes == 0 || actual.DownloadBytes == 0 {
				t.Fatal("internal DNS bypassed quota accounting")
			}
		})
	}
}
