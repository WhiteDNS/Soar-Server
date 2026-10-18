#!/usr/bin/env bash
set -Eeuo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
mkdir -p "$work/bin/linux-amd64"
cp "$repo/panel-install.sh" "$work/"
printf 'fixture, not an executable\n' >"$work/bin/linux-amd64/soar-panel"
(cd "$work" && sha256sum panel-install.sh bin/linux-amd64/soar-panel > SHA256SUMS)
bash "$work/panel-install.sh" --check-bundle >/dev/null
printf 'tampered\n' >>"$work/bin/linux-amd64/soar-panel"
if bash "$work/panel-install.sh" --check-bundle >/dev/null 2>&1;then echo 'FAIL: tampered bundle accepted';exit 1;fi
if bash "$work/panel-install.sh" --unknown >/dev/null 2>&1;then echo 'FAIL: unknown flag accepted';exit 1;fi
bash -n "$repo/panel-install.sh"
bash -n "$repo/panel-security.sh"
"${PYTHON:-python3}" - "$repo/panel-install.sh" <<'PY'
from pathlib import Path
import sys
Path(sys.argv[1]).read_bytes().decode('ascii')
Path(sys.argv[1]).with_name('panel-security.sh').read_bytes().decode('ascii')
PY
echo 'PASS: panel bundle verification, tamper rejection, argument validation, shell syntax, ASCII terminal text'
