# Engine upstream proxies and outbound reports

Available in **panel 0.14.0 and later**; these controls are absent from 0.13.0.
[Main panel guide](PANEL.en.md) · [فارسی](EGRESS.fa.md) · [Download 0.14.0](https://github.com/WhiteDNS/Soar-Server/releases/tag/panel-v0.14.0)

## 1. Preparation and upgrade

1. Download a backup from **Backup & restore** and keep it private.
2. On an older panel, check for and install the stable update from Panel updates, then reload the browser page. For a new installation, follow the [main guide](PANEL.en.md).
3. Prepare the proxy separately. The panel uses an existing SOCKS5 or HTTP CONNECT inbound; this form does not accept subscription URLs, VLESS/VMess links or Xray configuration files.
4. For a proxy on this server, find the actual loopback address and listening port in that service's configuration. `127.0.0.1:1080` is only an example. A loopback inbound does not need a public firewall opening.
5. Each engine's route applies to all accounts using that engine. This is not a per-account proxy setting.

| Task | English navigation | Persian navigation |
| --- | --- | --- |
| Proxy routes, proxy ports and logging settings | Server settings → Upstream proxies & traffic reports | تنظیمات سرور ← پراکسی خروجی و گزارش ترافیک |
| View, filter and download reports | Logs & assistant → Outbound traffic report | لاگ‌ها و دستیار ← گزارش ترافیک خروجی |

## 2. Configure a local proxy, step by step

Assume Xray or sing-box already has a **SOCKS5 inbound at `127.0.0.1:1080`**, with its own outbound routing configured:

1. Open **Server settings → Upstream proxies & traffic reports**.
2. In the desired engine's box, for example **Soar**, choose **SOCKS5** as the route.
3. Enter `127.0.0.1:1080` as **Proxy address**. Do not add `socks5://`, a URL path or credentials to that address.
4. If the inbound requires authentication, enter **both** username and password in their separate fields. Otherwise leave both empty.
5. Start with `80,443` in **Allowed proxy destination ports**. See the port examples below for other services.
6. Configure **Cotten and MasterDNS** independently. To send all three through one proxy, enter its route and address in all three boxes. Changing Soar does not change the others. CottenMax uses the Cotten setting.
7. If you need destination reports, select **Enable outbound traffic logging** and set retention and storage.
8. Click **Save outbound routes** and wait for success. Existing connections on active engines close and the engines restart with the new settings; expect a short interruption.
9. In WhiteDNS, connect a test account through each engine separately and open an HTTPS site. Check the public IP from inside that connection and compare it with the proxy's expected exit. Browsing from the server itself does not prove the tunnel user's route.

Changing an upstream does not require recreating accounts or reimporting their subscriptions. Disconnect and reconnect the client to test the new route. If you also enabled another engine on an account, refresh that account's subscription.

Example independent routes:

| Engine | Route | Example address | Allowed destination ports |
| --- | --- | --- | --- |
| Soar | SOCKS5 | `127.0.0.1:1080` | `80,443` |
| Cotten / CottenMax | HTTP CONNECT | `127.0.0.1:8080` | `80,443,8443` |
| MasterDNS | Direct | None | Main direct-port list in Server settings |

These ports do not imply a service exists there. Select the protocol actually offered by the inbound; an HTTP proxy is not SOCKS5 merely because its port matches an example.

## 3. Address, authentication and saving

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

An address is `HOST:PORT`, for example `proxy.example.com:443`, or `[::1]:1080` for IPv6. Replace the example hostname with your actual proxy. For HTTPS CONNECT, the certificate must be valid for the entered hostname or IP. This option does not enable panel HTTPS or issue a proxy certificate.

An empty password after saving does not mean it was deleted. To retain it, leave protocol, address and username unchanged. Reenter the password after changing any of those. Select **Remove saved authentication** to clear it. To replace a password, enter the new one without selecting removal. If settings conflict or the panel asks you to reload them, reload, review the current route and apply the change again.

## 4. Port policy, destinations and DNS

Proxy errors never fall back to direct access. SOCKS5 supports TCP and UDP
ASSOCIATE; HTTP(S) CONNECT rejects UDP. Proxy port policies accept lists/ranges,
such as `80,443,1000-2000`, or an explicit unrestricted option. Direct traffic
continues using the existing allowed-port list. Quotas, stream limits and
Anti-Flood still apply, with destination limits attached to the final public IP.
Private/loopback/link-local destinations remain blocked. Only the configured
proxy endpoint gets the administrator-authorized local-address exception.

| Setting | Effect |
| --- | --- |
| `80,443` | Only those two destination ports through this proxy route |
| `1-2000` | Every destination port from 1 through 2000, inclusive |
| `80,443,1000-2000,8443` | Individual ports combined with a range |
| Unrestricted ports on the proxy route | Removes this route's destination-port filter; other controls remain active |

**The proxy's listening port is different from the destination port.** In `127.0.0.1:1080`, 1080 is the proxy inbound. A site on 443 needs 443 in the destination list. `1-2000` does not include 8443. Valid ports are 1–65535; reversed ranges such as `2000-1` are rejected. Direct mode disables these proxy-specific inputs and uses the main server port list instead.

Opening ports does not add transport support. HTTP CONNECT still rejects UDP. SOCKS5 does not add arbitrary UDP forwarding to an engine/client that lacks it; MasterDNS still carries TCP over DNS.

Destination name resolution uses DNS-over-TCP through the same engine's proxy.
Cotten/MasterDNS tunneled DNS also follows the engine route; HTTP uses TCP.
Configured resolver endpoints and existing public fallbacks are retained. The
proxy must permit resolver connections. Resolving a **proxy hostname itself**
uses server bootstrap DNS; use a literal proxy IP to avoid that lookup.
Checked destination IPs are pinned and sent to the proxy. Downstream
domain-based routing must account for receiving IP destinations. The panel
does not install Xray, configure WARP/Psiphon or prove their final outbound path.

Internal resolver connections have an exception only for the panel's allowed resolver endpoints. You do not need to open destination port 53 to every client destination merely to permit internal DNS. Check access to the resolver in the proxy service and the network path.

## 5. Enable logging and obtain a report

1. In **Server settings → Upstream proxies & traffic reports**, select **Enable outbound traffic logging**.
2. Initial defaults are **24 hours** and **32 MiB**. Adjust for your traffic and click **Save outbound routes**. This save also restarts active engines.
3. Reconnect the test client after logging is enabled. Events from before enablement cannot be reconstructed.
4. Open **Logs & assistant → Outbound traffic report**.
5. Set **Start date and time** and **End date and time**, including hours, minutes and seconds where needed. End must not precede start.
6. Choose engine, route and status, or leave their all-values selections. **Account ID** is optional and requires an exact ID, not the account's display name. Copy an ID from a report row or an earlier export if needed.
7. Click **Show report**. After changing a filter, click it again before paging. The table shows up to 200 rows per page.
8. For newer results, advance the end time and show the report again. The initial one-hour range is a starting selection, not a continuously moving live window.
9. Click **Download CSV** or **Download JSON**. Downloads use the current form values; apply those same values with **Show report** first if you want the table and export to match.

### What is recorded?

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

### Time and volume: an example

For a 10:00–11:00 range, a connection that started at 09:59 and stayed open until 10:30 is excluded. One that started at 10:10 is included, with its **whole recorded connection payload**, even if it continued after 11:00. When investigating a provider event, start the range earlier to include connections already open at the event time.

Form and table times use the administrator device's timezone, which may differ from the server. CSV columns `started_utc`, `ended_utc` and `last_observed_utc` use UTC. JSON timestamps are Unix milliseconds. Establish the provider report's timezone before comparing times.

### Fields and states

| Field | Meaning |
| --- | --- |
| Engine / account | Originating engine and account ID; CottenMax appears under Cotten |
| Destination / IP | Requested host/IP and the checked final IP; an early failure may have no final IP yet |
| Protocol / port | TCP or UDP and destination port; purpose distinguishes internal DNS from application traffic |
| Route | `direct`, `socks5`, `http` or `https`, plus proxy endpoint without a password |
| Upload / download | Payload bytes in both directions; `≥` on a partial row means a recorded lower bound |
| `active` | Connection is open; a download is a snapshot at query time |
| `completed` | An established connection closed; this alone does not establish a successful HTTP response |
| `failed` | Connection establishment failed; inspect reason and attempts |
| `denied` | Policy, limits or Anti-Flood rejected the request |
| `interrupted` | A final record is missing; the end time and complete volume are unknown |

CSV also includes `connection_id`, `attempts`, drop/error counters, last observation and `bytes_known`. JSON is available for programmatic analysis. Downloads include all retained matches, independently of the current table page, up to **100,000 connections** per report. Narrow the time range or engine/account filter for large results. Exports contain destination and account information; redact sensitive fields before sharing them.

## 6. Retention, load and service interruptions

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

## 7. Troubleshooting and reverting a change

| Symptom or report reason | Check and action |
| --- | --- |
| Controls are missing | Upgrade to 0.14.0 or later, complete the update and reload the browser. |
| `proxy_unavailable` | Check that the proxy is running, protocol, endpoint, both authentication fields, network access and HTTPS certificate/name validity. Private authentication details are not exposed by this generic error. |
| `proxy_udp_unsupported` | HTTP/HTTPS CONNECT rejected UDP. If the engine and client support UDP, use a SOCKS5 service offering UDP ASSOCIATE. |
| Proxy is reachable but sites fail | Check destination ports, DNS-over-TCP through the proxy, that service's outbound rules, quotas and Anti-Flood. Reaching the proxy inbound alone is insufficient. |
| `destination_policy` | The destination port is not permitted by the selected route; distinguish direct and proxy port lists. |
| SOCKS5 UDP fails | The proxy must support UDP ASSOCIATE. A relay on a different host, or announced as a hostname, is not accepted. |
| Public exit IP did not change | Configure and reconnect the correct engine; inspect Xray/sing-box outbound rules and any direct exit in that service separately. |
| Report is empty | Logging must have been enabled before the connection. Check timezone, time range, engine/account filters and retention/capacity. Disabling logging does not immediately erase previously stored history. |
| Report looks stale or paging is disabled | Apply changed filters with Show report; advance end time for newer records. |
| Report too large or query takes too long | Narrow the range or choose an engine/account; queries have record and processing limits. |
| Queue-drop or storage-error warning | Treat the report as incomplete. Check disk space, service write access and load, and export before files rotate. |

To revert, enter the previous working route and ports in the same form and save. Choosing **Direct** intentionally permits direct access under the main server port list. If proxy routing is mandatory for you, restore a working proxy rather than using Direct as a temporary workaround. The panel never makes that fallback choice automatically.

For an abuse investigation, export CSV/JSON promptly and correlate timestamps/timezones, destination IP, port, protocol and provider evidence. A row's presence or absence alone does not prove a Botnet/C2 origin; account for logging state, missing history and other processes on the server.
