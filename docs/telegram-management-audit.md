# Telegram management review

Reviewed against local `main` based on `c41a836` (panel source version 0.11.0). This is an implementation review and local validation record; it does not claim publication, VPS deployment or real Telegram connectivity.

## Paths reviewed

- Token validation, private-chat pairing, expiry/renewal, replacement/disconnect, HTTP authentication and CSRF, credential exposure and persisted update offsets.
- Account listing/search/pagination, creation/editing, engine/session/stream/quota/expiry controls, disconnect/reset/delete, tunnel-key and subscription rotation, profile sharing and anti-flood quarantine release.
- Polling, backoff/rate limiting, outgoing scheduling, reporting baselines, restart recovery, notification state and runtime-failure transitions.
- Stable updates/restarts, maintenance job recovery, panel/tunnel settings and access-domain/HTTPS preparation and activation.
- Encrypted backup generation/upload, schedule/retry/catch-up, recovery-key lifecycle, administrator-uploaded restore inspection/confirmation, maintenance handoff and expiry cleanup.
- Dashboard settings, persistent delivery errors, Persian/English UI and mobile layouts.

## Findings addressed

| Finding | Result |
| --- | --- |
| Reports checked only after successful `getUpdates` | A separate one-second scheduler dispatches outgoing work under the existing serialization lock. Tests exercise HTTP 409, HTTP 500 and a blocked long poll. |
| Plain notifications sent `reply_markup: null` | Omit the field when no keyboard is present; the Telegram test server now rejects explicit null like the real API. Independently reported in PR #18. |
| Polling cadence allowed a 74-second idle report delay | Due-time checks use the separate scheduler. Network latency, rate limits and serialized operations can still delay delivery. |
| Earlier alerts or reports could suppress later scheduled work | Report and backup jobs run before alert processing and have separate delivery markers, errors and retry deadlines. |
| Most panel-wide controls required dashboard access | Added server/tunnel settings, diagnostics, anti-flood status/release and panel-domain/HTTPS management, retaining existing validations and maintenance services. |
| No automatic backup or bot restore | Added opt-in daily encrypted backups, manual exports and validated restore previews handed to the existing rollback-capable maintenance service. |
| Configuration changes could invalidate encryption access | Disconnect retains the private backup key. Key replacement is an explicit confirmation flow; public HTTP views never return it. |
| Delayed settings confirmation could overwrite newer edits | Settings use a transactional comparison with the preview snapshot; Telegram preferences also compare their complete prior configuration. |
| Failed action-marker persistence left an in-memory pending action | Clear the unexecuted marker on failed initial persistence. No mutation is dispatched. |
| MasterDNS failure counters persisted across process-local counter resets | Reset MasterDNS recovery flags and counters alongside Cotten during startup. |
| Oversized Unicode messages could exceed Telegram limits | Split ordinary messages at bounded UTF-16 boundaries; put the keyboard on the last part. |
| Abandoned restore previews could retain a database indefinitely | Scheduler releases expired confirmation data and drafts even without subsequent administrator input. |
| Successful outgoing messages could hide failed polling | Keep a separate polling error until receiving succeeds or the bot is reconfigured. |

## Backup and restore boundaries

Exports use gzip followed by AES-256-GCM with a fresh nonce and an authenticated format header. A 256-bit random recovery key lives in the private Telegram configuration, outside the account database. Uploaded documents are accepted only from the paired private administrator, in reply to an active restore prompt. Both declared and actual file sizes are bounded. Telegram file paths cannot be absolute, contain traversal segments or redirect to another endpoint. Decompression is bounded; authenticated ciphertext and full database inspection precede preview. The existing production inspector runs in its time-limited child process. A separate expiring confirmation is required before restore; updates are durably acknowledged and confirmations cannot be replayed.

The Telegram round trip is capped at 20 MiB encrypted/file size and 32 MiB expanded database size. Existing `.db` backups are accepted as well. The maintenance service independently validates restore input, makes a rollback backup and preserves current administrator credentials and machine-local panel/Telegram access. Automatic delivery is opt-in, uses the configured IANA timezone, retries failed attempts and catches up only the latest missed slot. Catch-up exports the current state. A crash between Telegram accepting a payload and durable receipt storage may result in a repeated delivery.

## Local validation

- Complete `go test ./... -count=1 -timeout 180s`: passed, including panel and maintenance service tests.
- Complete browser suite (`node tests/panel-ui.cjs` against an isolated built panel): passed, including the added automatic-backup fields, delivery-error display, translations and responsive checks.
- `go vet ./...`, JavaScript syntax checks and `git diff --check`: passed.
- Added focused tests cover encrypted round trips and tampering, confirmation/replay, private-key persistence after disconnect, schedule/retry/catch-up/restart, polling failures and blocking, independent outgoing jobs, corrupt/mismatched/oversized/unauthorized restores, settings conflicts and identity preservation, each server-setting control, two-stage access activation and expiry cleanup.
- Complete Telegram race suite (`go test -race ./internal/panel -run '^TestTelegram' -count=1 -timeout 900s`): passed in 278 seconds. Bcrypt-heavy fixture setup exceeded an earlier three-minute timeout; the completed run reported no races. Follow-up focused tests cover the final polling-status and preview-cleanup refinements.
- Linux amd64 panel cross-build: passed. Runtime validation remains local Windows fixtures and browser tests; no Linux/VPS installation was performed.

All added Telegram, DNS and certificate tests use fixtures. No production token, real message, remote backup, real certificate, VPS deployment or restricted-network field test was used. The bot remains part of the panel process and cannot recover a powered-off server or a server with no Telegram connectivity. See [the operational guide](telegram-emergency-management.fa.md).
