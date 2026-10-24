# WhiteDNS Server Management 0.8.3

- Add outbound anti-flood controls shared by Soar, Cotten and MasterDNS, including all tunnel domains. Limit connection starts, pending/open streams, destination IP/network activity, distinct destinations and upload traffic on existing connections.
- Temporarily quarantine repeated account violations and high-failure scans across diverse targets. Quarantine closes all engines for the account, survives a server restart and can be released by an administrator.
- Add a Persian/English protection page with enforce, monitor and off modes, rejection reasons, recent events and manual release. Reports do not retain destination addresses or traffic contents.
- Preserve account identities, keys, subscription URLs, usage and saved protection settings when older clients update other server settings.

## Upgrade notes

Protection defaults to **enforce**, including existing accounts. Its ceilings apply independently of the account/server stream settings: an existing zero/unlimited stream limit does not disable anti-flood protection. Shared links share the account's protection budget, and destination limits are shared between accounts. Review the defaults and calibrate them against legitimate load before a broad rollout; monitor mode records events without blocking.

Download a backup, install the update and refresh the panel page. Users do not need to re-import their tunnel profiles for these server-side controls. Inbound resolver IPs are not automatically banned. This feature does not inspect HTTPS request rates inside encrypted connections, cover other VPS processes, or guarantee prevention of every hosting suspension.

See [the operator guide](ANTIFLOOD.fa.md) and [validation scope](ANTIFLOOD-VALIDATION-2026-10-24.md). No VPS/load/restricted-network acceptance test or reproduction of an actual hosting abuse complaint has been performed.
