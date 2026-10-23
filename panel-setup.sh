#!/usr/bin/env bash
# Stable management panel; keep stdin attached to the user's SSH terminal.
set -Eeuo pipefail
export LC_ALL=C
umask 077
# Advance only after the matching release archive and checksum are verified.
PANEL_RELEASE=0.6.0
PANEL_REPO=WhiteDNS/Soar-Server
PANEL_WORK=
fail(){ printf '[ERROR] %s\n' "$*" >&2;exit 1; }
cleanup(){ [[ -z $PANEL_WORK ]] || rm -rf -- "$PANEL_WORK"; }
valid_checksum(){
 local digest name extra actual
 read -r digest name extra < "$1";name=${name#\*}
 [[ $digest =~ ^[a-f0-9]{64}$ && $name == "$3" && -z ${extra:-} && $(wc -l < "$1") -eq 1 ]] || return 1
 actual=$(sha256sum -- "$2");[[ ${actual%% *} == "$digest" ]]
}
valid_member(){ [[ $1 == "$2"/* && $1 != *'/../'* && $1 != */.. && $1 != *'/./'* && $1 != *\\* ]]; }
main(){
 local mode=${1:-install} command asset base member
 [[ $# -le 1 && ( $mode == install || $mode == --check-download ) ]] || fail 'Usage: panel-setup.sh [install|--check-download]'
 if [[ $mode == install ]];then
  [[ -t 0 ]] || fail 'Run from an interactive SSH terminal: bash <(curl -fsSL URL)'
  local ID VERSION_ID VERSION
  [[ -f /etc/os-release ]] || fail 'Ubuntu or Debian is required.'
  . /etc/os-release
  case "$ID:${VERSION_ID%%.*}" in ubuntu:22|ubuntu:24|ubuntu:26|debian:12|debian:13) ;;*) fail 'Supported: Ubuntu 22.04/24.04/26.04 or Debian 12/13.';;esac
  [[ $(uname -m) == x86_64 ]] || fail 'This panel release is for Linux amd64.'
 fi
 for command in curl tar sha256sum mktemp awk;do command -v "$command" >/dev/null || fail "Missing $command. Install curl/coreutils/tar, then retry.";done
 PANEL_WORK=$(mktemp -d "${TMPDIR:-/tmp}/whitedns-soar-panel.XXXXXXXX");trap cleanup EXIT
 asset="soar-panel-$PANEL_RELEASE-linux-amd64.tar.gz";base="https://github.com/$PANEL_REPO/releases/download/panel-v$PANEL_RELEASE"
 printf 'WhiteDNS Soar Panel %s - STABLE\nDownloading the pinned release and its SHA-256 checksum...\n' "$PANEL_RELEASE"
 for member in "$asset" "$asset.sha256";do curl --fail --show-error --silent --location --retry 3 --connect-timeout 15 --max-time 180 --proto '=https' --proto-redir '=https' "$base/$member" -o "$PANEL_WORK/$member" || fail 'Download failed. Check GitHub access and retry.';done
 valid_checksum "$PANEL_WORK/$asset.sha256" "$PANEL_WORK/$asset" "$asset" || fail 'Checksum mismatch. Installation was not started.'
 tar -tzf "$PANEL_WORK/$asset" > "$PANEL_WORK/members" || fail 'Invalid archive.'
 while IFS= read -r member;do valid_member "$member" "soar-panel-$PANEL_RELEASE-linux-amd64" || fail 'Unsafe archive path.';done < "$PANEL_WORK/members"
 tar -tvzf "$PANEL_WORK/$asset" | awk 'substr($1,1,1)!="-" && substr($1,1,1)!="d"{bad=1}END{exit bad}' || fail 'Archive contains unsupported links or special files.'
 tar --no-same-owner --no-same-permissions -xzf "$PANEL_WORK/$asset" -C "$PANEL_WORK"
 local installer="$PANEL_WORK/soar-panel-$PANEL_RELEASE-linux-amd64/panel-install.sh"
 bash "$installer" --check-bundle
 if [[ $mode == --check-download ]];then printf '[OK] Panel download and bundled checksums passed. No services changed.\n';return;fi
 printf 'The guided seven-step installer will now explain installation or migration.\n'
 if [[ $EUID == 0 ]];then bash "$installer";else command -v sudo >/dev/null || fail 'Run as root or install sudo.';sudo bash "$installer";fi
}
if [[ ${BASH_SOURCE[0]} == "$0" ]];then main "$@";fi
