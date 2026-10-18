# Account subscriptions

Panel 0.3.0 and WhiteDNS Android 1.6.8 add HTTP/HTTPS subscriptions. A subscription
retrieves updated profiles; it is not a new tunnel protocol. Existing direct
`soar://` links and older clients continue to work.

## Administrator

1. Open **Accounts → Link / QR → Subscription URL**.
2. Copy the URL or show its QR code to the account owner.
3. Prefer HTTPS, configured under **Domain & security**. Subscription URLs start
   at `/sub/` and never contain your private administrator login path.
4. Save changes to tunnel settings or rotate an account's tunnel key normally.
   The same subscription URL returns the current profile on the next refresh.
5. Use **Rotate subscription URL** to revoke only the old download address.
   Previously downloaded tunnel keys still work until you rotate those keys,
   disable/delete the account, or its quota/expiry blocks it.

Treat every subscription URL as a password. Anyone holding it can retrieve that
account's current profile. Subscriptions are individual, not a list of all panel
accounts. Changing the panel hostname requires giving users the new URL.

## WhiteDNS user

1. Install the official WhiteDNS 1.6.8 or later APK over the existing app.
2. Disconnect, open **Profiles → Connection → Import**, paste the subscription
   URL, and select **Import**. QR scanning also fills the import dialog; review
   and confirm before downloading.
3. Select the imported profile and connect in the usual Proxy or Full VPN mode.
4. **Refresh subscriptions** downloads the latest profiles while disconnected.
   Due subscriptions are also refreshed when the app opens/resumes and no tunnel
   is running. The panel recommends a 12-hour interval. This does not promise
   background refresh while Android has closed the app.
5. The subscription summary shows the last retrieved upload/download quota,
   expiry and refresh time. These are a snapshot, not a live traffic meter.

Refresh keeps stable profile identities, the selected profile, local connection
mode and manually selected resolver profiles. Provider-owned settings and DNS
lists update. A malformed or failed download leaves that subscription's saved
profiles untouched; other sources can still refresh. Missing entries in a valid
response are removed from that source. Deleting all of a source's profiles stops
its automatic refresh. A revoked source must be replaced with the new URL.

HTTPS uses the normal Android certificate checks; HTTPS-to-HTTP redirects are
blocked. An HTTP source requires a visible, explicit per-source opt-in because
its URL and profile data are unencrypted. The opt-in is retained for refresh.
No subscription URL is included in diagnostic logs or direct profile exports.
The fetcher accepts at most 1 MiB, 128 profiles and three redirects.

## Interoperability and backups

The default response is standard base64 containing newline-separated supported
profile URIs. `?format=plain` returns those lines directly. Response headers:

| Header | Meaning |
| --- | --- |
| `Subscription-Userinfo` | `upload`, `download`, `total` bytes; `expire` Unix seconds (zero means unlimited/no expiry) |
| `Profile-Title` | Base64-encoded account name |
| `Profile-Update-Interval` | Suggested refresh interval in hours |

Format compatibility does not make other VPN apps understand the Soar engine.
WhiteDNS accepts its supported Soar/MasterDNS/StormDNS/CottenDNS profile URI
formats, not arbitrary Xray/V2Ray configurations.

Inactive accounts return HTTP 403 without a profile; invalid, deleted or revoked
subscription tokens return 404. Successful requests and errors use `no-store`.
GET/HEAD requests are supported; rotation requires an authenticated, CSRF-checked
administrator request. Account keys and subscription credentials are independent.

Panel backups include subscription credentials and their account-bound index.
Restoring one on 0.3.0 or later preserves its URLs. Older 0.2.0 backups can be
restored, with subscriptions generated as needed. **A 0.3.0 backup cannot be
restored into 0.2.0**; retain your pre-upgrade backup for a downgrade. Restoring
an older backup also restores its old token state, so revoked URLs in that
backup can become valid again. Rotate them after such a restore if necessary.
