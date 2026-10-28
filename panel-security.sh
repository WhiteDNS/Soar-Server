#!/usr/bin/env bash
# Original WhiteDNS terminal workflow; all terminal text intentionally ASCII.
set -Eeuo pipefail
export LC_ALL=C
umask 077
security_require_terminal(){
 [[ $EUID == 0 ]] || { printf 'Run with sudo soar-panel-security\n' >&2; return 1; }
 [[ -t 0 ]] || { printf 'Run in an interactive SSH terminal.\n' >&2; return 1; }
}
security_paths(){
 db=/var/lib/soar-panel/panel.db
 access=/var/lib/soar-panel/access.json
 binary=/usr/local/bin/soar-panel
 certificates=/var/lib/soar-panel/certificates
 state=/var/lib/soar-panel-ops
 backup_root=/root
}
security_main(){
[[ $# == 0 ]] || { printf 'Usage: soar-panel-security\n' >&2;return 1; }
security_require_terminal
security_paths
[[ -f $db && -x $binary ]] || { printf 'Install the panel first.\n' >&2; exit 1; }
service_start=$(systemctl show soar-panel.service -p ExecStart --value)
bind=$(sed -n 's/.*--listen \([^ ;]*\).*/\1/p' <<< "$service_start")
[[ $bind =~ ^[a-zA-Z0-9.:-]+$ ]] || { printf 'Cannot determine the management listener.\n' >&2; exit 1; }
display_bind=$bind
if [[ $service_start == *--allow-insecure-http* && -r /var/lib/soar-panel/public-ip ]];then
 read -r public_ip </var/lib/soar-panel/public-ip
 if [[ $public_ip =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]];then display_bind="$public_ip:${bind##*:}";fi
fi
printf '\nWhiteDNS - domain, automatic HTTPS and private login address\n'
printf '1) Change primary domain / HTTPS / additional domains\n2) Show the current login address\n3) Disable HTTPS and private path (keep the current HTTP listener)\n4) Generate a new private login path\n5) Change additional HTTPS domains\n6) Reissue certificates now\n7) Show current access settings\n0) Cancel\n'
read -r -p 'Choose [1]: ' choice;choice=${choice:-1}
if [[ $choice == 2 ]];then exec "$binary" access-url --db "$db" --listen "$display_bind";fi
if [[ $choice == 7 ]];then exec "$binary" access-info --db "$db";fi
[[ $choice == 1 || $choice == 3 || $choice == 4 || $choice == 5 || $choice == 6 ]] || { printf 'Cancelled.\n'; exit 0; }
domain='';email='';random=();aliases=();domains=
if [[ $choice == 1 ]];then
 printf '\nCreate an A record such as panel.example.com pointing to this server.\n'
 printf 'Disable DNS proxying. TCP ports 80 and 443 must reach this server.\n'
 printf 'A conflicting web server on ports 80/443 must use a separate reverse-proxy setup.\n'
 read -r -p 'Panel domain (leave empty to keep the existing domain, or use only a random path): ' domain
 [[ -z $domain || $domain =~ ^[a-z0-9][a-z0-9.-]*[a-z0-9]$ ]] || { printf 'Invalid ASCII domain.\n' >&2; exit 1; }
 read -r -p 'Certificate account email (optional): ' email
 read -r -p 'Additional HTTPS domains (comma-separated; blank keeps aliases when primary is unchanged): ' extra_domains
 [[ -z $extra_domains ]] || aliases=(--panel-aliases "$extra_domains")
 read -r -p 'Generate a new random login path? [Y/n]: ' answer
 [[ $answer == n || $answer == N ]] || random=(--random-path)
 printf 'Certificates are obtained from Let\047s Encrypt and renewed automatically.\n'
 printf 'By continuing with HTTPS, you accept the certificate service terms.\n'
elif [[ $choice == 3 ]];then
 printf '\nRecovery disables HTTPS/private paths. HTTP listener remains %s.\n' "$bind"
 if [[ $bind == 127.* ]];then printf 'Use an SSH tunnel for this loopback listener.\n';else printf 'WARNING: this listener allows unencrypted public HTTP.\n';fi
elif [[ $choice == 4 ]];then
 random=(--random-path)
 printf 'The existing primary domain and additional domains are preserved.\n'
elif [[ $choice == 5 ]];then
 domains=$("$binary" access-domains --db "$db")
 [[ -n $domains ]] || { printf 'Configure a primary HTTPS domain first.\n' >&2;exit 1; }
 printf 'Primary domain remains unchanged. Existing domains:\n%s\n' "$domains"
 read -r -p 'Complete additional HTTPS domain list (comma-separated; blank removes all aliases): ' extra_domains
 aliases=(--panel-aliases "$extra_domains")
elif [[ $choice == 6 ]];then
 domains=$("$binary" access-domains --db "$db")
 [[ -n $domains ]] || { printf 'Configure HTTPS first.\n' >&2;exit 1; }
 printf 'Certificates renew automatically. Force reissue only for recovery; CA rate limits apply.\n'
 read -r -p 'Type RENEW to request fresh certificates: ' answer
 [[ $answer == RENEW ]] || exit 0
fi
printf 'The panel will restart briefly. Accounts, keys and traffic quotas are preserved.\n'
read -r -p 'Type APPLY to continue: ' answer
[[ $answer == APPLY ]] || { printf 'Cancelled; no settings changed.\n'; exit 0; }
install -d -m 0700 "$state"
exec 8>"$state/maintenance.lock"
flock -n 8 || { printf 'Another installer or maintenance operation is running. Retry later.\n' >&2;exit 1; }
backup=$(mktemp -d "$backup_root/soar-panel-access-XXXXXX")
had_access=0;had_certificates=0;was_active=0
systemctl is-active --quiet soar-panel.service && was_active=1
if [[ -f $access ]];then cp -p -- "$access" "$backup/access.json";had_access=1;fi
done=0
recover(){
 result=$?
 trap - EXIT INT TERM
 if (( ! done ));then
  recovery_failed=0
  printf '\nSetup did not complete. Restoring the previous access settings. Private backup: %s\n' "$backup" >&2
  systemctl stop soar-panel.service || recovery_failed=1
  if (( had_access ));then install -o soar-panel -g soar-panel -m 0600 "$backup/access.json" "$access" || recovery_failed=1
  else rm -f -- "$access" || recovery_failed=1;fi
  if [[ $choice == 6 && $had_certificates == 1 ]];then
   if mv -- "$certificates" "$backup/failed-certificates"; then
    cp -a -- "$backup/certificates" "$certificates" || recovery_failed=1
   else recovery_failed=1;fi
  fi
  (( ! was_active )) || systemctl start soar-panel.service || recovery_failed=1
  if (( recovery_failed ));then printf '[ERROR] Automatic recovery needs attention. Restore the private backup: %s\n' "$backup" >&2;fi
 fi
 exit "$result"
}
trap recover EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
systemctl stop soar-panel.service
if [[ $choice == 6 ]];then
 if [[ -d $certificates ]];then
  cp -a -- "$certificates" "$backup/certificates"
  had_certificates=1
 fi
 while IFS= read -r cert_domain;do
  [[ $cert_domain =~ ^[a-z0-9][a-z0-9.-]*[a-z0-9]$ ]] || exit 1
  rm -f -- "$certificates/$cert_domain" "$certificates/$cert_domain+rsa"
 done <<< "$domains"
fi
if [[ $choice == 3 ]];then
 "$binary" access-reset --db "$db"
else
 "$binary" configure-access --db "$db" --listen "$bind" --domain "$domain" --email "$email" "${random[@]}" "${aliases[@]}" >/dev/null
fi
chown soar-panel:soar-panel "$access"
if [[ -d $certificates ]];then chown -R soar-panel:soar-panel "$certificates";fi
if (( was_active ));then
 systemctl start soar-panel.service
 healthy=0
 for attempt in {1..20};do
  if "$binary" health --db "$db" --listen "$bind" >/dev/null 2>&1;then healthy=1;break;fi
  sleep 1
 done
 (( healthy )) || { printf 'Panel readiness failed.\n' >&2;exit 1; }
fi
done=1
printf '\nAccess settings saved. Bookmark this login address:\n'
"$binary" access-url --db "$db" --listen "$display_bind"
printf 'Administrator password is unchanged. Private rollback copy: %s\n' "$backup"
}
if [[ ${BASH_SOURCE[0]} == "$0" ]];then security_main "$@";fi
