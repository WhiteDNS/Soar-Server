# WhiteDNS Soar Panel 0.2.0-beta9

This server-side release polishes every dropdown and makes field help unobtrusive. Lantern
remains the author of the Soar engine; existing WhiteDNS profiles and apps keep
their current transport behavior.

- All selection fields now use a shared Persian listbox with consistent fonts,
  spacing, selected checkmarks, dark/light themes and mobile sizing. Keyboard
  navigation, Escape cancellation and outside-click dismissal are supported.
- Field explanations open only from the question-mark button: hover, keyboard
  focus or touch. Hovering or editing an input no longer opens a popup.
- Existing restore, update and secure-access features are retained:

- Restore a panel `.db` directly in Backup & Restore. Uploaded files are size-limited,
  checked for database integrity, account/key consistency and matching tunnel zone,
  then previewed before confirmation. Current administrator credentials, access
  settings and certificates are preserved. Old backups restore usage to their
  saved point in time. The service stops briefly during the actual replacement.
- Check official WhiteDNS panel releases and install a newer compatible amd64
  release from the panel. Downloads finish before stopping the current service.
  SHA-256 and the GitHub asset digest, when present, are checked. Failed activation
  restores the previous database and executables. Checksums provide integrity
  relative to the official release; they are not an independent code signature.
- A separate root maintenance service exposes only a group-restricted local Unix
  socket. The web panel still runs as the `soar-panel` user. Requests cannot select
  shell commands, service names, download hosts or installation destinations.
  Private before-operation copies are retained for the five most recent jobs.
- Configure a domain and automatic Let's Encrypt HTTPS from the panel or the
  English terminal wizard (`sudo soar-panel-security`). A direct A/AAAA record and
  reachable TCP/80 and TCP/443 are required. An existing web server on these ports
  requires a separately configured reverse proxy. Certificate failure preserves
  the existing address. Certificates renew automatically while the service runs.
- Generate a 256-bit random login path. New installations receive one by default;
  existing installations keep their address until they explicitly apply a new one.
  Preview and save the new address before applying it. Old URLs and unprefixed
  assets/API paths return 404. With native HTTPS enabled, public plaintext panel
  access is rejected. Cookies remain HttpOnly/SameSite Strict and are scoped to
  the private path; native HTTPS makes them Secure. A hidden URL supplements
  password authentication and HTTPS rather than replacing them.

Restore, updates and access changes require the current admin password. Service
restarts invalidate browser sessions; sign in again with the same password to
see the last maintenance result. Backup files contain private account keys.

Validation includes restore/rollback and update failure tests, corrupt backup and
key mismatch rejection, authentication/CSRF/password checks, session-bound restore
and access previews, version ordering, safe archive extraction, private-path asset
routing and certificate-failure preservation. Real browser checks retain existing
account/clipboard/settings coverage and add the maintenance views at mobile widths.

The visual system remains the original WhiteDNS implementation inspired by the
supplied Sanaei/3x-ui screenshots. No 3x-ui code or assets are included. This release
does not qualify Iranian access-network behavior or production load capacity.

Install or upgrade the explicit panel beta in an interactive SSH terminal:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/WhiteDNS/Soar-Server/panel-v0.2.0-beta9/panel-setup.sh)
```

[Persian installation, secure access and recovery guide](https://github.com/WhiteDNS/Soar-Server/blob/panel-v0.2.0-beta9/docs/PANEL.fa.md)

Certificate automation: [Go autocert](https://pkg.go.dev/golang.org/x/crypto/acme/autocert)
and [Let's Encrypt challenges](https://letsencrypt.org/docs/challenge-types/).
