# Engine upstream proxies and outbound reports

Prepared for panel 0.14.0; unavailable in the published 0.13.0 binaries.

Use **Server settings → Upstream proxies & traffic reports**. Soar, Cotten
(including CottenMax) and MasterDNS have independent routes. Valid saves briefly
restart active engines while preserving account identities, keys, subscriptions
and usage. Invalid saves leave engines running.

Choose direct, SOCKS5, HTTP CONNECT or HTTPS CONNECT. Enter `HOST:PORT`, such as
`127.0.0.1:1080` for an existing local Xray/sing-box inbound. Credentials have
separate fields. Saved passwords are never returned; blank preserves one only
when protocol, address and username are unchanged. Explicitly remove saved
authentication to clear it. HTTPS verifies certificates and hostnames. Ordinary
SOCKS5 and HTTP do not encrypt credentials.

SOCKS5 UDP relays must announce the proxy peer IP (or an unspecified address).
Alternate-host relays are rejected. HTTP proxy authentication uses Basic.

Proxy errors never fall back to direct access. SOCKS5 supports TCP and UDP
ASSOCIATE; HTTP(S) CONNECT rejects UDP. Proxy port policies accept lists/ranges,
such as `80,443,1000-2000`, or an explicit unrestricted option. Direct traffic
continues using the existing allowed-port list. Quotas, stream limits and
Anti-Flood still apply, with destination limits attached to the final public IP.
Private/loopback/link-local destinations remain blocked. Only the configured
proxy endpoint gets the administrator-authorized local-address exception.

Destination name resolution uses DNS-over-TCP through the same engine's proxy.
Cotten/MasterDNS tunneled DNS also follows the engine route; HTTP uses TCP.
Configured resolver endpoints and existing public fallbacks are retained. The
proxy must permit resolver connections. Resolving a **proxy hostname itself**
uses server bootstrap DNS; use a literal proxy IP to avoid that lookup.
Checked destination IPs are pinned and sent to the proxy. Downstream
domain-based routing must account for receiving IP destinations. The panel
does not install Xray, configure WARP/Psiphon or prove their final outbound path.

Destination logging is optional and off by default. Enable it before reproducing
a problem. **Logs & assistant → Outbound traffic report** provides engine,
account ID, requested host, checked IP/port, protocol, DNS/traffic purpose,
route, proxy endpoint without credentials, attempts, timestamps, payload byte
counters and result/reason. Payloads, DNS questions and credentials are not
logged. These records are not automatically sent to the assistant. Administrator
authentication is required for both viewing and downloads.

Select exact start/end dates and times in the administrator device timezone;
CSV uses UTC. Filtering is by **connection start time**. Bytes cover the whole
connection's payload, not just the selected interval or HTTP request counts.
They can differ from billed usage and wire traffic. CSV/JSON download every
retained match in one snapshot, independently of table pagination. Reports are
limited to 100,000 connections; narrow the range when necessary. The table
does not continuously poll the journal.

Private rotating JSONL segments live in `outbound/` beside the database, each
at most 1 MiB. Configure 1–720 hours and 4–256 MiB. Capacity can evict history
before the time limit; queries enforce the configured cutoff. Physical segment
deletion can have up to about one hour of rotation margin. Starts and ends are
persisted, with active counters checkpointed every ten seconds. After a crash,
an unfinished record is marked interrupted, its end remains unknown and
`bytes_known=false`: counters are the recorded lower bound, not proof of zero
traffic. Queue drops and storage errors are visible, including in CSV.

Traffic does not wait for disk I/O. Loss/error counters cover the current service
process. A damaged record is skipped with a visible storage-error count while
valid history remains available. Records lost under overload or disk failure
cannot be reconstructed. Database backups include private proxy credentials;
journal history is separate. Account restore preserves the current machine's
egress policy so older backups cannot silently restore direct access. Older
panel binaries do not enforce these new proxy settings after a downgrade.

These controls are not a C2 classifier or a suspension guarantee. Correlate
provider evidence (timestamp/timezone, destination, protocol, detection details)
with the retained report. Other server processes are outside this journal.
