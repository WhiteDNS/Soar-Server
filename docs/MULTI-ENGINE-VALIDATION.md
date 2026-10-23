# Multi-engine validation — 2026-10-23

Environment: macOS arm64, Go 1.26.0, installed Google Chrome with
Playwright 1.58.2. The original workspace generator requests Go 1.26.1;
the local test workspace used the installed 1.26.0 toolchain because the
toolchain download returned HTTP 403. Published engine/module versions
were retained, and `go mod verify` passes.

## Passed

- Engine selection and compatibility: omitted/Soar-only accounts, legacy
  `both`, all four, individual new engines and explicit combinations.
- Separate AES-256-GCM profile keys, alternate domain exports and account
  key rotation.
- Real StormDNS client TCP transfer through TXT, NS, CNAME and SRV DNS
  queries; real MasterDnsVPN client TCP transfer through its server.
- StormDNS client compatibility with the MasterDnsVPN server, matching
  the WhiteDNS client's accepted StormDNS/MasterDNS profile aliases.
- Shared Soar/new-engine stream admission, payload usage accounting,
  disabled-engine refusal and immediate key revocation.
- Focused race-detector run of all new-engine tests: 38.456 seconds.
- Full ordinary Go suite with the two macOS-incompatible Cotten tests
  excluded: panel passed in 84.192 seconds; other packages passed.
- Full browser suite: existing account/admin/settings/backup/domain
  workflows, four-profile subscriptions, per-engine sharing and QR codes,
  Persian/English, keyboard controls, and 320–1440 px layouts.
- `go vet ./...`, module verification, release-version tests, bootstrap
  checks and bundle checksums.
- Linux amd64 cross-compilation of all three release binaries, static
  panel executable, complete offline archive and original license notices.

## Host limitations and remaining deployment validation

The complete ordinary and race suites both fail only in the existing
`TestCottenUDPAccountingPreservesLargeDatagrams` and
`TestCottenNativeClientAndSharedBudget` tests. Their oversized UDP writes
return `message too long` on this macOS host. The unchanged upstream
checkout reproduces the datagram-accounting failure. The full race run
completed in 698.433 seconds with no race reports; its failures were the
same UDP writes. These tests remain enabled in Linux CI.

The installer shell test also encounters macOS Bash 3.2's lack of
`declare -g`; the supported Ubuntu/Debian installer needs modern Bash.
Its non-installing `--check-bundle` mode passes locally.

No production VPS was provided, so systemd activation, public DNS
delegation, physical Android connectivity, and performance under lossy
Iranian networks have not been tested in this session. The generated
Linux bundle is a custom build, not an upstream GitHub release.
