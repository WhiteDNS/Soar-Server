# MasterDNS integration

MasterDnsVPN is developed by MasterkinG32 / Amin Mahmoudi and licensed under MIT:
https://github.com/masterking32/MasterDnsVPN

The original protocol is pinned at `acbf1c61f90786f41b975d2e2f616afbce292b29`.
`patches/masterdns-engine.json` records the embedding patch digest and the complete source-file manifest. Build preparation rejects different source files, additional build inputs and modified patches. The release includes the original engine license.

## Accounts and routing

MasterDNS uses `md.<account>.<tunnel-zone>`, with the same existing delegation and optional alternate tunnel zones. `cot` remains reserved for Cotten. NS/SOA control responses remain the panel router's responsibility. Encrypted engine requests are dispatched through a bounded shared work queue. Accounts instantiate their MasterDNS runtime lazily on first traffic.

Each MasterDNS key is HMAC-SHA256 derived from the existing account seed with a dedicated context, distinct from the Cotten key. Profiles use AES-GCM method 5 and the existing `whitedns.profile` version 1 schema under `masterdns://`. Rotating account keys revokes MasterDNS credentials as well. Backups already contain the seed; no second secret database is needed.

All outbound connections and tunneled DNS use caller policy hooks. There is no unmetered native-dial fallback. The shared account accounting, rate pacing, expiry, port policy, public-destination restriction and stream limits apply across all selected engines. Runtime cancellation stops deferred work and closes streams.

MasterDNS retains its one-byte session identifiers: its separate per-account session limit is 1–255, default 255. Sessions and TCP streams do not represent device counts. The engine transports TCP payloads over UDP DNS; arbitrary UDP forwarding is outside this integration.

## Compatibility and administration

The API continues using the `engines` string. Existing values `soar`, `cotten` and `both` retain their meanings; missing values still mean Soar. New values are `masterdns`, `soar_masterdns`, `cotten_masterdns` and `all`. New accounts keep the existing Soar+Cotten default. Existing accounts require an explicit selection to enable MasterDNS.

The dashboard and Telegram support the same engine choices, key lifecycle, subscription exports and separate MasterDNS session limit. Existing subscription URLs and account identities are preserved. WhiteDNS Android recognizes the MasterDNS scheme through its existing Master/Storm compatibility path; Desktop also recognizes that scheme. Physical-device and restricted-network acceptance must be recorded separately from native integration tests.

## Building and verification

```bash
python3 scripts/prepare_panel_engines.py
export GOWORK="$PWD/.engine-source/patched.go.work"
go test ./... -count=1 -timeout 240s
go test -race ./internal/panel ./internal/panelops -count=1 -timeout 900s
(cd .engine-source/MasterDnsVPN && go test ./... && go test -race ./internal/udpserver -run TestEmbedded)
python3 scripts/build_panel_bundle.py --arch amd64
python3 scripts/build_panel_bundle.py --arch arm64
```

`TestMasterDNSNativeHTTPSAndAccounting` runs the original native MasterDNS client through the panel router to a TLS-verified test origin and checks account billing. Additional tests cover engine selection, key isolation, revocation, session validation, policy hooks, shutdown, subscriptions and browser sharing. Guided installation checks both local and recursive UDP DNS paths with an expiring, quota-limited installation account. A failed check follows the existing retry, rollback or explicitly unverified-install flow.

Saving panel settings successfully restarts active account engines on the shared DNS listener, closes their old streams and reloads the new settings. Failed validation does not restart engines. Account keys, subscription identities and persisted usage survive the restart. Profile settings on a phone still require refreshing its subscription.
