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
