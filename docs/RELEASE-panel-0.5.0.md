# WhiteDNS Panel 0.5.0

- Soar and Cotten are available in the same panel and subscription, with shared account quota, expiry, speed and connection limits. Existing Soar accounts and links are preserved.
- Add up to eight independent tunnel domains and choose a preferred domain without recreating accounts. DNS checks prove the response came from this server. Each domain and engine receives a stable subscription profile.
- Add up to eight HTTPS panel addresses, prepare certificates before applying changes, and retain the old address when replacing the primary domain. Alternative subscription URLs are shown alongside the main URL.
- The installer keeps the same questions and automatically checks both engines. Fixed UFW access under the existing systemd sandbox and required the maintenance service to respond before reporting installation success.
- Fixed custom resolver ports, resolver failover, concurrent resolver snapshots and the initial Cotten DNS reply. Both UDP and TCP DNS listeners are supported.

Install or upgrade using the existing command:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/WhiteDNS/Soar-Server/main/panel-setup.sh)
```

Keep UDP/53 and TCP/53 open in the VPS provider firewall. After adding backup domains or enabling Cotten on an existing account, refresh the subscription in WhiteDNS. Save alternative panel and subscription addresses before the current domain becomes inaccessible. This server release does not include a rebuilt Android APK or promise automatic client failover.

[Step-by-step Persian installation](PANEL.fa.md) · [Backup domain setup](DOMAINS.fa.md) · [VPS validation and limits](PANEL-0.5.0-VALIDATION.md)
