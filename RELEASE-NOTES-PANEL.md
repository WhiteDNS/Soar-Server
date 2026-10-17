# WhiteDNS Soar Panel 0.2.0-beta2

An opt-in **server-side** management panel for Lantern Soar. The familiar sidebar,
dashboard and account editor are inspired by Sanaei/3x-ui; the implementation is
original. Lantern remains the author of the Soar tunneling engine.

- Create independent accounts and export existing WhiteDNS-compatible links/QRs.
- Set combined upload/download quota, expiry, shared speed and concurrent TCP streams.
- Disable access, disconnect streams, reset usage or revoke/replace an account link.
- View real Linux resource statistics, separate traffic counters and management events.
- Download durable backups, export accounts and configure discovery/resolver defaults.
- Persian RTL, mobile layout, light/dark themes; English-only SSH installation steps.

**No WhiteDNS update is required.** Migrating a standalone server requires new
per-account profile links. The old shared-root profile stops working after migration;
the installer explains this and preserves the previous service/config for rollback.

The installer now offers direct browser access using the server IP and chosen
TCP port, plus a private SSH/HTTPS-proxy option. It displays the actual access URL.
Direct HTTP is an explicit test-server mode and is unencrypted; the CLI otherwise
retains its TLS/private-access checks. Upgrades preserve accounts, links and usage.
Backups contain private account keys. A TCP stream limit is not a device limit;
anyone sharing one profile shares its budget. Payload accounting excludes DNS overhead.

This is a beta, initially Linux amd64. It has automated tunnel, quota, authentication,
race and browser tests, plus a live Ubuntu/systemd deployment check. Real Iranian
access-network validation and production load qualification remain outstanding.

Install the explicit panel beta (in an interactive SSH terminal):

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/WhiteDNS/Soar-Server/panel-v0.2.0-beta2/panel-setup.sh)
```

[Persian installation, migration and recovery guide](https://github.com/WhiteDNS/Soar-Server/blob/panel-v0.2.0-beta2/docs/PANEL.fa.md)
