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
	return ensureHostFirewall(ctx, "dns")
}

func ensureHostFirewall(ctx context.Context, kind string) error {
	path, err := exec.LookPath("ufw")
	if err != nil {
		return nil
	}
	ports, comment, err := firewallPorts(kind)
	if err != nil {
		return err
	}
	return ensureFirewall(func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		return cmd.CombinedOutput()
	}, ports, comment)
}

func firewallPorts(kind string) ([]string, string, error) {
	switch kind {
	case "dns":
		return []string{"53/udp", "53/tcp"}, "soar-panel-dns", nil
	case "https":
		return []string{"80/tcp", "443/tcp"}, "soar-panel-https", nil
	}
	return nil, "", fmt.Errorf("unknown firewall action")
}

// UFW opens /run/ufw.lock even for status. Existing maintenance units mount
// /run and /etc read-only. A short-lived, fixed-action unit grants just UFW's
// write paths; the long-running maintenance service retains its old sandbox.
func PrepareFirewall(ctx context.Context, binary, kind string) error {
	if _, _, err := firewallPorts(kind); err != nil {
		return err
	}
	if _, err := exec.LookPath("ufw"); err != nil {
		return nil
	}
	if os.Getenv("INVOCATION_ID") == "" {
		return ensureHostFirewall(ctx, kind)
	}
	args, err := firewallUnitArgs(binary, kind)
	if err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, "systemd-run", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("cannot prepare %s firewall: %w: %s", kind, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func RunFirewallAction(ctx context.Context, kind string) error { return ensureHostFirewall(ctx, kind) }

func firewallUnitArgs(binary, kind string) ([]string, error) {
	if _, _, err := firewallPorts(kind); err != nil {
		return nil, err
	}
	if binary == "" {
		return nil, fmt.Errorf("maintenance binary is required")
	}
	return []string{"--quiet", "--wait", "--pipe", "--collect", "--property=RuntimeMaxSec=12s", "--property=ProtectSystem=strict", "--property=ReadWritePaths=/run -/etc/ufw -/var/lib/ufw", "--property=NoNewPrivileges=yes", "--property=PrivateTmp=yes", "--property=ProtectHome=yes", "--property=RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK", "--", binary, "firewall-" + kind}, nil
}

func ensureDNSFirewall(run func(...string) ([]byte, error)) error {
	return ensureFirewall(run, []string{"53/udp", "53/tcp"}, "soar-panel-dns")
}

func ensureFirewall(run func(...string) ([]byte, error), ports []string, comment string) error {
	out, err := run("status")
	if err != nil {
		return fmt.Errorf("cannot inspect DNS firewall: %w", err)
	}
	status := string(out)
	if !strings.Contains(status, "Status: active") {
		return nil
	}
	for _, port := range ports {
		found := false
		for _, line := range strings.Split(status, "\n") {
			fields := strings.Fields(line)
			bare, _, _ := strings.Cut(port, "/")
			if len(fields) >= 3 && (fields[0] == port || fields[0] == bare) && fields[1] == "ALLOW" && fields[2] == "Anywhere" {
				found = true
				break
			}
		}
		if !found {
			if _, err = run("allow", port, "comment", comment); err != nil {
				return fmt.Errorf("cannot allow DNS %s: %w", port, err)
			}
		}
	}
	return nil
}
