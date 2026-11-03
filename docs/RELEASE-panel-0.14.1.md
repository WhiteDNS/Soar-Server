# WhiteDNS Server Management 0.14.1

Publication uses the single manually triggered build-and-release workflow.

## Review this after upgrading

Destination limits now apply per account, and all accounts together may reach
8 times the configured value. Saved values are not converted. A strict policy
therefore allows up to 8 times more total traffic to one destination than it
did in 0.14.0. If a limit exists to protect one sensitive destination, divide
`target_*`, `network_*` and `target_upload_kib` by 8 where the allowed range
permits it.

## Routing and connection fixes

- One busy account no longer blocks other accounts from the same CDN, Telegram
  DC or `/24` network.
- A dead egress proxy or a down server uplink no longer quarantines accounts.
  `dial_failures` quarantine also needs a successful dial on the server within
  the last 10 seconds. Scanning on a quiet or proxied route is limited by
  `target_scan` and the rate limits instead.
- A full Cotten or MasterDNS queue now drops the query so the resolver retries,
  instead of answering REFUSED. Each account can hold at most 32 queued queries.
- Tunneled DNS retries over TCP when a SOCKS5 proxy fails over UDP.
- Names inside the panel's zones get NODATA with SOA instead of REFUSED, which
  made resolvers treat the server as lame. This includes disabled and unknown
  accounts. REFUSED remains for names outside those zones.
- Lookups through an egress proxy are cached for one minute.

## Panel and installation

- Saving settings without a change no longer disconnects every tunnel.
- Expired login attempts no longer lock out new addresses.
- Bundle downloads may take up to 15 minutes on slow links.
- New installs default to private access instead of plain HTTP. Reinstalls keep
  the current mode.
- The panel service runs with additional systemd sandboxing after the installer
  is rerun. In-panel updates do not rewrite the service unit.
- The server menu keeps only the five newest automatic snapshots.
- `AUDIT-REPORT.md` is no longer part of the standalone bundle.

Real-network acceptance on Linux with the WhiteDNS client still needs operator
verification.
