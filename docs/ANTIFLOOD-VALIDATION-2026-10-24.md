# Anti-flood validation — 2026-10-24

The implementation is based on current main `736ad1b04cf4c1dc39dfc258db86922a3919382e` (WhiteDNS Server Management 0.8.2). The rebase preserves product branding, Telegram management, MasterDNS, and engine restart behavior. Release metadata was subsequently prepared for 0.9.0; the local implementation checks below used the 0.8.2 base. No production server or published release was changed.

## Checks on the final three-engine implementation

- All 12 dedicated anti-flood test groups and their subtests passed. Cross-engine destination admission and quarantine disconnection cover Soar, Cotten, and MasterDNS.
- `go test -race ./internal/panel -run '^TestAntiFlood' -count=1 -timeout 240s` passed in 63.966 seconds.
- `go vet ./...` passed.
- The full `tests/panel-ui.cjs` browser suite passed using installed Chrome and an isolated localhost service. It covers anti-flood mode selection/confirmation, input validation, save/reload, legacy settings compatibility, preservation of unsaved edits during polling, Persian/English, and 1440/375/320 layouts. Existing account, subscription, settings, Telegram, backup, domain and administrator flows also passed.
- `tests/panel-limits-ui.cjs` passed branding/version synchronization, real modal dropdown interaction, MasterDNS profile/QR/subscription and stream/session controls. It then stopped at the existing Linux-only file-descriptor assertion; see platform limits below.
- All four `panel_release_test.py` tests passed with a workspace-local temporary directory.
- JavaScript syntax checks passed. Linux amd64 builds of `soar-panel`, `soar-panel-ops`, and `soar-check` passed. These implementation-check artifacts use the 0.8.2 base version. The release-metadata update to 0.9.0 is checked by the version consistency tests and the latest PR CI; no release has been published.
- Both engine sources were verified against their checked-in manifests: Cotten base `48b52018a0c261bccefaf20ad65f5f615ba823ea`, patch SHA-256 `5e1146fbf73c8e7b3e0dbdef6d91843448d2497db09b44e24081b385ac56c362`; MasterDNS base `acbf1c61f90786f41b975d2e2f616afbce292b29`, patch SHA-256 `24a4907e4a31ea7fc350f941dd0516026a6b156fe1b5f38f62a5b100bc025743`.

The dedicated tests cover atomic admission, pending connections, idempotent release, per-IP and per-prefix budgets, IPv4/IPv6 normalization, rolling windows, scan detection, diverse-target failures, escalating quarantine, durable quarantine/release, backup validation, observe/off modes, live upload policy changes, rejection before forwarding/billing, all-engine disconnection, resolver isolation, bounded table capacity/expiry, authentication/CSRF, pinned resolution, private destinations, cancellation and address fallback cleanup. These tests use local servers, in-process pipes or injected dialers; they do not send attack traffic to public destinations.

## Platform limits and CI follow-up

Validation ran on Windows with Go 1.26.1. The final `go test ./... -count=1 -timeout 240s` run, including the pinned native Cotten client, reported one failure in the unchanged `TestTelegramAPIAuthenticationSecretLifecycleAndBackups`: it requires POSIX file mode 0600, which Windows does not report. The panel package completed in 120.895 seconds; command and operations packages passed. This check must pass on Linux CI.

The unchanged browser diagnostics test requires positive Linux file-descriptor counts. The existing non-Linux metrics sampler intentionally does not provide them, so the limits browser suite cannot fully pass on Windows. Linux CI runs both browser suites.

The initial full race run before rebasing encountered different Windows TCP/UDP reserved port ranges and its five-minute timeout. The test helper now reserves both protocols on randomized test ports. Focused anti-flood race checks passed after this correction and after rebasing. The entire current panel race suite was not completed locally; the existing Linux CI runs it with a 900-second timeout.

Before the rebase, the full ordinary suite on the original 0.6.0 base also passed. Those earlier results do not substitute for the final current-main checks above.

No VPS deployment, production load acceptance test, restricted-network test, or test against an actual hosting abuse complaint was performed. Linux binaries were built, not executed in a Linux acceptance environment. Thresholds need calibration against legitimate traffic and provider requirements. HTTPS request rates inside encrypted persistent streams, other VPS processes' traffic, and volumetric inbound attacks are outside this implementation's detection scope.

Local artifacts are in `.cache/antiflood/`: binaries, screenshots, and a source manifest. See [the Persian operator guide](ANTIFLOOD.fa.md) for defaults, enforcement and recovery.
