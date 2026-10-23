# Four-engine server integration

This development build embeds Soar, CottenDNS, StormDNS and MasterDnsVPN in
the panel process. One public UDP/53 listener dispatches requests by account
and engine domain. The existing TCP/53 listener can frame DNS messages for
the embedded engines too; this does not add TCP resolver support to upstream
clients that only use UDP.

## Accounts and compatibility

The `engines` API field accepts `soar`, `cotten`, `stormdns`, `masterdns`,
`both`, `all`, or a comma-separated combination of engine names. `both`
continues to mean Soar and Cotten, and omitted fields preserve existing
accounts. The UI defaults new accounts to all four. Existing accounts must
be edited explicitly to enable additional engines.

For account zone `ACCOUNT.t.example.org`, the routes are:

| Engine | Domain | Link scheme |
| --- | --- | --- |
| Soar | `ACCOUNT.t.example.org` | `soar://` |
| CottenDNS | `cot.ACCOUNT.t.example.org` | `cottendns://` |
| StormDNS | `storm.ACCOUNT.t.example.org` | `stormdns://` |
| MasterDnsVPN | `master.ACCOUNT.t.example.org` | `masterdns://` |

All routes use the existing NS delegation. No additional IP address,
nameserver record or public port is required. Alternate tunnel domains
produce corresponding profiles for each enabled engine.

The existing `whitedns.profile` version 1 envelope is used for new links,
with `server.domain`, `server.encryption_key` and `server.encryption_method`.
The latter is 5 (AES-256-GCM). Engine keys use separate HMAC derivation
contexts from the account seed. Rotating the account key revokes all four;
rotating the subscription URL alone does not change tunnel keys.

Quota, expiry, enable/disable, speed and account/server stream limits are
enforced through the existing shared egress and metering path. Tunneled DNS
queries use the same metered, allowlisted resolver path. Engines cannot
open independent, unmetered egress through the embedded API.

StormDNS and MasterDnsVPN retain upstream session table limits (255 sessions
per engine per account). Soar/Cotten session settings remain specific to
those engines. A zero shared stream limit still means unlimited at the
panel level; upstream per-session safeguards remain in effect.

NS tunnel queries beneath the StormDNS route are dispatched to the engine.
Authoritative NS/SOA answers are retained at delegation, account and engine
apexes for QNAME minimisation. TXT/CNAME/SRV packet routing is also preserved.

## Source and build

To install the custom Linux amd64 bundle on a supported server, upload the
archive and run these commands in an interactive SSH terminal:

```sh
tar -xzf soar-panel-0.6.0-linux-amd64.tar.gz
cd soar-panel-0.6.0-linux-amd64
sudo bash panel-install.sh
```

The existing installer preserves panel accounts, keys and usage. After
upgrading, edit an account's engine selection to enable the new routes,
then refresh its subscription in the client. Until these changes are
published upstream, use this custom bundle for upgrades: upstream setup
commands and the panel updater fetch upstream releases.

Pinned MIT-licensed engine sources are checked into `third_party/StormDNS`
and `third_party/MasterDnsVPN`. Each directory retains its original license,
authors and a commit reference in `UPSTREAM.json`. The integration adds
`server/server.go`, `internal/config/whitedns.go` and
`internal/udpserver/whitedns.go`; upstream wire protocol code is unchanged.
The two engines are compiled into the panel, so no extra daemon, installer
or host dependency is needed. Do not run their standalone installers over
the panel: those installers attempt to take over port 53.

Prepare the existing patched Cotten workspace as described in the README,
then build the real native clients for regression tests:

```sh
python3 scripts/prepare_cotten_engine.py --workspace .engine-source/patched.go.work
export GOWORK="$PWD/.engine-source/patched.go.work"
go build -o .engine-source/storm-client stormdns-go/cmd/client
go build -o .engine-source/master-client masterdnsvpn-go/cmd/client
go build -o .engine-source/cotten-client ./.engine-source/CottenDNS/cmd/client
export PANEL_STORM_CLIENT="$PWD/.engine-source/storm-client"
export PANEL_MASTER_CLIENT="$PWD/.engine-source/master-client"
export PANEL_COTTEN_CLIENT="$PWD/.engine-source/cotten-client"
go test ./... -count=1 -timeout 240s
go test -race ./internal/panel ./internal/panelops -count=1 -timeout 900s
go vet ./...
python3 scripts/build_panel_bundle.py
```

Native tests cover encrypted TCP transfer, DNS record routing, shared
Soar/new-engine stream admission, usage accounting and key revocation.
`additional_engines` in runtime diagnostics reports per-engine starts,
running instances, startup errors, queue drops and stream counters.
