#!/usr/bin/env bash
# Original WhiteDNS installer. English-only terminal output for SSH compatibility.
set -Eeuo pipefail
export LC_ALL=C
umask 077
ROOT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
PANEL_VERSION=0.2.0-beta1
DB=/var/lib/soar-panel/panel.db
SERVICE=/etc/systemd/system/soar-panel.service
BIN="$ROOT_DIR/bin/linux-amd64/soar-panel"
die(){ printf '\n[ERROR] %s\n' "$*" >&2; exit 1; }
check_bundle(){
 [[ -f "$ROOT_DIR/SHA256SUMS" && -f "$BIN" ]] || die 'Incomplete panel bundle. Download and extract the full archive.'
 (cd "$ROOT_DIR" && sha256sum --check --status SHA256SUMS) || die 'Bundle checksum failed. Do not install this copy.'
 printf '[OK] Panel bundle checksums verified.\n'
}
if [[ ${1:-} == --check-bundle ]]; then check_bundle; exit 0; fi
[[ $# == 0 ]] || die 'Usage: bash panel-install.sh [--check-bundle]'
[[ $EUID == 0 ]] || die 'Run this installer with sudo bash panel-install.sh'
[[ -r /etc/os-release ]] || die 'This installer requires Ubuntu 22.04+ or Debian 12+.'
. /etc/os-release
case "$ID:${VERSION_ID%%.*}" in ubuntu:22|ubuntu:24|ubuntu:26|debian:12|debian:13) ;; *) die 'Supported systems: Ubuntu 22.04/24.04/26.04, Debian 12/13.' ;; esac
[[ $(uname -m) == x86_64 ]] || die 'This beta bundle is for amd64 servers only.'
for command in systemctl ss ip sha256sum install useradd awk curl dig; do command -v "$command" >/dev/null || die "Required command is missing: $command. On Ubuntu/Debian, run apt-get update && apt-get install -y curl dnsutils iproute2, then retry."; done
[[ -t 0 ]] || die 'Run in an interactive SSH terminal; do not pipe the installer into bash.'
check_bundle
printf '\nWhiteDNS Soar Panel %s - EXPERIMENTAL\n' "$PANEL_VERSION"
printf 'This is a server-side panel. Existing WhiteDNS apps do not need an update.\n'
printf 'The Soar tunneling engine is developed by Lantern: https://soar.lantern.io/\n'
printf 'Use a test server first. This wizard will explain every change before activation.\n'
printf '\n[1/7] Check server and existing services\n'
old_active=0; panel_active=0
systemctl is-active --quiet soar-server.service && old_active=1
systemctl is-active --quiet soar-panel.service && panel_active=1
printf 'Existing standalone Soar active: %s; existing panel active: %s\n' "$old_active" "$panel_active"
printf '\n[2/7] Check your DNS delegation\n'
printf 'Choose a dedicated zone, for example soar.example.com. Do not reuse a MasterDNS zone.\n'
printf 'In your DNS provider, create A: ns-soar -> YOUR_PUBLIC_IPV4 (DNS-only, proxy OFF).\n'
printf 'Then create NS: soar -> ns-soar.example.com. Replace example.com with your domain.\n'
printf 'Keep all existing MasterDNS/Cotton records unchanged. Each panel account needs no extra record.\n'
read -r -p 'Delegated base zone (max 24 ASCII characters): ' zone
[[ ${#zone} -ge 3 && ${#zone} -le 24 && $zone =~ ^[a-z0-9][a-z0-9.-]*[a-z0-9]$ && $zone == *.* ]] || die 'Invalid or too-long base zone.'
read -r -p "Main DNS zone [${zone#*.}]: " parent_zone
parent_zone=${parent_zone:-${zone#*.}}
read -r -p "Nameserver hostname [ns-soar.$parent_zone]: " ns_host
ns_host=${ns_host:-ns-soar.$parent_zone}
default_ip=$(ip -o -4 addr show scope global | awk 'NR==1{split($4,a,"/");print a[1]}')
read -r -p "Local IPv4 to bind DNS [${default_ip:-none}]: " listen_ip
listen_ip=${listen_ip:-$default_ip}
[[ $listen_ip =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]] || die 'Enter the IPv4 assigned to this server interface.'
ip -o -4 addr show | awk '{split($4,a,"/");print a[1]}' | awk -v target="$listen_ip" '$0==target{found=1}END{exit !found}' || die 'That IP is not assigned to a local interface. Behind NAT, use the local IP here.'
read -r -p "Public IP or SSH hostname for your access instructions [$listen_ip]: " ssh_host
ssh_host=${ssh_host:-$listen_ip};[[ $ssh_host =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]*$ ]] || die 'Invalid SSH hostname.'
read -r -p "Public IPv4 in your nameserver A record [$listen_ip]: " public_ip
public_ip=${public_ip:-$listen_ip}
printf 'UDP/53 must reach %s through your provider firewall and any NAT.\n' "$listen_ip"
# Run the stable installer's extensively tested DNS checks in a subshell. Sourcing
# the library never calls its main or alters the panel installer's variables/traps.
(
 source "$ROOT_DIR/lib/standalone-install.sh"
 ZONE=$zone;PARENT_ZONE=$parent_zone;NS_HOST=$ns_host;PUBLIC_IP=$public_ip;RESOLVERS=1.1.1.1,8.8.8.8
 valid_zone "$ZONE" && valid_domain "$PARENT_ZONE" && valid_domain "$NS_HOST" && public_ipv4 "$PUBLIC_IP" || die 'Invalid DNS names or public IPv4.'
 [[ $ZONE == *."$PARENT_ZONE" && $NS_HOST != "$ZONE" && $NS_HOST != *."$ZONE" ]] || die 'Use a child tunnel zone and an independent nameserver hostname.'
 dns_preflight || die 'DNS conflict or unreachable authoritative DNS. Correct the records before installing the panel.'
 # SOA may be unavailable on a fresh server until this service starts. Delegation
 # and A records must already exist, and the tunnel must not carry a stale DS.
 if ! dns_check;then
  read -r -p 'DNS is not fully ready. Type CONTINUE only if A/NS are correct and you accept a test installation: ' dns_answer
  [[ $dns_answer == CONTINUE ]] || die 'Cancelled before service changes. Fix DNS records and retry.'
 fi
)
printf '\n[3/7] Administrator account\n'
password='';pwfile=''
cleanup(){ [[ -z $pwfile ]] || rm -f -- "$pwfile"; unset password password2; }
trap cleanup EXIT
if [[ -e $DB ]]; then
 printf 'Existing database found. Its accounts, keys, admin password and usage will be preserved.\n'
else
 read -r -p 'Administrator name [admin]: ' admin;admin=${admin:-admin}
 [[ $admin =~ ^[a-zA-Z0-9_.-]{1,64}$ ]] || die 'Use 1-64 letters, numbers, dots, underscores or hyphens for the admin name.'
 printf 'Choose a unique 12-72 character ASCII password. Input is hidden.\n'
 read -r -s -p 'New administrator password: ' password;printf '\n'
 read -r -s -p 'Repeat password: ' password2;printf '\n'
 [[ $password == "$password2" && ${#password} -ge 12 && ${#password} -le 72 && $password != *[![:print:]]* ]] || die 'Passwords must match and be 12-72 printable ASCII characters.'
 unset password2
fi
printf '\n[4/7] Secure panel access\n'
printf 'Management listens on 127.0.0.1:2053 only. It is not exposed to the Internet.\n'
printf 'From your own computer, use an SSH tunnel and open http://127.0.0.1:2053 .\n'
printf 'For public HTTPS, follow docs/PANEL.fa.md; no automatic web-server changes are made.\n'
printf '\n[5/7] Review before activation\n'
printf 'DNS listener: %s:53/UDP\nPanel listener: 127.0.0.1:2053\nDatabase: %s\n' "$listen_ip" "$DB"
printf 'Service user: soar-panel; destination ports: 80 and 443 (editable in panel).\n'
printf 'Default discovery hint: Iran; bootstrap DNS: 1.1.1.1 and 8.8.8.8 (editable).\n'
printf 'These resolvers are starting points, not a guarantee of reachability in every network.\n'
if (( old_active ));then
 printf '\nWARNING: the standalone soar-server service must stop to free this DNS listener.\n'
 printf 'Its keys and files are kept. Existing shared-root profiles will STOP WORKING after migration.\n'
 printf 'Create panel accounts and distribute their new links. This is a planned migration.\n'
 read -r -p 'Type MIGRATE to accept this migration, or anything else to cancel: ' answer
 [[ $answer == MIGRATE ]] || die 'Cancelled. The existing service was not changed.'
else
 read -r -p 'Type INSTALL to install/start this experimental panel, or anything else to cancel: ' answer
 [[ $answer == INSTALL ]] || die 'Cancelled. No service was changed.'
fi
printf '\n[6/7] Install and verify\n'
backup="/root/soar-panel-backup-$(date -u +%Y%m%dT%H%M%SZ)-$$"
install -d -m 0700 "$backup"
[[ ! -f $SERVICE ]] || cp -p -- "$SERVICE" "$backup/soar-panel.service"
[[ ! -f /usr/local/bin/soar-panel ]] || cp -p -- /usr/local/bin/soar-panel "$backup/soar-panel"
activated=0
rollback(){
 result=$?
 if (( result && ! activated ));then
  printf '\n[ERROR] Activation failed. Restoring previously running services.\n' >&2
  systemctl stop soar-panel.service 2>/dev/null || true
  [[ ! -f "$backup/soar-panel" ]] || install -m 0755 "$backup/soar-panel" /usr/local/bin/soar-panel
  [[ ! -f "$backup/soar-panel.service" ]] || install -m 0644 "$backup/soar-panel.service" "$SERVICE"
  systemctl daemon-reload
  (( ! panel_active )) || systemctl start soar-panel.service || true
  (( ! old_active )) || systemctl start soar-server.service || true
  printf 'Private backup: %s\n' "$backup" >&2
 fi
 cleanup
}
trap rollback EXIT
(( ! panel_active )) || systemctl stop soar-panel.service
[[ ! -f $DB ]] || install -m 0600 "$DB" "$backup/panel.db"
if [[ -f $DB ]];then
 existing_zone=$("$BIN" zone --db "$DB")
 [[ $existing_zone == "$zone" ]] || die "This database uses $existing_zone. Rerun with that zone; existing profiles cannot be silently moved."
fi
id soar-panel >/dev/null 2>&1 || useradd --system --home-dir /var/lib/soar-panel --shell /usr/sbin/nologin soar-panel
install -d -o soar-panel -g soar-panel -m 0700 /var/lib/soar-panel
install -m 0755 "$BIN" /usr/local/bin/soar-panel
if [[ ! -f $DB ]];then
 pwfile=$(mktemp /root/soar-panel-password.XXXXXX);printf '%s' "$password" >"$pwfile";unset password
 /usr/local/bin/soar-panel init --db "$DB" --zone "$zone" --admin "$admin" --password-file "$pwfile"
 rm -f -- "$pwfile";pwfile=''
fi
chown soar-panel:soar-panel "$DB";chmod 0600 "$DB"
cat >"$SERVICE" <<UNIT
[Unit]
Description=WhiteDNS Soar Management Panel (beta)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=soar-panel
Group=soar-panel
ExecStart=/usr/local/bin/soar-panel run --db /var/lib/soar-panel/panel.db --dns-listen $listen_ip:53 --listen 127.0.0.1:2053 --trust-loopback-proxy
Restart=on-failure
RestartSec=3
TimeoutStopSec=10
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/soar-panel
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
UMask=0077
LimitNOFILE=8192

[Install]
WantedBy=multi-user.target
UNIT
(( ! old_active )) || systemctl stop soar-server.service
systemctl daemon-reload
systemctl start soar-panel.service
sleep 2
systemctl is-active --quiet soar-panel.service || die 'Panel failed to start. See journalctl -u soar-panel -n 40.'
ss -H -lun | awk -v target="$listen_ip:53" '$4==target{found=1}END{exit !found}' || die 'The expected UDP/53 listener was not found.'
command -v curl >/dev/null || die 'curl is required for the management health check; install it and rerun.'
code=$(curl --noproxy '*' --max-time 5 -s -o /dev/null -w '%{http_code}' http://127.0.0.1:2053/api/session)
[[ $code == 401 ]] || die 'Management authentication health check failed.'
systemctl enable soar-panel.service
(( ! old_active )) || systemctl disable soar-server.service
activated=1
printf '\n[7/7] Open the panel and create the first account\n'
printf 'On YOUR COMPUTER, open another terminal and run:\n\n  ssh -N -L 2053:127.0.0.1:2053 root@%s\n\n' "$ssh_host"
printf 'Keep that terminal open. Open http://127.0.0.1:2053 in your browser.\n'
printf 'Sign in, open Accounts, choose New account, set quota/expiry, then save.\n'
printf 'Open Link / QR and import the soar:// link into your existing WhiteDNS app.\n'
printf 'Verify the phone opens websites and its public IP matches this server.\n'
printf 'If it does not connect, check NS/A records, UDP/53 and client resolver reachability.\n'
printf 'Service: systemctl status soar-panel\nLogs: journalctl -u soar-panel -n 40 --no-pager\n'
printf 'Private rollback backup: %s\n' "$backup"
printf 'Guide: https://github.com/WhiteDNS/Soar-Server/blob/main/docs/PANEL.fa.md\n'
