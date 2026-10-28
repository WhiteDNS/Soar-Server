# WhiteDNS server management over SSH

After installing the bundle, open the interactive root menu:

```bash
sudo whitedns
# Equivalent commands:
sudo soar-panel
sudo soar-panel menu
sudo soar-panel-menu
```

The menu works when the web panel is stopped or the administrator password is
lost. It requires an interactive SSH terminal on a supported systemd Linux host.
Terminal labels use English ASCII for SSH compatibility.

| Menu | Operation |
| --- | --- |
| 1–2 | Version, service state, resources, maintenance state, login URL and SSH tunnel instructions |
| 3–7 | Start, stop, restart, enable or disable startup at boot |
| 8 | Replace the administrator username and password, with hidden password input |
| 9 | Change the primary web domain, manage up to seven additional web domains, obtain HTTPS certificates, generate a new private path, recover HTTP access, or reissue certificates |
| 10 | Inspect certificate identity, issuer, expiration and fingerprint |
| 11 | Change the HTTP management port and choose loopback or public test HTTP |
| 12 | Add/remove up to eight tunnel domains and choose the preferred domain |
| 13 | Change bootstrap resolvers, allowed destination ports, discovery hint and public IP used in DNS instructions |
| 14–16 | Create a database backup with a machine recovery snapshot, list private backups, validate and restore a database |
| 17 | Check and install the latest official newer release |
| 18–20 | View/follow/export service logs, inspect DNS/firewalls and prepare local DNS/HTTPS firewall rules |
| 21 | Show the administrator name and tunnel settings without password hashes or account keys |
| 22 | Remove the panel software, optionally also remove its private data |
| 23 | Help and recovery paths |
| 24 | Change the shared local UDP/TCP tunnel DNS listener port |

## Domains and certificates

Web domains and tunnel domains have separate settings. Create the required DNS
records at your provider before changing them. Web HTTPS needs DNS-only A/AAAA
records pointing to this server and inbound TCP/80 and TCP/443. Tunnel domains
need the appropriate A/NS delegation and inbound UDP/53 and TCP/53.

The HTTPS wizard obtains Let's Encrypt certificates and the running panel renews
them automatically. The reissue option removes only the configured domain's leaf
certificate cache entries after backing them up. Use it for recovery; repeated
requests can hit certificate authority rate limits. Failed setup restores the
previous access settings and cached certificates.

Keep old tunnel domains in the list while migrating clients. Removing one stops
links using it. Account keys, subscription tokens, limits and usage remain.
The immutable original base zone is retained internally for account identities.

The HTTP port option writes dedicated systemd drop-ins for both services, keeping
the maintenance health address synchronized. It supports the standard installer
service command; customize a nonstandard service manually. HTTPS stays on 443.
Open a public test HTTP port in the provider and local firewalls yourself. Public
HTTP sends credentials unencrypted. HTTP recovery in the HTTPS wizard retains
the existing HTTP listener; a loopback listener still requires an SSH tunnel.

## Backups, updates and recovery

Offline changes stop the panel briefly, stop the maintenance service, take a
private snapshot, validate/save the change, and restart only services that were
previously running. A failure restores data and managed drop-ins. Changes share
an exclusive lock with web maintenance and the installer. Passwords are passed
through a temporary mode-0600 file, never command arguments.

Terminal backups are under `/var/backups/soar-panel`, mode 0700, and are not
automatically deleted. They contain credentials, certificates and account keys.
Copy them securely to a separate machine and manage retention according to your
available disk space. A snapshot's `data/` folder holds the stopped-service data;
`panel.conf` and `ops.conf`, when present, hold the menu's previous drop-ins.
An uninstall backup also includes both service units and their drop-in folders.

Database restore accepts an inspected local `.db` file up to 32 MiB from the
same original base zone. It replaces account data, keys, usage and tunnel settings,
preserving the current administrator, access configuration, certificates and
Telegram pairing. The maintenance service creates its own rollback backup under
`/var/lib/soar-panel-ops` and rolls back a failed readiness check. Those job backups
have automatic retention; terminal snapshots have separate retention.

Updates use the existing official-release downloader, archive verification and
automatic rollback. The menu and HTTPS helper update with the binaries. The
operations binary also embeds the menu and installs it on startup, allowing
older installations to acquire the command through their existing updater.
Reopen the menu after an update.

The DNS port option changes the local shared listener for all three engines.
For a nonstandard local port, configure your DNS frontend to forward the tunnel
zone, or configure router/NAT forwarding from public UDP/TCP 53. Public recursive
DNS uses port 53 and NS records cannot carry a port. The menu keeps other DNS
services and their forwarding configuration intact. The installer tests each
engine locally on the selected port and separately through recursive DNS.

Uninstall always asks for `UNINSTALL`. Removing private data additionally asks
for `DELETE-DATA`, after creating a recovery copy. Recovery copies remain on disk
even with that option. SSH, firewall rules, the service user and unrelated
standalone services remain. All three tunnel engines managed by the panel stop.

For a failed recovery, stop both services and restore the root-only snapshot
before restarting. Keep the snapshot private. If disk space prevented a full
snapshot, the menu reports the error and attempts to restore the prior running
state without applying the requested change. Server readiness and DNS checks
do not verify connectivity from a phone or a restricted network.
