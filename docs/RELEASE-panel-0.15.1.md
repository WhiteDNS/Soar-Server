# WhiteDNS Server Management 0.15.1

Panels with more than one tunnel domain can now share and export every
domain's profiles, not only the preferred one.

- **Link / QR** shows a Domain row when several tunnel domains are set. The
  preferred domain is first and marked; choosing another shows that domain's
  Soar, Cotten and MasterDNS links and QR codes. The subscription tab is
  unchanged, because the subscription already covers every domain.
- **Export configs** opens one dialog with the security notice, a checkbox for
  each domain (all selected by default) and Copy / Download file actions. The
  file lists each account, a `## domain` heading per domain with its direct
  links (preferred domain first), and the subscription URL.
- Exported and shared links are the same profiles the subscription serves, with
  the same profile IDs. Links on the preferred domain, tunnel keys and
  subscription URLs are unchanged.

`GET /api/clients/{id}/link` also returns `domains`; `link` and `qr.png` accept
an optional `zone`. `POST /api/clients/export` accepts an optional `zones` list
and returns `domains` per account in place of `links`.

The release workflow now reads release metadata through the authenticated
GitHub API. Release files are still downloaded anonymously to confirm they are
public. In 0.15.0, the anonymous metadata request hit GitHub's rate limit after
the release had been published.

Large exports are built in one response. 512 accounts on 8 domains produce
about 8 MB, which can exceed the 45-second HTTPS write limit on a very slow
connection; export fewer accounts or domains at a time in that case.
