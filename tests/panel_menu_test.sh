#!/usr/bin/env bash
# Exercise transactions with real temporary files and service doubles; no root.
set -Eeuo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
source "$repo/panel-menu.sh"
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
DATA="$work/data"; DB="$DATA/panel.db"; STATE="$work/state"; BACKUPS="$work/backups"
PANEL_DROP="$work/units/panel/90-management.conf"; OPS_DROP="$work/units/ops/90-management.conf"
mkdir -p "$DATA" "$STATE" "${PANEL_DROP%/*}" "${OPS_DROP%/*}"
printf 'account keys and usage\n' >"$DB"
printf 'private path and domain\n' >"$DATA/access.json"
printf 'old panel override\n' >"$PANEL_DROP"
touch "$work/soar-panel.service.active" "$work/soar-panel-ops.service.active"
flock(){ [[ ! -f $work/locked ]]; }
chown(){ :; }
install(){
 if [[ $1 == -d && $2 == -m ]]; then shift 3; mkdir -p -- "$@";
 else command install "$@"; fi
}
systemctl(){
 printf '%s\n' "$*" >>"$work/systemctl.log"
 case "$1" in
  is-active) [[ -f $work/$3.active ]];;
  stop) rm -f "$work/$2.active";;
  start)
   if [[ -f $work/fail-start ]]; then rm "$work/fail-start"; return 1; fi
   touch "$work/$2.active";;
  disable|reset-failed) :;;
  daemon-reload) :;;
  show) printf '{ path=/usr/local/bin/soar-panel ; argv[]=/usr/local/bin/soar-panel run --db /var/lib/soar-panel/panel.db --dns-listen 8.8.8.8:53 --listen 127.0.0.1:2053 --trust-loopback-proxy ; ignore_errors=no ; }\n';;
  *) printf 'Unexpected systemctl call: %s\n' "$*" >&2; return 1;;
 esac
}
menu_wait(){
 if [[ -f $work/fail-health ]]; then rm "$work/fail-health"; return 1; fi
 [[ -f $work/soar-panel.service.active ]]
}
menu_wait_ops(){ [[ -f $work/soar-panel-ops.service.active ]]; }
ss(){ :; }
success(){ printf 'new account data\n' >"$DB"; }
failed_write(){ printf 'corrupted data\n' >"$DB"; printf 'bad override\n' >"$PANEL_DROP"; printf 'new\n' >"$OPS_DROP"; return 42; }
health_failure(){ success; touch "$work/fail-health"; }
start_failure(){ success; touch "$work/fail-start"; }
run_transaction(){
 local expected=$1 rc; shift
 set +e
 (set -e; menu_transaction "$@") >"$work/result" 2>&1
 rc=$?
 set -e
 if [[ $rc != "$expected" ]]; then cat "$work/result"; printf 'FAIL: %s expected %s got %s\n' "$*" "$expected" "$rc"; exit 1; fi
}
run_transaction 0 success
[[ $(cat "$DB") == 'new account data' && -f $work/soar-panel.service.active && -f $work/soar-panel-ops.service.active ]]
run_transaction 42 failed_write
[[ $(cat "$DB") == 'new account data' && $(cat "$PANEL_DROP") == 'old panel override' && ! -f $OPS_DROP ]]
[[ $(cat "$DATA/access.json") == 'private path and domain' ]]
[[ -f $work/soar-panel.service.active && -f $work/soar-panel-ops.service.active ]]
run_transaction 1 health_failure
[[ $(cat "$DB") == 'new account data' && -f $work/soar-panel.service.active ]]
run_transaction 1 start_failure
[[ -f $work/soar-panel.service.active && -f $work/soar-panel-ops.service.active ]]
rm "$work/soar-panel.service.active" "$work/soar-panel-ops.service.active"
run_transaction 0 success
[[ ! -f $work/soar-panel.service.active && ! -f $work/soar-panel-ops.service.active ]]
touch "$work/locked"
before=$(wc -l <"$work/systemctl.log")
run_transaction 1 success
[[ $(wc -l <"$work/systemctl.log") == "$before" ]]
rm "$work/locked"
printf '8443\n1\nAPPLY\n' | menu_transaction menu_port_action >"$work/result" 2>&1
grep -Fq -- '--listen 127.0.0.1:8443 --trust-loopback-proxy' "$PANEL_DROP"
grep -Fq -- '--health-address 127.0.0.1:8443' "$OPS_DROP"
printf '5353\nAPPLY\n' | menu_transaction menu_dns_port_action >"$work/result" 2>&1
grep -Fq -- '--dns-listen 8.8.8.8:5353' "$PANEL_DROP"
grep -Fq -- '--dns-port 5353' "$OPS_DROP"
# Restore the previous management override for the next rejection assertion.
printf '8443\n1\nAPPLY\n' | menu_transaction menu_port_action >"$work/result" 2>&1
printf '53\n' >"$work/input"
set +e
(set -e; menu_transaction menu_port_action <"$work/input") >"$work/result" 2>&1
rc=$?
set -e
[[ $rc == 1 ]]
grep -Fq -- '--listen 127.0.0.1:8443' "$PANEL_DROP"
# Cancellation must never reach service control or file deletion.
before=$(wc -l <"$work/systemctl.log")
menu_uninstall <<<0 >"$work/result"
menu_uninstall <<< $'2\nNO\n' >"$work/result"
[[ $(wc -l <"$work/systemctl.log") == "$before" && -f $DB ]]
UNITS="$work/uninstall-units"; BINDIR="$work/uninstall-bin"
for mode in 1 2; do
 mkdir -p "$UNITS/soar-panel.service.d" "$UNITS/soar-panel-ops.service.d" "$BINDIR" "$DATA" "$STATE"
 for unit in soar-panel soar-panel-ops; do
  printf 'unit\n' >"$UNITS/$unit.service"
  printf 'override\n' >"$UNITS/$unit.service.d/90-management.conf"
 done
 for name in soar-panel soar-panel-ops soar-panel-security soar-panel-menu; do printf 'executable fixture\n' >"$BINDIR/$name"; done
 printf 'unrelated tool\n' >"$BINDIR/unrelated"
 printf 'standalone unit\n' >"$UNITS/soar-server.service"
 printf 'preserved account data\n' >"$DB"
 mkdir -p "$STATE/job-fixture"; printf 'private history\n' >"$STATE/job-fixture/backup"
 set +e
 (set -e; menu_uninstall) <<<"$(printf '%s\nUNINSTALL\nDELETE-DATA' "$mode")" >"$work/result" 2>&1
 rc=$?
 set -e
 [[ $rc == 10 && ! -e $BINDIR/soar-panel && ! -e $UNITS/soar-panel.service && -f $BINDIR/unrelated && -f $UNITS/soar-server.service ]]
 if [[ $mode == 1 ]]; then [[ -f $DB && -f $STATE/job-fixture/backup ]];
 else [[ ! -e $DATA && ! -e $STATE/job-fixture && -f $STATE/maintenance.lock ]]; fi
 # The recovery copy must remain for both uninstall modes.
 find "$BACKUPS" -path '*/uninstall-*/data/panel.db' | grep -q .
done
bash "$repo/panel-menu.sh" --help >/dev/null
echo 'PASS: terminal transactions, failed-write/readiness/start rollback, inactive services, lock contention, port changes, uninstall cancellation/keep-data/delete-data'
