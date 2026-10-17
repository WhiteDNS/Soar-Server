#!/usr/bin/env bash
set -Eeuo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/../setup.sh"
test_dir=$(mktemp -d)
trap 'rm -rf -- "$test_dir"' EXIT
printf 'test bundle\n' > "$test_dir/bundle.tar.gz"
(cd "$test_dir" && sha256sum bundle.tar.gz > bundle.tar.gz.sha256)
valid_checksum "$test_dir/bundle.tar.gz.sha256" "$test_dir/bundle.tar.gz" bundle.tar.gz
printf 'PASS valid checksum\n'
printf 'damage\n' >> "$test_dir/bundle.tar.gz"
if valid_checksum "$test_dir/bundle.tar.gz.sha256" "$test_dir/bundle.tar.gz" bundle.tar.gz; then exit 1; fi
printf 'PASS damaged archive rejected\n'
sed 's/bundle.tar.gz/other.tar.gz/' "$test_dir/bundle.tar.gz.sha256" > "$test_dir/wrong-name"
if valid_checksum "$test_dir/wrong-name" "$test_dir/bundle.tar.gz" bundle.tar.gz; then exit 1; fi
printf 'PASS checksum filename mismatch rejected\n'
valid_member release/install.sh release
for member in /etc/passwd other/install.sh release/../../etc/passwd release/.. 'release/a\b'; do
    if valid_member "$member" release; then exit 1; fi
done
printf 'PASS archive traversal and wrong root rejected\n'
if (main --unknown); then exit 1; fi
printf 'PASS invalid invocation rejected\n'
printf '5 bootstrap checks passed.\n'
