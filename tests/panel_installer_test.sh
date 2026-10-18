#!/usr/bin/env bash
set -Eeuo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
mkdir -p "$work/bin/linux-amd64" "$work/lib"
cp "$repo/panel-install.sh" "$work/"
cp "$repo/panel-security.sh" "$work/"
cp "$repo/lib/panel-installer.sh" "$work/lib/"
cp "$repo/install.sh" "$work/lib/standalone-install.sh"
for name in soar-panel soar-panel-ops soar-check; do printf 'fixture, not an executable\n' >"$work/bin/linux-amd64/$name";done
(cd "$work" && sha256sum panel-install.sh panel-security.sh lib/*.sh bin/linux-amd64/* > SHA256SUMS)
bash "$work/panel-install.sh" --check-bundle >/dev/null
mv "$work/bin/linux-amd64/soar-panel-ops" "$work/ops"
if bash "$work/panel-install.sh" --check-bundle >/dev/null 2>&1;then echo 'FAIL: missing maintenance binary accepted';exit 1;fi
mv "$work/ops" "$work/bin/linux-amd64/soar-panel-ops"
printf 'tampered\n' >>"$work/bin/linux-amd64/soar-panel"
if bash "$work/panel-install.sh" --check-bundle >/dev/null 2>&1;then echo 'FAIL: tampered bundle accepted';exit 1;fi
if bash "$work/panel-install.sh" --unknown >/dev/null 2>&1;then echo 'FAIL: unknown flag accepted';exit 1;fi
if bash "$work/panel-install.sh" --check-bundle extra >/dev/null 2>&1;then echo 'FAIL: extra argument accepted';exit 1;fi
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
