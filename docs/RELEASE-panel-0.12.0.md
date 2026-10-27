# WhiteDNS Server Management 0.12.0

## Telegram management when the dashboard is unreachable

- Manage server and tunnel settings, resolvers, allowed ports, country, stream limits, tunnel domains, Soar tuning and anti-flood policy from the paired private administrator chat. Preview and confirm changes; stale approvals cannot overwrite newer edits. Valid settings restart active engines while preserving account identities, tunnel keys, subscription URLs and usage.
- View runtime diagnostics, recent structured logs and maintenance results; release account anti-flood quarantine with confirmation. Manage panel domains, HTTPS and the private access path with separate preparation and activation confirmations.
- Keep existing account creation, editing, limits, connection sharing, key/subscription rotation, disconnect, usage reset, disable/delete, stable updates and service restart controls.

## Reliable reports and encrypted backups

- Check report and backup schedules independently of incoming Telegram polling. Failed or blocked `getUpdates` no longer suppresses outgoing delivery. Reports and backups have separate retries and persistent delivery status/error display. Sending a message successfully does not hide a polling failure.
- Fix plain Telegram notifications by omitting an absent `reply_markup`, rather than sending JSON null. This addresses report, alert and connection-test failures even when token/chat pairing is valid; see the independently reported issue in [PR #18](https://github.com/WhiteDNS/Soar-Server/pull/18).
- Add manual and opt-in daily compressed, AES-256-GCM encrypted database backups. Configure the time and timezone in the bot or panel; automatic backups default to off, with 03:00 Asia/Tehran as the initial schedule.
- Restore a `.wdb` or existing panel `.db` by replying to `/restore`. The bot checks the file, displays account count, base zone, size and checksum, and requires a separate expiring confirmation. The existing maintenance service creates a rollback backup and preserves the current administrator, panel access and Telegram pairing.
- Store the backup recovery key separately outside the server. It is excluded from database backups and browser configuration responses. Disconnecting Telegram retains the local backup key. Importing a different key requires confirmation; earlier backups still require their original key.

## Fetch AI models instead of typing IDs

- Use **Fetch models** to retrieve and select models with the entered or saved provider credentials before saving the connection. Supports native Gemini, native Anthropic and OpenAI-compatible model-list endpoints, including pagination and Gemini generation-method filtering.
- Keep model selection after a failed fetch, discard late responses after destination changes, and reuse saved keys only at their saved destination. Discovery sends no diagnostic content and does not save configuration. Optional manual entry remains available for endpoints without model discovery.

## Upgrade and limits

MasterDNS final session cleanup now seals the transmit queue against late worker callbacks. This prevents a closed stream's queue from being repopulated while retaining protocol reset delivery before finalization. The pinned engine patch and checksums include the fix and its regression test.

Upgrade through Panel updates or the existing installer, then refresh the browser. Accounts, keys, subscription URLs and persisted usage remain valid. Enable automatic backup explicitly in `/backup` or Telegram settings, retrieve the recovery key and keep an independent copy. Backups reflect the current database snapshot; catch-up sends only the latest missed slot. A crash between Telegram accepting a payload and saving its receipt can repeat delivery.

The Telegram recovery route accepts at most 20 MiB per transferred file and 32 MiB after decompression. The bot requires a running panel process and server access to Telegram; it cannot recover a powered-off server or a server without Telegram connectivity. DNS changes at the domain provider remain an external prerequisite for domain migration. Soar profile changes require clients to refresh subscriptions or reimport.

Validation uses local mock Telegram/AI endpoints, DNS and certificate fixtures, complete Go and race checks, browser workflows and Linux amd64/arm64 release CI. No real bot token, provider account/quota, VPS deployment or restricted-network device connectivity is implied by automated checks.

[راهنمای فارسی مدیریت اضطراری و بازیابی](https://github.com/WhiteDNS/Soar-Server/blob/main/docs/telegram-emergency-management.fa.md) · [راهنمای لاگ‌ها و دستیار](https://github.com/WhiteDNS/Soar-Server/blob/main/docs/DIAGNOSTICS-ASSISTANT.fa.md)
