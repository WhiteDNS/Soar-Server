package panelops

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestDNSFirewallUpgradeAndExistingRules(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		want         []string
	}{
		{"inactive", "Status: inactive", nil},
		{"upgrade UDP only", "Status: active\n53/udp ALLOW Anywhere # existing", []string{"53/tcp"}},
		{"both exist", "Status: active\n53/udp ALLOW Anywhere\n53/tcp ALLOW Anywhere", nil},
		{"broad rule", "Status: active\n53 ALLOW Anywhere", nil},
		{"IPv6 only", "Status: active\n53/tcp (v6) ALLOW Anywhere (v6)", []string{"53/udp", "53/tcp"}},
		{"new firewall", "Status: active", []string{"53/udp", "53/tcp"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ports []string
			err := ensureDNSFirewall(func(args ...string) ([]byte, error) {
				if args[0] == "status" {
					return []byte(tc.status), nil
				}
				ports = append(ports, args[1])
				if strings.Join(args[2:], " ") != "comment soar-panel-dns" {
					t.Fatal(args)
				}
				return nil, nil
			})
			if err != nil || !reflect.DeepEqual(ports, tc.want) {
				t.Fatal(ports, tc.want, err)
			}
		})
	}
	if err := ensureDNSFirewall(func(...string) ([]byte, error) { return nil, errors.New("denied") }); err == nil {
		t.Fatal("firewall errors ignored")
	}
}
