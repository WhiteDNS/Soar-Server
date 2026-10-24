#!/usr/bin/env bash
set -Eeuo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
source "$repo/panel-setup.sh"
work=$(mktemp -d);trap 'rm -rf -- "$work"' EXIT
printf 'payload\n' >"$work/bundle"
hash=$(sha256sum "$work/bundle");hash=${hash%% *}
printf '%s  bundle\n' "$hash" >"$work/checksum"
valid_checksum "$work/checksum" "$work/bundle" bundle
if valid_checksum "$work/checksum" "$work/bundle" another;then echo 'FAIL wrong asset name';exit 1;fi
printf 'changed\n' >>"$work/bundle"
if valid_checksum "$work/checksum" "$work/bundle" bundle;then echo 'FAIL damaged archive';exit 1;fi
valid_member 'bundle/bin/panel' bundle
for member in 'bundle/../escape' '/etc/passwd' 'wrong/bin' 'bundle/a/../../escape';do if valid_member "$member" bundle;then echo 'FAIL unsafe path';exit 1;fi;done
if (main --unknown) >/dev/null 2>&1;then echo 'FAIL bad invocation';exit 1;fi
bash -n "$repo/panel-setup.sh"
echo 'PASS: panel bootstrap checksum, filename, traversal and invocation checks'

for host in x86_64 amd64 aarch64 arm64;do
 case "$host" in x86_64|amd64) arch=amd64 ;; *) arch=arm64 ;; esac
 [[ $(panel_arch "$host") == "$arch" ]]
 asset="soar-panel-0.7.0-linux-$arch.tar.gz"
 mkdir -p "$work/soar-panel-0.7.0-linux-$arch"
 printf '#!/usr/bin/env bash\nexit 0\n' >"$work/soar-panel-0.7.0-linux-$arch/panel-install.sh"
 tar -czf "$work/$asset" -C "$work" "soar-panel-0.7.0-linux-$arch"
 (cd "$work" && sha256sum "$asset" >"$asset.sha256")
 (
  PANEL_RELEASE=0.7.0
  uname(){ printf '%s\n' "$host"; }
  curl(){
   local url= destination=
   while (( $# ));do case "$1" in https://*) url=$1 ;; -o) shift;destination=$1 ;; esac;shift;done
   [[ $url == "https://github.com/WhiteDNS/Soar-Server/releases/download/panel-v0.7.0/$asset" || $url == "https://github.com/WhiteDNS/Soar-Server/releases/download/panel-v0.7.0/$asset.sha256" ]] || return 1
   cp "$work/${url##*/}" "$destination"
  }
  main --check-download
 ) >"$work/download-result"
 grep -q 'No services changed' "$work/download-result"
done
if (panel_arch ppc64le) >"$work/result" 2>&1;then echo 'FAIL unsupported architecture';exit 1;fi
grep -q 'Unsupported architecture' "$work/result"
if (PANEL_RELEASE=0.6.0;uname(){ printf aarch64; };main --check-download) >"$work/result" 2>&1;then echo 'FAIL arm64 accepted an old amd64-only release';exit 1;fi
grep -q 'has no arm64 bundle' "$work/result"
echo 'PASS: amd64/arm64 aliases, architecture-specific downloads and early unsupported/missing-asset errors'
