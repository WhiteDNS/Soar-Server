#!/usr/bin/env bash
set -Eeuo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
for arch in amd64 arm64; do
 mkdir -p "$work/$arch/bin/linux-$arch" "$work/$arch/lib"
 cp "$repo/panel-install.sh" "$repo/panel-security.sh" "$work/$arch/"
 cp "$repo/lib/panel-installer.sh" "$work/$arch/lib/"
 cp "$repo/install.sh" "$work/$arch/lib/standalone-install.sh"
 printf '%s\n' "$arch" >"$work/$arch/ARCH"
 for name in soar-panel soar-panel-ops soar-check;do printf 'fixture, not an executable\n' >"$work/$arch/bin/linux-$arch/$name";done
 (cd "$work/$arch" && sha256sum ARCH panel-install.sh panel-security.sh lib/*.sh bin/linux-*/* >SHA256SUMS)
 bash "$work/$arch/panel-install.sh" --check-bundle >/dev/null
 for machine in x86_64 amd64 aarch64 arm64 ppc64le;do
  if (source "$work/$arch/panel-install.sh";uname(){ printf '%s\n' "$machine"; };check_host_arch) >"$work/result" 2>&1;then
   [[ "$arch:$machine" == amd64:x86_64 || "$arch:$machine" == amd64:amd64 || "$arch:$machine" == arm64:aarch64 || "$arch:$machine" == arm64:arm64 ]] || { echo 'FAIL: wrong host accepted';exit 1; }
  else
   [[ "$arch:$machine" != amd64:x86_64 && "$arch:$machine" != amd64:amd64 && "$arch:$machine" != arm64:aarch64 && "$arch:$machine" != arm64:arm64 ]] || { cat "$work/result";exit 1; }
   grep -Ei 'architecture|does not match' "$work/result" >/dev/null
  fi
 done
 mv "$work/$arch/bin/linux-$arch/soar-panel-ops" "$work/ops"
 if bash "$work/$arch/panel-install.sh" --check-bundle >/dev/null 2>&1;then echo 'FAIL: missing maintenance binary accepted';exit 1;fi
 mv "$work/ops" "$work/$arch/bin/linux-$arch/soar-panel-ops"
 printf 'tampered\n' >>"$work/$arch/bin/linux-$arch/soar-panel"
 if bash "$work/$arch/panel-install.sh" --check-bundle >/dev/null 2>&1;then echo 'FAIL: tampered bundle accepted';exit 1;fi
 if bash "$work/$arch/panel-install.sh" --unknown >/dev/null 2>&1;then echo 'FAIL: unknown flag accepted';exit 1;fi
 if bash "$work/$arch/panel-install.sh" --check-bundle extra >/dev/null 2>&1;then echo 'FAIL: extra argument accepted';exit 1;fi
 rm "$work/$arch/ARCH"
 if bash "$work/$arch/panel-install.sh" --check-bundle >/dev/null 2>&1;then echo 'FAIL: missing architecture accepted';exit 1;fi
done
bash -n "$repo/panel-install.sh"
bash -n "$repo/panel-security.sh"
bash -n "$repo/lib/panel-installer.sh"
"${PYTHON:-python3}" - "$repo/panel-install.sh" <<'PY'
from pathlib import Path
import sys
Path(sys.argv[1]).read_bytes().decode('ascii')
Path(sys.argv[1]).with_name('panel-security.sh').read_bytes().decode('ascii')
Path(sys.argv[1]).parent.joinpath('lib/panel-installer.sh').read_bytes().decode('ascii')
PY
echo 'PASS: panel bundle verification, tamper rejection, argument validation, shell syntax, ASCII terminal text'
bash "$repo/tests/panel_install_flow_test.sh"
