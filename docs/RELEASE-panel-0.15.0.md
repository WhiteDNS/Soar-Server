# WhiteDNS Server Management 0.15.0

Export the connection configs of many accounts at once, and a clearer panel
layout with new icons.

- Select accounts in the Accounts table, then use **Export configs** to download
  one text file, or **Copy configs** to copy it. Each account block has its
  name, the direct Soar, Cotten and MasterDNS profile links it uses, and its
  subscription URL. Accounts no longer need to be opened one by one.
- The export contains every selected account's connection keys. The panel asks
  for confirmation first, and each export is recorded in Events.
- An account that had no subscription URL yet receives one on its first
  export, the same as opening its Link / QR dialog.
- The sidebar is grouped into Main, Monitoring & protection and Server, and
  every page has its own icon. The icon set is redrawn from Lucide outlines.
- Account rows show icons on Link / QR and Edit, and status badges carry a
  colored dot.

The new `POST /api/clients/export` endpoint accepts 1 to 512 account IDs,
requires the administrator session and CSRF token, and rejects the whole
request if any account no longer exists. Tunnel keys, subscription URLs and
the tunnel packet format are unchanged.
