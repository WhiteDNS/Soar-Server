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

func TestFirewallWorkerIsBoundedAndHasFixedActions(t *testing.T) {
	for _, kind := range []string{"dns", "https"} {
		args, err := firewallUnitArgs("/usr/local/bin/soar-panel-ops", kind)
		if err != nil {
			t.Fatal(err)
		}
		for _, required := range []string{"--wait", "--collect", "--property=RuntimeMaxSec=12s", "--property=ProtectSystem=strict", "--property=ReadWritePaths=/run -/etc/ufw -/var/lib/ufw", "--property=NoNewPrivileges=yes"} {
			found := false
			for _, arg := range args {
				if arg == required {
					found = true
				}
			}
			if !found {
				t.Fatal("worker lost sandbox or lifetime bound", required)
			}
		}
		if args[len(args)-2] != "/usr/local/bin/soar-panel-ops" || args[len(args)-1] != "firewall-"+kind {
			t.Fatal("worker executes a different command", args)
		}
	}
	if _, err := firewallUnitArgs("/usr/local/bin/soar-panel-ops", "arbitrary-command"); err == nil {
		t.Fatal("arbitrary firewall command accepted")
	}
}

func TestHTTPSFirewallPreservesRulesAndPropagatesStatusFailure(t *testing.T) {
	ports, comment, _ := firewallPorts("https")
	var calls []string
	err := ensureFirewall(func(args ...string) ([]byte, error) {
		if args[0] == "status" {
			return []byte("Status: active\n80/tcp ALLOW Anywhere # original"), nil
		}
		calls = append(calls, strings.Join(args, " "))
		return nil, nil
	}, ports, comment)
	if err != nil || !reflect.DeepEqual(calls, []string{"allow 443/tcp comment soar-panel-https"}) {
		t.Fatal(calls, err)
	}
	if err = ensureFirewall(func(...string) ([]byte, error) { return nil, errors.New("read-only filesystem") }, ports, comment); err == nil {
		t.Fatal("reported firewall success after status failed")
	}
}
