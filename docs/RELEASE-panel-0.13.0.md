# WhiteDNS Server Management 0.13.0

## Manage the server through SSH

Run `sudo whitedns`, `sudo soar-panel` or `sudo soar-panel menu` for the new
24-option server menu. It includes administrator password/name recovery,
service and boot controls, web and tunnel domains, automatic HTTPS and
certificate recovery, private login paths, management and DNS ports, network
settings, private backups, database restore, official updates, logs, diagnostics,
firewall preparation and uninstall with separate data-removal confirmation.

Offline changes create private recovery snapshots and restore them on failure.
A shared lock prevents concurrent installer, SSH and web maintenance operations.
The verified operations binary includes the menu, so updates from older panel
versions can install it without downloading an additional script. Existing
unrelated commands called `whitedns` are preserved.

See the [Persian SSH guide](SERVER-MENU.fa.md) or [English SSH guide](SERVER-MENU.en.md).

## Smoother panel navigation and live statistics

- Keep account rows and update their values in place instead of rebuilding the
  table on every poll. Preserve row controls, selections and keyboard focus.
- Reuse number/date formatters, coalesce simultaneous identical reads and stop
  polling/repainting inactive dashboard and account views.
- Keep a lightweight authenticated heartbeat on idle forms so disconnection,
  session expiry and connection recovery remain visible without heavy polling.
- Keep the latest dashboard figures visible when returning to the page. Do not
  append duplicate traffic samples when cached data is displayed.
- Reject responses from an older navigation/session or a read preceding a
  mutation, so delayed requests cannot restore stale values.
- Add a brief 160 ms opacity/position transition and stable numeral/scrollbar
  spacing. Respect the system's reduced-motion preference. Counters display
  actual server readings without animated intermediate numbers.

## Updates with manually configured TLS

Fixes #21. Health checks now use the running listener's actual scheme, port and
private path. A custom certificate uses its configured domain, or certificate
SAN, with certificate and hostname verification. The machine-local listener
description contains public certificates only. Built-in automatic HTTPS keeps
its separate port-443 health path. SSH health checks use the same implementation.

## Custom tunnel DNS port

Addresses #11. Choose a local UDP/TCP DNS port in the installer or SSH menu;
the service, local tunnel probes and UFW preparation use the selected port.
Existing installations retain their current listener port when reinstalling.
This applies to Soar, Cotten and MasterDNS, which share the listener.

Public recursive DNS still reaches UDP/TCP 53. A custom local listener requires
your DNS frontend to forward the tunnel zone, or your router/NAT to forward
public port 53, to that listener. An NS record cannot specify a custom port.
The installer does not replace other DNS services or create forwarding rules.
Keep the installation marked unverified until its recursive tunnel tests pass.

## Upgrade and recovery

Accounts, keys, subscriptions, saved usage and protection settings are retained.
Updates and approved offline changes briefly interrupt tunnels. Keep a private
backup before upgrading. Terminal snapshots under `/var/backups/soar-panel`
contain credentials and keys and are not automatically deleted. Uninstall keeps
a private recovery copy even when data removal is selected.

Existing direct TLS service flags are retained. Review provider firewall/NAT
rules separately. Local and CI validation cannot certify physical devices,
production VPS deployments or connectivity through restricted client networks.
