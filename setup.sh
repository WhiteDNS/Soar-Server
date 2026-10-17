#!/usr/bin/env bash
# Download a versioned bundle; keep stdin attached to the SSH terminal.
set -Eeuo pipefail
export LC_ALL=C
umask 077
VERSION=0.1.2
REPOSITORY=WhiteDNS/Soar-Server
WORK=
fail() { printf '[ERROR] %s\n' "$*" >&2; exit 1; }
cleanup() { [[ -z $WORK ]] || rm -rf -- "$WORK"; }
valid_checksum() {
    local digest name extra actual
    read -r digest name extra < "$1"
    name=${name#\*}
    [[ $digest =~ ^[a-f0-9]{64}$ && $name == "$3" && -z ${extra:-} ]] || return 1
    [[ $(wc -l < "$1") -eq 1 ]] || return 1
    actual=$(sha256sum -- "$2")
    [[ ${actual%% *} == "$digest" ]]
}
valid_member() {
    [[ $1 == "$2"/* && $1 != *'/../'* && $1 != */.. && $1 != *'/./'* && $1 != *\\* ]]
}
main() {
    local mode=${1:-install} command asset base member
    [[ $# -le 1 && ( $mode == install || $mode == update || $mode == --check-download ) ]] || fail 'Usage: setup.sh [install|update|--check-download]'
    if [[ $mode != --check-download ]]; then
        [[ -t 0 ]] || fail 'Run this in an interactive SSH terminal, using bash <(curl -fsSL URL).'
        local ID VERSION_ID
        [[ -f /etc/os-release ]] || fail 'Ubuntu 24.04 LTS is required.'
        . /etc/os-release
        [[ $ID == ubuntu && $VERSION_ID == 24.04 && $(uname -m) == x86_64 ]] || fail 'Supported server: Ubuntu 24.04 LTS, x86_64/amd64.'
    fi
    for command in curl tar sha256sum mktemp; do command -v "$command" >/dev/null || fail "Missing command: $command"; done
    WORK=$(mktemp -d "${TMPDIR:-/tmp}/whitedns-soar.XXXXXXXX")
    trap cleanup EXIT
    asset="soar-server-setup-$VERSION-linux-amd64.tar.gz"
    base="https://github.com/$REPOSITORY/releases/download/v$VERSION"
    printf 'WhiteDNS Soar server setup %s\nDownloading the versioned bundle and SHA-256 checksum...\n' "$VERSION"
    for member in "$asset" "$asset.sha256"; do
        curl --fail --show-error --silent --location --retry 3 --connect-timeout 15 --max-time 180 \
            --proto '=https' --proto-redir '=https' "$base/$member" -o "$WORK/$member" || fail 'Download failed. Check GitHub access and try the same command again.'
    done
    valid_checksum "$WORK/$asset.sha256" "$WORK/$asset" "$asset" || fail 'Checksum mismatch. No installation was started.'
    tar -tzf "$WORK/$asset" > "$WORK/members" || fail 'The downloaded archive is invalid.'
    while IFS= read -r member; do
        valid_member "$member" "soar-server-setup-$VERSION-linux-amd64" || fail 'Unexpected archive path. No installation was started.'
    done < "$WORK/members"
    tar --no-same-owner --no-same-permissions -xzf "$WORK/$asset" -C "$WORK"
    local installer="$WORK/soar-server-setup-$VERSION-linux-amd64/install.sh"
    bash "$installer" --check-bundle
    if [[ $mode == --check-download ]]; then
        printf '[OK] Download, archive checksum and bundled runtime checksums passed. No server changes made.\n'
        return
    fi
    printf 'The guided installer will now explain each step. Your SSH terminal stays interactive.\n'
    if [[ $EUID == 0 ]]; then
        if [[ $mode == update ]]; then bash "$installer" update; else bash "$installer"; fi
    else
        command -v sudo >/dev/null || fail 'Run this command as root, or install sudo for your server account.'
        if [[ $mode == update ]]; then sudo bash "$installer" update; else sudo bash "$installer"; fi
    fi
}
if [[ ${BASH_SOURCE[0]} == "$0" ]]; then main "$@"; fi
