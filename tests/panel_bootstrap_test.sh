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

latest_url=https://github.com/WhiteDNS/Soar-Server/releases/tag/panel-v0.7.0
curl(){
 local url= destination=
 while (( $# ));do case "$1" in https://*) url=$1 ;; -o|--output) shift;destination=$1 ;; esac;shift;done
 printf '%s\n' "$url" >>"$work/requests"
 if [[ $url == https://github.com/WhiteDNS/Soar-Server/releases/latest ]];then
  [[ ${discovery_fails:-0} == 0 ]] || return 22
  printf '%s' "$latest_url"
  return
 fi
 [[ $url == "https://github.com/WhiteDNS/Soar-Server/releases/download/panel-v$version/$asset" || $url == "https://github.com/WhiteDNS/Soar-Server/releases/download/panel-v$version/$asset.sha256" ]] || return 1
 cp "$work/${url##*/}" "$destination"
}
for version in 0.7.0 0.12.3;do
latest_url="https://github.com/WhiteDNS/Soar-Server/releases/tag/panel-v$version"
for host in x86_64 amd64 aarch64 arm64;do
 case "$host" in x86_64|amd64) arch=amd64 ;; *) arch=arm64 ;; esac
 [[ $(panel_arch "$host") == "$arch" ]]
 asset="soar-panel-$version-linux-$arch.tar.gz"
 mkdir -p "$work/soar-panel-$version-linux-$arch"
 printf '#!/usr/bin/env bash\n[[ $1 == --check-bundle ]]\n' >"$work/soar-panel-$version-linux-$arch/panel-install.sh"
 tar -czf "$work/$asset" -C "$work" "soar-panel-$version-linux-$arch"
 (cd "$work" && sha256sum "$asset" >"$asset.sha256")
 (
  PANEL_RELEASE=0.6.0 # A stale inherited value must not pin installation.
  uname(){ printf '%s\n' "$host"; }
  main --check-download
 ) >"$work/download-result"
 grep -q 'No services changed' "$work/download-result"
 grep -q "WhiteDNS Soar Panel $version - STABLE" "$work/download-result"
done
done
if (panel_arch ppc64le) >"$work/result" 2>&1;then echo 'FAIL unsupported architecture';exit 1;fi
grep -q 'Unsupported architecture' "$work/result"
echo 'PASS: latest stable and future releases, amd64/arm64 aliases and architecture-specific downloads'

for latest_url in \
 'https://github.com/WhiteDNS/Soar-Server/releases/tag/panel-v0.8.0-beta1' \
 'https://github.com/WhiteDNS/Soar-Server/releases/tag/panel-v0.8.0/extra' \
 'https://github.com/WhiteDNS/Soar-Server/releases/tag/panel-v01.8.0' \
 'https://github.com/WhiteDNS/Soar-Server/releases/tag/v0.1.3' \
 'https://github.com/another/repo/releases/tag/panel-v0.8.0' \
 'https://example.com/WhiteDNS/Soar-Server/releases/tag/panel-v0.8.0';do
 : >"$work/requests"
 if (main --check-download) >"$work/result" 2>&1;then echo 'FAIL invalid latest release accepted';exit 1;fi
 [[ $(wc -l <"$work/requests") -eq 1 ]] # No archive downloads after rejected metadata.
 grep -q 'Installation was not started' "$work/result"
done
latest_url=https://github.com/WhiteDNS/Soar-Server/releases/tag/panel-v0.12.3
: >"$work/requests"
if (discovery_fails=1;main --check-download) >"$work/result" 2>&1;then echo 'FAIL discovery failure ignored';exit 1;fi
[[ $(wc -l <"$work/requests") -eq 1 ]]
grep -q 'Could not find the latest stable panel release' "$work/result"

# The latest release must be complete and intact; never fall back to an older bundle.
rm -- "$work/$asset.sha256"
if (uname(){ printf aarch64; };main --check-download) >"$work/result" 2>&1;then echo 'FAIL missing checksum accepted';exit 1;fi
grep -q 'Installation was not started' "$work/result"
printf '%064d  %s\n' 0 "$asset" >"$work/$asset.sha256"
if (uname(){ printf aarch64; };main --check-download) >"$work/result" 2>&1;then echo 'FAIL damaged release accepted';exit 1;fi
grep -q 'Checksum mismatch' "$work/result"
echo 'PASS: invalid/prerelease/unofficial latest, failed discovery, missing checksum and checksum mismatch stop installation'
