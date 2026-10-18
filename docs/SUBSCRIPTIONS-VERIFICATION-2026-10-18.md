# Subscription and English interface verification — 18 October 2026

Panel 0.3.0 adds per-account subscriptions and Persian/English UI. Android 1.6.8
adds import, bounded HTTP/HTTPS fetch and refresh. Neither feature changes the
Soar wire protocol or its encryption algorithms.

## Automated checks

- All server Go tests and vet pass locally. Linux CI additionally passes race,
  installer/bootstrap, offline bundle and browser acceptance checks.
- Subscription coverage includes authenticated management, public delivery,
  quota/expiry headers, settings/key freshness, credential rotation independent
  of tunnel keys, inactive/deleted accounts, private login path separation,
  backup persistence, legacy backups and rejection of corrupt token indexes.
- Browser checks cover clipboard writes, QR/profile/subscription switching,
  rotation, account/settings/maintenance workflows, question-icon-only help,
  English/Persian persistence, preserved user-authored names, LTR menus/help and
  1440/768/414/375/320 layouts. The mobile drawer closes on an active page too.
- Android: all 136 unit cases pass, including six subscription parsing, network,
  reconciliation and persistence-format cases. Tests exercise malformed/oversize
  documents, duplicate identities, HTTP consent, redirects, revoked/inactive
  responses, repeated refresh and local preference retention. Existing device
  profile-selection tests compile; a local debug build packages successfully.
- Linux Android CI rebuilds and verifies all native ABIs and passes the existing
  Soar transport/wire compatibility tests. Official signing uses the established
  repository secrets; no signing keys are exported to the workspace.

## Live test VPS

The panel was upgraded from 0.2.0 through its own updater. Comparisons confirmed
preserved account identities/keys, policies, quotas, expiry, tuning and HTTPS
private login address; usage counters did not regress. Both services remain
active with zero automatic restarts after activation.

Each active account's public HTTPS subscription matched its direct profile.
Metadata, no-store headers, a URL independent of the administrator path, QR
responses and the resulting backup's integrity were checked. The deployed
English login and HTTPS routing were also checked in Chrome.

The compiled Android subscription model fetched a real HTTPS subscription twice,
validated its profile and metadata, and refreshed without duplicate profiles,
duplicate resolver groups or selection changes. Temporary credential inputs
were removed after this check. Administrator credentials remained on the VPS.

## Scope

These results establish server delivery, model-level client interoperability and
panel browser behavior. A physical Android Full VPN session using the new
subscription workflow and a test from an Iranian access network were not run in
this change. Background refresh while Android has closed the app is not promised.
The tunnel cores are unchanged from 1.6.7; this update does not establish universal
network reachability or resolve all previously documented path-specific issues.

## Evidence

- [Panel CI](https://github.com/WhiteDNS/Soar-Server/actions/runs/39125924711)
- [Panel pull request](https://github.com/WhiteDNS/Soar-Server/pull/2)
- [Android source CI](https://github.com/WhiteDNS/WhiteDNS-Android/actions/runs/39126003821)
- [Android official release build](https://github.com/WhiteDNS/WhiteDNS-Android/actions/runs/39126431557)
- [Android pull request](https://github.com/WhiteDNS/WhiteDNS-Android/pull/81)

Panel archive SHA-256:
`618c154297f3d57453bec9876b54814fbb85caabb7177bdadfa3d79a0bfd8c60`.


The official 1.6.8 build completed successfully and published all four ABI APKs
plus a universal APK. The downloaded universal APK matches the release checksum,
uses application ID `shop.whitedns.client`, version code 30, and the same signing
certificate as 1.6.7. This supports an in-place update without uninstalling.

Universal APK SHA-256:
`a729c73d305e24199e799f1cd5214090899e441f42c6490455f313a516043838`.

Signing certificate SHA-256:
`60b38f8049880e422f99733978c5cbad2134dfbf0029684e3bf5828084527628`.
