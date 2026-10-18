# WhiteDNS Soar Panel 0.2.0 — Stable

First stable management-panel release, promoted from beta10. Lantern develops
the Soar engine; WhiteDNS maintains the panel, installer and client integration.

- Separate account links/QRs, durable traffic budgets, expiry, shared speed and
  TCP stream limits; preserve keys and counters across upgrades.
- Persian responsive dashboard, dark/light themes, consistent dropdowns and
  help on question-mark controls.
- In-panel backup, restore and updates with administrator reauthentication;
  domain HTTPS, automatic certificate renewal and private login paths.
- Adaptive profile exports preserve existing explicit tuning. Stable installs
  discover stable updates only; existing betas can graduate to this release.

## Installation and upgrade

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/WhiteDNS/Soar-Server/main/panel-setup.sh)
```

The README now points to this stable panel installer. It pins 0.2.0, verifies
archive and internal checksums, and preserves stdin for the guided seven steps.
Supported: Ubuntu 22.04/24.04/26.04 or Debian 12/13 on amd64, systemd, root/sudo.
No Go, Node, Docker or separate database is required on the VPS.
The legacy `setup.sh` remains the standalone server installer without a panel.

Back up first. Beta8 and later can update from the panel; older installations
should rerun the panel installer once to install maintenance support. Accounts,
keys, usage, saved tuning and HTTPS/private-path settings are retained.
Standalone-to-panel migration requires explicit confirmation and new account
links; shared-root profiles do not migrate automatically.

The panel is stable; the Android transport patch remains a prerelease. For the
browser reliability fix use
[WhiteDNS 1.6.7-beta2 or newer](https://github.com/WhiteDNS/WhiteDNS-Android/releases/tag/1.6.7-beta2),
save adaptive tuning and reimport the same account's link. A server upgrade
cannot replace an installed native client engine.

## Checks and limits

Release gates cover Go tests, race detection, vet, installer/bootstrap checks,
offline bundle verification and account/settings/maintenance browser workflows.
The server wire format is unchanged.

Stable panel status does not certify Iranian carrier reachability, all websites,
physical Android Full VPN or production-scale load. Amazon automated responses
included anti-bot/intermediary pages. One earlier final-candidate run stalled
with unresponsive DNS paths and remains unexplained; later cache-free runs
succeeded. See the [root-cause report](https://github.com/WhiteDNS/Soar-Server/blob/panel-v0.2.0/docs/SOAR-WEB-RELIABILITY-2026-10-18.fa.md)
and [Persian installation guide](https://github.com/WhiteDNS/Soar-Server/blob/panel-v0.2.0/docs/PANEL.fa.md).
