# Server tests and release checks

Supported target: Ubuntu 24.04 LTS amd64 with systemd. Source and bundles pin
Soar v0.2.2 and its Go module checksums. See AUDIT-REPORT.md for the real-server
checks and their limits.

## Automated checks

- 68 installer cases cover validation, DNS delegation conflicts, mixed NS,
  managed paths, symlink activation, rollback, interrupted activation recovery,
  repeat installation, public profile generation and exact key preservation.
  They use temporary files and fake service commands; they do not install services.
- Six bootstrap checks cover a valid checksum, corrupt bytes, a mismatched
  checksum filename, archive traversal/wrong root, invalid invocation and
  Ubuntu OS metadata overwriting the unrelated VERSION shell variable.
- Six Go tests exercise a real loopback DNS tunnel and HTTPS origin, server-key
  rejection, TLS trust rejection, HTTP status handling and CLI input validation.
- CI builds the Linux bundle and verifies its internal runtime manifest before
  publishing release assets. No private test profiles are published.

## Reproduce

```bash
bash -n install.sh setup.sh
bash tests/installer_test.sh
bash tests/bootstrap_test.sh
go mod verify
go test ./... -count=1 -timeout 60s
go vet ./...
python3 scripts/build_bundle.py
bash dist/soar-server-setup-0.1.3-linux-amd64/install.sh --check-bundle
```

Git Bash on Windows needs MSYS=winsymlinks:nativestrict for native symlink cases.
A host without symlink support reports skips; Ubuntu CI runs the full suite.

After publication, verify the actual download without installing a service:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/WhiteDNS/Soar-Server/main/setup.sh) --check-download
```

A successful VPS diagnostic does not establish client-network reachability.
Iran operator tests, network switching, full-disk/SSH-loss recovery on a real
VPS, full uninstall and a novice usability trial remain separate acceptance work.

## Panel diagnostics and assistant

`internal/panel/assistant_test.go` exercises all three AI wire formats against local mock servers, destination consent, credential redaction, context filtering, validated proposals, session ownership, approval, replay/expiry rejection, stale settings and undo conflicts, burst coalescing and log persistence. It uses no real provider credentials.

`tests/panel-logs.cjs` runs inside `tests/panel-ui.cjs`. It checks the real panel with a local mock provider, chat and context preview, approval cancellation, apply/undo, reload recovery, safe text rendering, export and both languages/themes at 1440/768/414/375/320 pixels. Set `PANEL_UI_ONLY=logs` for this focused browser check; normal browser runs include it automatically.

Use the prepared pinned engine workspace (`.engine-source/patched.go.work`) for Go checks. The existing Telegram file-permission test requires Unix permission semantics; Windows reports different mode bits. A Windows-only run can skip exactly `TestTelegramAPIAuthenticationSecretLifecycleAndBackups`; keep it enabled on Linux CI. Local tests do not verify real AI account access/quota or restricted-network device connectivity.
