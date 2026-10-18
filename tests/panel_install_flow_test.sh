#!/usr/bin/env bash
# No root/network/services: exercise real DNS functions against dig fixtures,
# and installer state transitions against temporary files and command doubles.
set -Eeuo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
source "$repo/install.sh"
source "$repo/lib/panel-installer.sh"
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
mkdir -p "$work/bundle/lib"
cp "$repo/install.sh" "$work/bundle/lib/standalone-install.sh"
ROOT_DIR="$work/bundle"
ZONE=soar.example.com PARENT_ZONE=example.com NS_HOST=ns-soar.example.com PUBLIC_IP=8.8.4.4 RESOLVERS=1.1.1.1,8.8.8.8
zone=$ZONE parent_zone=$PARENT_ZONE ns_host=$NS_HOST public_ip=$PUBLIC_IP test_resolvers=$RESOLVERS
mode=fresh
passed=0
expect_rc() {
 local wanted=$1 label=$2 actual=0
 shift 2
 "$@" >"$work/result" 2>&1 || actual=$?
 if [[ $actual != "$wanted" ]]; then cat "$work/result";printf 'FAIL %s: expected %s, got %s\n' "$label" "$wanted" "$actual";exit 1;fi
 passed=$((passed+1));printf 'PASS %s\n' "$label"
}
dig() {
 local server= name= type= arg
 for arg in "$@"; do
  case "$arg" in @*) server=${arg#@};; NS|A|AAAA|CNAME|DS|SOA) type=$arg;; +*) ;; *) name=$arg;; esac
 done
 printf '%s\n' "$*" >>"$work/dig.log"
 if [[ $server == 1.1.1.1 || $server == 8.8.8.8 ]]; then
  [[ $mode != one_resolver_down || $server != 1.1.1.1 ]] || return 9
  case "$name/$type" in
   "$PARENT_ZONE/NS")
    [[ $mode != no_parents ]] || return 9
    printf 'NS1.Provider.Example.\nNS2.Provider.Example.\n';;
   ns1.provider.example/A) [[ $mode == no_addresses ]] || printf '9.9.9.9\n';;
   ns2.provider.example/A) [[ $mode == no_addresses ]] || printf '9.9.9.10\n';;
   "$NS_HOST/A") [[ $mode == no_records ]] || printf '%s\n' "$PUBLIC_IP";;
   "$ZONE/SOA") [[ $mode == fresh ]] || printf '%s. hostmaster.%s. 1 300 60 3600 30\n' "$ZONE" "$ZONE";;
  esac
  return 0
 fi
 [[ $server == 9.9.9.9 || $server == 9.9.9.10 ]] || { printf 'Unexpected DNS server: %s\n' "$server" >&2;return 9; }
 [[ $mode != parent_down || $server != 9.9.9.10 ]] || return 9
 if [[ $mode == udp_loss && $server == 9.9.9.10 && $name == "$ZONE" && $type == AAAA && " $* " == *' +notcp '* ]]; then return 9;fi
 printf ';; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 1\n'
 if [[ $mode == no_records || ( $mode == partial_records && $server == 9.9.9.10 ) ]]; then
  printf '%s. 300 IN SOA ns1.provider.example. admin.example.com. 1 300 60 3600 30\n' "$PARENT_ZONE"
 elif [[ $name == "$ZONE" ]]; then
  printf '%s. 300 IN NS %s.\n' "$ZONE" "$NS_HOST"
  [[ $mode != conflict ]] || printf '%s. 300 IN NS master.example.com.\n' "$ZONE"
  [[ $mode != ds ]] || printf '%s. 300 IN DS 1234 13 2 DEAD\n' "$ZONE"
 elif [[ $type == A ]]; then
  printf '%s. 300 IN A %s\n' "$NS_HOST" "$PUBLIC_IP"
 fi
 return 0
}
expect_rc 0 'fresh server: correct parent records pass without child SOA' panel_dns records
if grep -q ' SOA ' "$work/dig.log"; then echo 'FAIL: child SOA checked before activation';exit 1;fi
expect_rc 1 'fresh server: child SOA is required only after activation' panel_dns ready
mode=healthy
expect_rc 0 'post-start recursive DNS readiness succeeds' panel_dns ready
mode=udp_loss
expect_rc 0 'one lost parent UDP response recovers via TCP' panel_dns records
grep -q '+tcp @9.9.9.10 soar.example.com AAAA' "$work/dig.log"
mode=parent_down
expect_rc 2 'unreachable authority stays blocked even when another authority is healthy' panel_dns records
grep -q 'ns2.provider.example. / soar.example.com / AAAA' "$work/result"
mode=no_parents
expect_rc 2 'failed NS discovery is distinct from an actual conflict' panel_dns records
mode=no_addresses
expect_rc 2 'authority address lookup failure is diagnosed through selected resolvers' panel_dns records
grep -q 'Cannot resolve authoritative server' "$work/result"
mode=one_resolver_down
expect_rc 0 'working alternative resolver and mixed-case NS names are accepted' panel_dns records
mode=no_records
expect_rc 3 'missing A/NS cannot be bypassed before installation' panel_dns records
mode=partial_records
expect_rc 3 'all authoritative servers must confirm A/NS propagation' panel_dns records
mode=conflict
expect_rc 1 'mixed tunnel delegation remains blocked' panel_dns records
mode=ds
expect_rc 1 'stale DS remains blocked even if child SOA answers' panel_dns ready
mode=healthy
ns_host=ns.other.example
expect_rc 1 'external NS hostname is rejected with input validation' panel_dns validate
ns_host=$NS_HOST

retry_records() (
 local attempts=0
 panel_dns(){ attempts=$((attempts+1));(( attempts>1 )); }
 panel_ask(){ printf -v "$1" '%s' 1; }
 panel_require_records
 [[ $attempts == 2 ]]
)
expect_rc 0 'failed DNS can be retried without restarting the installer' retry_records
cancel_records() (
 panel_dns(){ return 3; }
 panel_ask(){ printf -v "$1" '%s' 4; }
 panel_require_records
)
expect_rc 1 'cancel is possible while records remain missing' cancel_records

ss(){ printf '%s\n' "$listeners"; }
listeners='UNCONN 0 0 127.0.0.53:53 0.0.0.0:* users:(("resolved",pid=50,fd=3))'
expect_rc 0 'local stub resolver does not block a specific VPS DNS address' panel_port_available u 10.0.0.2 53
listeners='UNCONN 0 0 0.0.0.0:53 0.0.0.0:* users:(("other",pid=500,fd=3))'
expect_rc 1 'unrelated wildcard UDP listener is found before service changes' panel_port_available u 10.0.0.2 53
managed_pids=500
expect_rc 0 'the explicitly managed service may hand over its DNS listener' panel_port_available u 10.0.0.2 53
managed_pids=50
expect_rc 1 'similar PID is not mistaken for a managed service' panel_port_available u 10.0.0.2 53
listeners='LISTEN 0 128 [::]:2053 [::]:* users:(("other",pid=9,fd=3))'
expect_rc 1 'management wildcard TCP conflicts are detected' panel_port_available t 127.0.0.1 2053

ufw(){ printf '%s\n' "$*" >>"$work/ufw.log";if [[ $1 == status ]];then printf 'Status: active\n';fi; }
firewall_added=()
panel_access=1 panel_port=2053 listen_ip=10.0.0.2
expect_rc 0 'active UFW opens tunnel UDP and direct management TCP' panel_open_firewall
grep -q '^allow 53/udp ' "$work/ufw.log"
grep -q '^allow 2053/tcp ' "$work/ufw.log"
: >"$work/ufw.log"
panel_access=2
expect_rc 0 'SSH management opens UDP/53 without exposing the private TCP port' panel_open_firewall
if grep -q '/tcp' "$work/ufw.log";then echo 'FAIL private management port opened';exit 1;fi

# A fake CLI echoes the requested endpoint, exercising actual installer URL selection.
cat >"$work/cli" <<'SH'
#!/usr/bin/env bash
while [[ $# -gt 0 ]];do if [[ $1 == --listen ]];then printf 'http://%s/private-path/\n' "$2";exit;fi;shift;done
SH
chmod +x "$work/cli"
INSTALLED_BIN="$work/cli" DB="$work/panel.db" panel_bind=10.0.0.2:2053 panel_access=1
[[ $(panel_access_url) == http://8.8.4.4:2053/private-path/ ]] || { echo 'FAIL NAT URL';exit 1; }
panel_access=2 panel_bind=127.0.0.1:2053
[[ $(panel_access_url) == http://127.0.0.1:2053/private-path/ ]] || { echo 'FAIL SSH URL';exit 1; }
printf 'PASS public NAT endpoint and private login path are preserved\n'

# Native Linux enforces these modes; Git Bash cannot reliably emulate directory
# chmod on Windows. This fixture tests the transaction contents and ordering.
case "$(uname -s)" in MINGW*|MSYS*) install(){ if [[ $1 == -d ]];then mkdir -p -- "${@: -1}";else command install "$@";fi; };;esac
backup="$work/backup" transaction_ready=0
transaction_files=("$work/old-binary" "$work/panel.db" "$work/new-unit" "$work/access.json")
printf 'old binary\n' >"${transaction_files[0]}"
printf 'original accounts, keys and usage\n' >"${transaction_files[1]}"
printf 'original access path\n' >"${transaction_files[3]}"
panel_snapshot
grep -Fq -- "$work/panel.db" "$backup/paths.tsv"
for path in "${transaction_files[@]}";do printf 'changed\n' >"$path";done
declare -A was_enabled=([soar-panel.service]=1 [soar-panel-ops.service]=1 [soar-server.service]=0)
ops_active=1 panel_active=1 old_active=0
systemctl(){ printf '%s\n' "$*" >>"$work/systemctl.log"; }
expect_rc 0 'activation rollback restores files, database and enabled services' panel_restore
[[ $(cat "$work/panel.db") == 'original accounts, keys and usage' && $(cat "$work/access.json") == 'original access path' && ! -e $work/new-unit ]] || { echo 'FAIL rollback contents';exit 1; }
grep -q '^enable soar-panel.service$' "$work/systemctl.log"
grep -q '^start soar-panel-ops.service$' "$work/systemctl.log"
if grep -q '^start soar-server.service$' "$work/systemctl.log";then echo 'FAIL inactive standalone was started';exit 1;fi
printf 'PASS rollback keeps original account data and removes newly created unit\n'
grep -q '^--force delete allow 53/udp comment soar-panel-installer$' "$work/ufw.log"
printf 'PASS rollback removes only firewall rules added by the installer\n'
preserve_firewall() (
 firewall_added=()
 ufw(){ if [[ $1 == status ]];then printf 'Status: active\n53/udp ALLOW Anywhere # existing-rule\n2053/tcp ALLOW Anywhere\n';else return 77;fi; }
 panel_access=1
 panel_open_firewall
 [[ ${#firewall_added[@]} == 0 ]]
)
expect_rc 0 'existing UFW allow rules and comments are preserved' preserve_firewall
checks_pending() (
 panel_verify_connection(){ return 1; }
 panel_ask(){ case "$1" in choice) printf -v "$1" '%s' 3;;answer) printf -v "$1" '%s' KEEP;;esac; }
 panel_run_checks
 [[ $connection_verified == 0 ]]
)
expect_rc 0 'KEEP retains a failed installation as unverified, without claiming success' checks_pending
checks_rollback() (
 panel_verify_connection(){ return 1; }
 panel_ask(){ printf -v "$1" '%s' 4; }
 panel_run_checks
)
expect_rc 1 'failed connection checks can trigger rollback' checks_rollback
checks_retry() (
 local attempts=0
 panel_verify_connection(){ attempts=$((attempts+1));(( attempts>1 )); }
 panel_ask(){ printf -v "$1" '%s' 1; }
 panel_run_checks
 [[ $attempts == 2 && $connection_verified == 1 ]]
)
expect_rc 0 'retry marks the installation verified only after a successful tunnel test' checks_retry
check_stages() (
 local calls=0
 CHECK_BIN=mock_check
 mock_check(){ printf '%s\n' "$*" >>"$work/tunnel.log";return "${fail_tunnel:-0}"; }
 panel_dns(){ [[ $1 == ready ]]; }
 probe_zone=account.soar.example.com probe_key=test-public-key probe_expires=9999999999
 listen_ip=10.0.0.2
 : >"$work/tunnel.log"
 fail_tunnel=1
 if panel_verify_connection;then return 1;fi
 [[ $(wc -l <"$work/tunnel.log") == 1 ]] || return 1
 : >"$work/tunnel.log"
 fail_tunnel=0
 panel_verify_connection || return 1
 grep -q -- '-resolver 10.0.0.2 -tcp off' "$work/tunnel.log"
 grep -q -- '-resolver 1.1.1.1,8.8.8.8 -tcp auto' "$work/tunnel.log"
)
expect_rc 0 'local and recursive authenticated tunnel tests are both required' check_stages
refresh_expiring_probe() (
 CHECK_BIN=mock_check INSTALLED_BIN=mock_cli
 mock_check(){ return 0; }
 mock_cli(){ [[ $1 == create-install-probe ]] || return 1;printf '0123456789abcdef0123456789abcdef account.soar.example.com public-key 9999999999\n'; }
 systemctl(){ printf '%s\n' "$*" >>"$work/renew.log"; }
 panel_wait_health(){ return 0; }
 panel_dns(){ return 0; }
 probe_expires=1
 panel_verify_connection || return 1
 [[ $probe_expires == 9999999999 ]] || return 1
 grep -q '^stop soar-panel.service$' "$work/renew.log"
 grep -q '^start soar-panel.service$' "$work/renew.log"
)
expect_rc 0 'long interactive retries refresh the expiring probe before testing again' refresh_expiring_probe
printf '%s installer flow assertions passed, plus firewall, URL and rollback content checks.\n' "$passed"
