# WhiteDNS Panel 0.6.0

- Improved connection/session limits, stream settings migration and preservation, traffic accounting, resolver health diagnostics and backup snapshots.
- Included the pinned Cotten reliability patch for queued outbound connections, cancellation, deadlines and TCP DNS framing. See [the investigation and limits](COTTEN-TCP-RELIABILITY-2026-10-21.md).
- Aligned the runtime, installer and offline bundle version with 0.6.0. Tagged releases now publish the tested Linux amd64 archive and SHA-256 checksum after all panel CI checks pass.

The earlier 0.6.0 release entry contained no downloadable assets and its build configuration still identified the panel as 0.5.0. The empty release's tag was advanced from PR #8 merge commit `49e31969f36dc8c6ec0965c7fb3a836e0622fafc` to the packaging correction. This correction includes no additional tunnel behavior changes beyond merged PR #8. Source provenance is recorded in the bundle's `SOURCE-VERSIONS.txt`.

Use the existing installation command after the archive and checksum appear on the release:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/WhiteDNS/Soar-Server/main/panel-setup.sh)
```

Back up the panel before upgrading. Keep UDP/53 and TCP/53 open in the VPS provider firewall. CI checks do not establish connectivity on Iranian operators. This release does not include a rebuilt Android APK; server upgrades do not update the native client installed on a phone.

The [0.5.0 VPS validation report](PANEL-0.5.0-VALIDATION.md) is historical evidence for that version, not a new 0.6.0 deployment or operator test.
