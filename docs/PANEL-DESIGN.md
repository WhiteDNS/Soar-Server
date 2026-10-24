# WhiteDNS Server Management: implementation contract

The panel is a server-side addition. No Android files or wire protocol are changed.
Lantern owns the upstream Soar engine; WhiteDNS develops this management layer.
The operator workflow (sidebar, dashboard, account table, edit dialog) is inspired
by MHSanaei/3x-ui. This is original Go/HTML/CSS/JS; no GPL 3x-ui source/assets are
incorporated. System fonts avoid third-party font redistribution.

## Data path

One physical authoritative UDP socket dispatches unchanged DNS queries by account
zone to separate `soar.Serve` instances. Each account has a random label and an
independent Ed25519 key. Its label is immediately before the delegated base zone;
encoded transport labels remain unchanged. Account zones fit the existing
WhiteDNS 45-character limit. The base zone is limited to 24 ASCII characters,
leaving at least 100 bits of unpredictable label. No anonymous root-zone tunnel
exists. Root SOA answers support authoritative checks. Unknown accounts get REFUSED.

Soar sees resolver addresses, not reliable device IPs. An account is a shared
configuration identity, **not cryptographic device authentication**. A DNS observer
who learns the zone can attempt to consume that account's budget. Server keys
authenticate the server to the client; they are not user password credentials.
Do not advertise anti-sharing, distinct device counts, or subscriber identity.

Custom `Dial` enforces ports and upstream `soar.PublicOnly` on the actual resolved
destination. Keep this guard: supplying custom Dial bypasses upstream defaults.
Private destinations are overridden only by unexported integration-test wiring;
there is no production option to bypass the guard.
The systemd unit permits AF_NETLINK because Go's interface-address enumeration
needs it for that guard. Denying it makes PublicOnly fail closed for all targets;
an authenticated DNS handshake alone therefore is not a deployment health check.

## Durable accounting

bbolt transactions serialize budget reservations across all directions/streams.
Upload is committed before upstream Write and unconsumed bytes are refunded.
Download is committed after upstream Read and before returning payload to Soar.
Each accounting operation is bounded to 32 KiB, with smaller chunks under rate
limits. All traffic shares upload+download budget and aggregate rate scheduler.
This measures forwarded TCP payload, not DNS wire traffic, retransmission, or
datacenter billing. There is no unsynced periodic counter whose loss enables free
traffic. A crash can charge bounded unfinished payload conservatively. Reset
changes usage epoch; old reservations/refunds cannot charge or erase a new period.
Metadata revisions independently protect admin edits from lost updates.

Disabled/expired/exhausted clients are checked at dial and accounting operations;
reconciliation closes active runtimes immediately after admin changes and within
one second of time-based expiry. Idle sessions do not reserve quota. Rotating
zone/key closes old sessions and preserves accounting. Disconnect closes streams
but permits an enabled account to reconnect. Reset closes old sessions and starts
a new usage epoch. The rate limit is aggregate bytes/sec, not a per-device cap.

Initial limits: 512 persisted accounts, 8 sessions/account, at most 128 outbound
streams/account, and 2048 global pending+active outbound streams. These are hard
ceilings, not measured capacity promises. DNS queues are bounded and shed overload.
Per-account engine reflection limits remain enabled; a production capacity/security
review must consider aggregate DNS/handshake pressure across accounts.

## Control plane

Embedded assets require no CDN, frontend build service, external QR API or online
font. Persian RTL interface has mobile and keyboard handling, native dialogs,
explicit destructive-operation confirmation, real metrics, empty/error states,
dark/light theme. Unknown host metrics show a dash, not invented zero values.
All account names and notes are inserted as text, never HTML. CSV formula cells
are escaped. Client files use the existing `whitedns.profile` v1 schema and `soar://`.

Global `soarSettings` persist the 16 supported profile transport controls. A
missing object in a legacy database reads as the adaptive defaults;
legacy settings writes preserve saved tuning. Invalid ranges or crossed name
lengths fail before committing. The API does not accept cipher/MTU switches the
engine cannot apply. Exported links embed a snapshot: changes require reimport,
without rotating identity or changing server-side quotas. Every settings field
has a text explanation available on hover, keyboard focus and a touch button.

The CLI defaults to loopback. Public binds require TLS or an explicit
`--allow-insecure-http` choice for a test server. The wizard offers direct IP/port
HTTP and private SSH/HTTPS-proxy modes, shows the resulting URL, and preserves
the prior access mode as the rerun default. Fresh installations suggest direct
HTTP with a clear plaintext notice. A local HTTPS proxy can be explicitly trusted
only from loopback. Passwords are bcrypt cost 12. Sessions
are memory-only, expire after 12 hours, and are invalidated by restart. Cookies are
HttpOnly/SameSite Strict and Secure over HTTPS. State-changing API requests require
a session CSRF token and reject foreign Origin. Login attempts are throttled;
database/backup contain private seeds and password hashes and must remain 0600.
Sessions/CSRF/key material are not logged. Audit keeps the latest 2000 admin events,
API returns the latest 200. Browsing destinations/content are not recorded.

## Rollout

Stable standalone setup remains separate. Panel 0.7.0 sources support IPv4 on amd64 and arm64; the public bootstrap
tracks the latest verified published release. Architecture-specific bundles and
updates are tested on native amd64 and arm64 CI runners. A running standalone instance is stopped only after
typing MIGRATE, preserving its keys/config and offering rollback. Migrating root
shared profiles to individual accounts requires distributing new profile links,
with the existing profile format. The browser transport fixes require WhiteDNS
1.6.7 or newer; a server upgrade cannot replace an installed native core. Never deploy over an
existing delegated MasterDNS zone. Test on isolated ports before a production switch.

Automated checks cover authenticated HTTPS through unmodified upstream clients,
account isolation, concurrent exact quotas, restart/backup persistence, rate and
stream limits, disable/rotate, private destination protection, old-epoch isolation,
authentication/CSRF, QR generation and responsive browser workflows. Passing these
checks does not establish reachability on Iranian access networks; field testing
on those networks remains required.


## Subscriptions and interface language (0.3.0)

Two optional Bolt buckets bind independent 32-byte subscription tokens to client
IDs and index SHA-256(token) to client IDs. Account deletion removes both entries.
Read-only public GET/HEAD `/sub/<token>` routes precede the private admin prefix
without revealing it. HTTPS configuration enforces TLS and the configured host.
Responses use a single transaction snapshot of account data and current settings;
no cached profile can pin a stale tunnel key. Disabled/expired/exhausted accounts
receive 403 without profile data. Rotation is an authenticated, CSRF-checked POST
and never changes tunnel keys or accounting epochs. New backups validate both
subscription tables; legacy backups migrate lazily. See SUBSCRIPTIONS.md.

The self-hosted `i18n.js` catalog translates authored static DOM nodes once and
explicit dynamic UI strings at their creation sites. No dynamic DOM rewriting
or third-party translation service touches account names, notes, keys or input
values. The browser-local language choice selects `fa-IR`/`en-GB` formatting,
RTL/LTR layout and contextual help direction. Switching reloads the current page;
authentication remains under the existing session/cookie rules.
