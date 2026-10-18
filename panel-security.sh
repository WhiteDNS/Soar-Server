#!/usr/bin/env bash
# Original WhiteDNS terminal workflow; all terminal text intentionally ASCII.
set -Eeuo pipefail
export LC_ALL=C
umask 077
[[ $EUID == 0 ]] || { printf 'Run with sudo soar-panel-security\n' >&2; exit 1; }
[[ -t 0 ]] || { printf 'Run in an interactive SSH terminal.\n' >&2; exit 1; }
db=/var/lib/soar-panel/panel.db
access=/var/lib/soar-panel/access.json
binary=/usr/local/bin/soar-panel
[[ -f $db && -x $binary ]] || { printf 'Install the panel first.\n' >&2; exit 1; }
bind=$(systemctl show soar-panel.service -p ExecStart --value | sed -n 's/.*--listen \([^ ;]*\).*/\1/p')
[[ $bind =~ ^[a-zA-Z0-9.:-]+$ ]] || { printf 'Cannot determine the management listener.\n' >&2; exit 1; }
printf '\nWhiteDNS - domain, automatic HTTPS and private login address\n'
printf '1) Configure domain / HTTPS / random login path\n2) Show the current login address\n3) Recover direct IP access (disable HTTPS and private path)\n'
read -r -p 'Choose [1]: ' choice;choice=${choice:-1}
if [[ $choice == 2 ]];then exec "$binary" access-url --db "$db" --listen "$bind";fi
[[ $choice == 1 || $choice == 3 ]] || { printf 'Cancelled.\n'; exit 0; }
domain='';email='';random=()
if [[ $choice == 1 ]];then
 printf '\nCreate an A record such as panel.example.com pointing to this server.\n'
 printf 'Disable DNS proxying. TCP ports 80 and 443 must reach this server.\n'
 printf 'A conflicting web server on ports 80/443 must use a separate reverse-proxy setup.\n'
 read -r -p 'Panel domain (leave empty to keep the existing domain, or use only a random path): ' domain
 [[ -z $domain || $domain =~ ^[a-z0-9][a-z0-9.-]*[a-z0-9]$ ]] || { printf 'Invalid ASCII domain.\n' >&2; exit 1; }
 read -r -p 'Certificate account email (optional): ' email
 read -r -p 'Generate a new random login path? [Y/n]: ' answer
 [[ $answer == n || $answer == N ]] || random=(--random-path)
 printf 'Certificates are obtained from Let\047s Encrypt and renewed automatically.\n'
 printf 'By continuing with HTTPS, you accept the certificate service terms.\n'
else
 printf '\nRecovery opens unencrypted direct-IP access again. Existing tunnel profiles are unchanged.\n'
fi
printf 'The panel will restart briefly. Accounts, keys and traffic quotas are preserved.\n'
read -r -p 'Type APPLY to continue: ' answer
[[ $answer == APPLY ]] || { printf 'Cancelled; no settings changed.\n'; exit 0; }
backup=$(mktemp -d /root/soar-panel-access-XXXXXX)
had_access=0
if [[ -f $access ]];then cp -p -- "$access" "$backup/access.json";had_access=1;fi
done=0
recover(){
 result=$?
 if (( ! done ));then
  printf '\nSetup did not complete. Restoring the previous access settings.\n' >&2
  if (( had_access ));then install -o soar-panel -g soar-panel -m 0600 "$backup/access.json" "$access";else rm -f -- "$access";fi
  systemctl restart soar-panel.service || true
 fi
 exit "$result"
}
trap recover EXIT
systemctl stop soar-panel.service
if [[ $choice == 3 ]];then
 "$binary" access-reset --db "$db"
else
 "$binary" configure-access --db "$db" --listen "$bind" --domain "$domain" --email "$email" "${random[@]}"
fi
chown soar-panel:soar-panel "$access"
if [[ -d /var/lib/soar-panel/certificates ]];then chown -R soar-panel:soar-panel /var/lib/soar-panel/certificates;fi
systemctl start soar-panel.service
sleep 2
"$binary" health --db "$db" --listen "$bind"
done=1
printf '\nSecure access settings saved. Bookmark this private login address:\n'
"$binary" access-url --db "$db" --listen "$bind"
printf 'Administrator password is unchanged. Private rollback copy: %s\n' "$backup"
