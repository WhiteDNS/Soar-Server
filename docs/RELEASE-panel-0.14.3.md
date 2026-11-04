# WhiteDNS Server Management 0.14.3

Publication uses the single manually triggered build-and-release workflow.

## Review this after upgrading

Servers behind 1:1 NAT could previously reach their own public IP through the
tunnel, because that address is on no interface. The panel's public IPv4 is now
blocked everywhere unless its port is listed in the new setting below.

## Outbound ports and upstream proxies

- New setting: **Allowed ports on this server's IP**. It is empty by default, so
  tunnel connections to this server stay blocked on every route, even with
  unrestricted proxy ports. A listed port opens only on the panel's exact public
  IPv4, and it must also be in the engine's route list. `127.0.0.1` and private
  addresses always stay closed, and restoring a backup does not change this list.
  IPv6 is not covered.
- **Warning:** a listed port is reachable by every account from inside the server,
  even if a firewall closes it from outside. Do not list the panel or SSH ports.
- A slow proxy chain now gets the full dial time when the destination has one
  address. A destination that never answers now holds its slot for up to 15 s.
- SOCKS5 UDP replies from an IPv4-mapped source are no longer dropped.
- New log reasons: `destination_not_public` for this server or a private address,
  and `proxy_dns_failed` for a proxy that is down or refuses port 53. DNS still
  does not fall back to direct.

## Engines

- CottenDNS `be99443`: DNS cache answers no longer outlive their TTL, and idle UDP
  sockets close on long-running associations.
- Soar `v0.2.4`: fixes a client-side stream leak. The panel does not use that code,
  and the wire protocol is unchanged.

## Development

- Pull requests and the release workflow now test native clients through proxies
  on custom ports and test own-server access in a network namespace.

Real-network acceptance on Linux with the WhiteDNS client and real proxy software
still needs operator verification.
