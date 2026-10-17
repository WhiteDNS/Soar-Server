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
