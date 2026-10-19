# Panel 0.5.0 validation

Acceptance was performed on an authorized Ubuntu 24.04 amd64 VPS on 19 October 2026. Test credentials, addresses and private login paths are intentionally excluded.

- Upgraded an existing 0.4.0 installation using the interactive offline installer, then repeated installation with the final firewall fix. All six checks passed: authenticated local Soar HTTPS, public DNS delegation, recursive Soar HTTPS, Cotten UDP HTTPS, Cotten TCP HTTPS and recursive Cotten HTTPS.
- Compared private database hashes before and after testing: all seven existing account identities, administrator credentials, transport seeds and subscription credentials were preserved. Durable traffic totals did not decrease. Temporary test accounts were removed.
- Used the native Windows Cotten client from outside the VPS, with normal HTTPS certificate validation and verification of the server's exit IP. Direct UDP and public recursive DNS passed, including three concurrent HTTPS requests and the first reply to a fresh internal DNS query.
- Tested both engines on two independent tunnel domain routes in a separate Linux database. External Cotten UDP and TCP passed on both routes using port 5354; the original Soar client passed on both. The second domain route used direct authoritative queries, without modifying public parent DNS.
- Reduced the shared account quota after recording traffic and confirmed that a subsequent real Cotten HTTPS connection was rejected. Bulk deletion removed both temporary accounts and revoked the subscription.
- Issued two real publicly trusted HTTPS certificates. Login worked on both panel domains. An invalid replacement failed without changing working access. Swapping the preferred hostname retained the old hostname and login path. The original panel service was restored afterward.
- Reproduced an actual UFW failure inside the existing strict systemd maintenance sandbox. The fixed, bounded firewall worker ran successfully under that original unit. A separate network namespace with active UFW verified UDP/TCP 53 and HTTPS rules, preserved SSH, and repeated without duplicate rules. The host firewall remained inactive and its configuration unchanged.
- Verified that the main panel, maintenance worker and system DNS resolver remained active, and that the maintenance worker stopped restarting after the fix.

Automated validation includes Go unit and integration tests, vet, race tests, native Cotten interoperability, installer rollback/readiness tests and browser workflows at desktop and mobile widths in Persian and English. CI also builds the public module without a local workspace before testing the exact pinned engine checkout.

## Limits

The Windows source system denied outbound TCP to the VPS's port 53 before a connection could be made. External TCP was therefore verified on port 5354, while actual TCP/53 was verified locally on the VPS by the installer. This does not prove every user's ISP permits TCP/53.

No new Android APK was released as part of this server update. Client-side fixes reach phones only after rebuilding Android with the updated engine. No test here claims that a blocked domain can repair itself, that a saved subscription URL can change remotely, or that the app automatically switches domains or engines. Users must save alternative subscription addresses and test the alternative profiles on their own network.
