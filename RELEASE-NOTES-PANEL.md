# WhiteDNS Soar Panel 0.3.1 — Installer DNS and connectivity checks

Fresh installations now verify the parent A/NS records before starting the
service, then check child SOA and authenticated HTTPS through both the local DNS
listener and recursive resolvers after startup. Missing records cannot be
bypassed with CONTINUE. Failed checks offer retry, resolver changes, rollback,
or an explicitly unverified installation that exits with a nonzero status.

Authoritative DNS uses addresses resolved through the selected resolvers,
retries UDP and falls back to TCP. Errors identify the server, record and
transport. Existing conflicting delegations and stale child DS remain blocked.
The wizard can install missing network tools, accepts corrected input, uses the
default-route local address, and keeps public IPv4 separate on NAT hosts.

Active UFW gets the reviewed UDP/53 rule as well as direct-management TCP when
needed. Existing broad allow rules are preserved; rollback removes only newly
added rules. Provider firewall and NAT rules still need provider-side setup.

A reserved, quota-limited account performs installation checks without using
customer keys or usage. It is removed before completion, expires after an
interruption, and is renewed if an interactive retry takes longer. Rollback
restores database, access settings, helper binaries, units and service enable
states. Reinstalling keeps existing account keys, usage and saved tuning.

Rerun the guided installer to apply installer/firewall changes to an existing
server. An in-panel binary update alone does not rerun the installation wizard.
Passing server-side checks does not guarantee a working DNS path on every phone
network; test the exported account on the intended network.

# WhiteDNS Soar Panel 0.3.0 — Subscriptions and English

Each account now has a private subscription URL and QR code. With WhiteDNS
Android 1.6.8 or later, users import that URL once and refresh it to receive
updated resolver lists, tuning and rotated tunnel keys. Existing direct profiles
continue to work. Responses include quota, usage, expiry and the recommended
12-hour refresh interval.

Subscription credentials are separate from tunnel keys and the private admin
login path. Rotate a subscription URL to revoke its downloads; disable the
account or rotate its tunnel key to revoke previously downloaded access.
Deleted accounts invalidate their subscription. Inactive accounts export no
profile. Use HTTPS for subscription delivery.

The login page and toolbar now switch the full interface between Persian and
English, with RTL/LTR layout, localized dates, menus, messages and field help.
The choice persists in the browser. User-entered names and notes are preserved.
An English installation guide is included in the offline bundle.

Upgrade from Panel updates, or rerun the README installer. Existing accounts,
usage, keys, tuning, TLS settings and private login path are retained. Download a
backup first. New backups preserve subscription URLs; 0.2.0 backups remain
readable, but 0.3.0 backups require 0.3.0 or later for restore. Restoring an older
backup also restores that backup's token state.

See [subscription usage and security](docs/SUBSCRIPTIONS.md),
[English installation](docs/PANEL.en.md) and [راهنمای فارسی](docs/PANEL.fa.md).
The tunnel engine is [Lantern Soar](https://github.com/getlantern/soar).
