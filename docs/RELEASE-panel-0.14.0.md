# WhiteDNS Server Management 0.14.0

Publication uses the single manually triggered build-and-release workflow.

- Independent direct/SOCKS5/HTTP CONNECT/HTTPS CONNECT routes for Soar, Cotten
  (including CottenMax) and MasterDNS. Proxy failures never fall back to direct.
- Separate proxy destination port lists/ranges or unrestricted proxy ports;
  direct access keeps its restrictive port policy. Existing account limits,
  quotas and Anti-Flood remain enforced against final checked destinations.
- Destination and tunneled DNS use the engine's proxy. SOCKS5 UDP association
  is supported; HTTP UDP is denied. A literal proxy IP avoids bootstrap DNS.
- Optional private outbound metadata journals with per-engine/account filters,
  exact custom start/end times, complete CSV/JSON downloads, bounded storage,
  explicit loss counters and interrupted-connection evidence.
- Saved proxy passwords are redacted, isolated from ordinary settings and AI
  context, and preserved safely across ordinary saves. Account restore preserves
  current machine-local egress policy.
- Green/black settings and report views, both languages/themes and mobile
  layouts. Heavy journals are queried on demand instead of background polling.

See [Persian](EGRESS.fa.md) and [English](EGRESS.en.md) instructions before
enabling destination logging or changing outbound routes. Reporting is based
on connection start time and whole-connection payload bytes. It is not packet
capture, a C2 detector, or a guarantee against provider suspension. Proxy and
real-network acceptance still need operator verification on their infrastructure.
