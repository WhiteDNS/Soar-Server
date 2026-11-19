# WhiteDNS Server Management 0.16.0

The panel can now run in Docker. Each release publishes
`ghcr.io/whitedns/soar-panel:<version>` and `:latest` for amd64 and arm64.

- The image contains the same verified binaries as this release's Linux
  bundles. The panel runs as an unprivileged user and binds DNS 53 and HTTPS
  80/443 with `CAP_NET_BIND_SERVICE`, as the native service does.
- `docker/compose.yml` runs it with host networking and a named data volume.
  The Persian guide `docs/DOCKER.fa.md` covers first setup, access through an
  SSH tunnel, HTTPS, updates, backup, restore and administrator password reset.
- `run --container` leaves out the root maintenance service. Updating and
  restoring happen with `docker compose` instead of inside the panel, and the
  Panel update and Backup & restore pages show those steps. Enabling HTTPS
  from the panel works without the maintenance service; the host firewall must
  allow ports 80 and 443.
- `--dns-listen auto:53` binds the server's default-route IPv4 address, like
  the installer, so host networking does not conflict with systemd-resolved.
- `init` and `admin-reset` accept `--password-file -` to read the password from
  standard input. Passwords are still never accepted as command-line arguments.

Native installations are unchanged. Every release now builds the image and
smoke-tests it on both architectures before publishing: it initializes from
standard input, binds DNS 53 as the unprivileged user and checks container
mode.
