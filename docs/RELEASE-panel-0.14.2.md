# WhiteDNS Server Management 0.14.2

Publication uses the single manually triggered build-and-release workflow.

## Security

- In-panel updates now require a release signed with an offline Ed25519 key.
  Versions after 0.14.2 are not offered or installed without a valid signature,
  so a stolen GitHub account alone cannot push an update to panels on this
  version. Updating from 0.14.1 or older to this version does not check the
  signature, because those panels do not have this code yet.
- Restoring a backup no longer parses the uploaded database as root. Inspection
  and keeping the current administrator and egress settings now run as the panel
  user on a private copy.
- `AUDIT-REPORT.md` was removed from the repository.

## Limits

- The install bootstrap still checks SHA-256 only and does not verify the
  signature.
- If the signing key changes, operators must rerun the installer to keep
  receiving in-panel updates.

## Development

- Pull requests now run read-only tests that cannot publish a release.

Real-network acceptance on Linux with the WhiteDNS client still needs operator
verification.
