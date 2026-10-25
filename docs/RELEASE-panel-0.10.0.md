# WhiteDNS Server Management 0.10.0

- Enable CottenMax alongside Normal mode for managed Cotton accounts, using each account's existing domain and shared key on the same UDP/TCP DNS listener.
- Preserve account policy for Max TCP streams and tunneled DNS: quota, rate, stream admission, revocation, settings restarts and outbound anti-flood protection use the panel's existing hooks. Normal and Max share the account's Cotton session budget; diagnostics include both modes.
- Update the reproducibly pinned Cotton source to `7db7b73658c791cce62300d29647e4d369d6e1e4`, with a checksummed embedding patch, current resolver-pool fixes and optional Max download FEC. FEC remains a client choice.
- Keep the documentation/source link only in the sidebar. Remove the repeated content footer from every tab.
- Add an accessible fold control at the sidebar edge. The expanded/compact preference survives navigation and reload; mobile retains its drawer.

## Upgrade

Install the server update and refresh the panel page. Existing accounts, domains, keys and subscription addresses remain valid. For automatic Max handshake sizing with long panel account domains, update to Android 1.6.14 or Desktop 1.4.5. Older Max clients can require a larger initial QNAME setting; the server upgrade alone cannot change an installed client's configuration.

Validation includes native client Normal/Max UDP/TCP HTTPS, primary/backup domains, tunneled DNS, shared usage and quota disconnection, combined session admission, and browser checks in Persian/English. Linux amd64/arm64 CI verifies complete tests, race checks and release bundles before publication. No physical-device or restricted-network field acceptance is claimed.
