#!/usr/bin/env bash
# Runs entirely in a temporary directory. Does not install services or need root.
set -Eeuo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
source "$ROOT/install.sh"
TEST_ROOT=$(mktemp -d)
trap 'rm -rf -- "$TEST_ROOT"' EXIT
PASSED=0
pass() { PASSED=$((PASSED + 1)); printf 'PASS %s\n' "$1"; }
expect_yes() { local name=$1; shift; if "$@"; then pass "$name"; else printf 'FAIL %s\n' "$name"; exit 1; fi; }
expect_no() { local name=$1; shift; if "$@"; then printf 'FAIL %s\n' "$name"; exit 1; else pass "$name"; fi; }

expect_yes 'valid public IPv4' public_ipv4 8.8.8.8
for value in 10.0.0.1 100.64.0.1 127.0.0.1 169.254.1.1 172.16.0.1 192.168.1.1 192.0.2.1 198.18.0.1 198.51.100.1 203.0.113.1 224.0.0.1 256.1.1.1 01.1.1.1 '1.2.3.4;id'; do
    expect_no "reject nonpublic / invalid $value" public_ipv4 "$value"
done
expect_yes 'valid zone' valid_zone t.example.com
for value in '-x.example.com' 'x-.example.com' 'a..example.com' 'https://example.com' 'a b.example.com' 'a.example.com$(id)' 'a.example.com.'; do
    expect_no "reject invalid domain $value" valid_zone "$value"
done
expect_no 'reject zone too long for safe handshake' valid_zone aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.example.com
expect_yes 'resolver list' valid_resolvers 1.1.1.1,8.8.8.8
expect_no 'resolver empty entry' valid_resolvers 1.1.1.1,,8.8.8.8
expect_no 'resolver shell injection' valid_resolvers '1.1.1.1$(touch /tmp/not-allowed)'

init_paths "$TEST_ROOT/server"
mkdir -p "$CONFIG_DIR" "$STATE_DIR" "$LIB_DIR/releases" "$(dirname -- "$UNIT")" "$(dirname -- "$MANAGER")"
ZONE=t.example.com PARENT_ZONE=example.com NS_HOST=ns.example.com PUBLIC_IP=8.8.8.8
BIND_IP=10.0.0.2 LISTEN=10.0.0.2:53 RESOLVERS=1.1.1.1,8.8.8.8 CURRENT_RELEASE=old PREVIOUS_RELEASE=
write_settings "$CONFIG"
expect_yes 'settings roundtrip' load_settings
printf 'ZONE=$(touch should-not-exist)\n' > "$CONFIG"
expect_no 'settings never execute shell expressions' load_settings
[[ ! -e should-not-exist ]] || exit 1
ZONE=t.example.com PARENT_ZONE=example.com NS_HOST=ns.example.com PUBLIC_IP=8.8.8.8
BIND_IP=10.0.0.2 LISTEN=10.0.0.2:53 RESOLVERS=1.1.1.1 CURRENT_RELEASE=old PREVIOUS_RELEASE=
write_settings "$CONFIG"
printf 'ZONE=other.example.com\n' >> "$CONFIG"
expect_no 'duplicate config keys rejected' load_settings
write_settings "$CONFIG"
expect_yes 'restored config loads' load_settings
CURRENT_RELEASE=..
expect_no 'release directory traversal rejected' validate_settings
CURRENT_RELEASE=old

ss() { printf '%s\n' "${MOCK_SS:-}"; }
MOCK_SS='UNCONN 0 0 127.0.0.53%lo:53 0.0.0.0:*'
expect_yes 'keep local resolver and bind VPS address' port_available 10.0.0.2
expect_no 'wildcard bind conflicts with local resolver' port_available 0.0.0.0
MOCK_SS='UNCONN 0 0 0.0.0.0:53 0.0.0.0:*'
expect_no 'wildcard listener blocks specific bind' port_available 10.0.0.2
MOCK_SS='UNCONN 0 0 10.0.0.2:53 0.0.0.0:*'
expect_no 'same address conflicts' port_available 10.0.0.2
MOCK_SS='UNCONN 0 0 [::]:53 [::]:*'
expect_no 'IPv6 wildcard treated conservatively' port_available 10.0.0.2
BIND_IP=100.0.0.0
[[ $(direct_address) == 100.0.0.0 ]] || exit 1
pass 'direct address substitution is exact'
BIND_IP=0.0.0.0
[[ $(direct_address) == 127.0.0.1 ]] || exit 1
pass 'wildcard maps to loopback for local test'
BIND_IP=10.0.0.2

# A parent referral usually appears in the authority section, not the answer.
dns_query() {
    case "$3" in
        A) printf '%s\n' "$PUBLIC_IP" ;;
        NS) printf 'parent.example.net.\n' ;;
        SOA) printf 't.example.com. t.example.com. 1 3600 600 86400 60\n' ;;
    esac
}
dig() {
    if [[ " $* " == *' DS '* ]]; then
        if [[ ${MOCK_DS:-0} == 1 ]]; then printf 't.example.com. 60 IN DS 1234 13 2 DEAD\n'; fi
    else
        printf 't.example.com. 300 IN NS ns.example.com.\n'
    fi
}
expect_yes 'parent referral and recursive SOA accepted' dns_check
MOCK_DS=1
expect_no 'stale child DNSSEC DS prevents success report' dns_check
unset MOCK_DS

# A shared Master/Cotten delegation must be rejected before starting Soar.
parent_query() {
    if [[ ${MOCK_PARENT_TIMEOUT:-0} == 1 ]]; then return 9; fi
    printf ';; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 1\n'
    if [[ $2 == "$ZONE" ]]; then
        printf '%s' "${MOCK_PARENT_ZONE:-}"
    else
        printf '%s' "${MOCK_PARENT_HOST:-}"
    fi
}
expect_yes 'unused DNS names pass preflight' dns_preflight
MOCK_PARENT_ZONE=$'t.example.com. 300 IN NS ns.example.com.\n'
MOCK_PARENT_HOST=$'ns.example.com. 300 IN A 8.8.8.8\n'
expect_yes 'exact Soar delegation and address pass preflight' dns_preflight
MOCK_PARENT_ZONE+=$'t.example.com. 300 IN NS master.example.com.\n'
expect_no 'mixed Master and Soar delegation blocked before activation' dns_preflight
MOCK_PARENT_ZONE=$'t.example.com. 300 IN A 1.2.3.4\n'
expect_no 'existing website address blocks tunnel name' dns_preflight
MOCK_PARENT_ZONE=$'t.example.com. 300 IN CNAME website.example.net.\n'
expect_no 'existing CNAME blocks tunnel name' dns_preflight
MOCK_PARENT_ZONE=$'t.example.com. 300 IN DS 1234 13 2 DEAD\n'
expect_no 'existing child DS blocked before activation' dns_preflight
MOCK_PARENT_ZONE=
MOCK_PARENT_HOST=$'ns.example.com. 300 IN A 1.2.3.4\n'
expect_no 'nameserver of another service cannot be repointed' dns_preflight
MOCK_PARENT_HOST=
MOCK_PARENT_TIMEOUT=1
expect_no 'unreachable parent cannot report names safe' dns_preflight
unset MOCK_PARENT_TIMEOUT
MOCK_PARENT_ZONE=$'example.com. 300 IN SOA parent.example.net. admin.example.net. 1 3600 600 86400 60\n'
expect_yes 'parent SOA authority is not an occupied tunnel' dns_preflight
MOCK_SS='UNCONN 0 0 10.0.0.2:53 0.0.0.0:* users:(("soar-server",pid=123,fd=3))'
MANAGED_LISTENER_PID=123
expect_yes 'configure may replace the managed Soar listener' port_available 10.0.0.2
MANAGED_LISTENER_PID=12
expect_no 'configure cannot ignore another listener with similar PID' port_available 10.0.0.2
unset MANAGED_LISTENER_PID

# Corruption and duplicate manifest paths must fail before executing bundled files.
BUNDLE_DIR="$TEST_ROOT/bundle"
mkdir -p "$BUNDLE_DIR/bin/linux-amd64"
for file in install.sh bin/linux-amd64/soar-server bin/linux-amd64/soar-check SOURCE-VERSIONS.txt; do printf 'fixture\n' > "$BUNDLE_DIR/$file"; done
make_manifest() { (cd "$BUNDLE_DIR"; sha256sum install.sh bin/linux-amd64/soar-server bin/linux-amd64/soar-check SOURCE-VERSIONS.txt > SHA256SUMS); }
make_manifest
expect_yes 'complete bundle validates' verify_bundle
printf 'damaged\n' >> "$BUNDLE_DIR/bin/linux-amd64/soar-server"
expect_no 'corrupt server rejected' verify_bundle
make_manifest
cp "$BUNDLE_DIR/SHA256SUMS" "$TEST_ROOT/sums"
head -n 1 "$TEST_ROOT/sums" > "$BUNDLE_DIR/SHA256SUMS"
head -n 1 "$TEST_ROOT/sums" >> "$BUNDLE_DIR/SHA256SUMS"
tail -n 2 "$TEST_ROOT/sums" >> "$BUNDLE_DIR/SHA256SUMS"
expect_no 'duplicate manifest cannot hide missing binary' verify_bundle

# Test activation rollback with fake service commands, inside the temporary root.
ZONE=t.example.com PARENT_ZONE=example.com NS_HOST=ns.example.com PUBLIC_IP=8.8.8.8
BIND_IP=10.0.0.2 LISTEN=10.0.0.2:53 RESOLVERS=1.1.1.1 CURRENT_RELEASE=old PREVIOUS_RELEASE=
write_settings "$CONFIG"
cp "$CONFIG" "$TEST_ROOT/old.conf"
printf 'old-unit\n' > "$UNIT"
printf 'old-manager\n' > "$LIB_DIR/manager.sh"
printf 'soar-server-setup-v1\n' > "$MARKER"
mkdir -p "$LIB_DIR/releases/old" "$LIB_DIR/releases/new"
printf 'new-manager\n' > "$LIB_DIR/releases/new/manager.sh"
printf 'new-unit\n' > "$LIB_DIR/releases/new/soar-server.service"
systemctl() { printf '%s\n' "$*" >> "$TEST_ROOT/systemctl.log"; return 0; }
chown() { :; }

if ln -s releases/old "$LIB_DIR/current" 2>/dev/null && [[ -L $LIB_DIR/current ]]; then
    begin_transaction
    printf 'broken-config\n' > "$CONFIG"
    printf 'broken-unit\n' > "$UNIT"
    printf 'broken-manager\n' > "$LIB_DIR/manager.sh"
    rm -f "$LIB_DIR/current"
    ln -s releases/new "$LIB_DIR/current"
    restore_transaction
    cmp -s "$CONFIG" "$TEST_ROOT/old.conf" || exit 1
    [[ $(cat "$UNIT") == old-unit && $(cat "$LIB_DIR/manager.sh") == old-manager && $(readlink "$LIB_DIR/current") == releases/old ]] || exit 1
    grep -Fxq 'start soar-server.service' "$TEST_ROOT/systemctl.log" || exit 1
    pass 'failed activation restores config, unit, manager, binary pointer and running service'
    grep -Fxq 'reset-failed soar-server.service' "$TEST_ROOT/systemctl.log" || exit 1
    pass 'recovery clears systemd failure limits before restarting the old service'
    rm -rf "$TXN_DIR"; TXN_DIR=
    CURRENT_RELEASE=new PREVIOUS_RELEASE=old
    service_ready() { return 0; }
    activate_release
    [[ $(readlink "$LIB_DIR/current") == releases/new && $TXN_ACTIVE == 0 ]] || exit 1
    expect_yes 'activation config remains readable' load_settings
    pass 'successful activation commits new release'
    cp "$CONFIG" "$TEST_ROOT/committed.conf"
    cp "$UNIT" "$TEST_ROOT/committed.unit"
    printf 'old-manager\n' > "$LIB_DIR/releases/old/manager.sh"
    printf 'old-unit\n' > "$LIB_DIR/releases/old/soar-server.service"
    if (
        trap cleanup EXIT
        service_ready() { return 1; }
        CURRENT_RELEASE=old PREVIOUS_RELEASE=new
        activate_release
    ) > "$TEST_ROOT/failure.log" 2>&1; then
        printf 'FAIL failed activation returned success\n'; exit 1
    fi
    cmp -s "$CONFIG" "$TEST_ROOT/committed.conf" || exit 1
    cmp -s "$UNIT" "$TEST_ROOT/committed.unit" || exit 1
    [[ $(readlink "$LIB_DIR/current") == releases/new ]] || exit 1
    pass 'failed health check invokes exit trap and restores committed release'
    begin_transaction
    printf 'interrupted-config\n' > "$CONFIG"
    rm -f "$LIB_DIR/current"
    ln -s releases/old "$LIB_DIR/current"
    TXN_ACTIVE=0; TXN_DIR=; TXN_EXISTED=0; TXN_RUNNING=0; TXN_ENABLED=0
    recover_pending
    cmp -s "$CONFIG" "$TEST_ROOT/committed.conf" || exit 1
    [[ $(readlink "$LIB_DIR/current") == releases/new ]] || exit 1
    pass 'interrupted activation is recovered from its on-disk journal'
    cat > "$LIB_DIR/releases/new/soar-server" <<'FAKE'
#!/usr/bin/env bash
case "$1" in
  keygen) printf 'private fake-seed\npublic fake-public\n' ;;
  pubkey) [[ $(cat "$2") == fake-seed ]] || exit 1; printf 'fake-public\n' ;;
esac
FAKE
    chmod 755 "$LIB_DIR/releases/new/soar-server"
    RELEASE_ID=new
    prepare_key
    cp "$KEY" "$TEST_ROOT/preserved.key"
    prepare_key
    cmp -s "$KEY" "$TEST_ROOT/preserved.key" || exit 1
    pass 'existing private key is preserved on repeated preparation'
    install_wizard > "$TEST_ROOT/rerun.log"
    cmp -s "$CONFIG" "$TEST_ROOT/committed.conf" || exit 1
    cmp -s "$KEY" "$TEST_ROOT/preserved.key" || exit 1
    pass 'rerunning installer leaves key and configuration unchanged'
    printf 'invalid-key\n' > "$KEY"
    if (prepare_key) > "$TEST_ROOT/key.log" 2>&1; then printf 'FAIL invalid existing key accepted\n'; exit 1; fi
    [[ $(cat "$KEY") == invalid-key ]] || exit 1
    pass 'invalid existing key is rejected without replacement'
else
    printf 'SKIP native symlink transaction tests (host does not support symlinks)\n'
fi

public_key() { printf 'AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=\n'; }
decode_profile() {
    local payload=$1
    payload=${payload#soar://}; payload=${payload//-/+}; payload=${payload//_/\/}
    case $((${#payload} % 4)) in 2) payload+='==' ;; 3) payload+='=' ;; esac
    printf '%s' "$payload" | base64 -d
}
ZONE=soar.example.com
ir_link=$(profile_link IR)
ir_json=$(decode_profile "$ir_link")
[[ $ir_link == soar://* && $ir_json == *'"country":"IR"'* && $ir_json == *'"discovery":true'* && $ir_json == *'"public_key":'* ]] || exit 1
pass 'Iran import link includes discovery, domain and public key'
global_json=$(decode_profile "$(profile_link GLOBAL)")
[[ $global_json == *'"country":""'* && $global_json == *'"resolvers":["1.1.1.1","8.8.8.8"]'* ]] || exit 1
pass 'global profile keeps discovery and bootstrap alternatives'
expect_no 'invalid profile country rejected' profile_link 'IR"injection'
public_key() { return 1; }
expect_no 'missing public key cannot produce a broken import link' profile_link IR

printf '\n%d installer checks passed. No system services were changed.\n' "$PASSED"
