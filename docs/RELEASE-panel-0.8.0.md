# WhiteDNS Panel 0.8.0

- Add MasterDnsVPN alongside Soar and Cotten with isolated per-account domains and keys. Select one engine, any pair, or all three in one subscription.
- Apply the existing shared quota, speed, expiry, TCP stream limits, destination policy and account revocation to MasterDNS traffic.
- Add MasterDNS profile links and QR codes, a separate 1–255 tunnel-session limit, runtime diagnostics and Telegram management and recovery alerts.
- Preserve existing accounts: legacy `both` still means Soar + Cotten. Enable MasterDNS explicitly; account identities, existing tunnel keys and subscription URLs stay valid.
- Restart active account engines after a successful settings save while preserving keys, subscriptions and persisted usage. Failed saves leave engines running.
- Verify native MasterDNS HTTPS during guided installation, using private short-lived credentials and the same rollback path as other tunnel checks.
- Pin the original MasterDnsVPN source and the complete embedding patch with checksums. Include original MIT attribution and Linux amd64/arm64 bundles.

MasterDNS uses its original TCP-over-UDP-DNS protocol; this release does not add arbitrary UDP/QUIC support to that engine. WhiteDNS Android and Desktop already recognize `masterdns://` profile links. Protocol support is verified separately from real-device and restricted-network field acceptance.

Back up before upgrading. Refresh the subscription after enabling MasterDNS. No additional DNS delegation is needed under the existing tunnel zone. Full source, integration and verification details are in [MASTERDNS.md](MASTERDNS.md).
