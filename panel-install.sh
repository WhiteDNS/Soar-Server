#!/usr/bin/env bash
# Original WhiteDNS installer. English-only terminal output for SSH compatibility.
set -Eeuo pipefail
export LC_ALL=C
umask 077
ROOT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
PANEL_VERSION=0.3.1
DB=/var/lib/soar-panel/panel.db
SERVICE=/etc/systemd/system/soar-panel.service
BIN="$ROOT_DIR/bin/linux-amd64/soar-panel"
OPS_BIN="$ROOT_DIR/bin/linux-amd64/soar-panel-ops"
OPS_SERVICE=/etc/systemd/system/soar-panel-ops.service
INSTALLED_BIN=/usr/local/bin/soar-panel
CHECK_BIN=/usr/local/bin/soar-check
die(){ printf '\n[ERROR] %s\n' "$*" >&2; exit 1; }
check_bundle(){
 local file
 [[ -f "$ROOT_DIR/SHA256SUMS" ]] || die 'Incomplete panel bundle. Download and extract the full archive.'
 for file in panel-install.sh panel-security.sh lib/panel-installer.sh lib/standalone-install.sh bin/linux-amd64/soar-panel bin/linux-amd64/soar-panel-ops bin/linux-amd64/soar-check; do
  [[ -f "$ROOT_DIR/$file" ]] && awk -v path="$file" '{sub(/^\*/,"",$2)} $2==path{n++}END{exit n!=1}' "$ROOT_DIR/SHA256SUMS" || die "Missing bundle file or checksum: $file"
 done
 (cd "$ROOT_DIR" && sha256sum --check --status SHA256SUMS) || die 'Bundle checksum failed. Do not install this copy.'
 printf '[OK] Panel bundle checksums verified.\n'
}
main(){
if [[ $# == 1 && $1 == --check-bundle ]]; then check_bundle; return 0; fi
[[ $# == 0 ]] || die 'Usage: bash panel-install.sh [--check-bundle]'
[[ $EUID == 0 ]] || die 'Run this installer with sudo bash panel-install.sh'
[[ -r /etc/os-release ]] || die 'This installer requires Ubuntu 22.04+ or Debian 12+.'
. /etc/os-release
case "$ID:${VERSION_ID%%.*}" in ubuntu:22|ubuntu:24|ubuntu:26|debian:12|debian:13) ;; *) die 'Supported systems: Ubuntu 22.04/24.04/26.04, Debian 12/13.' ;; esac
[[ $(uname -m) == x86_64 ]] || die 'This panel bundle is for amd64 servers only.'
[[ -t 0 ]] || die 'Run in an interactive SSH terminal; do not pipe the installer into bash.'
check_bundle
source "$ROOT_DIR/lib/panel-installer.sh"
[[ -d /run/systemd/system ]] || die 'A running systemd host is required; containers without systemd are unsupported.'
panel_dependencies
exec 9>/run/lock/soar-panel-install.lock
flock -n 9 || die 'Another panel installation is running. Wait for it to finish.'
printf '\nWhiteDNS Soar Panel %s - STABLE\n' "$PANEL_VERSION"
printf 'Direct profiles require a Soar-enabled WhiteDNS app; subscriptions require WhiteDNS 1.6.8+.\n'
printf 'The Soar tunneling engine is developed by Lantern: https://soar.lantern.io/\n'
printf 'Use a test server first. This wizard will explain every change before activation.\n'
printf '\n[1/7] Check server and existing services\n'
old_active=0; panel_active=0
systemctl is-active --quiet soar-server.service && old_active=1
systemctl is-active --quiet soar-panel.service && panel_active=1
printf 'Existing standalone Soar active: %s; existing panel active: %s\n' "$old_active" "$panel_active"
declare -gA was_enabled=()
for unit in soar-panel.service soar-panel-ops.service soar-server.service; do
 was_enabled[$unit]=0
 if systemctl is-enabled --quiet "$unit" 2>/dev/null; then was_enabled[$unit]=1; fi
done
managed_pids=
if (( panel_active )); then managed_pids+=" $(systemctl show soar-panel.service -p MainPID --value)";fi
if (( old_active )); then managed_pids+=" $(systemctl show soar-server.service -p MainPID --value)";fi
printf '\n[2/7] Check your DNS delegation\n'
printf 'Choose a dedicated zone, for example soar.example.com. Do not reuse a MasterDNS zone.\n'
printf 'In your DNS provider, create A: ns-soar -> YOUR_PUBLIC_IPV4 (DNS-only, proxy OFF).\n'
printf 'Then create NS: soar -> ns-soar.example.com. Replace example.com with your domain.\n'
printf 'Keep all existing MasterDNS/Cotton records unchanged. Each panel account needs no extra record.\n'
zone=;parent_zone=;ns_host=;public_ip=;listen_ip=;test_resolvers=1.1.1.1,8.8.8.8
panel_collect_dns
panel_require_records
panel_port_available u "$listen_ip" 53 || die 'UDP/53 belongs to another service. Choose another local IPv4 or a dedicated VPS; keep the existing service running.'
printf '\n[3/7] Administrator account\n'
password='';pwfile=''
cleanup(){ [[ -z $pwfile ]] || rm -f -- "$pwfile"; unset password password2; }
trap cleanup EXIT
if [[ -e $DB ]]; then
 printf 'Existing database found. Its accounts, keys, admin password and usage will be preserved.\n'
else
 while :; do
  panel_ask admin 'Administrator name' admin
  [[ $admin =~ ^[a-zA-Z0-9_.-]{1,64}$ ]] && break
  printf 'Use 1-64 letters, numbers, dots, underscores or hyphens.\n'
 done
 printf 'Choose a unique 12-72 character ASCII password. Input is hidden.\n'
 while :; do
  IFS= read -r -s -p 'New administrator password: ' password;printf '\n'
  IFS= read -r -s -p 'Repeat password: ' password2;printf '\n'
  [[ $password == "$password2" && ${#password} -ge 12 && ${#password} -le 72 && $password != *[![:print:]]* ]] && break
  printf 'Passwords must match and be 12-72 printable ASCII characters. Try again.\n'
 done
 unset password2
fi
printf '\n[4/7] Choose how to open the panel\n'
printf '1) Direct IP and port over HTTP (test server; passwords are not encrypted).\n'
printf '2) Private access through SSH, or through an HTTPS proxy you configure.\n'
access_default=1
if [[ -f $SERVICE ]] && ! grep -q -- --allow-insecure-http "$SERVICE";then access_default=2;fi
while :; do
panel_ask panel_access 'Access mode (1 or 2)' "$access_default"
panel_ask panel_port 'Management TCP port' 2053
if ! [[ $panel_port =~ ^[1-9][0-9]{0,4}$ ]] || ((panel_port>65535)); then printf 'Enter a TCP port from 1 to 65535.\n';continue;fi
case "$panel_access" in
 1) panel_bind="$listen_ip:$panel_port";panel_options=--allow-insecure-http;panel_url="http://$public_ip:$panel_port";;
 2) panel_bind="127.0.0.1:$panel_port";panel_options=--trust-loopback-proxy;panel_url="http://127.0.0.1:$panel_port";;
 *) printf 'Choose access mode 1 or 2.\n';continue;;
esac
panel_port_available t "${panel_bind%:*}" "$panel_port" && break
printf 'Choose a different management port.\n'
done
printf 'Panel address: %s\n' "$panel_url"
if [[ $panel_access == 1 ]];then
 printf 'Allow inbound TCP/%s in your provider firewall and NAT. Use HTTPS for production.\n' "$panel_port"
 printf 'If UFW is active, this installation adds one TCP/%s allow rule.\n' "$panel_port"
else
 printf 'SSH instructions will be shown at the end. See docs/PANEL.fa.md for HTTPS.\n'
fi
printf '\n[5/7] Review before activation\n'
printf 'DNS listener: %s:53/UDP\nPanel listener: %s\nPanel URL: %s\nDatabase: %s\n' "$listen_ip" "$panel_bind" "$panel_url" "$DB"
printf 'If UFW is active, allow inbound UDP/53 for the tunnel. Existing rules are kept.\n'
printf 'Open UDP/53 in your provider firewall and forward it through NAT to %s.\n' "$listen_ip"
printf 'Service user: soar-panel; new-install destination ports: 80/443. Existing port policy is kept.\n'
printf 'New-install bootstrap DNS: %s; discovery hint: Iran (editable). Existing saved settings are kept.\n' "$test_resolvers"
printf 'These resolvers are starting points, not a guarantee of reachability in every network.\n'
if (( old_active ));then
 printf '\nWARNING: the standalone soar-server service must stop to free this DNS listener.\n'
 printf 'Its keys and files are kept. Existing shared-root profiles will STOP WORKING after migration.\n'
 printf 'Create panel accounts and distribute their new links. This is a planned migration.\n'
 read -r -p 'Type MIGRATE to accept this migration, or anything else to cancel: ' answer
 [[ $answer == MIGRATE ]] || die 'Cancelled. The existing service was not changed.'
else
 read -r -p 'Type INSTALL to install/start this panel, or anything else to cancel: ' answer
 [[ $answer == INSTALL ]] || die 'Cancelled. No service was changed.'
fi
printf '\n[6/7] Install and verify\n'
backup="/root/soar-panel-backup-$(date -u +%Y%m%dT%H%M%SZ)-$$"
ops_active=0;systemctl is-active --quiet soar-panel-ops.service && ops_active=1
activated=0;transaction_ready=0;probe_id=
firewall_added=()
transaction_files=("$SERVICE" "$OPS_SERVICE" "$INSTALLED_BIN" /usr/local/bin/soar-panel-ops /usr/local/bin/soar-panel-security "$CHECK_BIN" "$DB" /var/lib/soar-panel/access.json /var/lib/soar-panel/public-ip)
rollback(){
 result=$?
 if (( result && ! activated ));then
  printf '\n[ERROR] Activation failed. Restoring previously running services.\n' >&2
  panel_restore || printf '[ERROR] Automatic recovery was incomplete. Restore the private backup before retrying.\n' >&2
  printf 'Private backup: %s\n' "$backup" >&2
 fi
 cleanup
}
trap rollback EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
(( ! ops_active )) || systemctl stop soar-panel-ops.service
(( ! panel_active )) || systemctl stop soar-panel.service
panel_snapshot
if [[ -f $DB ]];then
 existing_zone=$("$BIN" zone --db "$DB")
 [[ $existing_zone == "$zone" ]] || die "This database uses $existing_zone. Rerun with that zone; existing profiles cannot be silently moved."
fi
id soar-panel >/dev/null 2>&1 || useradd --system --home-dir /var/lib/soar-panel --shell /usr/sbin/nologin soar-panel
install -d -o soar-panel -g soar-panel -m 0700 /var/lib/soar-panel
printf '%s\n' "$public_ip" >/var/lib/soar-panel/public-ip
chmod 0600 /var/lib/soar-panel/public-ip
install -m 0755 "$BIN" /usr/local/bin/soar-panel
install -m 0755 "$OPS_BIN" /usr/local/bin/soar-panel-ops
install -m 0755 "$ROOT_DIR/panel-security.sh" /usr/local/bin/soar-panel-security
install -m 0755 "$ROOT_DIR/bin/linux-amd64/soar-check" "$CHECK_BIN"
fresh_install=0
if [[ ! -f $DB ]];then
 fresh_install=1
 pwfile=$(mktemp /root/soar-panel-password.XXXXXX);printf '%s' "$password" >"$pwfile";unset password
 /usr/local/bin/soar-panel init --db "$DB" --zone "$zone" --resolvers "$test_resolvers" --admin "$admin" --password-file "$pwfile"
 rm -f -- "$pwfile";pwfile=''
fi
panel_create_probe || die 'Cannot prepare the temporary tunnel test account.'
chown soar-panel:soar-panel "$DB";chmod 0600 "$DB"
if (( fresh_install )) && [[ ! -e /var/lib/soar-panel/access.json ]];then
 /usr/local/bin/soar-panel configure-access --db "$DB" --listen "$panel_bind" --random-path
 chown soar-panel:soar-panel /var/lib/soar-panel/access.json
fi
cat >"$SERVICE" <<UNIT
[Unit]
Description=WhiteDNS Soar Management Panel
After=network-online.target
Wants=network-online.target
Wants=soar-panel-ops.service

[Service]
Type=simple
User=soar-panel
Group=soar-panel
ExecStart=/usr/local/bin/soar-panel run --db /var/lib/soar-panel/panel.db --dns-listen $listen_ip:53 --listen $panel_bind $panel_options
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
# Go net.InterfaceAddrs uses NETLINK to reject the server's own addresses.
# Without it Lantern PublicOnly deliberately fails closed for every destination.
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK
UMask=0077
LimitNOFILE=8192

[Install]
WantedBy=multi-user.target
UNIT
cat >"$OPS_SERVICE" <<UNIT
[Unit]
Description=WhiteDNS private panel maintenance service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
Group=soar-panel
ExecStart=/usr/local/bin/soar-panel-ops --health-address $panel_bind
Restart=on-failure
RestartSec=3
RuntimeDirectory=soar-panel-ops
RuntimeDirectoryMode=0750
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/usr/local/bin /var/lib/soar-panel /var/lib/soar-panel-ops /run/soar-panel-ops
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK
UMask=0077

[Install]
WantedBy=multi-user.target
UNIT
install -d -m 0700 /var/lib/soar-panel-ops
(( ! old_active )) || systemctl stop soar-server.service
systemctl daemon-reload
systemctl restart soar-panel-ops.service
systemctl is-active --quiet soar-panel-ops.service || die 'Maintenance service failed to start.'
systemctl start soar-panel.service
panel_wait_health || die 'Panel failed readiness checks. See journalctl -u soar-panel -n 40.'
ss -H -lun | awk -v target="$listen_ip:53" '$4==target{found=1}END{exit !found}' || die 'The expected UDP/53 listener was not found.'
panel_open_firewall || die 'Cannot apply the reviewed firewall rules.'
panel_run_checks || die 'Connection checks cancelled; rolling back.'
panel_remove_probe || die 'Cannot remove the temporary test account or restart the panel.'
systemctl enable soar-panel.service
systemctl enable soar-panel-ops.service
(( ! old_active )) || systemctl disable soar-server.service
activated=1
panel_url=$(panel_access_url)
printf '\n[7/7] Open the panel and create the first account\n'
if (( connection_verified )); then printf '[OK] Installation and server-side tunnel checks completed.\n'
else printf '[WARN] Installed with CHECKS PENDING. Working client connectivity has NOT been confirmed.\n';fi
if [[ $panel_access == 1 ]];then
 printf 'Open this address directly in your browser: %s\n' "$panel_url"
 printf 'No SSH tunnel is required. This test HTTP connection is not encrypted.\n'
else
 printf 'On YOUR COMPUTER, open another terminal and run:\n\n  ssh -N -L %s:127.0.0.1:%s root@%s\n\n' "$panel_port" "$panel_port" "$ssh_host"
 printf 'Keep that terminal open. Open %s in your browser.\n' "$panel_url"
fi
printf 'Sign in, open Accounts, choose New account, set quota/expiry, then save.\n'
printf 'Open Link / QR and import the soar:// link into your existing WhiteDNS app.\n'
printf 'Verify the phone opens websites and its public IP matches this server.\n'
printf 'If it does not connect, check NS/A records, UDP/53 and client resolver reachability.\n'
printf 'Service: systemctl status soar-panel\nLogs: journalctl -u soar-panel -n 40 --no-pager\n'
printf 'Private rollback backup: %s\n' "$backup"
printf 'Guide: https://github.com/WhiteDNS/Soar-Server/blob/panel-v0.3.1/docs/PANEL.fa.md\n'
printf '\nConfigure a domain, automatic HTTPS and a random login path now or later.\n'
read -r -p 'Open the optional domain/HTTPS setup now? [y/N]: ' secure_now || secure_now=n
if [[ $secure_now == y || $secure_now == Y ]];then
 /usr/local/bin/soar-panel-security || printf '[WARN] HTTPS setup did not complete; the previous panel address was retained. Retry: sudo soar-panel-security\n'
else printf 'Later, run: sudo soar-panel-security\n';fi
if (( connection_verified ));then return 0;else return 2;fi
}
if [[ ${BASH_SOURCE[0]} == "$0" ]]; then main "$@"; fi
