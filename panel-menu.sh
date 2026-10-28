#!/usr/bin/env bash
# WhiteDNS SSH administration. ASCII output works in minimal SSH terminals.
set -Eeuo pipefail
export LC_ALL=C
umask 077
BIN=/usr/local/bin/soar-panel
DB=/var/lib/soar-panel/panel.db
DATA=/var/lib/soar-panel
STATE=/var/lib/soar-panel-ops
BACKUPS=/var/backups/soar-panel
UNITS=/etc/systemd/system
BINDIR=/usr/local/bin
PANEL_DROP=/etc/systemd/system/soar-panel.service.d/90-management.conf
OPS_DROP=/etc/systemd/system/soar-panel-ops.service.d/90-management.conf

menu_error(){ printf '[ERROR] %s\n' "$*" >&2; }
menu_ask(){ local value; IFS= read -r -p "$2: " value || return 1; printf -v "$1" '%s' "$value"; }
menu_confirm(){ local answer; menu_ask answer "Type $1 to $2 (anything else cancels)" && [[ $answer == "$1" ]]; }
menu_pause(){ local ignored; IFS= read -r -p 'Press Enter to return to the menu...' ignored || true; }
menu_lock(){
 install -d -m 0700 "$STATE"
 exec 8>"$STATE/maintenance.lock"
 flock -n 8 || { menu_error 'Another installation or maintenance operation is running. Retry later.'; return 1; }
}
menu_listener(){
 local start
 start=$(systemctl show soar-panel.service -p ExecStart --value) || return
 sed -n 's/.*--listen \([^ ;]*\).*/\1/p' <<< "$start"
}
menu_dns_listener(){
 local start
 start=$(systemctl show soar-panel.service -p ExecStart --value) || return
 sed -n 's/.*--dns-listen \([^ ;]*\).*/\1/p' <<< "$start"
}
menu_wait(){
 local bind attempt
 bind=$(menu_listener)
 [[ $bind =~ ^[a-zA-Z0-9.:-]+$ || $bind =~ ^\[[a-fA-F0-9:]+\]:[0-9]+$ ]] || return 1
 for attempt in {1..20}; do
  if systemctl is-active --quiet soar-panel.service && "$BIN" health --db "$DB" --listen "$bind" >/dev/null 2>&1; then return 0; fi
  sleep 1
 done
 menu_error 'Panel readiness failed. Inspect the logs.'
 return 1
}
menu_wait_ops(){
 local attempt
 for attempt in {1..20}; do
  if systemctl is-active --quiet soar-panel-ops.service && "$BIN" maintenance-status >/dev/null 2>&1; then return 0; fi
  sleep 1
 done
 menu_error 'Maintenance service readiness failed. Inspect its logs.'
 return 1
}
menu_address(){
 local bind public
 bind=$(menu_listener)
 [[ -n $bind ]] || { menu_error 'Cannot determine the panel listener.'; return 1; }
 if [[ $bind != 127.* && $bind != '[::1]:'* && -r $DATA/public-ip ]]; then
  read -r public <"$DATA/public-ip"
  [[ $public =~ ^[0-9.]+$ ]] && bind="$public:${bind##*:}"
 fi
 "$BIN" access-url --db "$DB" --listen "$bind"
 printf 'Private login paths are credentials. Share this address only with administrators.\n'
 if [[ $bind == 127.* || $bind == '[::1]:'* ]]; then
  printf 'For the local listener, run on your computer:\n  ssh -N -L %s:127.0.0.1:%s root@YOUR_SERVER_IP\n' "${bind##*:}" "${bind##*:}"
 fi
}
menu_status(){
 printf '\nWhiteDNS version: '; "$BIN" version
 systemctl --no-pager --full status soar-panel.service soar-panel-ops.service || true
 printf '\nStart at boot:\n'
 systemctl is-enabled soar-panel.service soar-panel-ops.service || true
 printf '\nMaintenance job:\n'
 "$BIN" maintenance-status || true
 printf '\nDisk and memory:\n'; df -h "$DATA"; free -h
}

# Run offline commands in a transaction. Snapshots contain credentials and are
# root-only. An error or signal restores the snapshot and previous running state.
menu_transaction() (
 set -Eeuo pipefail
 menu_lock
 local panel_active=0 ops_active=0 snapshot ready=0 committed=0 path name
 systemctl is-active --quiet soar-panel.service && panel_active=1
 systemctl is-active --quiet soar-panel-ops.service && ops_active=1
 install -d -m 0700 "$BACKUPS"
 snapshot=$(mktemp -d "$BACKUPS/snapshot-$(date -u +%Y%m%dT%H%M%SZ)-XXXXXX")
 recover(){
  local rc=$? recovery_failed=0
  trap - EXIT INT TERM
  if (( ! committed )); then
   printf '\nRestoring the previous state. Private snapshot: %s\n' "$snapshot" >&2
   systemctl stop soar-panel.service || recovery_failed=1
   if (( ready )); then
    if mv -- "$DATA" "$snapshot/failed-data"; then
     cp -a -- "$snapshot/data" "$DATA" || recovery_failed=1
    else recovery_failed=1; fi
    for name in panel ops; do
     if [[ $name == panel ]]; then path=$PANEL_DROP; else path=$OPS_DROP; fi
     if [[ -e $snapshot/$name.conf ]]; then install -m 0644 "$snapshot/$name.conf" "$path" || recovery_failed=1
     else rm -f -- "$path" || recovery_failed=1; fi
    done
    systemctl daemon-reload || recovery_failed=1
   fi
   (( ! ops_active )) || systemctl start soar-panel-ops.service || recovery_failed=1
   (( ! panel_active )) || systemctl start soar-panel.service || recovery_failed=1
   (( ! ops_active )) || menu_wait_ops || recovery_failed=1
   (( ! panel_active )) || menu_wait || recovery_failed=1
   if (( recovery_failed )); then menu_error "Automatic recovery needs attention. Restore the private snapshot: $snapshot"; fi
   (( rc != 0 )) || rc=1
  fi
  exit "$rc"
 }
 trap recover EXIT
 trap 'exit 130' INT
 trap 'exit 143' TERM
 systemctl stop soar-panel-ops.service
 systemctl stop soar-panel.service
 cp -a -- "$DATA" "$snapshot/data"
 [[ ! -f $PANEL_DROP ]] || cp -p "$PANEL_DROP" "$snapshot/panel.conf"
 [[ ! -f $OPS_DROP ]] || cp -p "$OPS_DROP" "$snapshot/ops.conf"
 ready=1
 "$@"
 chown -R soar-panel:soar-panel "$DATA"
 chmod 0700 "$DATA"
 systemctl daemon-reload
 if (( ops_active )); then systemctl start soar-panel-ops.service; menu_wait_ops; fi
 if (( panel_active )); then systemctl start soar-panel.service; menu_wait; fi
 committed=1
 printf '\nCompleted. Private recovery snapshot: %s\n' "$snapshot"
)
menu_backup_action(){
 local target
 target="$BACKUPS/panel-$(date -u +%Y%m%dT%H%M%SZ)-$$.db"
 "$BIN" backup --db "$DB" --output "$target"
 "$BIN" inspect-backup --input "$target"
 printf 'Database backup: %s\nKeep this file private: it contains account keys.\n' "$target"
}
menu_reset_action() (
 local admin password repeat pwfile=
 trap '[[ -z $pwfile ]] || rm -f -- "$pwfile"; unset password repeat' EXIT
 "$BIN" server-info --db "$DB"
 menu_ask admin 'New administrator name (1-64 letters, numbers, dots, underscores or hyphens)'
 [[ $admin =~ ^[a-zA-Z0-9_.-]{1,64}$ ]] || { menu_error 'Invalid administrator name.'; return 1; }
 while :; do
  IFS= read -r -s -p 'New password (12-72 printable ASCII characters): ' password; printf '\n'
  IFS= read -r -s -p 'Repeat new password: ' repeat; printf '\n'
  [[ $password == "$repeat" && ${#password} -ge 12 && ${#password} -le 72 && $password != *[![:print:]]* ]] && break
  printf 'Passwords must match and contain 12-72 printable ASCII characters.\n'
 done
 pwfile=$(mktemp "$BACKUPS/.password-XXXXXX")
 printf '%s' "$password" >"$pwfile"; unset password repeat
 "$BIN" admin-reset --db "$DB" --admin "$admin" --password-file "$pwfile"
 printf 'Administrator updated. Sign in again with the new credentials.\n'
)
menu_tunnels_action(){
 local zones primary nameservers
 "$BIN" server-info --db "$DB"
 printf '\nConfigure A/NS records with your DNS provider before adding domains.\n'
 printf 'Use up to 8 domains, each at most 24 characters. Keep old domains to preserve their working links.\n'
 printf 'Removing a domain stops connections through that domain. Account keys and usage are kept.\n'
 menu_ask zones 'Complete comma-separated tunnel domain list (blank cancels)'
 [[ -n $zones ]] || return 0
 menu_ask primary 'Preferred tunnel domain (blank selects the first)'
 menu_ask nameservers 'Optional zone=ns.example.com pairs, comma-separated'
 menu_confirm SAVE 'save these tunnel domains' || return 0
 "$BIN" configure-tunnels --db "$DB" --zones "$zones" --primary "$primary" --nameservers "$nameservers"
}
menu_network_action(){
 local resolvers ports country public
 "$BIN" server-info --db "$DB"
 printf '\nBlank fields preserve their current values. These are tunnel destination ports, not panel or SSH ports.\n'
 menu_ask resolvers 'Bootstrap resolvers, comma-separated (e.g. 1.1.1.1,8.8.8.8)'
 menu_ask ports 'Allowed destination TCP ports, comma-separated (e.g. 80,443)'
 menu_ask country 'Discovery hint: IR or GLOBAL'
 menu_ask public 'Public IPv4 for DNS instructions (does not change server networking)'
 menu_confirm SAVE 'save network settings' || return 0
 "$BIN" configure-network --db "$DB" --resolvers "$resolvers" --ports "$ports" --country "$country" --public-ip "$public"
 if [[ -n $public ]]; then printf '%s\n' "$public" >"$DATA/public-ip"; chmod 0600 "$DATA/public-ip"; fi
}
menu_port_action(){
 local port start command old bind mode address
 start=$(systemctl show soar-panel.service -p ExecStart --value)
 command=$(sed -n 's/.*argv\[\]=\(.*\) ; ignore_errors=.*/\1/p' <<< "$start")
 [[ $command == '/usr/local/bin/soar-panel run '* && $command =~ ^[a-zA-Z0-9_./:=\ -]+$ ]] || {
  menu_error 'Custom service command detected. Edit its systemd configuration manually.'; return 1;
 }
 old=$(menu_listener)
 menu_ask port 'New management TCP port (1-65535; 53, 80 and 443 are reserved)'
 [[ $port =~ ^[1-9][0-9]{0,4}$ ]] && (( port <= 65535 && port != 53 && port != 80 && port != 443 )) || { menu_error 'Invalid or reserved port.'; return 1; }
 printf '1) Private loopback / SSH / HTTPS proxy\n2) Public HTTP (unencrypted; only for testing)\n'
 menu_ask mode 'Access mode (1 or 2)'
 case "$mode" in
  1) address=127.0.0.1;;
  2) menu_ask address 'Local IPv4 assigned to this server'; [[ $address =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]] && ip -4 -o addr show | awk '{print $4}' | cut -d/ -f1 | grep -Fxq "$address" || { menu_error 'Use an IPv4 assigned to this server.'; return 1; };;
  *) menu_error 'Invalid access mode.'; return 1;;
 esac
 bind="$address:$port"
 if ss -H -ltn | awk -v port=":$port" '$4 ~ (port "$"){found=1}END{exit !found}'; then menu_error 'That TCP port is already in use.'; return 1; fi
 menu_confirm APPLY "set management listener to $bind" || return 0
 command=${command/--listen "$old"/--listen "$bind"}
 command=${command/ --allow-insecure-http/}; command=${command/ --trust-loopback-proxy/}
 if [[ $mode == 2 ]]; then command+=' --allow-insecure-http'; else command+=' --trust-loopback-proxy'; fi
 install -d -m 0755 "${PANEL_DROP%/*}" "${OPS_DROP%/*}"
 printf '[Service]\nExecStart=\nExecStart=%s\n' "$command" >"$PANEL_DROP"
 local dns;dns=$(menu_dns_listener)
 printf '[Service]\nExecStart=\nExecStart=/usr/local/bin/soar-panel-ops --health-address %s --dns-port %s\n' "$bind" "${dns##*:}" >"$OPS_DROP"
 chmod 0644 "$PANEL_DROP" "$OPS_DROP"
 printf 'HTTPS, if configured, continues on TCP/443.\n'
 printf 'For public HTTP, allow TCP/%s in the provider and local firewalls. SSH access is unchanged.\n' "$port"
}
menu_dns_port_action(){
 local start command old host port bind
 start=$(systemctl show soar-panel.service -p ExecStart --value)
 command=$(sed -n 's/.*argv\[\]=\(.*\) ; ignore_errors=.*/\1/p' <<< "$start")
 [[ $command == '/usr/local/bin/soar-panel run '* && $command =~ ^[a-zA-Z0-9_./:=\ -]+$ ]] || { menu_error 'Custom service command detected; edit its systemd configuration manually.'; return 1; }
 old=$(menu_dns_listener);host=${old%:*};bind=$(menu_listener)
 [[ -n $old && -n $bind ]] || return 1
 printf 'Current tunnel listener: %s\nAll three engines share this listener.\n' "$old"
 printf 'Recursive DNS always reaches public UDP/TCP 53. For a custom local port, configure zone forwarding in your existing DNS frontend, or NAT from public 53. An NS record cannot contain a port.\n'
 menu_ask port 'New local UDP/TCP tunnel port (1-65535)'
 [[ $port =~ ^[1-9][0-9]{0,4}$ ]] && (( port<=65535 && port!=80 && port!=443 )) && [[ $port != "${bind##*:}" ]] || { menu_error 'Invalid or conflicting port.'; return 1; }
 if ss -H -lntu | awk -v port=":$port" '$5 ~ (port "$"){found=1}END{exit !found}'; then menu_error 'That UDP/TCP port is already in use.'; return 1; fi
 menu_confirm APPLY "change the DNS listener to $host:$port" || return 0
 command=${command/--dns-listen "$old"/--dns-listen "$host:$port"}
 install -d -m 0755 "${PANEL_DROP%/*}" "${OPS_DROP%/*}"
 printf '[Service]\nExecStart=\nExecStart=%s\n' "$command" >"$PANEL_DROP"
 printf '[Service]\nExecStart=\nExecStart=/usr/local/bin/soar-panel-ops --health-address %s --dns-port %s\n' "$bind" "$port" >"$OPS_DROP"
 chmod 0644 "$PANEL_DROP" "$OPS_DROP"
 printf 'Update provider/router/frontend forwarding to %s:%s for both UDP and TCP. Existing account identities and links are kept.\n' "$host" "$port"
}
menu_service() (
 set -Eeuo pipefail
 local action=$1
 menu_lock
 case "$action" in
  start) systemctl start soar-panel-ops.service soar-panel.service; menu_wait_ops; menu_wait;;
  stop) systemctl stop soar-panel.service soar-panel-ops.service;;
  restart) systemctl restart soar-panel-ops.service; systemctl restart soar-panel.service; menu_wait_ops; menu_wait;;
  enable|disable) systemctl "$action" soar-panel.service soar-panel-ops.service;;
  *) return 1;;
 esac
)
menu_update(){
 local version
 printf 'Installed version: '; "$BIN" version
 version=$("$BIN" latest-release) || return
 printf 'Latest verified official release: %s\n' "$version"
 printf 'The helper verifies the release archive, saves a rollback backup and checks readiness.\n'
 menu_confirm UPDATE "update to $version" || return 0
 "$BIN" maintenance --action update --version "$version"
 printf 'Reopen the menu to load its updated version.\n'
}
menu_restore(){
 local path
 menu_ask path 'Absolute path to a panel .db backup (blank cancels)'
 [[ -n $path ]] || return 0
 [[ $path == /* && -f $path && ! -L $path ]] || { menu_error 'Use a regular local backup file with an absolute path.'; return 1; }
 "$BIN" inspect-backup --input "$path" || return
 printf 'This replaces accounts, keys, settings and usage with the backup. Current admin, panel address and Telegram pairing remain.\n'
 printf 'The helper rejects a different base zone and rolls back if readiness fails.\n'
 menu_confirm RESTORE 'restore this database backup' || return 0
 "$BIN" maintenance --action restore --input "$path"
}
menu_logs(){
 local choice lines
 printf '1) Recent panel logs\n2) Follow panel logs (Ctrl+C returns)\n3) Maintenance logs\n4) Save both logs to a private file\n0) Back\n'
 menu_ask choice 'Choose'
 case "$choice" in
  1|3) menu_ask lines 'Number of lines (blank = 100)'; lines=${lines:-100}; [[ $lines =~ ^[1-9][0-9]{0,3}$ ]] || return 1
   if [[ $choice == 1 ]]; then journalctl -u soar-panel.service -n "$lines" --no-pager; else journalctl -u soar-panel-ops.service -n "$lines" --no-pager; fi;;
  2) (trap 'exit 0' INT; journalctl -u soar-panel.service -n 40 -f) || true;;
  4) install -d -m 0700 "$BACKUPS"; local file="$BACKUPS/logs-$(date -u +%Y%m%dT%H%M%SZ)-$$.txt"
   journalctl -u soar-panel.service -u soar-panel-ops.service -n 500 --no-pager >"$file"
   printf 'Private diagnostic log: %s\nReview it before sharing.\n' "$file";;
  0) return 0;; *) return 1;;
 esac
}
menu_diagnostics(){
 local host
 printf '\nSystem time:\n'; timedatectl status || true
 printf '\nListening TCP/UDP ports:\n'; ss -lntu
 printf '\nPanel readiness:\n'; local bind; bind=$(menu_listener); "$BIN" health --db "$DB" --listen "$bind" || true
 printf '\nService failures:\n'; systemctl --failed --no-pager
 printf '\nLocal firewall:\n'; if command -v ufw >/dev/null; then ufw status verbose; else printf 'UFW is not installed. Check your firewall and provider rules.\n'; fi
 printf '\nRequired provider/NAT rules: UDP/53, TCP/53; TCP/80 and TCP/443 for automatic HTTPS.\n'
 printf 'The management HTTP port is needed publicly only when public HTTP access is enabled.\n'
 menu_ask host 'Optional domain for A/AAAA/NS checks (blank skips)'
 if [[ -n $host ]]; then
  [[ $host =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]+$ && ${#host} -le 253 ]] || return 1
  if command -v dig >/dev/null; then
   dig +time=3 +tries=1 "$host" A; dig +time=3 +tries=1 "$host" AAAA; dig +time=3 +tries=1 "$host" NS
  else printf 'Install dnsutils to run DNS queries.\n'; fi
 fi
 printf 'Server readiness does not prove connectivity from a restricted client network.\n'
}
menu_certificates(){
 local domains host
 domains=$("$BIN" access-domains --db "$DB") || return
 [[ -n $domains ]] || { printf 'HTTPS is not configured. Use the domain/HTTPS wizard.\n'; return 0; }
 printf 'Certificates renew automatically while the panel is running and ports 80/443 are reachable.\n'
 command -v openssl >/dev/null || { menu_error 'Install openssl to inspect certificates.'; return 1; }
 while IFS= read -r host; do
  printf '\n%s\n' "$host"
  timeout 12 openssl s_client -connect 127.0.0.1:443 -servername "$host" -verify_hostname "$host" -verify_return_error </dev/null 2>/dev/null | openssl x509 -noout -subject -issuer -dates -fingerprint -sha256 || return
 done <<< "$domains"
}
menu_firewall() (
 menu_lock
 local dns;dns=$(menu_dns_listener)
 printf 'Prepare local firewall rules for DNS (UDP/TCP %s) and HTTPS (TCP 80/443). Provider rules require your provider console.\n' "${dns##*:}"
 menu_confirm APPLY 'prepare these local firewall rules' || return 0
 /usr/local/bin/soar-panel-ops "firewall-dns:${dns##*:}"
 /usr/local/bin/soar-panel-ops firewall-https
)
menu_uninstall() (
 set -Eeuo pipefail
 local mode snapshot unit
 printf '\n1) Remove panel software; KEEP database, certificates and settings\n2) Remove panel software AND private panel data\n0) Cancel\n'
 menu_ask mode 'Choose'
 case "$mode" in 1|2) ;; *) return 0;; esac
 printf 'All three managed tunnel engines will stop. Standalone services, SSH and firewall rules are kept.\n'
 printf 'A private recovery copy will be kept in %s.\n' "$BACKUPS"
 menu_confirm UNINSTALL 'remove the panel software' || return 0
 if [[ $mode == 2 ]]; then menu_confirm DELETE-DATA 'also remove the panel database, certificates, Telegram settings and maintenance history' || return 0; fi
 menu_lock
 install -d -m 0700 "$BACKUPS"
 snapshot=$(mktemp -d "$BACKUPS/uninstall-$(date -u +%Y%m%dT%H%M%SZ)-XXXXXX")
 local panel_active=0 ops_active=0 saved=0
 systemctl is-active --quiet soar-panel.service && panel_active=1
 systemctl is-active --quiet soar-panel-ops.service && ops_active=1
 resume_if_unsaved(){ local rc=$?; if (( ! saved )); then
  (( ! ops_active )) || systemctl start soar-panel-ops.service || true
  (( ! panel_active )) || systemctl start soar-panel.service || true
 fi; exit "$rc"; }
 trap resume_if_unsaved EXIT
 systemctl stop soar-panel-ops.service soar-panel.service
 cp -a -- "$DATA" "$snapshot/data"
 for unit in soar-panel soar-panel-ops; do
  cp -a "$UNITS/$unit.service" "$snapshot/"
  [[ ! -d $UNITS/$unit.service.d ]] || cp -a "$UNITS/$unit.service.d" "$snapshot/"
 done
 printf 'Private recovery copy: %s\n' "$snapshot"
 saved=1
 systemctl disable soar-panel.service soar-panel-ops.service
 # Fixed installation paths only. Never remove user-selected paths or other engines.
 rm -f -- "$UNITS/soar-panel.service" "$UNITS/soar-panel-ops.service"
 rm -rf -- "$UNITS/soar-panel.service.d" "$UNITS/soar-panel-ops.service.d"
 rm -f -- "$BINDIR/soar-panel" "$BINDIR/soar-panel-ops" "$BINDIR/soar-panel-security" "$BINDIR/soar-panel-menu"
 if [[ -L $BINDIR/whitedns && $(readlink "$BINDIR/whitedns") == "$BINDIR/soar-panel-menu" ]]; then rm -f -- "$BINDIR/whitedns"; fi
 if [[ $mode == 2 ]]; then
  mv -- "$DATA" "$snapshot/removed-data"
  # Keep the locked inode in place so another installer cannot race uninstall.
  find "$STATE" -mindepth 1 -maxdepth 1 ! -name maintenance.lock -exec rm -rf -- {} +
 fi
 systemctl daemon-reload
 systemctl reset-failed soar-panel.service soar-panel-ops.service 2>/dev/null || true
 printf 'Panel removed. Private recovery copy: %s\n' "$snapshot"
 printf 'Firewall rules and the service user are retained to avoid affecting other services.\n'
 exit 10
)
menu_help(){
 cat <<'HELP'
Open menu: sudo whitedns  |  sudo soar-panel  |  sudo soar-panel menu
Service: systemctl status soar-panel soar-panel-ops
Logs: journalctl -u soar-panel -n 100 --no-pager
Panel data: /var/lib/soar-panel (private)
Maintenance backups: /var/lib/soar-panel-ops (private, automatically pruned)
Terminal backups: /var/backups/soar-panel (private; no automatic deletion)
The SSH menu is available to root, including when web login is unavailable.
Changes to passwords, tunnel domains, network settings and ports briefly stop
the panel. Failed offline changes restore the previous snapshot automatically.
Database restore preserves the current administrator and local access settings.
Create DNS A/NS records at your DNS provider; this menu cannot edit your provider.
HTTPS uses Let's Encrypt with automatic renewal. Tunnel and web domains are
configured separately. Changing the web domain does not change tunnel domains.
Guide: https://github.com/WhiteDNS/Soar-Server/blob/main/docs/SERVER-MENU.en.md
HELP
}
main(){
 if [[ $# == 1 && $1 == --help ]]; then menu_help; return 0; fi
 [[ $# == 0 ]] || { menu_error 'Usage: soar-panel-menu [--help]'; return 1; }
 [[ $EUID == 0 ]] || { menu_error 'Run: sudo whitedns'; return 1; }
 [[ -t 0 && -t 1 ]] || { menu_error 'Open an interactive SSH terminal.'; return 1; }
 [[ -x $BIN && -f $DB ]] || { menu_error 'Install the WhiteDNS panel first.'; return 1; }
 [[ -d /run/systemd/system ]] || { menu_error 'A running Linux systemd host is required.'; return 1; }
 local choice rc
 trap ':' INT
 while :; do
  printf '\n=================================================\n WhiteDNS Server Management - SSH menu\n'
  printf ' Version: '; "$BIN" version
  printf ' Panel: '; systemctl is-active soar-panel.service || true
  printf '=================================================\n'
  cat <<'MENU'
  1) Status, version, resources and maintenance job
  2) Show panel login URL and SSH access instructions
  3) Start panel and maintenance service
  4) Stop panel and all managed tunnel engines
  5) Restart panel and maintenance service
  6) Enable start at boot
  7) Disable start at boot
  8) Reset administrator username and password
  9) Panel domains, HTTPS certificates and private login path
 10) Inspect certificates and automatic renewal status
 11) Change management port / private or public HTTP access
 12) Add/remove tunnel domains and change preferred domain
 13) Change bootstrap DNS, destination ports and discovery hint
 14) Create database backup and full recovery snapshot
 15) List private backups
 16) Inspect or restore a database backup
 17) Check and install the latest official update
 18) View, follow or export logs
 19) Diagnostics, DNS and firewall status
 20) Prepare local DNS / HTTPS firewall rules
 21) Show administrator and tunnel settings (brief stop)
 22) Uninstall panel / optionally remove private data
 23) Help, paths and recovery information
 24) Change the tunnel DNS UDP/TCP listener port
  0) Exit
MENU
  menu_ask choice 'Choose' || return 0
  rc=0
  # Each action runs with errexit enabled in its own shell. Do not place the
  # function call in an if/|| condition: that would disable errexit inside it.
  set +e
  (
   set -Eeuo pipefail
   trap 'exit 130' INT
   case "$choice" in
    1) menu_status;; 2) menu_address;;
    3) menu_service start;; 4) menu_confirm STOP 'stop the panel and its tunnels' && menu_service stop;;
    5) menu_confirm RESTART 'restart the panel and its tunnels' && menu_service restart;;
    6) menu_service enable;; 7) menu_service disable;;
    8) menu_confirm RESET 'reset administrator credentials (brief restart)' && menu_transaction menu_reset_action;;
    9) /usr/local/bin/soar-panel-security;; 10) menu_certificates;;
    11) menu_transaction menu_port_action; menu_address;;
    12) menu_transaction menu_tunnels_action;; 13) menu_transaction menu_network_action;;
    14) menu_transaction menu_backup_action;;
    15) if [[ -d $BACKUPS ]]; then find "$BACKUPS" -mindepth 1 -maxdepth 1 -printf '%TY-%Tm-%Td %TH:%TM  %f\n' | sort -r; else printf 'No terminal backups yet.\n'; fi;;
    16) menu_restore;; 17) menu_update;; 18) menu_logs;; 19) menu_diagnostics;; 20) menu_firewall;;
    21) menu_transaction "$BIN" server-info --db "$DB";;
    22) menu_uninstall;; 23) menu_help;; 24) menu_transaction menu_dns_port_action;; 0) exit 10;; *) printf 'Choose a number from 0 to 24.\n';;
   esac
  )
  rc=$?
  set -e
  [[ $rc != 10 ]] || return 0
  [[ $rc == 0 || $rc == 1 ]] || menu_error "Operation exited with status $rc; check the message above."
  menu_pause
 done
}
if [[ ${BASH_SOURCE[0]} == "$0" ]]; then main "$@"; fi
