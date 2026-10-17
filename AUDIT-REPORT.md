# Server validation and limits

Reviewed October 17, 2026. This public report uses no test VPS addresses,
real domains, client import links, credentials or private key material.

- 68 installer regression checks passed before the initial public packaging.
- Six Go diagnostic tests, go vet and pinned module verification passed.
- Real Ubuntu 24.04 amd64 installation preserved systemd-resolved on loopback.
- Upgrade, explicit rollback and a deliberately broken activation preserved
  the exact server key and domain; the previous service restarted successfully.
- A forced process kill exercised Restart=on-failure.
- A real reboot automatically started the service and preserved key permissions.
- Local and recursive paths passed authenticated, certificate-verified HTTPS.
- External client tests passed HTTPS, expected egress IP, concurrent requests,
  Conservative mode and another request after 130 seconds idle.
- Country IR discovery connected without a manually configured resolver list
  from the available external network, including after reboot.

These checks do not certify operation on Iranian networks. Field tests inside
Iran, multiple operators, novice usability and Android network transitions
remain necessary. Deliberate full-disk, SSH-cut activation and full uninstall
were covered by local recovery tests, not performed on the real VPS.

The bootstrap downloads a versioned release, checks the SHA-256 archive and
runtime manifest, and retains interactive stdin for the guided installer.
Additional bootstrap checks are run by CI. Release checksums are delivered from
the same GitHub origin and do not constitute an independent publisher signature.
