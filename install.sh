#!/usr/bin/env bash
# Soar server setup. Terminal text is intentionally ASCII English.
# Sourceable for tests; no changes are made until main runs.
set -Eeuo pipefail
export LC_ALL=C
umask 077

SETUP_VERSION=0.1.2
SOAR_VERSION=v0.2.2
BUNDLE_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
TXN_ACTIVE=0
TXN_DIR=
TXN_EXISTED=0
TXN_RUNNING=0
TXN_ENABLED=0
WORK_DIR=

init_paths() {
    local root=${1:-}
    CONFIG_DIR="$root/etc/soar"
    STATE_DIR="$root/var/lib/soar"
    LIB_DIR="$root/usr/local/lib/soar"
    MANAGER="$root/usr/local/bin/soar-manager"
    UNIT="$root/etc/systemd/system/soar-server.service"
    CONFIG="$CONFIG_DIR/server.conf"
    KEY="$CONFIG_DIR/server.key"
    MARKER="$STATE_DIR/managed-by"
}
init_paths

say() { printf '%s\n' "$*"; }
ok() { say "[OK] $*"; }
warn() { say "[WARN] $*" >&2; }
die() { say "[ERROR] $*" >&2; exit 1; }
step() { printf '\nStep %s/7: %s\n\n' "$1" "$2"; }
ask() {
    local label=$1 default=${2:-}
    if [[ -n $default ]]; then label="$label [$default]"; fi
    IFS= read -r -p "$label: " REPLY || die "Input closed. Run the script from an interactive SSH terminal."
    REPLY=${REPLY:-$default}
}
confirm() {
    local message=$1
    ask "$message (y/N)" n
    [[ $REPLY == y || $REPLY == Y ]]
}
pause() { ask 'Press Enter when you are ready' ''; }
is_managed() { [[ -f $MARKER ]] && [[ $(cat -- "$MARKER") == soar-server-setup-v1 ]]; }
is_installed() { is_managed && [[ -f $CONFIG && -L $LIB_DIR/current && -f $UNIT ]]; }

valid_ipv4() {
    local ip=$1 part
    local -a pieces
    [[ $ip =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
    IFS=. read -r -a pieces <<< "$ip"
    for part in "${pieces[@]}"; do
        [[ ${#part} -le 3 && ( $part == 0 || $part != 0* ) ]] || return 1
        (( 10#$part <= 255 )) || return 1
    done
}
public_ipv4() {
    valid_ipv4 "$1" || return 1
    local a b c d
    IFS=. read -r a b c d <<< "$1"
    (( a > 0 && a < 224 && a != 10 && a != 127 )) || return 1
    ! (( a == 100 && b >= 64 && b <= 127 || a == 169 && b == 254 ||
         a == 172 && b >= 16 && b <= 31 || a == 192 && b == 168 ||
         a == 192 && b == 0 && (c == 0 || c == 2) || a == 192 && b == 88 && c == 99 ||
         a == 198 && (b == 18 || b == 19) || a == 198 && b == 51 && c == 100 ||
         a == 203 && b == 0 && c == 113 ))
}
valid_domain() {
    local name=$1 label
    local -a labels
    [[ ${#name} -le 253 && $name == *.* && $name != .* && $name != *. ]] || return 1
    [[ $name =~ ^[a-z0-9.-]+$ ]] || return 1
    IFS=. read -r -a labels <<< "$name"
    for label in "${labels[@]}"; do
        [[ ${#label} -ge 1 && ${#label} -le 63 && $label != -* && $label != *- ]] || return 1
    done
    [[ $name != *..* ]]
}
valid_zone() { valid_domain "$1" && [[ ${#1} -le 45 ]]; }
direct_address() { if [[ $BIND_IP == 0.0.0.0 ]]; then printf '%s\n' 127.0.0.1; else printf '%s\n' "$BIND_IP"; fi; }
valid_resolvers() {
    local value part
    value=$1
    [[ -n $value && $value != ,* && $value != *, && $value != *,,* ]] || return 1
    local -a parts
    IFS=, read -r -a parts <<< "$value"
    [[ ${#parts[@]} -le 5 ]] || return 1
    for part in "${parts[@]}"; do valid_ipv4 "$part" || return 1; done
}
validate_settings() {
    valid_zone "$ZONE" && valid_domain "$PARENT_ZONE" && valid_domain "$NS_HOST" || return 1
    [[ $ZONE == *."$PARENT_ZONE" && $NS_HOST == *."$PARENT_ZONE" ]] || return 1
    [[ $NS_HOST != "$ZONE" && $NS_HOST != *."$ZONE" ]] || return 1
    public_ipv4 "$PUBLIC_IP" && valid_ipv4 "$BIND_IP" && valid_resolvers "$RESOLVERS" || return 1
    [[ $LISTEN == "$BIND_IP:53" && $CURRENT_RELEASE =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]*$ ]] || return 1
    [[ -z $PREVIOUS_RELEASE || $PREVIOUS_RELEASE =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]*$ ]]
}
load_settings() {
    local name value seen='|'
    ZONE= PARENT_ZONE= NS_HOST= PUBLIC_IP= BIND_IP= LISTEN= RESOLVERS= CURRENT_RELEASE= PREVIOUS_RELEASE=
    [[ -f $CONFIG && ! -L $CONFIG ]] || return 1
    while IFS='=' read -r name value || [[ -n $name ]]; do
        case "$name" in
            ZONE|PARENT_ZONE|NS_HOST|PUBLIC_IP|BIND_IP|LISTEN|RESOLVERS|CURRENT_RELEASE|PREVIOUS_RELEASE)
                [[ $seen != *"|$name|"* && $value != *[[:space:]]* ]] || return 1
                printf -v "$name" '%s' "$value"
                seen+="$name|" ;;
            *) return 1 ;;
        esac
    done < "$CONFIG"
    validate_settings
}
write_settings() {
    local output=$1 name
    for name in ZONE PARENT_ZONE NS_HOST PUBLIC_IP BIND_IP LISTEN RESOLVERS CURRENT_RELEASE PREVIOUS_RELEASE; do
        printf '%s=%s\n' "$name" "${!name}"
    done > "$output"
}

verify_bundle() {
    local expected file actual count=0
    [[ -f $BUNDLE_DIR/SHA256SUMS ]] || { warn "This command needs the extracted setup bundle, including SHA256SUMS and bin/."; return 1; }
    while read -r expected file; do
        file=${file#\*}
        [[ $expected =~ ^[a-f0-9]{64}$ ]] || return 1
        case "$file" in
            install.sh|bin/linux-amd64/soar-server|bin/linux-amd64/soar-check|SOURCE-VERSIONS.txt) ;;
            *) warn "Unexpected path in bundle manifest."; return 1 ;;
        esac
        [[ -f $BUNDLE_DIR/$file && ! -L $BUNDLE_DIR/$file ]] || return 1
        actual=$(sha256sum -- "$BUNDLE_DIR/$file"); actual=${actual%% *}
        [[ $actual == "$expected" ]] || { warn "Bundle checksum failed. Download or copy the complete bundle again."; return 1; }
        count=$((count + 1))
    done < "$BUNDLE_DIR/SHA256SUMS"
    [[ $count == 4 ]] || return 1
    for file in install.sh bin/linux-amd64/soar-server bin/linux-amd64/soar-check SOURCE-VERSIONS.txt; do
        [[ $(awk -v path="$file" '{sub(/^\*/, "", $2)} $2 == path {n++} END {print n+0}' "$BUNDLE_DIR/SHA256SUMS") == 1 ]] || return 1
    done
    RELEASE_ID="$SETUP_VERSION-$(sha256sum -- "$BUNDLE_DIR/SHA256SUMS" | cut -c1-12)"
    ok 'All bundled file checksums match.'
    say 'Checksums detect damaged files. Obtain the bundle and its checksum from your trusted distributor.'
}

require_system() {
    [[ $EUID == 0 ]] || die 'Run with sudo: sudo bash install.sh (or sudo soar-manager).'
    [[ -t 0 ]] || die 'Use an interactive SSH terminal. Extract the bundle, then run sudo bash install.sh.'
    [[ -f /etc/os-release ]] || die 'This version requires Ubuntu 24.04 LTS.'
    local ID VERSION_ID
    . /etc/os-release
    [[ $ID == ubuntu && $VERSION_ID == 24.04 ]] || die 'This version supports Ubuntu 24.04 LTS only. No server settings were changed.'
    [[ $(uname -m) == x86_64 ]] || die 'This bundle supports x86_64/amd64 only. ARM support needs a separate tested bundle.'
    [[ -d /run/systemd/system ]] || die 'systemd is required. Containers and Linux shells without systemd are not supported.'
    [[ $(df -Pk /usr/local | awk 'END {print $4}') -ge 102400 ]] || die 'At least 100 MiB of free disk space is required under /usr/local.'
    command -v flock >/dev/null || die 'Missing flock. Install util-linux and run again.'
    exec 9>/run/lock/soar-server-setup.lock
    flock -n 9 || die 'Another setup or management command is running. Wait for it to finish.'
}
dependencies() {
    local command missing=0
    for command in dig ss ip curl; do command -v "$command" >/dev/null || missing=1; done
    [[ -f /etc/ssl/certs/ca-certificates.crt ]] || missing=1
    if [[ $missing == 1 ]]; then
        say 'Some small system tools are missing. They check DNS, ports and HTTPS certificates.'
        say 'The next command installs ca-certificates, dnsutils, iproute2 and curl using apt.'
        say 'It does not upgrade the operating system or replace your DNS service.'
        confirm 'Install these required packages now?' || die 'Packages are required. You can run this setup again later.'
        apt-get update
        DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates dnsutils iproute2 curl
    fi
}
check_owned_paths() {
    local path
    if ! is_managed; then
        if getent passwd soar-tunnel >/dev/null || getent group soar-tunnel >/dev/null; then
            die 'The account/group soar-tunnel already exists outside this setup. Resolve that account conflict first.'
        fi
        for path in "$CONFIG_DIR" "$STATE_DIR" "$LIB_DIR" "$UNIT" "$MANAGER"; do
            [[ ! -e $path && ! -L $path ]] || die "Existing unmanaged path: $path. Back it up and resolve the conflict first."
        done
    fi
}
port_available() {
    local bind=$1 endpoint ip snapshot row
    snapshot=$(ss -H -lunp 'sport = :53') || { warn 'Could not inspect UDP listeners. Resolve the ss error before continuing.'; return 1; }
    while IFS= read -r row; do
        if [[ ${MANAGED_LISTENER_PID:-} =~ ^[1-9][0-9]*$ && $row == *"pid=$MANAGED_LISTENER_PID,"* ]]; then continue; fi
        endpoint=$(awk '{print $4}' <<< "$row")
        [[ -n $endpoint ]] || continue
        ip=${endpoint%:53}; ip=${ip%%%*}
        case "$ip" in '*'|0.0.0.0|'[::]'|'::') return 1 ;; esac
        [[ $bind != 0.0.0.0 && $ip != "$bind" ]] || return 1
    done <<< "$snapshot"
}
local_ip() { ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i=1;i<=NF;i++) if ($i=="src") {print $(i+1);exit}}'; }
bind_is_local() {
    [[ $1 == 0.0.0.0 ]] || ip -4 -o addr show | awk '{split($4,a,"/");print a[1]}' | grep -Fxq -- "$1"
}

collect_settings() {
    local default_ip parent ns
    say 'Open the control panel where you manage your domain DNS records.'
    say 'For example: if you own example.com, use an unused name such as soar.example.com.'
    say 'Do not enter https://, a URL path, or the domain of an existing website.'
    while :; do
        ask 'Tunnel domain (example: soar.example.com)'
        ZONE=${REPLY,,}; ZONE=${ZONE%.}
        valid_zone "$ZONE" && break
        warn 'Use an ASCII domain with dots, no spaces, and at most 45 characters. For international domains use Punycode.'
    done
    say ''
    say 'Now enter the DNS zone you can edit in that panel (usually your main domain).'
    parent=${ZONE#*.}
    while :; do
        ask 'Main DNS zone (example: example.com)' "$parent"
        PARENT_ZONE=${REPLY,,}; PARENT_ZONE=${PARENT_ZONE%.}
        valid_domain "$PARENT_ZONE" && [[ $ZONE == *."$PARENT_ZONE" ]] && break
        warn 'The tunnel must be a subdomain of this DNS zone. Check the domain selected in your DNS panel.'
    done
    say ''
    say 'Open your VPS provider panel. Copy the public IPv4 address shown for this server.'
    say 'Use the public IPv4, not your home IP and not a private address such as 10.x.x.x.'
    while :; do
        ask 'Server public IPv4'
        PUBLIC_IP=$REPLY
        public_ipv4 "$PUBLIC_IP" && break
        warn 'Enter a public IPv4 address as four numbers separated by dots. Reserved and private addresses cannot be used.'
    done
    say ''
    say 'The nameserver is a DNS name pointing to this VPS. It is not a website.'
    say 'Use a new, unused name in your main DNS zone. Keep it outside the tunnel subdomain.'
    ns="ns-soar.$PARENT_ZONE"
    while :; do
        ask 'Nameserver hostname' "$ns"
        NS_HOST=${REPLY,,}; NS_HOST=${NS_HOST%.}
        valid_domain "$NS_HOST" && [[ $NS_HOST == *."$PARENT_ZONE" && $NS_HOST != "$ZONE" && $NS_HOST != *."$ZONE" ]] && break
        warn 'Choose a hostname inside the main DNS zone, but outside the tunnel subdomain.'
    done
    default_ip=$(local_ip)
    if ! valid_ipv4 "$default_ip"; then default_ip=0.0.0.0; fi
    say ''
    say 'The listen address is the address inside this VPS. The detected value usually works.'
    say 'On cloud servers it can differ from the public IP because of provider NAT.'
    say 'Local addresses currently configured on this server:'
    ip -4 -o addr show scope global | awk '{print "  " $2 "  " $4}'
    while :; do
        ask 'Local listen IPv4 (press Enter to use the detected value)' "$default_ip"
        BIND_IP=$REPLY
        if ! valid_ipv4 "$BIND_IP" || ! bind_is_local "$BIND_IP"; then
            warn 'That IPv4 is not configured on this VPS. Use an address in the list above, or 0.0.0.0 for all IPv4 interfaces.'
            continue
        fi
        if ! port_available "$BIND_IP"; then
            warn 'Another DNS service already uses UDP/53 on this address.'
            ss -H -lunp 'sport = :53'
            say 'Keep the existing DNS service running. If it only uses 127.x.x.x, choose the non-loopback VPS address.'
            say 'Otherwise use a dedicated VPS or have the existing DNS service reconfigured first.'
            continue
        fi
        break
    done
    LISTEN="$BIND_IP:53"
    say ''
    say 'Resolvers carry the test query to your server through DNS. No account is required.'
    say 'You may keep the suggested public resolvers, or enter 1 to 5 working resolver IPv4 addresses separated by commas.'
    while :; do
        ask 'Resolvers for the server-side test' '1.1.1.1,8.8.8.8'
        RESOLVERS=$REPLY
        valid_resolvers "$RESOLVERS" && break
        warn 'Use IPv4 addresses separated by commas, without spaces. Example: 1.1.1.1,8.8.8.8'
    done
}
show_dns_records() {
    local ns_label=${NS_HOST%."$PARENT_ZONE"} tunnel_label=${ZONE%."$PARENT_ZONE"}
    say "In the DNS panel for $PARENT_ZONE, ADD these two records:"
    printf '\n  Type  Name (short)  Name (full)  Value\n'
    printf '  A     %s  %s  %s\n' "$ns_label" "$NS_HOST" "$PUBLIC_IP"
    printf '  NS    %s  %s  %s\n\n' "$tunnel_label" "$ZONE" "$NS_HOST"
    say 'Most DNS panels ask for the short name. Use the full name only if your panel requires it.'
    say 'For the NS value, some panels need a trailing dot; others add it automatically.'
    say 'If the panel has an orange cloud / HTTP proxy switch, set the A record to DNS only.'
    say 'Keep the default TTL. Do not change your main domain nameservers, website or email records.'
    say 'Use unused names. Do not overwrite an existing A, AAAA, CNAME or NS record for another service.'
    say 'Save both records. DNS caches may need time to refresh; you can retry the checks later.'
}

# Query the parent, not the prospective tunnel server: a recursive NS query can
# already have followed an old delegation to another application.
parent_query() {
    dig +time=3 +tries=1 @"$1" "$2" "$3" +norecurse +noall +comments +answer +authority 2>/dev/null
}
dns_preflight() {
    local resolver ns name type response records parents checked=0 failed=0 conflict=0
    local -a resolvers
    DNS_PREFLIGHT_RECORDS=
    IFS=, read -r -a resolvers <<< "$RESOLVERS"
    parents=$(
        for resolver in "${resolvers[@]}"; do dns_query "$resolver" "$PARENT_ZONE" NS || true; done | sort -u
    )
    while read -r ns; do
        valid_domain "${ns%.}" || continue
        for name in "$ZONE" "$NS_HOST"; do
            for type in NS A AAAA CNAME DS; do
                # A DS on the nameserver hostname is unrelated to the tunnel.
                [[ $name != "$NS_HOST" || $type != DS && $type != NS ]] || continue
                response=$(parent_query "$ns" "$name" "$type") || response=
                if [[ ! $response =~ status:\ (NOERROR|NXDOMAIN), ]]; then
                    failed=1; continue
                fi
                checked=$((checked + 1))
                records=$(awk -v name="$name." -v zone="$ZONE." -v parent="$PARENT_ZONE." -v host="$NS_HOST." -v ip="$PUBLIC_IP" '
                    /^;/ {next}
                    tolower($1)==tolower(name) {
                        if (name==zone && $4=="NS" && tolower($5)!=tolower(host)) print
                        if (name==zone && ($4=="A" || $4=="AAAA" || $4=="CNAME" || $4=="DS")) print
                        if (name==host && ($4=="CNAME" || $4=="AAAA" || ($4=="A" && $5!=ip))) print
                    }
                    $4=="NS" && tolower($1)!=tolower(name) && tolower($1)!=tolower(parent) {
                        owner=tolower($1); target=tolower(name)
                        if (length(target)>length(owner) && substr(target,length(target)-length(owner))=="." owner) print
                    }
                ' <<< "$response")
                if [[ -n $records ]]; then
                    conflict=1
                    DNS_PREFLIGHT_RECORDS+="$records"$'\n'
                fi
            done
        done
    done <<< "$parents"
    if [[ $conflict == 1 ]]; then
        warn 'These DNS names are already used by different records or another tunnel:'
        printf '%s' "$DNS_PREFLIGHT_RECORDS" | sort -u
        say 'MasterDNS, CottenDNS and Soar need separate tunnel subdomains.'
        say 'Keep existing service records. Choose an unused name, such as soar.your-domain.'
        say 'If you added an extra Soar NS to an existing tunnel, remove only that extra NS in your DNS panel.'
        return 1
    fi
    if [[ $checked == 0 || $failed == 1 ]]; then
        warn 'Could not check every parent DNS response. No service settings have been changed.'
        say 'Check resolver access and the main DNS zone, then retry or choose different details.'
        return 2
    fi
    ok 'Parent DNS records do not conflict with these Soar names.'
}
require_safe_dns() {
    while ! dns_preflight; do
        say '1) Enter different domain details   2) Retry after DNS changes   3) Cancel'
        ask 'Choose 1, 2 or 3' 1
        case "$REPLY" in
            1) collect_settings; show_dns_records; pause ;;
            2) ;;
            3) return 1 ;;
            *) warn 'Choose a number from the menu.' ;;
        esac
    done
}

write_unit() {
    cat > "$1" <<'UNIT'
[Unit]
Description=Soar DNS tunnel server (managed by soar-server-setup)
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=soar-tunnel
Group=soar-tunnel
EnvironmentFile=/etc/soar/server.conf
ExecStart=/usr/local/lib/soar/current/soar-server -zone ${ZONE} -key-file /etc/soar/server.key -listen ${LISTEN} -allow-ports 80,443
WorkingDirectory=/var/lib/soar
Restart=on-failure
RestartSec=3
TimeoutStopSec=15
UMask=0077
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
# AF_NETLINK is needed by Go net.InterfaceAddrs for the public-only dial guard.
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK
LockPersonality=true
TasksMax=128
MemoryMax=512M
LimitNOFILE=8192

[Install]
WantedBy=multi-user.target
UNIT
}
service_ready() {
    local i
    for i in {1..10}; do
        if systemctl is-active --quiet soar-server.service && dig +time=1 +tries=1 @"$(direct_address)" "$ZONE" SOA +short | grep -q .; then
            return 0
        fi
        sleep 1
    done
    return 1
}
begin_transaction() {
    TXN_DIR=$(mktemp -d "$STATE_DIR/.transaction.XXXXXX")
    TXN_EXISTED=0; TXN_RUNNING=0; TXN_ENABLED=0
    [[ -f $CONFIG ]] && TXN_EXISTED=1
    systemctl is-active --quiet soar-server.service && TXN_RUNNING=1
    systemctl is-enabled --quiet soar-server.service 2>/dev/null && TXN_ENABLED=1
    local file
    for file in "$CONFIG" "$UNIT" "$LIB_DIR/manager.sh"; do
        [[ ! -f $file ]] || cp -p -- "$file" "$TXN_DIR/$(basename -- "$file")"
    done
    if [[ -L $LIB_DIR/current ]]; then readlink -- "$LIB_DIR/current" > "$TXN_DIR/current-target"; fi
    printf '%s\n' "$TXN_EXISTED" > "$TXN_DIR/was-installed"
    printf '%s\n' "$TXN_RUNNING" > "$TXN_DIR/was-running"
    printf '%s\n' "$TXN_ENABLED" > "$TXN_DIR/was-enabled"
    printf 'ready\n' > "$TXN_DIR/recovery-ready"
    TXN_ACTIVE=1
}
restore_transaction() {
    [[ $TXN_ACTIVE == 1 ]] || return 0
    TXN_ACTIVE=0
    warn 'Activation did not finish. Restoring the previous server configuration.'
    systemctl stop soar-server.service >/dev/null 2>&1 || true
    local file backup
    for file in "$CONFIG" "$UNIT" "$LIB_DIR/manager.sh"; do
        backup="$TXN_DIR/$(basename -- "$file")"
        if [[ -f $backup ]]; then cp -p -- "$backup" "$file"; else rm -f -- "$file"; fi
    done
    rm -f -- "$LIB_DIR/current"
    if [[ -f $TXN_DIR/current-target ]]; then
        ln -s -- "$(cat -- "$TXN_DIR/current-target")" "$LIB_DIR/current"
    fi
    if [[ $TXN_EXISTED == 0 ]]; then rm -f -- "$MANAGER"; fi
    systemctl daemon-reload || true
    if [[ $TXN_ENABLED == 1 ]]; then systemctl enable soar-server.service >/dev/null 2>&1 || true
    else systemctl disable soar-server.service >/dev/null 2>&1 || true; fi
    if [[ $TXN_RUNNING == 1 ]]; then
        systemctl reset-failed soar-server.service >/dev/null 2>&1 || true
        if ! systemctl start soar-server.service; then warn 'Previous configuration restored, but the service did not restart. Run systemctl status soar-server.service.'; fi
    fi
    printf 'restored\n' > "$TXN_DIR/rolled-back"
}
recover_pending() {
    is_managed || return 0
    local pending target
    for pending in "$STATE_DIR"/.transaction.*; do
        [[ -d $pending && -f $pending/recovery-ready && ! -f $pending/committed && ! -f $pending/rolled-back ]] || continue
        warn 'An interrupted activation was found. Restoring its saved configuration before continuing.'
        TXN_DIR=$pending
        TXN_EXISTED=$(cat -- "$pending/was-installed")
        TXN_RUNNING=$(cat -- "$pending/was-running")
        TXN_ENABLED=$(cat -- "$pending/was-enabled")
        [[ $TXN_EXISTED =~ ^[01]$ && $TXN_RUNNING =~ ^[01]$ && $TXN_ENABLED =~ ^[01]$ ]] || die "Recovery metadata is invalid. Keep $pending for manual recovery."
        if [[ $TXN_EXISTED == 1 ]]; then
            [[ -f $pending/server.conf && -f $pending/soar-server.service && -f $pending/manager.sh && -f $pending/current-target ]] || die "Recovery backup is incomplete. Keep $pending for manual recovery."
        fi
        if [[ -f $pending/current-target ]]; then
            target=$(cat -- "$pending/current-target")
            [[ $target =~ ^releases/[a-zA-Z0-9][a-zA-Z0-9._-]*$ ]] || die 'Recovery release path is invalid.'
        fi
        TXN_ACTIVE=1
        restore_transaction
        TXN_DIR=
    done
}
finish_transaction() {
    printf 'committed\n' > "$TXN_DIR/committed"
    TXN_ACTIVE=0
    rm -rf -- "$TXN_DIR"
    TXN_DIR=
}
cleanup() {
    local rc=$?
    trap - EXIT
    if [[ $TXN_ACTIVE == 1 ]]; then restore_transaction; fi
    if [[ -n $WORK_DIR && -d $WORK_DIR ]]; then rm -rf -- "$WORK_DIR"; fi
    if [[ $rc != 0 ]]; then
        warn 'Setup stopped. Your private key was not printed or replaced.'
        say 'Read the error above, fix it, and run the same command again.' >&2
    fi
    exit "$rc"
}
activate_release() {
    begin_transaction
    local temp
    temp=$(mktemp "$CONFIG_DIR/.config.XXXXXX")
    write_settings "$temp"
    chown root:soar-tunnel "$temp"; chmod 640 "$temp"; mv -f -- "$temp" "$CONFIG"
    temp=$(mktemp "$(dirname -- "$UNIT")/.soar-unit.XXXXXX")
    install -m 644 -- "$LIB_DIR/releases/$CURRENT_RELEASE/soar-server.service" "$temp"; mv -f -- "$temp" "$UNIT"
    temp=$(mktemp "$LIB_DIR/.manager.XXXXXX")
    install -m 755 -- "$LIB_DIR/releases/$CURRENT_RELEASE/manager.sh" "$temp"; mv -f -- "$temp" "$LIB_DIR/manager.sh"
    rm -f -- "$LIB_DIR/.current-next"
    ln -s -- "releases/$CURRENT_RELEASE" "$LIB_DIR/.current-next"
    mv -Tf -- "$LIB_DIR/.current-next" "$LIB_DIR/current"
    ln -sfn -- "$LIB_DIR/manager.sh" "$MANAGER"
    systemctl daemon-reload
    systemctl enable soar-server.service
    systemctl reset-failed soar-server.service >/dev/null 2>&1 || true
    systemctl restart soar-server.service
    service_ready || die 'The service did not pass its local DNS test. Activation will be rolled back. Check journalctl -u soar-server.service -n 40.'
    finish_transaction
}
prepare_release() {
    local release="$LIB_DIR/releases/$RELEASE_ID" temp
    if [[ -d $release ]]; then
        cmp -s -- "$BUNDLE_DIR/bin/linux-amd64/soar-server" "$release/soar-server" &&
        cmp -s -- "$BUNDLE_DIR/bin/linux-amd64/soar-check" "$release/soar-check" || die 'An existing release has different bytes. Keep it for investigation and use a fresh bundle.'
        return
    fi
    temp=$(mktemp -d "$LIB_DIR/releases/.stage.XXXXXX")
    install -m 755 -- "$BUNDLE_DIR/bin/linux-amd64/soar-server" "$temp/soar-server"
    install -m 755 -- "$BUNDLE_DIR/bin/linux-amd64/soar-check" "$temp/soar-check"
    install -m 644 -- "$BUNDLE_DIR/SOURCE-VERSIONS.txt" "$temp/SOURCE-VERSIONS.txt"
    install -m 755 -- "$BUNDLE_DIR/install.sh" "$temp/manager.sh"
    write_unit "$temp/soar-server.service"; chmod 644 "$temp/soar-server.service"
    chmod 755 "$temp"
    mv -- "$temp" "$release"
}
prepare_directories() {
    # Record ownership before creating the service account, so an interrupted
    # first install can resume without mistaking its account for an unrelated one.
    install -d -m 700 "$STATE_DIR"
    printf '%s\n' soar-server-setup-v1 > "$MARKER"
    chmod 600 "$MARKER"
    if ! getent group soar-tunnel >/dev/null; then groupadd --system soar-tunnel; fi
    if ! getent passwd soar-tunnel >/dev/null; then
        useradd --system --gid soar-tunnel --home-dir /nonexistent --shell /usr/sbin/nologin soar-tunnel
    fi
    [[ $(id -u soar-tunnel) != 0 ]] || die 'The service account must not be root.'
    getent group soar-tunnel >/dev/null || die 'Missing service group soar-tunnel. Resolve the account conflict before continuing.'
    install -d -m 750 -o root -g soar-tunnel "$CONFIG_DIR" "$STATE_DIR"
    install -d -m 755 "$LIB_DIR" "$LIB_DIR/releases" "$(dirname -- "$MANAGER")"
}
prepare_key() {
    [[ ! -L $KEY ]] || die 'The private key must not be a symlink.'
    [[ ! -e $KEY || -f $KEY ]] || die 'The private key path is not a regular file.'
    if [[ ! -f $KEY ]]; then
        local temp
        temp=$(mktemp "$CONFIG_DIR/.key.XXXXXX")
        "$LIB_DIR/releases/$RELEASE_ID/soar-server" keygen | awk '$1 == "private" {print $2}' > "$temp"
        "$LIB_DIR/releases/$RELEASE_ID/soar-server" pubkey "$temp" >/dev/null
        chown root:soar-tunnel "$temp"; chmod 640 "$temp"; mv -- "$temp" "$KEY"
    else
        [[ ! -L $KEY ]] || die 'The private key must not be a symlink.'
        "$LIB_DIR/releases/$RELEASE_ID/soar-server" pubkey "$KEY" >/dev/null || die 'Existing key is invalid. Restore your backup; it will not be overwritten.'
        chown root:soar-tunnel "$KEY"; chmod 640 "$KEY"
        ok 'Existing private key preserved.'
    fi
}
firewall_guide() {
    say 'DNS queries must reach this VPS on UDP port 53. TCP/53 is not required by this Soar server.'
    say 'In your VPS provider panel, open Firewall / Security group and add:'
    say '  Direction: Inbound   Protocol: UDP   Port: 53   Source: 0.0.0.0/0'
    say 'Keep your existing SSH rule (usually TCP/22). Save the firewall rule.'
    say 'If this VPS is behind your own router, forward public UDP/53 to the listen address shown above.'
    if command -v ufw >/dev/null && ufw status | grep -q '^Status: active'; then
        say ''
        say 'The VPS also has an active UFW firewall.'
        if ufw status | grep -q '53/udp'; then
            say 'A UDP/53 rule already exists. Review it to make sure it allows inbound DNS.'
            ufw status
        elif confirm 'Add one UFW allow rule for UDP/53, leaving all other rules unchanged?'; then
            ufw allow 53/udp comment 'soar-server-setup'
            printf '%s\n' ufw-udp53 > "$STATE_DIR/ufw-owned"
        else
            say 'To allow it manually, run in another SSH window: sudo ufw allow 53/udp'
        fi
    else
        say 'UFW is not active. This setup will not enable it or replace your firewall rules.'
        say 'If you use nftables, iptables, firewalld or another firewall, allow inbound UDP/53 there as well.'
    fi
    say 'The VPS also needs outbound DNS and outbound TCP/80 and TCP/443.'
    pause
}
dns_query() {
    local resolver=$1 name=$2 type=$3
    dig +time=3 +tries=1 @"$resolver" "$name" "$type" +short 2>/dev/null
}
dns_check() {
    local resolver ns a_ok=0 parent_ok=0 soa_ok=0 parent_count=0 matched=0 ds=0 response addresses
    local -a resolvers parents
    IFS=, read -r -a resolvers <<< "$RESOLVERS"
    for resolver in "${resolvers[@]}"; do
        addresses=$(dns_query "$resolver" "$NS_HOST" A) || addresses=
        if [[ $addresses == "$PUBLIC_IP" ]]; then a_ok=1; fi
        mapfile -t parents < <(dns_query "$resolver" "$PARENT_ZONE" NS || true)
        for ns in "${parents[@]}"; do
            valid_domain "${ns%.}" || continue
            parent_count=$((parent_count + 1))
            response=$(dig +time=3 +tries=1 @"$ns" "$ZONE" NS +norecurse +noall +answer +authority 2>/dev/null) || response=
            if awk -v zone="$ZONE." -v host="$NS_HOST." 'tolower($1)==zone && $4=="NS" {if (tolower($5)==host) found=1; else other=1} END {exit !(found && !other)}' <<< "$response"; then
                matched=$((matched + 1)); parent_ok=1
            fi
            response=$(dig +time=3 +tries=1 @"$ns" "$ZONE" DS +norecurse +noall +answer 2>/dev/null) || response=
            if awk -v zone="$ZONE." 'tolower($1)==zone && $4=="DS" {found=1} END {exit !found}' <<< "$response"; then ds=1; fi
        done
        response=$(dns_query "$resolver" "$ZONE" SOA) || response=
        [[ -z $response ]] || soa_ok=1
    done
    if [[ $a_ok == 1 ]]; then ok 'Nameserver A record matches the expected public IPv4.'
    else warn 'Nameserver A record does not match yet. Check the A record, DNS-only mode and caches.'; fi
    if [[ $parent_ok == 1 ]]; then ok 'A parent nameserver returned the expected NS delegation.'
    else warn 'NS delegation was not confirmed. Add the NS record in the main DNS zone, not at your registrar nameserver screen. Check for outdated NS records on this tunnel name.'; fi
    if (( matched < parent_count )); then warn 'Some parent nameservers did not confirm the record. The update may still be propagating or a DNS path may be blocked.'; fi
    if [[ $soa_ok == 1 ]]; then ok 'A recursive resolver returned an SOA response.'
    else warn 'Recursive SOA lookup failed. Check delegation, UDP/53, firewall and DNS caches.'; fi
    if [[ $ds == 1 ]]; then
        warn 'A DS record exists for the tunnel subdomain. This Soar server does not sign DNSSEC.'
        say 'Review the DS record for this subdomain with your DNS provider. Do not disable DNSSEC for the main domain.'
    fi
    [[ $a_ok == 1 && $parent_ok == 1 && $soa_ok == 1 && $ds == 0 ]]
}
public_key() { "$LIB_DIR/current/soar-server" pubkey "$KEY"; }
profile_link() {
    local country=${1:-IR} name public
    [[ $country == IR || $country == GLOBAL ]] || return 1
    name="Soar $country - Auto discovery"
    [[ $country != GLOBAL ]] || country=
    public=$(public_key) || return 1
    printf '{"schema":"whitedns.profile","version":1,"profile":{"name":"%s","server":{"domain":"%s","public_key":"%s"},"resolvers":["1.1.1.1","8.8.8.8"],"soarSettings":{"country":"%s","discovery":true,"transport":"auto","dialTimeoutSeconds":90,"discoveryConcurrency":4}}}' \
        "$name" "$ZONE" "$public" "$country" | base64 -w 0 | tr '/+' '_-' | tr -d '=' | sed 's/^/soar:\/\//'
    printf '\n'
}
profile_command() {
    load_settings || die 'Configuration is missing or invalid.'
    say 'Choose the network where the PHONE is located, not the server location.'
    say 'IR uses Iranian candidates plus network DNS and global fallback. GLOBAL is for other countries.'
    local country
    while :; do
        ask 'Client network: IR or GLOBAL' IR
        country=${REPLY^^}
        [[ $country == IR || $country == GLOBAL ]] && break
        warn 'Enter IR or GLOBAL.'
    done
    say 'Copy the entire soar:// line into WhiteDNS Soar Alpha3 (or newer), Import connection.'
    profile_link "$country"
    say 'Discovery stays enabled. The public resolver addresses are bootstrap hints, not a required single path.'
    say 'For restrictive DNS networks, try the Conservative preset and scan local resolvers with this Soar profile.'
}
probe() {
    local resolvers=$1 mode=${2:-auto}
    "$LIB_DIR/current/soar-check" -zone "$ZONE" -pubkey "$(public_key)" -resolver "$resolvers" -tcp "$mode" -timeout 40s
}
test_connection() {
    load_settings || die 'Configuration is missing or invalid. Restore /etc/soar/server.conf from your backup.'
    say 'This test makes an HTTPS request through Soar to https://example.com/.'
    say 'It verifies the server public key and the HTTPS certificate. It does not test the user phone or an external network.'
    if ! systemctl is-active --quiet soar-server.service; then warn 'Server service is not running. Use Status or View logs first.'; return 1; fi
    say ''; say 'Test A: local DNS listener + authenticated tunnel + outbound HTTPS'
    local direct direct_ok=0 recursive_ok=0 dns_ok=0
    direct=$(direct_address)
    if probe "$direct" off; then direct_ok=1
    else warn 'Local test failed. Check service logs, outbound DNS, web access and CA certificates.'; fi
    say ''; say 'Test B: DNS records and delegation'
    if dns_check; then dns_ok=1; fi
    say ''; say 'Test C: recursive DNS path + authenticated tunnel + outbound HTTPS'
    if probe "$RESOLVERS" auto; then recursive_ok=1
    else
        warn 'Recursive tunnel test failed. Check provider UDP/53, delegation, DNS caches and resolver availability.'
        say 'If local test A passed, reinstalling or changing the server key is unlikely to help.'
    fi
    say ''; say 'External-network test: NOT PERFORMED. This command runs on the VPS network.'
    local report
    report=$(mktemp "$STATE_DIR/.checks.XXXXXX")
    printf 'local=%s\ndns=%s\nrecursive=%s\nexternal=not_performed\nchecked_at=%s\n' \
        "$direct_ok" "$dns_ok" "$recursive_ok" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$report"
    chmod 600 "$report"; mv -f -- "$report" "$STATE_DIR/last-checks.txt"
    if [[ $direct_ok == 1 && $recursive_ok == 1 && $dns_ok == 1 ]]; then
        ok 'Server-side checks passed. A later test from the user network is still required.'
        return 0
    fi
    warn 'Server setup is installed, but one or more checks are pending. Fix the items above and choose Test connection again.'
    return 1
}
show_details() {
    load_settings || die 'Configuration is missing or invalid.'
    say ''; say 'Connection details (the private key is NOT included):'
    printf '  Domain: %s\n  Public key: %s\n  Server IPv4: %s\n  Listen: %s\n  Soar version: %s\n  Release: %s\n' "$ZONE" "$(public_key)" "$PUBLIC_IP" "$LISTEN" "$SOAR_VERSION" "$CURRENT_RELEASE"
    say 'This server requires the Soar engine; existing MasterDNS/CottenDNS engines are not compatible.'
    say 'Anyone who obtains the domain and public key can use this server. Soar has no per-user login.'
    say "Private key backup location: $KEY (keep it secret; it is not displayed)."
    say 'Importable Iran profile (automatic resolver discovery; requires the Soar-enabled WhiteDNS build):'
    profile_link IR
    say 'For a different client network, run: sudo soar-manager profile'
}
write_details() {
    local temp
    temp=$(mktemp "$CONFIG_DIR/.details.XXXXXX")
    {
        say 'format=soar-server-details-v1'
        printf 'zone=%s\nserver_public_key=%s\npublic_ipv4=%s\nsoar_version=%s\n' "$ZONE" "$(public_key)" "$PUBLIC_IP" "$SOAR_VERSION"
        say 'external_network_test=not_performed'
    } > "$temp"
    chmod 600 "$temp"; mv -f -- "$temp" "$CONFIG_DIR/connection-details.txt"
}
status_command() {
    if ! is_installed; then say 'No complete managed Soar installation was found.'; return 1; fi
    load_settings || die 'Invalid server configuration.'
    say "Installed release: $CURRENT_RELEASE"
    systemctl --no-pager --full status soar-server.service || true
    say ''; say 'UDP/53 listeners:'; ss -H -lunp 'sport = :53'
}
doctor() {
    status_command || return 1
    say ''; show_dns_records
    say ''; test_connection
}
install_wizard() {
    if is_installed; then
        ok 'Soar is already installed. The key and settings were left unchanged.'
        say 'Use sudo soar-manager for tests, logs and management. Use a new extracted bundle for updates.'
        return 0
    fi
    step 1 'Check requirements'
    say 'You need a VPS, a domain with editable DNS records, and access to the VPS firewall panel.'
    say 'Keep this SSH window open. The setup does not change SSH or replace your DNS resolver.'
    say 'You can stop with Ctrl+C. Run the same command again to retry.'
    check_owned_paths
    verify_bundle || die 'The setup bundle is missing, incomplete or damaged.'
    dependencies
    ok 'Ubuntu 24.04 amd64 and systemd detected.'
    step 2 'Enter server and domain details'
    collect_settings
    require_safe_dns || die 'Cancelled before service changes.'
    step 3 'Create two DNS records'
    show_dns_records
    pause
    require_safe_dns || die 'Cancelled before service changes.'
    step 4 'Review and install the server'
    printf '  Tunnel: %s\n  Nameserver: %s\n  Public IPv4: %s\n  Listen: %s\n' "$ZONE" "$NS_HOST" "$PUBLIC_IP" "$LISTEN"
    say 'The server will run as a dedicated system account and start automatically after reboot.'
    say 'Only public web destinations on TCP/80 and TCP/443 are allowed. Private destinations stay blocked.'
    say 'Important: the domain and PUBLIC key allow use of this server. There are no per-user passwords or quotas.'
    confirm 'Install this configuration now?' || die 'Installation cancelled before service changes.'
    prepare_directories
    prepare_release
    prepare_key
    CURRENT_RELEASE=$RELEASE_ID; PREVIOUS_RELEASE=
    validate_settings || die 'Configuration validation failed.'
    activate_release
    write_details
    ok 'Server installed and local DNS listener verified.'
    step 5 'Allow DNS through the firewalls'
    firewall_guide
    step 6 'Test the server and fix pending checks'
    while ! test_connection; do
        say ''; say '1) Retry after fixing DNS / firewall   2) Change test resolvers   3) Save and finish with checks pending'
        ask 'Choose 1, 2 or 3' 1
        case "$REPLY" in
            1) ;;
            2)
                ask 'Resolver IPv4 addresses, separated by commas' "$RESOLVERS"
                if valid_resolvers "$REPLY"; then
                    RESOLVERS=$REPLY
                    local temp
                    temp=$(mktemp "$CONFIG_DIR/.config.XXXXXX"); write_settings "$temp"
                    chown root:soar-tunnel "$temp"; chmod 640 "$temp"; mv -f -- "$temp" "$CONFIG"
                else warn 'Invalid resolver list. Settings were not changed.'; fi ;;
            3) break ;;
            *) warn 'Choose a number from the menu.' ;;
        esac
    done
    step 7 'Save details and learn the management commands'
    show_details
    say ''; say "Connection details saved to $CONFIG_DIR/connection-details.txt (root access only)."
    say 'Next time, run: sudo soar-manager'
    say 'For errors, choose View logs or Diagnose and follow the suggestions.'
}
configure_command() {
    local MANAGED_LISTENER_PID
    is_installed || die 'Install the server first.'
    load_settings || die 'Invalid server configuration.'
    MANAGED_LISTENER_PID=$(systemctl show soar-server.service -p MainPID --value)
    say 'Change the managed Soar domain or addresses. The private key and installed binaries are preserved.'
    say 'Existing DNS records and other tunnel services are not edited. Use a separate, unused subdomain.'
    collect_settings
    require_safe_dns || return 0
    show_dns_records
    pause
    require_safe_dns || return 0
    confirm 'Activate these new Soar settings now?' || return 0
    validate_settings || die 'Configuration validation failed.'
    activate_release
    write_details
    ok 'New settings activated. The server key was preserved.'
    if ! test_connection; then warn 'Some checks are pending. Run sudo soar-manager test after DNS propagation.'; fi
    show_details
}
update_command() {
    is_installed || die 'Install the server first.'
    verify_bundle || die 'Extract the new full bundle, then run: sudo bash install.sh update'
    load_settings || die 'Invalid server configuration.'
    if [[ $CURRENT_RELEASE == "$RELEASE_ID" ]]; then ok 'This exact release is already installed. No changes made.'; return 0; fi
    say 'The update keeps your domain, firewall configuration and private key. The service briefly restarts.'
    say "Current release: $CURRENT_RELEASE"
    say "New release:     $RELEASE_ID"
    confirm 'Activate this bundled release?' || return 0
    prepare_release
    PREVIOUS_RELEASE=$CURRENT_RELEASE; CURRENT_RELEASE=$RELEASE_ID
    activate_release
    write_details
    ok 'New release activated; local DNS test passed.'
    if ! test_connection; then warn 'End-to-end checks are pending. Use Rollback if the previous release worked.'; fi
}
rollback_command() {
    load_settings || die 'Invalid server configuration.'
    [[ -n $PREVIOUS_RELEASE && -x $LIB_DIR/releases/$PREVIOUS_RELEASE/soar-server ]] || die 'No previous release is available.'
    say "Previous release: $PREVIOUS_RELEASE. The current private key will be preserved."
    confirm 'Switch back to this release?' || return 0
    local old=$CURRENT_RELEASE
    CURRENT_RELEASE=$PREVIOUS_RELEASE; PREVIOUS_RELEASE=$old
    activate_release
    write_details
    ok 'Previous binaries restored. Run Test connection to check the recursive path.'
}
uninstall_command() {
    is_managed || die 'This setup does not own the server files. Nothing removed.'
    say 'This stops Soar and removes its service, installed binaries and manager.'
    say 'Your private key will be kept unless you explicitly choose to delete it.'
    say 'Other services, SSH rules, DNS records and provider firewall settings will not be removed.'
    ask 'Type UNINSTALL to continue'
    [[ $REPLY == UNINSTALL ]] || { say 'Cancelled.'; return 0; }
    systemctl disable --now soar-server.service || true
    rm -f -- "$UNIT" "$MANAGER"
    systemctl daemon-reload
    systemctl reset-failed soar-server.service 2>/dev/null || true
    if [[ -f $STATE_DIR/ufw-owned ]] && command -v ufw >/dev/null && ufw status | grep -q 'soar-server-setup'; then
        if confirm 'Remove the UFW UDP/53 rule created by this setup?'; then
            ufw --force delete allow 53/udp comment 'soar-server-setup'
            rm -f -- "$STATE_DIR/ufw-owned"
        fi
    fi
    rm -rf -- "$LIB_DIR"
    rm -f -- "$CONFIG" "$CONFIG_DIR/connection-details.txt"
    if [[ -f $KEY ]]; then
        ask 'Press Enter to KEEP the private key, or type DELETE KEY to permanently delete it'
        if [[ $REPLY == 'DELETE KEY' ]]; then rm -f -- "$KEY"; else say "Private key kept at $KEY. Back it up securely."; fi
    fi
    say 'The ownership marker and service account are kept for a safe reinstall.'
    say 'Remove the two DNS records manually if you no longer need this tunnel.'
    ok 'Soar service and binaries removed.'
}
menu() {
    local choice
    while :; do
        say ''; say 'Soar server manager'
        say '  1) Status - see whether the server service is running'
        say '  2) Test connection - check DNS, server authentication and HTTPS'
        say '  3) Show connection details - domain and PUBLIC key'
        say '  4) View logs - inspect the last 60 service messages'
        say '  5) Diagnose - show service status, required DNS records and tests'
        say '  6) Restart - restart only the Soar service'
        say '  7) Update - instructions for using a newer setup bundle'
        say '  8) Rollback - activate the previously installed server binaries'
        say '  9) Uninstall - remove this managed server installation'
        say ' 10) Change domain / addresses - preserve the server key'
        say ' 11) Client profile - generate an import link for IR or GLOBAL'
        say '  0) Exit'
        ask 'Choose a number' 0; choice=$REPLY
        case "$choice" in
            1) status_command || true ;;
            2) test_connection || true ;;
            3) show_details ;;
            4) journalctl -u soar-server.service -n 60 --no-pager ;;
            5) doctor || true ;;
            6) systemctl restart soar-server.service; ok 'Restart requested. Choose Test connection to verify it.' ;;
            7) say 'Obtain the new bundle from your trusted distributor, copy it to the VPS, and extract it.'
               say 'From the new extracted folder run: sudo bash install.sh update' ;;
            8) rollback_command ;;
            9) uninstall_command; return ;;
            10) configure_command ;;
            11) profile_command ;;
            0) return ;;
            *) warn 'Choose a number from 0 to 11.' ;;
        esac
    done
}
usage() {
    cat <<'HELP'
Soar server setup 0.1.2 - Ubuntu 24.04 LTS, amd64

First install: extract the complete bundle, then run sudo bash install.sh
Later:        sudo soar-manager

Commands: install, status, test, details, doctor, logs, restart,
          update, rollback, configure, profile, uninstall
Read-only bundle check: bash install.sh --check-bundle
Help:                  bash install.sh --help

The wizard explains each step. A domain and a VPS with inbound UDP/53 are needed.
This is the server installer. It does not install a phone or desktop client.
HELP
}
main() {
    local action=${1:-}
    [[ $# -le 1 ]] || die 'Use one command. Run bash install.sh --help.'
    case "$action" in
        --help|-h|help) usage; return ;;
        --check-bundle) verify_bundle; return ;;
        ''|install|status|test|details|doctor|logs|restart|profile|update|rollback|configure|uninstall) ;;
        *) die 'Unknown command. Run bash install.sh --help.' ;;
    esac
    require_system
    trap cleanup EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    trap 'exit 129' HUP
    recover_pending
    if [[ -z $action ]]; then
        if is_installed; then action=menu; else action=install; fi
    fi
    case "$action" in
        install) install_wizard ;;
        menu) menu ;;
        status) status_command ;;
        test) test_connection ;;
        details) show_details ;;
        profile) profile_command ;;
        doctor) doctor ;;
        logs) journalctl -u soar-server.service -n 60 --no-pager ;;
        restart) is_installed || die 'No managed server installed.'; systemctl restart soar-server.service; status_command ;;
        update) update_command ;;
        configure) configure_command ;;
        rollback) rollback_command ;;
        uninstall) uninstall_command ;;
    esac
}
if [[ ${BASH_SOURCE[0]} == "$0" ]]; then main "$@"; fi
