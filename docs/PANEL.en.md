# Install and use WhiteDNS Server Management

WhiteDNS builds the management panel and installer. The tunnel engine is
[Lantern Soar](https://github.com/getlantern/soar), alongside the compatible
[WhiteDNS CottenDNS fork](https://github.com/WhiteDNS/CottenDNS). The dashboard uses original
code with a workflow inspired by 3x-ui; Xray is not installed.

Supported servers: Ubuntu 22.04/24.04/26.04 or Debian 12/13, amd64/x86_64 or arm64/aarch64,
with root/sudo and systemd. No Docker, Go, Node or external database is required.

Panel 0.14.0 provides separate archives for each architecture. The installer rejects mismatched bundles before installation. On every run, the public bootstrap resolves GitHub's latest stable panel release and downloads the matching architecture archive and checksum. It stops before installation if discovery, download or verification fails; it never silently installs an older version.

For the 0.14.0 engine proxies, proxy port policies and destination reports, follow
the [complete upstream and outbound reporting guide](EGRESS.en.md) ([فارسی](EGRESS.fa.md)).

## 1. Prepare a server and DNS

Use a server outside a restricted network with a public IP and outbound web
access. You need a domain and access to its DNS records. The following are
examples; replace the domain and IP with your own:

| Type | Name | Value |
| --- | --- | --- |
| A | `ns-soar.example.com` | Your server's public IPv4 address |
| NS | `soar.example.com` | `ns-soar.example.com` |
| A | `panel.example.com` | Your server's public IPv4 address |

Disable DNS proxying for the A records. Choose a new delegated subdomain if
MasterDNS or another service already uses one: do not add a second unrelated
nameserver to its delegation. No separate DNS record is needed per account.

Allow UDP/53 and TCP/53 for the DNS tunnel, TCP/80 for certificate validation and TCP/443
for HTTPS. Keep your SSH port accessible. The installer checks conflicts and
DNS delegation; if another service owns port 53, resolve that conflict first.

## 2. Run the guided installer

Connect over SSH and paste:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/WhiteDNS/Soar-Server/main/panel-setup.sh)
```

If curl is missing:

```bash
sudo apt-get update
sudo apt-get install -y curl
```

Follow the numbered English prompts. Enter your delegated zone, for example
`soar.example.com`. Pick an administrator username and strong password. The
installer discovers the latest stable release, downloads the matching architecture archive, verifies its checksum, installs
the services and prints the complete login URL. Save that URL: its random path
is required. The bare IP/domain intentionally does not show a login page.

The 0.5.0 wizard offers to install missing DNS/network tools. The local listener
address is detected from the default route; behind NAT, enter the provider's
public IPv4 separately. DNS names accept uppercase or a trailing dot and are
normalized. Incorrect input can be corrected without restarting installation.

Before service changes, all discovered parent authorities must confirm the A/NS
records and no conflicting delegation or child DS. Authoritative checks use the
selected resolvers to find server addresses, retry UDP and fall back to TCP.
An unreachable authority reports its name and failed query and offers retry or
resolver changes. Child SOA is deliberately checked only after startup.

After startup, a temporary account with a 1 MiB quota and ten-minute expiry checks
authenticated HTTPS through the local listener and recursive DNS. It uses no
customer account or usage, is renewed during long retries, and is removed before
completion. Expired installer accounts are cleaned after an interrupted install.
Both local and recursive checks must pass for a verified installation. These
server-side tests do not establish reachability from the phone's network.

If checks fail, fix the displayed problem and retry, change test resolvers, roll
back, or explicitly type KEEP to retain an **unverified** installation. KEEP
finishes with a nonzero exit status; it does not certify working connectivity.
Active UFW receives UDP/53 and TCP/53 and, for direct access, the management TCP rule.
Existing broad allow rules and their comments are preserved. Automatic rollback
restores previous program/data/access files and service enable states and removes
only firewall rules added by this installation. Provider firewall/NAT changes
still need provider-side setup.

Rerun the installer to apply these changes on an existing server; the in-panel
binary updater does not run the installation wizard. Existing account keys,
usage, credentials and saved tuning are retained.

## 3. Enable HTTPS

Sign in, then open **Domain & security**, or run this in SSH:

```bash
sudo soar-panel-security
```

Enter `panel.example.com`. Ensure its A record points to this server, DNS
proxying is disabled and TCP/80 and TCP/443 are open. Certificate issuance can
take a few minutes. The old address stays active during preparation. Save the
new URL, confirm it, then apply. Sign in at the new HTTPS URL with the same
password. Certificates renew automatically. The random login path supplements
HTTPS and a strong password; it does not replace them.

Switch between **English** and **فارسی** using the language button on the login
page or toolbar. The selection is saved in that browser; English uses a left
sidebar and Gregorian dates, Persian uses a right sidebar and Persian dates.

## 4. Create and connect an account

Open **Accounts → New account**. Set a name, data quota in GiB, expiry, optional
shared speed in KiB/s and concurrent TCP stream limit. Zero quota/speed means
unlimited; an empty expiry means no expiry. Streams are connections, not device
counts. All devices sharing an account also share its quota and limits.

Open **Link / QR**. A **Direct profile** can be imported into existing WhiteDNS
clients. A **Subscription URL** requires
[WhiteDNS 1.6.8 or later](https://github.com/WhiteDNS/WhiteDNS-Android/releases/latest)
and retrieves future configuration changes without importing another profile.
Read the [subscription guide](SUBSCRIPTIONS.md) for refresh and revocation.

Install the official APK, open **Profiles → Connection → Import**, paste the
link and confirm. Select that profile, choose Proxy or Full VPN and connect.
For Full VPN, approve Android's VPN permission. Working DNS paths depend on
the user's network; no configuration guarantees connectivity in every country.

## 5. Settings, backup, restore and updates

Start with **Automatic (recommended)** under Server settings. Hover over or tap
the **?** beside a field for its explanation. Save, then refresh the account
subscription or import its direct profile again. Server quota/expiry limits
are enforced immediately; exported phone tuning updates on refresh/import.

Use **Backup & restore → Download backup** before upgrades. It contains private
account keys, subscription credentials, usage and tuning: store it securely.
To restore, select the compatible `.db` file, check it, review its summary,
enter your current administrator password and confirm replacement. Current
login credentials and machine-local HTTPS/access settings are preserved.
The file must use the same delegated tunnel zone. Restore restarts the service.

**Panel updates → Check for updates → Install update** installs a newer stable
official release after password confirmation. Packages are checksum-checked,
and program/data are backed up before replacement. A failed activation rolls
back. Connections briefly drop during restart. Older installations without the
maintenance service should rerun the guided installer once.

## Troubleshooting

For the complete interactive SSH management menu, run `sudo whitedns` or
`sudo soar-panel menu`. It includes password recovery, domain/certificate setup,
ports, backups, updates, logs and uninstall. See [server menu](SERVER-MENU.en.md).

Use the full URL including its random path. To inspect service status:

```bash
sudo systemctl status soar-panel soar-panel-ops --no-pager
```

The security helper can recover/change the login address through SSH. For DNS
errors, check delegation and UDP/53 and TCP/53. For certificates, check A/AAAA records,
DNS proxying and ports 80/443. If the phone connects but browsing stalls, use
the latest app, refresh the subscription while disconnected, reconnect and
review the app diagnostics. A healthy server does not prove every DNS path
works on every network.

## Soar and Cotten accounts

Installation uses one delegated DNS zone and the same guided command. No second DNS record, encryption key or engine installer is required. Both engines run inside the panel service. The automatic checks verify real HTTPS through Soar and Cotten, including Cotten over UDP and TCP.

For a new account leave **Connection engine** on **Soar and Cotten (recommended)**, save it, then open **Link / QR → Subscription**. Import that subscription in the latest WhiteDNS app. Select either of its two profiles; if one does not work on the phone's network, try the other. Importing a subscription does not automatically switch engines. Both profiles share the account quota, expiry, speed and outbound connection limit.

Existing accounts remain Soar and keep their links. Edit an account to enable both engines, then refresh its subscription. Direct Soar and Cotten links and QR codes are also available. Revoking or deleting an account closes both engines. Full backups already include the keys needed to restore both.

The local installer and maintenance helper prepare active UFW rules automatically. The VPS provider firewall/NAT must permit both UDP/53 and TCP/53. DoT/DoH are not exposed by this panel release.

Local 0.5.0 supports up to eight independent tunnel domains and eight HTTPS panel hostnames. Domain changes preserve account keys, quotas and subscription credentials. The initial installation still requires only one tunnel domain. See [the domain guide (Persian)](DOMAINS.fa.md) for DNS records, alternative subscription URLs and SSH recovery. Server-side DNS checks do not establish reachability on a filtered client network.


## MasterDNS accounts

Panel 0.8.0 adds MasterDnsVPN. Select MasterDNS, a pair including it, or All three engines in the account editor. Refresh the existing subscription in WhiteDNS; direct MasterDNS links and QR codes are also available. The account uses the existing DNS delegation with an isolated MasterDNS domain and key.

Quota, rate, expiry and TCP stream budgets are shared across the selected engines. The separate MasterDNS session limit is 1–255, default 255; sessions are not device counts. Existing account selections stay unchanged. MasterDNS transports TCP over UDP DNS; arbitrary UDP/QUIC is not added. See [MasterDNS integration](MASTERDNS.md).

## Engine upstream proxies and port rules — 0.14.0+

Open **Server settings → Upstream proxies & traffic reports**. Each engine has
its own route. CottenMax uses Cotten's setting; setting Soar alone does not
route the other engines through that proxy.

For an existing local SOCKS5 inbound, choose **SOCKS5**, enter its actual address
such as `127.0.0.1:1080`, and enter both authentication fields if required.
This is an example, not a proxy installed by the panel. Start with destination
ports `80,443`; lists such as `80,443,1000-2000` and **Unrestricted ports on the
proxy route** are also available. These exceptions do not open direct ports.
HTTP/HTTPS CONNECT supports TCP only. A failed proxy never falls back to direct.

Click **Save outbound routes**. Active engines restart and their existing
connections close briefly; accounts, keys, subscriptions and usage are preserved.
Reconnect a client through each configured engine and check its actual public
exit IP. Xray/sing-box outbound routing is configured in that service separately.

## Destination reports and downloads — 0.14.0+

Logging is **off by default**. In the same settings card, select **Enable outbound
traffic logging**, choose retention (initially 24 hours) and storage (initially
32 MiB), then save. Reproduce the traffic after logging has been enabled.

Open **Logs & assistant → Outbound traffic report**, enter exact start/end dates
and times, choose engine/account/route/status filters and click **Show report**.
Use **Download CSV** or **Download JSON** for all retained matches, including
rows outside the displayed page. Apply the current form filters with Show report
first if you want the table and download to match.

Dates use your device timezone; CSV uses UTC. Filtering uses **connection start
time** and volumes are the **whole connection payload**, not just bytes inside
the chosen interval. Reports do not continuously refresh. Advance the end time
and show the report again for newer results. History cannot be reconstructed
before logging was enabled or after eviction. No traffic content or proxy
password is logged.

The [full English guide](EGRESS.en.md) covers protocol/address/password examples,
DNS routing, port ranges, all report fields/states, retention and partial records,
backup/restore, troubleshooting and reverting settings. [راهنمای کامل فارسی](EGRESS.fa.md).
