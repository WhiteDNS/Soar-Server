# WhiteDNS Soar Panel 0.2.0-beta10

This release adds an adaptive profile preset for the WhiteDNS 1.6.7-beta2 Soar
transport fixes. The server's tunnel wire protocol, account keys and admin access
are unchanged. Lantern remains the author of the Soar engine.

- Fresh installations use adaptive defaults: start at SafeQName=101 and allow
  learning up to MaxQName=253 and MaxAnswer=1232, with 128 total queries and a
  32-query per-path ceiling. Discovery targets two healthy paths with two probes
  at 1000 ms pacing. These are ceilings, not forced packet sizes on every network.
- Add “Adaptive (recommended)” alongside the existing limited, balanced and
  performance choices. A preset fills the form; saving and reimporting the same
  account's link are explicit steps. Existing saved tuning is preserved.
- Explain that a fixed 101-character ceiling can severely limit TLS handshake
  upload, and that updating a panel cannot replace an old Android native core.
- Keep all beta9 dropdown/help fixes and beta8 backup, restore, in-panel updates,
  HTTPS/domain configuration and private login paths.

Use the signed [WhiteDNS 1.6.7-beta2](https://github.com/WhiteDNS/WhiteDNS-Android/releases/tag/1.6.7-beta2)
or newer for the transport patch. MasterDNS/StormDNS and CottenDNS formats and
engines are unchanged by that app patch. Server and profile updates alone do not
replace the native engine on an installed phone.

[Persian root-cause analysis and qualification limits](https://github.com/WhiteDNS/Soar-Server/blob/panel-v0.2.0-beta10/docs/SOAR-WEB-RELIABILITY-2026-10-18.fa.md)
includes real Chrome before/after observations, the supplied MasterDNS comparison,
constrained DNS tests and the distinction between Amazon anti-bot responses and
transport failures. Iranian access networks and physical-device Full VPN remain
unqualified by the desktop tests.

Install or upgrade the explicit panel beta:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/WhiteDNS/Soar-Server/panel-v0.2.0-beta10/panel-setup.sh)
```

[Persian installation and recovery guide](https://github.com/WhiteDNS/Soar-Server/blob/panel-v0.2.0-beta10/docs/PANEL.fa.md)
