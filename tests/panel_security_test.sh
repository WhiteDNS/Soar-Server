#!/usr/bin/env bash
set -Eeuo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
source "$repo/panel-security.sh"
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
mkdir -p "$work/data/certificates" "$work/state" "$work/backups"
printf 'database\n' >"$work/data/panel.db"
printf 'old access\n' >"$work/data/access.json"
printf 'old certificate\n' >"$work/data/certificates/panel.example.org"
printf 'old rsa certificate\n' >"$work/data/certificates/panel.example.org+rsa"
printf 'account key\n' >"$work/data/certificates/acme_account+key"
security_require_terminal(){ :; }
security_paths(){
 db="$work/data/panel.db"; access="$work/data/access.json"; binary="$work/panel"
 certificates="$work/data/certificates"; state="$work/state"; backup_root="$work/backups"
}
cat >"$work/panel" <<'BIN'
#!/usr/bin/env bash
set -eu
dir=$(cd -- "$(dirname -- "$0")" && pwd)
case "$1" in
 access-domains) printf 'panel.example.org\n';;
 access-url) printf 'https://panel.example.org/\n';;
 configure-access)
  printf '%s\n' "$@" >"$dir/arguments"
  printf 'new access\n' >"$dir/data/access.json"
  printf 'new certificate\n' >"$dir/data/certificates/panel.example.org"
  [[ ! -f $dir/fail-issuance ]];;
 health) :;;
 *) exit 3;;
esac
BIN
chmod +x "$work/panel"
flock(){ [[ ! -f $work/locked ]]; }
chown(){ :; }
install(){
 if [[ $1 == -d ]]; then shift 3; mkdir -p -- "$@";
 else local args=("$@"); cp -- "${args[${#args[@]}-2]}" "${args[${#args[@]}-1]}"; fi
}
systemctl(){
 printf '%s\n' "$*" >>"$work/systemctl.log"
 case "$1" in
  show) printf '{ argv[]=/usr/local/bin/soar-panel run --listen 127.0.0.1:2053 ; }\n';;
  is-active) [[ -f $work/active ]];;
  stop) rm -f "$work/active";;
  start|restart) touch "$work/active";;
  *) return 1;;
 esac
}
run_security(){
 local expected=$1 input=$2 rc
 set +e
 (set -e; security_main) <<<"$input" >"$work/result" 2>&1
 rc=$?
 set -e
 if [[ $rc != "$expected" ]]; then cat "$work/result"; printf 'FAIL: expected %s, got %s\n' "$expected" "$rc";exit 1;fi
}
touch "$work/active"
run_security 0 $'6\nRENEW\nAPPLY'
[[ $(cat "$work/data/certificates/panel.example.org") == 'new certificate' && -f $work/active ]]
[[ $(cat "$work/data/certificates/acme_account+key") == 'account key' ]]
printf 'old access\n' >"$work/data/access.json"
printf 'old certificate\n' >"$work/data/certificates/panel.example.org"
printf 'old rsa certificate\n' >"$work/data/certificates/panel.example.org+rsa"
touch "$work/fail-issuance"
run_security 1 $'6\nRENEW\nAPPLY'
[[ $(cat "$work/data/access.json") == 'old access' && $(cat "$work/data/certificates/panel.example.org") == 'old certificate' ]]
[[ $(cat "$work/data/certificates/panel.example.org+rsa") == 'old rsa certificate' && -f $work/active ]]
rm "$work/fail-issuance" "$work/active"
run_security 0 $'4\nAPPLY'
[[ ! -f $work/active ]]
grep -Fxq -- '--random-path' "$work/arguments"
if grep -Fxq -- '--panel-aliases' "$work/arguments"; then echo 'FAIL: random path changed aliases';exit 1;fi
run_security 0 $'5\n\nAPPLY'
grep -Fxq -- '--panel-aliases' "$work/arguments"
touch "$work/locked"
before=$(wc -l <"$work/systemctl.log")
run_security 1 $'4\nAPPLY'
after=$(wc -l <"$work/systemctl.log")
# Reading ExecStart is harmless; lock rejection must not stop/restart a service.
[[ $after -eq $((before+1)) ]]
echo 'PASS: certificate reissue, cache/access rollback, ACME account preservation, inactive service, aliases, random paths and shared lock'
