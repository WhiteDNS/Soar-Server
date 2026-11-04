# WhiteDNS Server Management 0.14.4

The embedded CottenDNS engine is updated to `5e2a9045e0fb`. It keeps DNS request
policy separate in its cache and restores the full upstream setup budget for
generic UDP connections.

- DNS requests with different CD/DO flags, EDNS response sizes or options use
  independent cache and in-flight identities. Matching requests still share
  work and keep each caller's transaction ID and question spelling.
- Normal and CottenMax clients correlate replies with bounded, randomized DNS
  transaction IDs. Active IDs are never reassigned, and retired IDs stay
  unavailable within the current issuance cycle.
- A slow SOCKS5 UDP proxy setup can use the existing 15-second embedded dial
  budget. The earlier DNS resolver's five-second wrapper could reject a valid
  connection before that budget elapsed.
- Idle UDP destination cleanup continues independently of cache misses. Active
  incoming traffic and aliases keep using the same canonical socket; expired
  destinations can reopen when traffic resumes.

Update CottenDNS clients as well as Panel to receive the client cache and reply
matching fixes. The engine integration preserves Panel account admission,
shared quotas, cancellation and Normal/Max session accounting. The existing
tunnel packet format is retained.

The release workflow builds the pinned, checksum-verified engine workspace,
tests native clients and proxy routes, runs race and installer checks, and
publishes the same verified AMD64 and ARM64 bundles. Local connection and idle
boundary checks do not replace a live multi-day server soak.
