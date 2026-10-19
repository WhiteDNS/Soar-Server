package panelops

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// EnsureDNSFirewall also runs when a 0.4.x maintenance service upgrades itself:
// the new root helper starts after activation and prepares TCP/53 automatically.
// Existing broad allow rules and comments remain intact. An inactive firewall
// stays inactive; provider firewalls are configured outside this process.
func EnsureDNSFirewall(ctx context.Context) error {
	path, err := exec.LookPath("ufw")
	if err != nil {
		return nil
	}
	return ensureDNSFirewall(func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		return cmd.CombinedOutput()
	})
}

func ensureDNSFirewall(run func(...string) ([]byte, error)) error {
	out, err := run("status")
	if err != nil {
		return fmt.Errorf("cannot inspect DNS firewall: %w", err)
	}
	status := string(out)
	if !strings.Contains(status, "Status: active") {
		return nil
	}
	for _, port := range []string{"53/udp", "53/tcp"} {
		found := false
		for _, line := range strings.Split(status, "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 3 && (fields[0] == port || fields[0] == "53") && fields[1] == "ALLOW" && fields[2] == "Anywhere" {
				found = true
				break
			}
		}
		if !found {
			if _, err = run("allow", port, "comment", "soar-panel-dns"); err != nil {
				return fmt.Errorf("cannot allow DNS %s: %w", port, err)
			}
		}
	}
	return nil
}
