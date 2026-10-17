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
bash dist/soar-server-setup-0.1.2-linux-amd64/install.sh --check-bundle
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
