# WhiteDNS Panel 0.7.0

- Added Linux arm64 bundles alongside amd64, architecture-aware installation and verified updates, ELF architecture checks, and native amd64/arm64 CI build, test, browser and release stages (issue #10).
- Added a dedicated Telegram bot for one private administrator. Set up with a bot token and a five-minute, single-use pairing link in dashboard settings; no manual chat ID is required.
- Added Persian/English button menus, guided account creation and editing, account controls, copyable subscription/direct connection strings, and `/stats` for current server resources and usage. Telegram sends no QR images.
- Added confirmed panel/tunnel restarts and verified official stable panel updates through the existing maintenance service, plus account/runtime/release alerts and a daily current-state report at 09:00 Asia/Tehran by default.
- Made credential changes atomic against concurrent dashboard saves, retained unacknowledged commands through storage failures, and persisted the first reporting baseline so missed reports recover once without a backlog.
- Kept bot credentials and delivery/action state in a private sidecar outside downloadable database backups. Telegram failures do not stop tunneling or dashboard account management.

See [Telegram setup, scope, and recovery](TELEGRAM.md). Back up the panel before upgrading. Complete outages cannot send Telegram messages until the panel returns. Reports show current account totals, not previous-day traffic.

The source, runtime, dashboard, installer, and offline bundle identify as 0.7.0. The public bootstrap stays pinned to the previously verified 0.6.0 release until the 0.7.0 archive and checksum are published and verified. A source push does not publish release assets or upgrade a running server.
