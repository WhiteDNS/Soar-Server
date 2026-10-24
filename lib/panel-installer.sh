#!/usr/bin/env bash
# Sourceable installer helpers. No services or files change when sourced.
panel_ask() {
 local target=$1 label=$2 default=${3:-} value
 [[ -z $default ]] || label+=" [$default]"
 read -r -p "$label: " value || die 'Input closed. Rerun in an interactive SSH terminal.'
 printf -v "$target" '%s' "${value:-$default}"
}
panel_dns() (
 source "$ROOT_DIR/lib/standalone-install.sh"
 ZONE=$zone;PARENT_ZONE=$parent_zone;NS_HOST=$ns_host;PUBLIC_IP=$public_ip;RESOLVERS=$test_resolvers
 case "$1" in
  names|validate)
   valid_zone "$ZONE" && (( ${#ZONE} <= 24 )) && valid_domain "$PARENT_ZONE" && valid_domain "$NS_HOST" &&
    [[ $ZONE == *."$PARENT_ZONE" && $NS_HOST == *."$PARENT_ZONE" && $NS_HOST != "$ZONE" && $NS_HOST != *."$ZONE" ]] || return 1
   [[ $1 != validate ]] || { public_ipv4 "$PUBLIC_IP" && valid_resolvers "$RESOLVERS"; };;
  public-ip) public_ipv4 "$2";;
  records)
   dns_preflight || return $?
   [[ $DNS_RECORDS_READY == 1 ]] || { warn 'Create both A/NS records shown above, wait for propagation, then retry.'; return 3; };;
  ready) dns_check;;
 esac
)
panel_dependencies() {
 local tool missing=0 answer
 for tool in systemctl sha256sum install useradd awk flock; do
  command -v "$tool" >/dev/null || die "Missing system tool: $tool. Install coreutils, util-linux and passwd."
 done
 for tool in dig ip ss curl; do command -v "$tool" >/dev/null || missing=1; done
 [[ -r /etc/ssl/certs/ca-certificates.crt ]] || missing=1
 if (( missing )); then
  printf 'DNS/network tools are missing. Required packages: ca-certificates curl dnsutils iproute2.\n'
  panel_ask answer 'Install these packages using apt-get? (Y/n)' Y
  [[ $answer == Y || $answer == y ]] || die 'Install the listed packages, then rerun. No services changed.'
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl dnsutils iproute2
 fi
}
panel_collect_dns() {
 local default_ip default_public
 while :; do
  panel_ask zone 'Tunnel subdomain (example: soar.example.com, max 24 characters)' "${zone:-}"
  zone=${zone,,};zone=${zone%.}
  panel_ask parent_zone 'Main DNS zone you can edit' "${zone#*.}"
  parent_zone=${parent_zone,,};parent_zone=${parent_zone%.}
  panel_ask ns_host 'Nameserver hostname (inside the main zone, outside the tunnel)' "ns-soar.$parent_zone"
  ns_host=${ns_host,,};ns_host=${ns_host%.}
  if ! panel_dns names; then
   printf '[WARN] Use valid DNS names and a tunnel subdomain of at most 24 characters.\n'
   printf 'The nameserver must be inside the main DNS zone and outside the tunnel subdomain.\n'
   continue
  fi
  default_ip=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++)if($i=="src"){print $(i+1);exit}}') || default_ip=
  printf 'IPv4 addresses on this server:\n'
  ip -o -4 addr show scope global | awk '{print "  " $2 "  " $4}'
  while :; do
   panel_ask listen_ip 'Local IPv4 for the DNS listener (behind NAT, use the local IP)' "${listen_ip:-$default_ip}"
   if [[ $listen_ip =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]] &&
     ip -o -4 addr show | awk '{split($4,a,"/");print a[1]}' | awk -v target="$listen_ip" '$0==target{found=1}END{exit !found}'; then break; fi
   printf '[WARN] Choose an IPv4 actually assigned to this server.\n'
  done
  default_public=${public_ip:-}
  if [[ -z $default_public ]] && panel_dns public-ip "$listen_ip"; then default_public=$listen_ip; fi
  while :;do
   panel_ask public_ip 'PUBLIC IPv4 from your VPS provider (the A record value)' "$default_public"
   panel_dns public-ip "$public_ip" && break
   printf 'Enter the public IPv4 shown by your VPS provider; private/NAT interface IPs are not accepted here.\n'
  done
  while :;do
   panel_ask test_resolvers 'Resolvers for server-side DNS and tunnel tests' "${test_resolvers:-1.1.1.1,8.8.8.8}"
   panel_dns validate && break
   printf 'Enter resolver IPv4s separated by commas, without spaces.\n'
  done
  break
 done
 while :;do
  panel_ask ssh_host 'Public IP or SSH hostname for access instructions' "$public_ip"
  [[ $ssh_host =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]*$ ]] && break
  printf 'Enter an IP or hostname without a URL, port or spaces.\n'
 done
 printf '\nIn the DNS panel for %s, ADD these records (DNS-only, proxy OFF):\n' "$parent_zone"
 printf '  A   %s -> %s\n  NS  %s -> %s\n' "${ns_host%."$parent_zone"}" "$public_ip" "${zone%."$parent_zone"}" "$ns_host"
 printf 'Keep existing website, email and other tunnel records unchanged.\n'
}
panel_require_records() {
 local choice
 while ! panel_dns records; do
  printf '\nNo service changes yet. 1) Retry DNS  2) Edit details  3) Change test resolvers  4) Cancel\n'
  panel_ask choice 'Choose' 1
  case "$choice" in
   1) ;;
   2) panel_collect_dns;;
   3) panel_ask test_resolvers 'Resolver IPv4s, separated by commas' "$test_resolvers"
      panel_dns validate || { printf '[WARN] Invalid resolver list; restoring defaults.\n';test_resolvers=1.1.1.1,8.8.8.8; };;
   4) die 'Cancelled before service changes.';;
   *) printf 'Choose 1, 2, 3 or 4.\n';;
  esac
 done
 printf '[OK] A/NS and parent conflict checks passed. Child SOA is checked AFTER service startup.\n'
}
panel_port_available() {
 local protocol=$1 address=$2 port=$3 snapshot row endpoint host pid managed
 snapshot=$(ss -H -l"$protocol"np "sport = :$port") || return 1
 while IFS= read -r row; do
  [[ -n $row ]] || continue
  managed=0
  for pid in ${managed_pids:-}; do
   if [[ $pid =~ ^[1-9][0-9]*$ && $row == *"pid=$pid,"* ]]; then managed=1; fi
  done
  (( ! managed )) || continue
  endpoint=$(awk '{print $4}' <<< "$row");host=${endpoint%:*};host=${host%%%*}
  case "$host" in '*'|0.0.0.0|'[::]'|'::'|"$address")
   printf '[WARN] Existing listener conflicts with %s:%s/%s:\n%s\n' "$address" "$port" "$protocol" "$row" >&2
   return 1;;
  esac
 done <<< "$snapshot"
}
panel_wait_health() {
 local attempt
 for attempt in {1..30}; do
  if systemctl is-active --quiet soar-panel.service && systemctl is-active --quiet soar-panel-ops.service &&
    curl --silent --fail --max-time 2 --unix-socket /run/soar-panel-ops/control.sock http://localhost/status >/dev/null 2>&1 &&
    "$INSTALLED_BIN" health --db "$DB" --listen "$panel_bind" >/dev/null 2>&1; then return 0; fi
  sleep 1
 done
 return 1
}
panel_open_firewall() {
 local status port
 local -a ports=(53/udp 53/tcp)
 [[ $panel_access != 1 ]] || ports+=("$panel_port/tcp")
 if command -v ufw >/dev/null; then
  status=$(ufw status) || { printf '[WARN] Cannot inspect UFW.\n';return 1; }
  if [[ $status == *'Status: active'* ]]; then
   for port in "${ports[@]}"; do
    # Preserve existing broad allow rules and their comments. Only rules added
    # by this transaction may be removed during rollback.
    if ! awk -v port="$port" '$1==port && $2=="ALLOW" && $3=="Anywhere"{found=1}END{exit !found}' <<< "$status"; then
     ufw allow "$port" comment 'soar-panel-installer' || return 1
     firewall_added+=("$port")
    fi
   done
  fi
 fi
 printf 'Provider firewall/NAT must also forward public UDP/53 and TCP/53 to %s:53.\n' "$listen_ip"
}
# Back up only after the running panel is stopped, so bbolt is consistent.
panel_snapshot() {
 local index path
 install -d -m 0700 "$backup" || return 1
 : >"$backup/paths.tsv" || return 1
 for index in "${!transaction_files[@]}"; do
  path=${transaction_files[$index]}
  printf 'file-%s\t%s\n' "$index" "$path" >>"$backup/paths.tsv" || return 1
  [[ ! -e $path ]] || cp -p -- "$path" "$backup/file-$index" || return 1
 done
 transaction_ready=1
}
panel_access_url() {
 local url_bind=$panel_bind
 [[ $panel_access != 1 ]] || url_bind="$public_ip:$panel_port"
 "$INSTALLED_BIN" access-url --db "$DB" --listen "$url_bind"
}
panel_restore() {
 local index path unit port
 systemctl stop soar-panel.service || true
 systemctl stop soar-panel-ops.service || true
 if (( transaction_ready )); then
  for index in "${!transaction_files[@]}"; do
   path=${transaction_files[$index]}
   if [[ -f $backup/file-$index ]]; then cp -p -- "$backup/file-$index" "$path" || return 1
   else rm -f -- "$path" || return 1; fi
  done
 fi
 systemctl daemon-reload || return 1
 for port in "${firewall_added[@]}"; do
  ufw --force delete allow "$port" comment 'soar-panel-installer' || return 1
 done
 for unit in soar-panel.service soar-panel-ops.service soar-server.service; do
  if [[ ${was_enabled[$unit]} == 1 ]]; then systemctl enable "$unit" || return 1
  else systemctl disable "$unit" 2>/dev/null || true; fi
  systemctl reset-failed "$unit" 2>/dev/null || true
 done
 (( ! ops_active )) || systemctl start soar-panel-ops.service || return 1
 (( ! panel_active )) || systemctl start soar-panel.service || return 1
 (( ! old_active )) || systemctl start soar-server.service || return 1
}
panel_create_probe() {
 local identity
 [[ -n ${probe_profile:-} ]] || probe_profile=$(mktemp /root/soar-panel-cotten.XXXXXX) || return 1
 probe_master_profile=${probe_master_profile:-$probe_profile.masterdns}
 identity=$("$INSTALLED_BIN" create-install-probe --db "$DB" --output "$probe_profile" --masterdns-output "$probe_master_profile") || return 1
 read -r probe_id probe_zone probe_key probe_expires <<< "$identity"
 [[ $probe_id =~ ^[a-f0-9]{32}$ && -n $probe_zone && -n $probe_key && $probe_expires =~ ^[0-9]+$ ]]
}
panel_remove_probe() {
 local -a options=()
 [[ -n ${probe_id:-} ]] || return 0
 systemctl stop soar-panel.service || return 1
 if (( fresh_install )); then options=(--update-resolvers --resolvers "$test_resolvers"); fi
 "$INSTALLED_BIN" remove-install-probe --db "$DB" --probe-id "$probe_id" "${options[@]}" || return 1
 probe_id=
 systemctl start soar-panel.service || return 1
 panel_wait_health
}
panel_verify_connection() {
 if (( $(date +%s) + 300 >= probe_expires )); then
  printf 'Refreshing the expiring temporary test account.\n'
  systemctl stop soar-panel.service || return 1
  panel_create_probe || return 1
  systemctl start soar-panel.service || return 1
  panel_wait_health || return 1
 fi
 printf '\nTest A: local DNS listener, authenticated account and outbound HTTPS\n'
 "$CHECK_BIN" -zone "$probe_zone" -pubkey "$probe_key" -resolver "$listen_ip" -tcp off -timeout 40s || return 1
 printf '\nTest B: public DNS delegation after startup\n'
 panel_dns ready || return 1
 printf '\nTest C: authenticated HTTPS through recursive DNS (%s)\n' "$test_resolvers"
 "$CHECK_BIN" -zone "$probe_zone" -pubkey "$probe_key" -resolver "$test_resolvers" -tcp auto -timeout 60s || return 1
 printf '\nTest D: Cotten authenticated HTTPS over local UDP DNS\n'
 "$CHECK_BIN" -cotten-profile-file "$probe_profile" -resolver "$listen_ip" -tcp off -timeout 40s || return 1
 printf '\nTest E: Cotten authenticated HTTPS over local TCP DNS\n'
 "$CHECK_BIN" -cotten-profile-file "$probe_profile" -resolver "$listen_ip" -tcp only -timeout 40s || return 1
 printf '\nTest F: Cotten authenticated HTTPS through recursive DNS\n'
 "$CHECK_BIN" -cotten-profile-file "$probe_profile" -resolver "$test_resolvers" -tcp auto -timeout 60s || return 1
 printf '\nTest G: MasterDNS authenticated HTTPS over local UDP DNS\n'
 "$CHECK_BIN" -masterdns-profile-file "$probe_master_profile" -resolver "$listen_ip" -tcp off -timeout 40s || return 1
 printf '\nTest H: MasterDNS authenticated HTTPS through recursive UDP DNS\n'
 "$CHECK_BIN" -masterdns-profile-file "$probe_master_profile" -resolver "$test_resolvers" -tcp off -timeout 60s || return 1
 printf '[OK] Server-side tunnel checks passed. Test the phone on its own network next.\n'
}
panel_run_checks() {
 local choice answer
 connection_verified=0
 while :; do
  if panel_verify_connection; then connection_verified=1;return 0;fi
  printf '\n[WARN] Tunnel connectivity is NOT verified. Check the failed test above.\n'
  printf '1) Retry after DNS/firewall fixes  2) Change test resolvers  3) Keep an UNVERIFIED installation  4) Roll back\n'
  panel_ask choice 'Choose' 1
  case "$choice" in
   1) ;;
   2) panel_ask test_resolvers 'Test resolver IPv4s, separated by commas' "$test_resolvers"
      panel_dns validate || { printf '[WARN] Invalid resolver list; restoring defaults.\n';test_resolvers=1.1.1.1,8.8.8.8; };;
   3) panel_ask answer 'Type KEEP to retain the installation with checks pending'
      [[ $answer != KEEP ]] || return 0;;
   4) return 1;;
   *) printf 'Choose 1, 2, 3 or 4.\n';;
  esac
 done
}
